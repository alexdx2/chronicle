package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// LoadOptions configures Load.
type LoadOptions struct {
	// DBPath is where the SQLite file backing the loaded store is created.
	// "" builds it in a fresh temporary directory that the returned closer
	// removes entirely (db file plus its -wal/-shm siblings); a non-empty
	// DBPath is used as-is and is the caller's to delete — the closer still
	// closes the connection, it just leaves the file in place.
	//
	// store.Open(":memory:") is deliberately not used for "": store.Open
	// (store/store.go) opens its *sql.DB with unlimited pooled connections
	// (db.SetMaxOpenConns(0)), and modernc.org/sqlite gives each new
	// physical connection to ":memory:" its own private, schema-less
	// database — a second connection from the pool (any concurrent access
	// at all) sees "no such table: graph_nodes" instead of the data a prior
	// connection just wrote (confirmed empirically against this exact
	// store.Open before writing Load: a goroutine racing a freshly-inserted
	// row via ListNodes hit exactly that error). A throwaway on-disk file in
	// os.MkdirTemp has no such split-brain risk and is just as disposable.
	DBPath string
}

// Load builds a fresh SQLite-backed store from s and returns the *graph.Graph
// over it: nodes, edges (From/To resolved from the snapshot's keys to the
// new store's ids), evidence (node- and edge-attached, BlobSHA included),
// aliases, glossary terms and file coverage are inserted via the store's
// ordinary write methods (UpsertNode, UpsertEdge, AddEvidence, AddAlias,
// UpsertTerm, UpsertCoverage) — never raw SQL — in the order the store's own
// references require: nodes before the edges that resolve From/To against
// them, both before the evidence that attaches to whichever node or edge id
// they resolved to.
//
// The store never journals: SetJournaling(false) runs before the first
// write and is never turned back on. A snapshot-loaded store is an
// ephemeral, disposable graph built fresh for one review/CI run — not
// something anyone will `chronicle journal flush` out of — and it starts
// with no lamport/actor history of its own to append to coherently.
//
// FromStore (snapshot/export.go) deliberately excludes two kinds of derived
// state so the snapshot doesn't carry recomputable bytes (see
// snapshot/derived.go): trust/confidence/freshness on every node and edge,
// and the evidence backing flow:use_case nodes and their
// TRIGGERS_FLOW/REQUIRES edges. Load restores both, in this order:
//
//  1. DeriveFlows, once per domain named in the header, re-walks the
//     endpoint → controller → INJECTS topology just inserted and regenerates
//     the flow layer's edges and — because it always re-adds an edge's
//     derive-flows evidence, whether or not the edge already existed — their
//     evidence too.
//  2. RecalculateAllTrust then recomputes confidence/freshness/trust_score
//     for every node and edge from whatever evidence is now present,
//     matching the original graph's values for anything with surviving
//     (non-derived, or just-regenerated) evidence.
//
// The order matters: recomputing trust before DeriveFlows would derive it
// from evidence that doesn't exist yet.
//
// One gap survives this: DeriveFlows only ever backfills evidence for
// EDGES it revisits — ensureFlowNode (graph/derive_flows.go) skips a
// flow:use_case NODE that already exists, by design, for the live-graph
// incremental case. Such a node's own evidence was exactly what the
// snapshot excluded, so after Load it has none, and RecalculateAllTrust
// leaves a node with no evidence exactly as inserted (1.0/1.0/1.0) rather
// than deriving a number from nothing. That node's trust therefore does not
// reproduce whatever the original graph had last derived for it. This is
// inherent to the snapshot format (trust is never stored) combined with
// existing, unmodified DeriveFlows behavior, not something Load can fix
// without either duplicating DeriveFlows' node-creation logic or forcibly
// deleting and recreating every flow node — which would also discard a
// traced (non-derived) flow's real evidence. See the task report for the
// fixture-verified scope of this (flow:use_case nodes, and in principle any
// other node whose entire evidence set was derived).
func Load(s *Snapshot, reg *registry.Registry, opt LoadOptions) (*graph.Graph, func() error, error) {
	if s == nil {
		return nil, nil, fmt.Errorf("snapshot: Load: snapshot is nil")
	}

	dbPath := opt.DBPath
	tempDir := ""
	if dbPath == "" {
		dir, err := os.MkdirTemp("", "chronicle-snapshot-*")
		if err != nil {
			return nil, nil, fmt.Errorf("snapshot: Load: create temp dir: %w", err)
		}
		tempDir = dir
		dbPath = filepath.Join(dir, "chronicle.db")
	}

	st, err := store.Open(dbPath)
	if err != nil {
		if tempDir != "" {
			os.RemoveAll(tempDir)
		}
		return nil, nil, fmt.Errorf("snapshot: Load: open store: %w", err)
	}

	closer := func() error {
		closeErr := st.Close()
		if tempDir == "" {
			return closeErr
		}
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			if closeErr != nil {
				return fmt.Errorf("snapshot: Load closer: close: %v; remove temp dir: %w", closeErr, rmErr)
			}
			return fmt.Errorf("snapshot: Load closer: remove temp dir: %w", rmErr)
		}
		return closeErr
	}

	// Off before the very first write — see LoadOptions and the package
	// doc comment above for why a snapshot-loaded store never journals.
	st.SetJournaling(false)

	// One transaction for the whole bulk load. At snapshot scale (thousands
	// of nodes/edges/evidence rows) the dominant cost is round trips, not
	// row count: every store write method commits its own statement
	// immediately outside a transaction, each one its own fsync under WAL.
	// Batching is a straight performance necessity here, not a correctness
	// one — SetJournaling above already makes every write a journal no-op,
	// in or out of a transaction.
	if err := st.WithTx(func(tx *store.Store) error {
		return loadInto(tx, s)
	}); err != nil {
		closer()
		return nil, nil, fmt.Errorf("snapshot: Load: %w", err)
	}

	g := graph.New(st, reg)

	for _, domain := range s.Header.Domains {
		if err := g.DeriveFlows(domain, 0); err != nil {
			closer()
			return nil, nil, fmt.Errorf("snapshot: Load: derive flows for domain %q: %w", domain, err)
		}
	}

	if err := g.RecalculateAllTrust(); err != nil {
		closer()
		return nil, nil, fmt.Errorf("snapshot: Load: recalculate trust: %w", err)
	}

	return g, closer, nil
}

// loadInto writes every record in s into st via the store's ordinary write
// methods. st may be the top-level store or a WithTx transaction handle —
// every store write method takes a DBTX-shaped executor either way, so the
// logic here does not need to know which.
func loadInto(st *store.Store, s *Snapshot) error {
	nodeIDs := make(map[string]int64, len(s.Nodes))
	for _, n := range s.Nodes {
		id, err := st.UpsertNode(store.NodeRow{
			NodeKey:       n.ID,
			Layer:         n.Layer,
			NodeType:      n.Type,
			DomainKey:     n.Domain,
			Name:          n.Name,
			QualifiedName: n.QualifiedName,
			FilePath:      n.File,
			Lang:          n.Lang,
			OwnerKey:      n.Attrs["owner_key"],
			Environment:   n.Attrs["environment"],
			Visibility:    n.Attrs["visibility"],
			SymbolName:    n.Attrs["symbol_name"],
			SupportKind:   n.Attrs["support_kind"],
			RepoName:      n.Attrs["repo_name"],
			Status:        n.Status,
			// Confidence/Freshness/TrustScore are not part of the snapshot
			// format (FromStore never writes them — see Load's doc comment):
			// 1.0 is a neutral placeholder, corrected below for every node
			// RecalculateAllTrust can derive a real value for.
			Confidence: 1.0,
			Freshness:  1.0,
			TrustScore: 1.0,
			Metadata:   metadataOrEmpty(n.Metadata),
		})
		if err != nil {
			return fmt.Errorf("node %q: %w", n.ID, err)
		}
		nodeIDs[n.ID] = id
	}

	edgeIDs := make(map[string]int64, len(s.Edges))
	for _, e := range s.Edges {
		fromID, ok := nodeIDs[e.From]
		if !ok {
			return fmt.Errorf("edge %q: from-node %q not found in this snapshot", e.ID, e.From)
		}
		toID, ok := nodeIDs[e.To]
		if !ok {
			return fmt.Errorf("edge %q: to-node %q not found in this snapshot", e.ID, e.To)
		}
		id, err := st.UpsertEdge(store.EdgeRow{
			EdgeKey:          e.ID,
			FromNodeID:       fromID,
			ToNodeID:         toID,
			FromNodeKey:      e.From,
			ToNodeKey:        e.To,
			EdgeType:         e.Type,
			DerivationKind:   e.Derivation,
			DependencySource: e.DependencySource,
			ContextKey:       e.Context,
			Active:           e.Active,
			Confidence:       1.0, // see the matching node-side comment above
			Freshness:        1.0,
			TrustScore:       1.0,
			Metadata:         metadataOrEmpty(e.Metadata),
		})
		if err != nil {
			return fmt.Errorf("edge %q: %w", e.ID, err)
		}
		edgeIDs[e.ID] = id
	}

	for _, n := range s.Nodes {
		nodeID := nodeIDs[n.ID]
		for _, ev := range n.Evidence {
			if _, err := st.AddEvidence(evidenceRowFrom("node", nodeID, 0, ev)); err != nil {
				return fmt.Errorf("evidence %q on node %q: %w", ev.ID, n.ID, err)
			}
		}
	}
	for _, e := range s.Edges {
		edgeID := edgeIDs[e.ID]
		for _, ev := range e.Evidence {
			if _, err := st.AddEvidence(evidenceRowFrom("edge", 0, edgeID, ev)); err != nil {
				return fmt.Errorf("evidence %q on edge %q: %w", ev.ID, e.ID, err)
			}
		}
	}

	for _, n := range s.Nodes {
		nodeID := nodeIDs[n.ID]
		for _, a := range n.Aliases {
			if _, err := st.AddAlias(store.AliasRow{
				NodeID:     nodeID,
				Alias:      a.Alias,
				AliasKind:  a.Kind,
				Confidence: a.Confidence,
			}); err != nil {
				return fmt.Errorf("alias %q on node %q: %w", a.Alias, n.ID, err)
			}
		}
	}

	for _, t := range s.Terms {
		if _, err := st.UpsertTerm(store.DomainTerm{
			DomainKey:    t.Domain,
			Term:         t.Term,
			Aliases:      t.Aliases,
			AntiPatterns: t.AntiPatterns,
			Description:  t.Description,
			Context:      t.Context,
			Examples:     t.Examples,
		}); err != nil {
			return fmt.Errorf("term %q: %w", t.ID, err)
		}
	}

	// Coverage carries no domain in the canonical format — groupCoverage
	// (snapshot/export.go) folds every domain's file_coverage rows for one
	// path into a single record, so which domain originally produced any
	// one (file, extractor) row is not recoverable from the snapshot. Every
	// row is written under the snapshot's own first header domain (or ""
	// for a domain-less snapshot); ListCoverage("") — what the next
	// FromStore reads back — spans every domain regardless, so this choice
	// does not affect what a later export sees.
	coverageDomain := ""
	if len(s.Header.Domains) > 0 {
		coverageDomain = s.Header.Domains[0]
	}
	for _, c := range s.Coverage {
		for _, ext := range c.Extractors {
			if err := st.UpsertCoverage(store.CoverageRow{
				DomainKey:        coverageDomain,
				FilePath:         c.Path,
				ExtractorID:      ext.ID,
				ExtractorVersion: ext.Version,
				Status:           ext.Status,
				BlobSHA:          c.Blob,
			}); err != nil {
				return fmt.Errorf("coverage %q/%q: %w", c.Path, ext.ID, err)
			}
		}
	}

	return nil
}

// evidenceRowFrom builds the store.EvidenceRow for one snapshot Evidence
// record attached to a node or edge. Exactly one of nodeID/edgeID is used by
// store.AddEvidence, selected by targetKind; the other is ignored, so
// passing 0 for it is harmless.
func evidenceRowFrom(targetKind string, nodeID, edgeID int64, ev Evidence) store.EvidenceRow {
	lineStart, lineEnd, colStart, colEnd := parseRange(ev.Range)
	polarity := ev.Polarity
	if polarity == "" {
		polarity = "positive"
	}
	return store.EvidenceRow{
		TargetKind:         targetKind,
		NodeID:             nodeID,
		EdgeID:             edgeID,
		SourceKind:         ev.Source,
		FilePath:           ev.File,
		LineStart:          lineStart,
		LineEnd:            lineEnd,
		ColumnStart:        colStart,
		ColumnEnd:          colEnd,
		Locator:            ev.Locator,
		ExtractorID:        ev.Extractor,
		ExtractorVersion:   ev.ExtractorVersion,
		ASTRule:            ev.Rule,
		Confidence:         ev.Confidence,
		EvidenceStatus:     ev.Status,
		EvidencePolarity:   polarity,
		EvidenceUID:        ev.ID,
		BlobSHA:            ev.Blob,
		Assertion:          string(ev.Assertion),
		AssertionKind:      ev.AssertionKind,
		AssertionVersion:   ev.AssertionVersion,
		VerificationStatus: ev.Verification,
	}
}

// metadataOrEmpty turns a snapshot record's possibly-nil Metadata into the
// string store.NodeRow/EdgeRow.Metadata expects — "{}" for nil/empty,
// exactly like the graph_nodes/graph_edges columns' own NOT NULL DEFAULT
// '{}'. The inverse of export.go's rawOrNil.
func metadataOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

// parseRange reverses export.go's formatRange: "" -> all zero, "L-L" ->
// LineStart/LineEnd only, "L:C-L:C" -> Column{Start,End} too. It is exactly
// the inverse of what formatRange renders, so re-exporting a Load'ed
// evidence row through formatRange reproduces the same Range string again —
// idempotent, though not necessarily identical to whatever LineEnd/ColumnEnd
// the ORIGINAL row had before formatRange's own defaulting collapsed them
// into that string. That distinction is unrecoverable: the snapshot format
// keeps only the rendered Range string, never the four separate ints.
func parseRange(r string) (lineStart, lineEnd, colStart, colEnd int) {
	if r == "" {
		return 0, 0, 0, 0
	}
	left, right, ok := strings.Cut(r, "-")
	if !ok {
		return 0, 0, 0, 0
	}
	if lStr, cStr, hasCol := strings.Cut(left, ":"); hasCol {
		lineStart, _ = strconv.Atoi(lStr)
		colStart, _ = strconv.Atoi(cStr)
		if lStr2, cStr2, hasCol2 := strings.Cut(right, ":"); hasCol2 {
			lineEnd, _ = strconv.Atoi(lStr2)
			colEnd, _ = strconv.Atoi(cStr2)
		}
		return
	}
	lineStart, _ = strconv.Atoi(left)
	lineEnd, _ = strconv.Atoi(right)
	return
}

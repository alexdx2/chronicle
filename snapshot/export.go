package snapshot

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// ExportOptions carries the two values FromStore cannot derive from the
// store alone. Registry is a fingerprint string (RegistryFingerprint), not
// a *registry.Registry, so FromStore's only dependency is store.Store —
// callers resolve the registry and hash it themselves, once, the same way
// they resolve PrevSeq.
type ExportOptions struct {
	PrevSeq  int    // seq of the file being replaced; the new one is PrevSeq+1
	Registry string // fingerprint of the active registry (RegistryFingerprint)
	// Warn receives one line per export-time inconsistency (an edge dropped
	// because an endpoint is not exported). Nil writes to stderr, so a
	// dropped relationship is never silent.
	Warn func(msg string)
}

// FromStore exports the live, current state of st into a Snapshot — the
// content Encode will later normalize, canonicalize and write as
// graph.jsonl. Only current rows are considered: ListNodes/ListEdges
// already exclude versioned-out rows (valid_to_revision_id set). From
// there:
//
//   - a node with status "deleted" is dropped entirely — a deleted fact's
//     canonical representation is its absence, so a later `git diff` on
//     graph.jsonl shows a removed line rather than a status flip. Every
//     other status (active, stale, external, ...) is written as-is.
//   - an edge whose type is derived (IsDerivedEdgeType) is dropped
//     entirely: the edge itself is the recomputable part, not just its
//     evidence.
//   - an edge with an endpoint that is not exported (deleted, or missing) is
//     dropped and reported through ExportOptions.Warn: Decode would drop it
//     as a dangling_edge anyway, and it must not do so silently.
//   - evidence rows that are derived (IsDerivedEvidence) are dropped from
//     whichever node or edge carried them. A node or edge that loses ALL of
//     its evidence this way is still written, with an empty evidence list —
//     it is a real graph fact (not itself a derived edge type), just with
//     no non-derived provenance left to show.
//
// Aliases, glossary terms (every domain) and file coverage are collected in
// full; nothing in either is derived.
func FromStore(st *store.Store, opt ExportOptions) (*Snapshot, error) {
	snapNodes, err := exportNodes(st)
	if err != nil {
		return nil, err
	}
	warn := opt.Warn
	if warn == nil {
		warn = func(msg string) { fmt.Fprintln(os.Stderr, "chronicle: snapshot: "+msg) }
	}
	snapEdges, err := exportEdges(st, snapNodes, warn)
	if err != nil {
		return nil, err
	}
	snapTerms, err := exportTerms(st)
	if err != nil {
		return nil, err
	}
	coverage, err := st.ListCoverage("")
	if err != nil {
		return nil, fmt.Errorf("snapshot: FromStore: list coverage: %w", err)
	}
	domains, err := st.GetDomains()
	if err != nil {
		return nil, fmt.Errorf("snapshot: FromStore: get domains: %w", err)
	}

	return &Snapshot{
		Header: Header{
			Kind:       "header",
			Format:     Format,
			Version:    Version,
			Seq:        opt.PrevSeq + 1,
			Domains:    domains,
			FactSchema: FactSchema,
			Registry:   opt.Registry,
		},
		Nodes:    snapNodes,
		Edges:    snapEdges,
		Terms:    snapTerms,
		Coverage: groupCoverage(coverage),
	}, nil
}

// exportNodes lists every current, non-deleted node with its attrs,
// metadata, aliases and filtered evidence.
func exportNodes(st *store.Store) ([]Node, error) {
	rows, err := st.ListNodes(store.NodeFilter{})
	if err != nil {
		return nil, fmt.Errorf("snapshot: FromStore: list nodes: %w", err)
	}

	out := make([]Node, 0, len(rows))
	for _, n := range rows {
		if n.Status == "deleted" {
			continue
		}
		ev, err := st.ListEvidenceByNode(n.NodeID)
		if err != nil {
			return nil, fmt.Errorf("snapshot: FromStore: evidence for node %q: %w", n.NodeKey, err)
		}
		aliases, err := st.ListAliasesByNode(n.NodeID)
		if err != nil {
			return nil, fmt.Errorf("snapshot: FromStore: aliases for node %q: %w", n.NodeKey, err)
		}
		out = append(out, Node{
			Kind:          "node",
			ID:            n.NodeKey,
			Layer:         n.Layer,
			Type:          n.NodeType,
			Domain:        n.DomainKey,
			Name:          n.Name,
			QualifiedName: n.QualifiedName,
			File:          n.FilePath,
			Lang:          n.Lang,
			Status:        n.Status,
			Attrs:         nodeAttrs(n),
			Metadata:      rawOrNil(n.Metadata),
			Aliases:       toAliases(aliases),
			Evidence:      filterEvidence(ev),
		})
	}
	return out, nil
}

// exportEdges lists every current edge whose type is not itself derived,
// batch-loading their evidence in one round trip.
func exportEdges(st *store.Store, nodes []Node, warn func(string)) ([]Edge, error) {
	present := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		present[n.ID] = true
	}
	rows, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		return nil, fmt.Errorf("snapshot: FromStore: list edges: %w", err)
	}

	kept := make([]store.EdgeRow, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, e := range rows {
		if IsDerivedEdgeType(e.EdgeType) {
			continue
		}
		if !present[e.FromNodeKey] || !present[e.ToNodeKey] {
			warn(fmt.Sprintf("dangling_edge: edge %q dropped from the export: endpoint %q or %q is not an exported node",
				e.EdgeKey, e.FromNodeKey, e.ToNodeKey))
			continue
		}
		kept = append(kept, e)
		ids = append(ids, e.EdgeID)
	}

	evByEdge, err := st.ListEvidenceByEdgeIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("snapshot: FromStore: evidence for edges: %w", err)
	}

	out := make([]Edge, 0, len(kept))
	for _, e := range kept {
		out = append(out, Edge{
			Kind:             "edge",
			ID:               e.EdgeKey,
			Type:             e.EdgeType,
			From:             e.FromNodeKey,
			To:               e.ToNodeKey,
			Derivation:       e.DerivationKind,
			DependencySource: e.DependencySource,
			Context:          e.ContextKey,
			Active:           e.Active,
			Metadata:         rawOrNil(e.Metadata),
			Evidence:         filterEvidence(evByEdge[e.EdgeID]),
		})
	}
	return out, nil
}

// exportTerms lists the domain glossary across every domain in one call —
// nothing in it is derived, so there is no filtering to do here.
func exportTerms(st *store.Store) ([]Term, error) {
	terms, err := st.GetGlossary("")
	if err != nil {
		return nil, fmt.Errorf("snapshot: FromStore: glossary: %w", err)
	}
	out := make([]Term, 0, len(terms))
	for _, t := range terms {
		out = append(out, Term{
			Kind:         "term",
			ID:           t.DomainKey + "#" + t.Term,
			Domain:       t.DomainKey,
			Term:         t.Term,
			Aliases:      t.Aliases,
			AntiPatterns: t.AntiPatterns,
			Description:  t.Description,
			Context:      t.Context,
			Examples:     t.Examples,
		})
	}
	return out, nil
}

// nodeAttrs collects the six rarely-populated node fields the canonical
// format keeps out of the top-level record, only when non-empty — the
// exact set Global Constraints names, no more.
func nodeAttrs(n store.NodeRow) map[string]string {
	attrs := make(map[string]string, 6)
	if n.OwnerKey != "" {
		attrs["owner_key"] = n.OwnerKey
	}
	if n.Environment != "" {
		attrs["environment"] = n.Environment
	}
	if n.Visibility != "" {
		attrs["visibility"] = n.Visibility
	}
	if n.SymbolName != "" {
		attrs["symbol_name"] = n.SymbolName
	}
	if n.SupportKind != "" {
		attrs["support_kind"] = n.SupportKind
	}
	if n.RepoName != "" {
		attrs["repo_name"] = n.RepoName
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// rawOrNil turns a store metadata/assertion column — often "", "{}", or a
// real JSON object — into a json.RawMessage, or nil when blank. Encode's
// own canonicalization collapses "{}"/"null" to nil too; nil-ing an empty
// string here just avoids handing Encode a zero-length RawMessage that
// isn't valid JSON on its own.
func rawOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func toAliases(rows []store.AliasRow) []Alias {
	if len(rows) == 0 {
		return nil
	}
	out := make([]Alias, 0, len(rows))
	for _, a := range rows {
		out = append(out, Alias{Alias: a.Alias, Kind: a.AliasKind, Confidence: a.Confidence})
	}
	return out
}

// filterEvidence drops derived evidence (IsDerivedEvidence) and maps what's
// left to the canonical Evidence record. A nil result is expected and
// correct for a fact whose only evidence was derived — see FromStore's doc
// comment.
func filterEvidence(rows []store.EvidenceRow) []Evidence {
	var out []Evidence
	for _, e := range rows {
		if IsDerivedEvidence(e.SourceKind, e.ExtractorID) {
			continue
		}
		out = append(out, Evidence{
			ID:               e.EvidenceUID,
			Source:           e.SourceKind,
			File:             e.FilePath,
			Blob:             e.BlobSHA,
			Range:            formatRange(e),
			Locator:          e.Locator,
			Extractor:        e.ExtractorID,
			ExtractorVersion: e.ExtractorVersion,
			Rule:             e.ASTRule,
			Polarity:         e.EvidencePolarity,
			Status:           e.EvidenceStatus,
			Verification:     e.VerificationStatus,
			Confidence:       e.Confidence,
			Assertion:        rawOrNil(e.Assertion),
			AssertionKind:    e.AssertionKind,
			AssertionVersion: e.AssertionVersion,
		})
	}
	return out
}

// formatRange renders an evidence row's source location: "L:C-L:C" when
// columns are known, "L-L" when only lines are, empty when neither is (a
// non-textual source: manifest, git history). A present start with no
// matching end (line or column) defaults the end to the start, so a
// single-point location still renders instead of silently losing the
// range.
func formatRange(e store.EvidenceRow) string {
	if e.LineStart <= 0 {
		return ""
	}
	lineEnd := e.LineEnd
	if lineEnd <= 0 {
		lineEnd = e.LineStart
	}
	if e.ColumnStart > 0 {
		colEnd := e.ColumnEnd
		if colEnd <= 0 {
			colEnd = e.ColumnStart
		}
		return fmt.Sprintf("%d:%d-%d:%d", e.LineStart, e.ColumnStart, lineEnd, colEnd)
	}
	return fmt.Sprintf("%d-%d", e.LineStart, lineEnd)
}

// groupCoverage folds per-(domain, file, extractor) coverage rows into one
// canonical Coverage record per file path — the canonical format has no
// domain field on Coverage, so a file's coverage is project-wide, not
// per-domain. ListCoverage("") orders rows by (file_path, extractor_id), so
// rows for the same path already arrive contiguous and extractor-sorted;
// grouping relies on that order instead of re-sorting, which keeps this
// deterministic.
//
// The same (path, extractor) can have a row per domain — a restored snapshot
// writes under one domain and a later run under the file's real one. The
// newest observation wins (ties: the row that has a blob), and the path emits
// one ExtractorRun per extractor. Path Blob is the blob of the best row
// across extractors by the same rule.
func groupCoverage(rows []store.CoverageRow) []Coverage {
	better := func(a, b store.CoverageRow) bool { // is a better than b?
		if a.ObservedAt != b.ObservedAt {
			return a.ObservedAt > b.ObservedAt
		}
		return a.BlobSHA != "" && b.BlobSHA == ""
	}
	var out []Coverage
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].FilePath == rows[i].FilePath {
			j++
		}
		cov := Coverage{Kind: "coverage", Path: rows[i].FilePath}
		var best *store.CoverageRow
		for k := i; k < j; {
			m := k
			for m < j && rows[m].ExtractorID == rows[k].ExtractorID {
				m++
			}
			pick := &rows[k]
			for n := k + 1; n < m; n++ {
				if better(rows[n], *pick) {
					pick = &rows[n]
				}
			}
			cov.Extractors = append(cov.Extractors, ExtractorRun{
				ID: pick.ExtractorID, Version: pick.ExtractorVersion, Status: pick.Status,
			})
			if best == nil || better(*pick, *best) {
				best = pick
			}
			k = m
		}
		cov.Blob = best.BlobSHA
		out = append(out, cov)
		i = j
	}
	return out
}

// RegistryFingerprint returns a stable "sha256:<hex>" fingerprint of reg's
// effective schema (layers, node/edge types, derivation kinds, statuses,
// salience roles), via its own ToSchemaJSON. ToSchemaJSON builds several of
// those fields by ranging over reg's internal maps, whose iteration order
// Go deliberately randomizes from call to call; left alone, the "same"
// registry would fingerprint differently on every run, which would make
// `chronicle graph update --check` spuriously fail even when nothing
// changed. Every such slice is sorted here before hashing so the
// fingerprint is a pure function of the registry's content, independent of
// map iteration order — matching this package's own byte-for-byte
// determinism contract.
func RegistryFingerprint(reg *registry.Registry) string {
	if reg == nil {
		return ""
	}
	schema := reg.ToSchemaJSON("", "", nil)
	sort.Strings(schema.Layers)
	sort.Strings(schema.DerivationKinds)
	sort.Strings(schema.NodeStatuses)
	sort.Strings(schema.SalienceRoles)
	for k := range schema.NodeTypes {
		sort.Strings(schema.NodeTypes[k])
	}

	b, err := marshalCompact(schema)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ReadHeader reads only the first line of a graph.jsonl-shaped stream and
// decodes it as a Header — the cheap alternative to a full Decode when all
// a caller needs is the previous file's Seq (`chronicle graph update`) or a
// summary (`chronicle graph show`). It never reads past the first line, and
// applies the same size limits Decode does so a hostile or corrupt first
// line cannot exhaust memory.
func ReadHeader(r io.Reader) (Header, error) {
	lim := DefaultLimits()
	sc := bufio.NewScanner(io.LimitReader(r, lim.MaxBytes))
	sc.Buffer(make([]byte, 0, initialScanBuf(lim.MaxLine)), lim.MaxLine)

	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return Header{}, fmt.Errorf("snapshot: ReadHeader: %w", err)
		}
		return Header{}, fmt.Errorf("snapshot: ReadHeader: empty input")
	}
	line := sc.Bytes()

	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return Header{}, fmt.Errorf("snapshot: ReadHeader: %w", err)
	}
	if probe.Kind != "header" {
		return Header{}, fmt.Errorf("snapshot: ReadHeader: first line has kind %q, want %q", probe.Kind, "header")
	}

	var h Header
	if err := json.Unmarshal(line, &h); err != nil {
		return Header{}, fmt.Errorf("snapshot: ReadHeader: %w", err)
	}
	return h, nil
}

// ChangeSummary counts how one snapshot's records differ from another's, by
// record identity (node/edge/term ID, coverage Path).
type ChangeSummary struct{ Added, Removed, Changed int }

// Summarize compares prev and next record-by-record — nodes, edges and
// terms by ID, coverage by Path — and reports how many exist only in next
// (Added), only in prev (Removed), or in both with different content
// (Changed). Like Encode, it normalizes both snapshots and canonicalizes
// their raw JSON sub-fields in place as it goes (same operation, same
// documented side effect); two snapshots differing only in record order, or
// only in metadata/assertion JSON formatting, therefore compare as
// unchanged — matching what Encode would actually write for each.
func Summarize(prev, next *Snapshot) ChangeSummary {
	prevRecs := canonicalRecordBytes(prev)
	nextRecs := canonicalRecordBytes(next)

	var cs ChangeSummary
	for key, nb := range nextRecs {
		pb, ok := prevRecs[key]
		switch {
		case !ok:
			cs.Added++
		case !bytes.Equal(pb, nb):
			cs.Changed++
		}
	}
	for key := range prevRecs {
		if _, ok := nextRecs[key]; !ok {
			cs.Removed++
		}
	}
	return cs
}

// canonicalRecordBytes builds a "kind:identity" -> canonical-JSON-bytes map
// for every record in s, for Summarize's content comparison. Canonicalization
// errors are ignored (best-effort): s holds data straight from a store or a
// tolerant Decode, never handler-validated JSON, and a record that fails to
// canonicalize still compares — just on its less-normalized bytes — rather
// than vanishing from the summary.
func canonicalRecordBytes(s *Snapshot) map[string][]byte {
	Normalize(s)
	out := make(map[string][]byte, len(s.Nodes)+len(s.Edges)+len(s.Terms)+len(s.Coverage))
	for i := range s.Nodes {
		_ = canonicalizeNodeRaw(&s.Nodes[i])
		b, _ := marshalCompact(s.Nodes[i])
		out["node:"+s.Nodes[i].ID] = b
	}
	for i := range s.Edges {
		_ = canonicalizeEdgeRaw(&s.Edges[i])
		b, _ := marshalCompact(s.Edges[i])
		out["edge:"+s.Edges[i].ID] = b
	}
	for _, t := range s.Terms {
		b, _ := marshalCompact(t)
		out["term:"+t.ID] = b
	}
	for _, c := range s.Coverage {
		b, _ := marshalCompact(c)
		out["coverage:"+c.Path] = b
	}
	return out
}

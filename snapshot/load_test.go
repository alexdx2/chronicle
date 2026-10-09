package snapshot

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	return reg
}

// TestLoad_RoundTrip proves Load is FromStore's mirror image: exporting the
// fixture, loading the result into a fresh store, and exporting THAT store
// again produces byte-identical graph.jsonl content (via Encode) to the
// first export. Two independent FromStore calls are used (rather than
// reusing one Snapshot value across both the "want" comparison and the
// value fed to Load) because Encode documents that it mutates its argument
// in place (Normalize + raw-JSON canonicalization) — TestFromStore_Deterministic
// already establishes that two exports of the same unchanged store are
// byte-identical, so this doesn't weaken the assertion.
func TestLoad_RoundTrip(t *testing.T) {
	s := copyFixtureStore(t)
	reg := testRegistry(t)
	opt := ExportOptions{PrevSeq: 4, Registry: "sha256:abc"}

	want, err := FromStore(s, opt)
	if err != nil {
		t.Fatalf("FromStore (want): %v", err)
	}
	var wantBuf bytes.Buffer
	if err := Encode(&wantBuf, want); err != nil {
		t.Fatalf("Encode (want): %v", err)
	}
	if wantBuf.Len() == 0 {
		t.Fatal("original export is empty — test fixture is not exercising anything")
	}

	forLoad, err := FromStore(s, opt)
	if err != nil {
		t.Fatalf("FromStore (forLoad): %v", err)
	}

	g, closer, err := Load(forLoad, reg, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer closer()

	got, err := FromStore(g.Store(), opt)
	if err != nil {
		t.Fatalf("FromStore (reloaded): %v", err)
	}
	var gotBuf bytes.Buffer
	if err := Encode(&gotBuf, got); err != nil {
		t.Fatalf("Encode (reloaded): %v", err)
	}

	if !bytes.Equal(wantBuf.Bytes(), gotBuf.Bytes()) {
		t.Fatalf("round trip mismatch:\n--- original ---\n%s\n--- reloaded ---\n%s",
			wantBuf.String(), gotBuf.String())
	}
}

// TestLoad_TrustRecomputed proves trust is actually recomputed to match the
// original — not merely present or non-zero — for every node and edge whose
// ORIGINAL stored trust is itself consistent with its own current evidence.
//
// The exclusion (originalConsistentWithOwnEvidence, below) is structural, not
// a hardcoded key list: it recomputes graph.ComputeTrust — the exact function
// RecalculateNodeTrust/RecalculateEdgeTrust call — directly against the
// ORIGINAL store's own current evidence for that node/edge (not the
// snapshot's, which may have dropped derived rows) and skips only when that
// disagrees with what the fixture has stored, i.e. the fixture's stored trust
// predates some earlier, unrelated change to its evidence (or was set some
// other way entirely) and there is no correct value left to hold Load to.
// Verified directly against the fixture, this excludes exactly 3 of 126
// nodes and 0 of 198 edges:
//   - infra:broker:battle-result: one evidence row whose status is "stale" —
//     recomputing gives confidence 0, but the fixture still stores 0.6, i.e.
//     nothing recomputed it after that transition.
//   - contract:endpoint:tom-and-jerry:post:/email/digest and .../post/push:
//     materialized "external" boundary-marker nodes with a hardcoded
//     confidence 0.8 and no evidence at all (see graph's
//     materializeExternalEndpoints) — there is nothing to recompute from.
//
// tom-and-jerry's 19 flow:use_case nodes are NOT excluded: their original
// evidence (one chronicle:derive_flows row, confidence 0.6) recomputes to
// exactly the fixture's stored 0.6/1.0/0.6, and after the
// graph/derive_flows.go fix (ensureFlowNode now re-asserts a flow node's
// evidence even when the node already exists, not just on first creation)
// Load restores that evidence too, so these nodes are included and checked
// like any other — this is what closes that finding: this test no longer
// looks away from them.
func TestLoad_TrustRecomputed(t *testing.T) {
	s := copyFixtureStore(t)
	reg := testRegistry(t)

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}

	g, closer, err := Load(snap, reg, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer closer()

	const eps = 1e-9

	// originalConsistentWithOwnEvidence reports whether trust (confidence,
	// freshness, trustScore) already matches what graph.ComputeTrust derives
	// from evidence — the set of rows passed in is the caller's choice, so
	// this works for both a node's ListEvidenceByNode and an edge's
	// ListEvidenceByEdge.
	originalConsistentWithOwnEvidence := func(confidence, freshness, trustScore float64, evidence []store.EvidenceRow) (bool, float64, float64, float64) {
		impliedConf, impliedFresh, impliedTrust, _ := graph.ComputeTrust(evidence)
		ok := math.Abs(confidence-impliedConf) <= eps &&
			math.Abs(freshness-impliedFresh) <= eps &&
			math.Abs(trustScore-impliedTrust) <= eps
		return ok, impliedConf, impliedFresh, impliedTrust
	}

	nodesCompared, nodesSkipped := 0, 0
	for _, n := range snap.Nodes {
		orig, err := s.GetNodeByKey(n.ID)
		if err != nil {
			t.Fatalf("original store: GetNodeByKey(%q): %v", n.ID, err)
		}
		origEvidence, err := s.ListEvidenceByNode(orig.NodeID)
		if err != nil {
			t.Fatalf("original store: ListEvidenceByNode(%q): %v", n.ID, err)
		}
		ok, impliedConf, impliedFresh, impliedTrust := originalConsistentWithOwnEvidence(
			orig.Confidence, orig.Freshness, orig.TrustScore, origEvidence)
		if !ok {
			nodesSkipped++
			t.Logf("node %s: original stored trust (%.4f/%.4f/%.4f) already disagrees with what its own current evidence implies (%.4f/%.4f/%.4f) — skipping as a pre-existing fixture artifact, not a Load defect",
				n.ID, orig.Confidence, orig.Freshness, orig.TrustScore, impliedConf, impliedFresh, impliedTrust)
			continue
		}

		got, err := g.Store().GetNodeByKey(n.ID)
		if err != nil {
			t.Fatalf("reloaded store: GetNodeByKey(%q): %v", n.ID, err)
		}
		if math.Abs(orig.Confidence-got.Confidence) > eps {
			t.Errorf("node %s: confidence = %v, want %v (original, within %g)", n.ID, got.Confidence, orig.Confidence, eps)
		}
		if math.Abs(orig.Freshness-got.Freshness) > eps {
			t.Errorf("node %s: freshness = %v, want %v (original, within %g)", n.ID, got.Freshness, orig.Freshness, eps)
		}
		if math.Abs(orig.TrustScore-got.TrustScore) > eps {
			t.Errorf("node %s: trust_score = %v, want %v (original, within %g)", n.ID, got.TrustScore, orig.TrustScore, eps)
		}
		nodesCompared++
	}
	if nodesCompared == 0 {
		t.Fatal("no nodes were compared — test fixture is not exercising anything")
	}
	t.Logf("compared trust on %d nodes (%d skipped: original already stale relative to its own evidence)", nodesCompared, nodesSkipped)

	// Edge trust is a separate code path (RecalculateEdgeTrust) from node
	// trust (RecalculateNodeTrust) and needs its own proof, not an inference
	// from the node case above.
	edgesCompared, edgesSkipped := 0, 0
	for _, e := range snap.Edges {
		orig, err := s.GetEdgeByKey(e.ID)
		if err != nil {
			t.Fatalf("original store: GetEdgeByKey(%q): %v", e.ID, err)
		}
		origEvidence, err := s.ListEvidenceByEdge(orig.EdgeID)
		if err != nil {
			t.Fatalf("original store: ListEvidenceByEdge(%q): %v", e.ID, err)
		}
		ok, impliedConf, impliedFresh, impliedTrust := originalConsistentWithOwnEvidence(
			orig.Confidence, orig.Freshness, orig.TrustScore, origEvidence)
		if !ok {
			edgesSkipped++
			t.Logf("edge %s: original stored trust (%.4f/%.4f/%.4f) already disagrees with what its own current evidence implies (%.4f/%.4f/%.4f) — skipping as a pre-existing fixture artifact, not a Load defect",
				e.ID, orig.Confidence, orig.Freshness, orig.TrustScore, impliedConf, impliedFresh, impliedTrust)
			continue
		}

		got, err := g.Store().GetEdgeByKey(e.ID)
		if err != nil {
			t.Fatalf("reloaded store: GetEdgeByKey(%q): %v", e.ID, err)
		}
		if math.Abs(orig.Confidence-got.Confidence) > eps {
			t.Errorf("edge %s: confidence = %v, want %v (original, within %g)", e.ID, got.Confidence, orig.Confidence, eps)
		}
		if math.Abs(orig.Freshness-got.Freshness) > eps {
			t.Errorf("edge %s: freshness = %v, want %v (original, within %g)", e.ID, got.Freshness, orig.Freshness, eps)
		}
		if math.Abs(orig.TrustScore-got.TrustScore) > eps {
			t.Errorf("edge %s: trust_score = %v, want %v (original, within %g)", e.ID, got.TrustScore, orig.TrustScore, eps)
		}
		edgesCompared++
	}
	if edgesCompared == 0 {
		t.Fatal("no edges were compared — test fixture is not exercising anything")
	}
	t.Logf("compared trust on %d edges (%d skipped: original already stale relative to its own evidence)", edgesCompared, edgesSkipped)
}

// TestLoad_NoJournalWrites proves SetJournaling(false) produces zero journal
// writes with real assertions against the loaded store's own tables and
// files, not by reasoning about the code: journal_outbox and graph_changelog
// must both be empty, an explicit FlushJournal call after Load must report
// zero events flushed, and it must not create a .depbot/events/-equivalent
// directory next to the loaded db.
func TestLoad_NoJournalWrites(t *testing.T) {
	s := copyFixtureStore(t)
	reg := testRegistry(t)

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}

	g, closer, err := Load(snap, reg, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer closer()

	st := g.Store()

	var outboxCount int
	if err := st.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox`, &outboxCount); err != nil {
		t.Fatalf("count journal_outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Errorf("journal_outbox has %d rows, want 0", outboxCount)
	}

	var changelogCount int
	if err := st.QueryRowScan(`SELECT COUNT(*) FROM graph_changelog`, &changelogCount); err != nil {
		t.Fatalf("count graph_changelog: %v", err)
	}
	if changelogCount != 0 {
		t.Errorf("graph_changelog has %d rows, want 0", changelogCount)
	}

	n, err := st.FlushJournal()
	if err != nil {
		t.Fatalf("FlushJournal: %v", err)
	}
	if n != 0 {
		t.Errorf("FlushJournal flushed %d events, want 0", n)
	}

	if _, err := os.Stat(filepath.Join(st.Dir(), "events")); !os.IsNotExist(err) {
		t.Errorf("events directory exists after FlushJournal (err=%v), want it never created", err)
	}
}

// TestLoad_TermsAndCoverage exercises loadAliasesTermsCoverage's term and
// file-coverage restoration — the one part of Load neither the tom-and-jerry
// fixture (0 domain_language rows, no file_coverage table at all in the
// tracked db) nor buildSyntheticSnapshot's performance fixture (nodes/edges/
// evidence only) touches at all. Built by hand rather than derived from a
// store, since nothing else in this file already has term/coverage data to
// reuse.
func TestLoad_TermsAndCoverage(t *testing.T) {
	reg := testRegistry(t)

	snap := &Snapshot{
		Header: Header{
			Kind: "header", Format: Format, Version: Version, FactSchema: FactSchema,
			Domains: []string{"testdomain"},
		},
		Terms: []Term{
			{
				Kind: "term", ID: "testdomain#Order", Domain: "testdomain", Term: "Order",
				Aliases: []string{"PO", "purchase order"}, AntiPatterns: []string{"Invoice"},
				Description: "A customer order.", Context: "commerce", Examples: []string{"order-123"},
			},
		},
		Coverage: []Coverage{
			{
				Kind: "coverage", Path: "src/order.service.ts", Blob: "abc123",
				Extractors: []ExtractorRun{
					{ID: "chronicle-ast", Version: "1", Status: "extracted"},
					{ID: "chronicle-scan", Version: "fact-schema@1", Status: "extracted"},
				},
			},
		},
	}

	g, closer, err := Load(snap, reg, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer closer()

	terms, err := g.Store().GetGlossary("testdomain")
	if err != nil {
		t.Fatalf("GetGlossary: %v", err)
	}
	if len(terms) != 1 {
		t.Fatalf("len(terms) = %d, want 1: %+v", len(terms), terms)
	}
	term := terms[0]
	if term.Term != "Order" || term.DomainKey != "testdomain" || term.Description != "A customer order." ||
		term.Context != "commerce" ||
		len(term.Aliases) != 2 || term.Aliases[0] != "PO" || term.Aliases[1] != "purchase order" ||
		len(term.AntiPatterns) != 1 || term.AntiPatterns[0] != "Invoice" ||
		len(term.Examples) != 1 || term.Examples[0] != "order-123" {
		t.Errorf("term = %+v, want the fields from the snapshot's Term record", term)
	}

	coverage, err := g.Store().ListCoverage("")
	if err != nil {
		t.Fatalf("ListCoverage: %v", err)
	}
	if len(coverage) != 2 {
		t.Fatalf("len(coverage rows) = %d, want 2 (one row per extractor): %+v", len(coverage), coverage)
	}
	byExtractor := map[string]store.CoverageRow{}
	for _, c := range coverage {
		byExtractor[c.ExtractorID] = c
	}
	ast, ok := byExtractor["chronicle-ast"]
	if !ok {
		t.Fatalf("missing coverage row for chronicle-ast: %+v", coverage)
	}
	if ast.FilePath != "src/order.service.ts" || ast.ExtractorVersion != "1" || ast.Status != "extracted" || ast.BlobSHA != "abc123" {
		t.Errorf("chronicle-ast coverage row = %+v, want the fields from the snapshot's Coverage record", ast)
	}
	scan, ok := byExtractor["chronicle-scan"]
	if !ok {
		t.Fatalf("missing coverage row for chronicle-scan: %+v", coverage)
	}
	if scan.FilePath != "src/order.service.ts" || scan.ExtractorVersion != "fact-schema@1" || scan.Status != "extracted" || scan.BlobSHA != "abc123" {
		t.Errorf("chronicle-scan coverage row = %+v, want the fields from the snapshot's Coverage record", scan)
	}

	// Re-exporting must reproduce the same grouped Term/Coverage shape —
	// this is the actual round trip loadAliasesTermsCoverage exists to
	// support, not just individual rows landing in the right table.
	reExported, err := FromStore(g.Store(), ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore (reloaded): %v", err)
	}
	if len(reExported.Terms) != 1 {
		t.Fatalf("re-export: len(Terms) = %d, want 1", len(reExported.Terms))
	}
	if len(reExported.Coverage) != 1 {
		t.Fatalf("re-export: len(Coverage) = %d, want 1 (one record per path)", len(reExported.Coverage))
	}
	if got := len(reExported.Coverage[0].Extractors); got != 2 {
		t.Errorf("re-export: Coverage[0].Extractors has %d entries, want 2: %+v", got, reExported.Coverage[0].Extractors)
	}
}

// buildSyntheticSnapshot builds a Snapshot with numNodes nodes, numEdges
// edges (spread across the nodes so From/To are always valid keys) and
// numEvidence evidence records (round-robined onto nodes, each given a
// distinct Range so AddEvidence's dedup key never collapses two of them into
// one row) — enough volume to exercise Load's full pipeline (bulk insert,
// DeriveFlows, RecalculateAllTrust) at the scale TestLoad_Performance checks.
func buildSyntheticSnapshot(numNodes, numEdges, numEvidence int) *Snapshot {
	nodeKey := func(i int) string { return fmt.Sprintf("code:provider:perf:n%d", i) }

	snap := &Snapshot{
		Header: Header{
			Kind: "header", Format: Format, Version: Version, FactSchema: FactSchema,
			Domains: []string{"perf"},
		},
		Nodes: make([]Node, numNodes),
		Edges: make([]Edge, numEdges),
	}

	for i := 0; i < numNodes; i++ {
		snap.Nodes[i] = Node{
			Kind: "node", ID: nodeKey(i), Layer: "code", Type: "provider", Domain: "perf",
			Name: fmt.Sprintf("N%d", i), Status: "active",
		}
	}

	edgeTypes := []string{"CALLS", "IMPORTS", "REFERENCES", "USES", "SENDS_TO"}
	for i := 0; i < numEdges; i++ {
		from := i % numNodes
		to := (i*7 + 13) % numNodes
		if to == from {
			to = (to + 1) % numNodes
		}
		edgeType := edgeTypes[i%len(edgeTypes)]
		fromKey, toKey := nodeKey(from), nodeKey(to)
		snap.Edges[i] = Edge{
			Kind: "edge", ID: fromKey + "->" + toKey + ":" + edgeType,
			Type: edgeType, From: fromKey, To: toKey,
			Derivation: "hard", Active: true,
		}
	}

	for k := 0; k < numEvidence; k++ {
		n := &snap.Nodes[k%numNodes]
		n.Evidence = append(n.Evidence, Evidence{
			ID:               fmt.Sprintf("ev-%d", k),
			Source:           "file",
			File:             fmt.Sprintf("src/file%d.go", k%500),
			Range:            fmt.Sprintf("%d-%d", k+1, k+1),
			Extractor:        "chronicle-scan",
			ExtractorVersion: "1",
			Confidence:       0.9,
			Status:           "valid",
			Polarity:         "positive",
		})
	}

	return snap
}

// TestLoad_Performance is the brief's synthetic-volume timing check — 5,000
// nodes, 16,000 edges, 26,000 evidence, under 20s — skipped under -short so
// it never slows down the ordinary test loop.
//
// Fix round 1: this was temporarily run at reduced evidence volume (1,000,
// not 26,000) because store.AddEvidence's dedup lookup is a full table scan
// regardless of table size (its WHERE clause wraps every comparison column
// in COALESCE(...), which defeats every index on graph_evidence), making N
// sequential AddEvidence calls O(N^2) — 26,000 extrapolated to roughly two
// minutes against the unmodified function. Load now inserts evidence via
// store.InsertEvidenceBatch instead (see Load's and InsertEvidenceBatch's own
// doc comments), which skips the dedup lookup entirely — safe here because a
// snapshot's evidence is already deduplicated by construction and Load always
// targets a brand-new, empty store. The brief's original numbers are restored
// below now that they hold in practice (see the task report for the measured
// time).
func TestLoad_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("performance test skipped under -short")
	}
	reg := testRegistry(t)

	const numNodes, numEdges, numEvidence = 5000, 16000, 26000
	snap := buildSyntheticSnapshot(numNodes, numEdges, numEvidence)

	start := time.Now()
	g, closer, err := Load(snap, reg, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer closer()
	elapsed := time.Since(start)

	t.Logf("Load(%d nodes, %d edges, %d evidence) took %s", numNodes, numEdges, numEvidence, elapsed)
	if elapsed > 20*time.Second {
		t.Errorf("Load took %s, want < 20s", elapsed)
	}

	var nodeCount int
	if err := g.Store().QueryRowScan(`SELECT COUNT(*) FROM graph_nodes`, &nodeCount); err != nil {
		t.Fatalf("count graph_nodes: %v", err)
	}
	if nodeCount != numNodes {
		t.Errorf("graph_nodes count = %d, want %d", nodeCount, numNodes)
	}
}

// TestLoad_TempDBCleanup proves the closer returned for DBPath == "" actually
// removes the temporary database — not just that a closer function exists —
// by capturing the directory containing the temp db file and checking it is
// gone after calling the closer.
func TestLoad_TempDBCleanup(t *testing.T) {
	s := copyFixtureStore(t)
	reg := testRegistry(t)

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}

	g, closer, err := Load(snap, reg, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dbDir := g.Store().Dir()
	if dbDir == "" || dbDir == "." {
		t.Fatalf("loaded store's Dir() = %q, want a real temp directory", dbDir)
	}
	if _, err := os.Stat(dbDir); err != nil {
		t.Fatalf("temp db directory %q should exist before closing: %v", dbDir, err)
	}

	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}

	if _, err := os.Stat(dbDir); !os.IsNotExist(err) {
		t.Fatalf("temp db directory %q still exists after closer (err=%v), want it removed", dbDir, err)
	}
}

// TestLoad_ExplicitDBPathNotDeleted proves the DBPath != "" path is honored
// literally: Load uses the caller's path (not a temp dir), and the closer
// closes the connection but leaves the caller's file in place for them to
// manage — the doc comment's "the caller's to delete" half of the contract.
func TestLoad_ExplicitDBPathNotDeleted(t *testing.T) {
	s := copyFixtureStore(t)
	reg := testRegistry(t)

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "explicit.db")
	g, closer, err := Load(snap, reg, LoadOptions{DBPath: dbPath})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := g.Store().Dir(); got != filepath.Dir(dbPath) {
		t.Errorf("Dir() = %q, want %q", got, filepath.Dir(dbPath))
	}

	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("explicit DBPath %q should still exist after closer: %v", dbPath, err)
	}
}

// TestLoad_StaleMarkedEdgeStaysInactive pins Load against the way the real
// scan pipeline records a removal: never a deleted row, always a soft delete.
// A full rescan re-observes every edge it still finds (UpsertEdge with the new
// revision's id) and chronicle_stale_mark (store.MarkStaleEdges) then sets
// active=0 on the one it did not find, staling its evidence too. The exported
// snapshot keeps that edge with "active": false (spec §4.3), and Load must
// reproduce it as inactive — a reloaded graph where a removed call is live
// again would hand every impact query on it a dependency the code no longer
// has, and a FromStore → Load → FromStore round trip would silently flip the
// recorded fact.
//
// The trap this guards against: RecalculateAllTrust derives an edge's status
// from its evidence, and store.UpdateEdgeTrust maps every status except
// "removed"/"contradicted" to active=1 — so an edge whose evidence is merely
// "stale" (exactly what MarkStaleEdges leaves behind) is reactivated by the
// very trust pass Load runs last.
func TestLoad_StaleMarkedEdgeStaysInactive(t *testing.T) {
	s := copyFixtureStore(t)
	s.SetJournaling(false)
	reg := testRegistry(t)
	const target = "code:provider:tom-and-jerry:arena-api/src/arena/jerry.client->contract:endpoint:tom-and-jerry:get:/jerry/traps:CALLS_ENDPOINT"

	rev, err := s.CreateRevision("tom-and-jerry", "", "rescan-sha", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	edges, err := s.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.EdgeKey == target {
			found = true
			continue // the one call the rescan no longer finds
		}
		e.LastSeenRevisionID = rev
		if _, err := s.UpsertEdge(e); err != nil {
			t.Fatalf("re-observe %s: %v", e.EdgeKey, err)
		}
	}
	if !found {
		t.Fatalf("fixture has no edge %s", target)
	}
	n, err := s.MarkStaleEdges("tom-and-jerry", rev)
	if err != nil {
		t.Fatalf("MarkStaleEdges: %v", err)
	}
	if n != 1 {
		t.Fatalf("MarkStaleEdges marked %d edges, want exactly 1 (%s)", n, target)
	}

	opt := ExportOptions{PrevSeq: 1, Registry: "sha256:abc"}
	snap, err := FromStore(s, opt)
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}
	if e := findSnapEdge(snap, target); e == nil || e.Active {
		t.Fatalf("exported edge = %+v, want present with Active=false", e)
	}

	g, closer, err := Load(snap, reg, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer closer()

	loaded, err := g.Store().GetEdgeByKey(target)
	if err != nil {
		t.Fatalf("GetEdgeByKey on loaded store: %v", err)
	}
	if loaded.Active {
		t.Errorf("loaded edge %s is active, want inactive — Load reactivated a stale-marked removal", target)
	}

	again, err := FromStore(g.Store(), opt)
	if err != nil {
		t.Fatalf("FromStore (reloaded): %v", err)
	}
	if e := findSnapEdge(again, target); e == nil || e.Active {
		t.Errorf("re-exported edge = %+v, want present with Active=false (round trip flipped the fact)", e)
	}
	for _, orig := range snap.Edges {
		if e := findSnapEdge(again, orig.ID); e != nil && e.Active != orig.Active {
			t.Errorf("edge %s: Active %v after Load, %v in the snapshot", orig.ID, e.Active, orig.Active)
		}
	}
}

func findSnapEdge(s *Snapshot, id string) *Edge {
	for i := range s.Edges {
		if s.Edges[i].ID == id {
			return &s.Edges[i]
		}
	}
	return nil
}

// With several domains, a coverage row lands under the domain of the node
// that lives in the file — the domain later runs upsert under — not under the
// first header domain.
func TestLoad_CoverageLandsUnderTheFilesDomain(t *testing.T) {
	reg := testRegistry(t)
	node := func(domain, name, file string) Node {
		return Node{
			Kind: "node", ID: "code:provider:" + domain + ":" + name, Layer: "code", Type: "provider",
			Domain: domain, Name: name, File: file, Status: "active",
		}
	}
	snap := &Snapshot{
		Header: Header{Kind: "header", Format: Format, Version: Version, FactSchema: FactSchema, Domains: []string{"a", "b"}},
		Nodes:  []Node{node("a", "A", "src/a.ts"), node("b", "B", "src/b.ts")},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "src/a.ts", Blob: "blobA", Extractors: []ExtractorRun{{ID: "chronicle-scan", Version: "v", Status: "extracted"}}},
			{Kind: "coverage", Path: "src/b.ts", Blob: "blobB", Extractors: []ExtractorRun{{ID: "chronicle-scan", Version: "v", Status: "extracted"}}},
			{Kind: "coverage", Path: "src/orphan.ts", Blob: "blobO", Extractors: []ExtractorRun{{ID: "chronicle-scan", Version: "v", Status: "extracted"}}},
		},
	}
	g, closer, err := Load(snap, reg, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer closer()
	rows, err := g.Store().ListCoverage("")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.FilePath] = r.DomainKey
	}
	want := map[string]string{"src/a.ts": "a", "src/b.ts": "b", "src/orphan.ts": "a"}
	for p, d := range want {
		if got[p] != d {
			t.Errorf("coverage for %s under domain %q, want %q (all: %v)", p, got[p], d, got)
		}
	}
}

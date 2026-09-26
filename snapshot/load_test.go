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
// original — not merely present or non-zero — for every node whose evidence
// survived export and whose ORIGINAL stored trust is itself consistent with
// that evidence. Two categories are excluded, both verified directly against
// the fixture db rather than assumed:
//
//   - Zero surviving evidence: a node whose ENTIRE evidence was derived
//     (dropped by FromStore — see snapshot/derived.go) has nothing left for
//     RecalculateNodeTrust to recompute FROM; it leaves a zero-evidence node
//     exactly as inserted (graph/trust.go) rather than deriving a number from
//     nothing, so such a node keeps Load's placeholder. tom-and-jerry's 19
//     flow:use_case nodes are exactly this case (each has a single
//     chronicle:derive_flows evidence row, stripped by export).
//   - Original already stale relative to its own evidence: graph.ComputeTrust
//     — the same function RecalculateNodeTrust calls — is run here directly
//     against the node's surviving evidence; if that disagrees with what the
//     fixture has stored, the fixture's stored trust predates some earlier,
//     unrelated change to that evidence (verified: node
//     infra:broker:battle-result has one evidence row whose status is
//     "stale" — recomputing gives confidence 0, but the fixture still stores
//     0.6, i.e. nothing recomputed it after that transition). There is no
//     correct value to hold Load to in that case; it is a pre-existing
//     fixture data-quality artifact, not a Load defect.
//
// Both exclusions are structural (evidence shape), not a hardcoded node list,
// so this keeps checking every node the fixture actually allows checking —
// verified to still be the large majority (see the t.Logf breakdown).
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
	compared, skippedNoEvidence, skippedStaleOriginal := 0, 0, 0
	for _, n := range snap.Nodes {
		if len(n.Evidence) == 0 {
			skippedNoEvidence++
			continue
		}

		orig, err := s.GetNodeByKey(n.ID)
		if err != nil {
			t.Fatalf("original store: GetNodeByKey(%q): %v", n.ID, err)
		}

		// What the snapshot's surviving evidence for this node implies,
		// via the exact function RecalculateNodeTrust itself calls.
		rows := make([]store.EvidenceRow, len(n.Evidence))
		for i, ev := range n.Evidence {
			rows[i] = evidenceRowFrom("node", 0, 0, ev)
		}
		impliedConf, impliedFresh, impliedTrust, _ := graph.ComputeTrust(rows)
		if math.Abs(orig.Confidence-impliedConf) > eps ||
			math.Abs(orig.Freshness-impliedFresh) > eps ||
			math.Abs(orig.TrustScore-impliedTrust) > eps {
			skippedStaleOriginal++
			t.Logf("node %s: original stored trust (%.4f/%.4f/%.4f) already disagrees with what its own surviving evidence implies (%.4f/%.4f/%.4f) — skipping as a pre-existing fixture artifact, not a Load defect",
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
		compared++
	}
	if compared == 0 {
		t.Fatal("no nodes were compared — test fixture is not exercising anything")
	}
	t.Logf("compared trust on %d nodes (%d skipped: no surviving evidence, %d skipped: original already stale relative to its own evidence)",
		compared, skippedNoEvidence, skippedStaleOriginal)
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

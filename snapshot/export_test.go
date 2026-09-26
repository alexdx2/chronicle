package snapshot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// copyFixtureStore copies the tracked tom-and-jerry demo database into a
// fresh temp dir and opens the copy. Tests must never open the checked-in
// fixture in place: store.Open runs migrations and creates -wal/-shm
// siblings next to whatever file it's given.
func copyFixtureStore(t *testing.T) *store.Store {
	t.Helper()
	data, err := os.ReadFile("../fixtures/tom-and-jerry/.depbot/chronicle.db")
	if err != nil {
		t.Fatalf("read fixture db: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "chronicle.db")
	if err := os.WriteFile(dst, data, 0644); err != nil {
		t.Fatalf("copy fixture db: %v", err)
	}
	s, err := store.Open(dst)
	if err != nil {
		t.Fatalf("open copied fixture db: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestFromStore_FixtureCounts pins FromStore's filtering against the real
// tom-and-jerry fixture: node/edge counts must equal current rows minus
// derived, and not one surviving evidence row may be derived.
func TestFromStore_FixtureCounts(t *testing.T) {
	s := copyFixtureStore(t)

	allNodes, err := s.ListNodes(store.NodeFilter{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	wantNodes := 0
	for _, n := range allNodes {
		if n.Status != "deleted" {
			wantNodes++
		}
	}

	allEdges, err := s.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	wantEdges := 0
	for _, e := range allEdges {
		if !IsDerivedEdgeType(e.EdgeType) {
			wantEdges++
		}
	}
	if wantNodes == 0 || wantEdges == 0 {
		t.Fatal("fixture produced zero expected nodes/edges — test fixture is not exercising anything")
	}

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}

	if len(snap.Nodes) != wantNodes {
		t.Errorf("len(Nodes) = %d, want %d (current rows minus deleted)", len(snap.Nodes), wantNodes)
	}
	if len(snap.Edges) != wantEdges {
		t.Errorf("len(Edges) = %d, want %d (current rows minus derived edge types)", len(snap.Edges), wantEdges)
	}

	for _, n := range snap.Nodes {
		for _, ev := range n.Evidence {
			if IsDerivedEvidence(ev.Source, ev.Extractor) {
				t.Errorf("node %s: evidence %s has derived source=%q extractor=%q, should have been filtered",
					n.ID, ev.ID, ev.Source, ev.Extractor)
			}
		}
	}
	for _, e := range snap.Edges {
		for _, ev := range e.Evidence {
			if IsDerivedEvidence(ev.Source, ev.Extractor) {
				t.Errorf("edge %s: evidence %s has derived source=%q extractor=%q, should have been filtered",
					e.ID, ev.ID, ev.Source, ev.Extractor)
			}
		}
	}

	// The fixture has a real fact whose ONLY evidence is chronicle:derive_flows
	// (verified directly against the copied db before writing this assertion):
	// this pins the brief's carve-out precisely — the fact stays, its evidence
	// empties — rather than just "some evidence somewhere got filtered".
	const flowNodeKey = "flow:use_case:tom-and-jerry:get:/api/score"
	found := false
	for _, n := range snap.Nodes {
		if n.ID != flowNodeKey {
			continue
		}
		found = true
		if len(n.Evidence) != 0 {
			t.Errorf("node %s: Evidence = %+v, want empty (its only evidence was chronicle:derive_flows)", flowNodeKey, n.Evidence)
		}
	}
	if !found {
		t.Fatalf("expected node %s to still be present (a fact with no surviving evidence is not itself derived)", flowNodeKey)
	}
}

// TestFromStore_Deterministic proves two exports of the same unchanged
// store encode to byte-identical graph.jsonl content.
func TestFromStore_Deterministic(t *testing.T) {
	s := copyFixtureStore(t)

	opt := ExportOptions{PrevSeq: 4, Registry: "sha256:abc"}
	snap1, err := FromStore(s, opt)
	if err != nil {
		t.Fatalf("FromStore (1): %v", err)
	}
	snap2, err := FromStore(s, opt)
	if err != nil {
		t.Fatalf("FromStore (2): %v", err)
	}

	var buf1, buf2 bytes.Buffer
	if err := Encode(&buf1, snap1); err != nil {
		t.Fatalf("Encode (1): %v", err)
	}
	if err := Encode(&buf2, snap2); err != nil {
		t.Fatalf("Encode (2): %v", err)
	}

	if buf1.Len() == 0 {
		t.Fatal("encoded snapshot is empty")
	}
	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatalf("two exports of the same unchanged store produced different bytes:\n--- 1 ---\n%s\n--- 2 ---\n%s",
			buf1.String(), buf2.String())
	}
}

// TestFromStore_Range exercises all three branches of the Range algorithm
// against a small synthetic store built for exactly this purpose (the
// fixture db has no column data recorded).
func TestFromStore_Range(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	nodeID, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:a", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "A", FilePath: "svc/a.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	seed := func(locator string, lineStart, lineEnd, colStart, colEnd int) {
		t.Helper()
		if _, err := s.AddEvidence(store.EvidenceRow{
			TargetKind: "node", NodeID: nodeID, SourceKind: "file",
			FilePath: "svc/a.ts", ExtractorID: "chronicle-scan", ExtractorVersion: "1",
			LineStart: lineStart, LineEnd: lineEnd, ColumnStart: colStart, ColumnEnd: colEnd,
			Locator: locator, Confidence: 0.9, EvidenceStatus: "valid", EvidencePolarity: "positive",
		}); err != nil {
			t.Fatalf("AddEvidence %s: %v", locator, err)
		}
	}
	seed("cols", 42, 46, 10, 5) // lines and columns -> "L:C-L:C"
	seed("rows", 7, 7, 0, 0)    // lines only -> "L-L"
	seed("none", 0, 0, 0, 0)    // neither -> ""

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}
	if len(snap.Nodes) != 1 {
		t.Fatalf("len(Nodes) = %d, want 1", len(snap.Nodes))
	}

	byLocator := map[string]string{}
	for _, ev := range snap.Nodes[0].Evidence {
		byLocator[ev.Locator] = ev.Range
	}
	if len(byLocator) != 3 {
		t.Fatalf("got %d evidence entries, want 3: %+v", len(byLocator), snap.Nodes[0].Evidence)
	}
	if got := byLocator["cols"]; got != "42:10-46:5" {
		t.Errorf(`Range with lines+columns = %q, want "42:10-46:5"`, got)
	}
	if got := byLocator["rows"]; got != "7-7" {
		t.Errorf(`Range with only lines = %q, want "7-7"`, got)
	}
	if got := byLocator["none"]; got != "" {
		t.Errorf(`Range with neither = %q, want ""`, got)
	}
}

// TestFromStore_TermsAndCoverage exercises the two record kinds the fixture
// db and the Range test don't touch at all: glossary terms (mapped
// verbatim) and file coverage (grouped from per-extractor rows into one
// record per path — this pins groupCoverage's multi-extractor behavior,
// including which extractor's blob wins).
func TestFromStore_TermsAndCoverage(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	if _, err := s.UpsertTerm(store.DomainTerm{
		DomainKey: "d", Term: "order", Aliases: []string{"po"}, AntiPatterns: []string{"invoice"},
		Description: "An order.", Context: "ctx", Examples: []string{"ex1"},
	}); err != nil {
		t.Fatalf("UpsertTerm: %v", err)
	}

	if err := s.UpsertCoverage(store.CoverageRow{
		DomainKey: "d", FilePath: "svc/a.ts", ExtractorID: "chronicle-ast", ExtractorVersion: "1",
		Status: "extracted", BlobSHA: "blobA1",
	}); err != nil {
		t.Fatalf("UpsertCoverage ast: %v", err)
	}
	if err := s.UpsertCoverage(store.CoverageRow{
		DomainKey: "d", FilePath: "svc/a.ts", ExtractorID: "chronicle-scan", ExtractorVersion: "fact-schema@1",
		Status: "extracted", BlobSHA: "blobA1",
	}); err != nil {
		t.Fatalf("UpsertCoverage scan: %v", err)
	}
	if err := s.UpsertCoverage(store.CoverageRow{
		DomainKey: "d", FilePath: "svc/b.ts", ExtractorID: "chronicle-scan", ExtractorVersion: "fact-schema@1",
		Status: "no_runtime_architecture", BlobSHA: "blobB1",
	}); err != nil {
		t.Fatalf("UpsertCoverage b: %v", err)
	}

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}

	if len(snap.Terms) != 1 {
		t.Fatalf("len(Terms) = %d, want 1", len(snap.Terms))
	}
	if term := snap.Terms[0]; term.ID != "d#order" || term.Domain != "d" || term.Term != "order" ||
		term.Description != "An order." || term.Context != "ctx" ||
		len(term.Aliases) != 1 || term.Aliases[0] != "po" ||
		len(term.AntiPatterns) != 1 || term.AntiPatterns[0] != "invoice" ||
		len(term.Examples) != 1 || term.Examples[0] != "ex1" {
		t.Errorf("Term = %+v, want id=d#order with every glossary field carried over", term)
	}

	if len(snap.Coverage) != 2 {
		t.Fatalf("len(Coverage) = %d, want 2 (one record per path)", len(snap.Coverage))
	}
	byPath := map[string]Coverage{}
	for _, c := range snap.Coverage {
		byPath[c.Path] = c
	}

	a, ok := byPath["svc/a.ts"]
	if !ok {
		t.Fatal("missing coverage for svc/a.ts")
	}
	if a.Blob != "blobA1" {
		t.Errorf("svc/a.ts Blob = %q, want %q", a.Blob, "blobA1")
	}
	if len(a.Extractors) != 2 {
		t.Fatalf("svc/a.ts Extractors = %+v, want 2 entries", a.Extractors)
	}
	// ListCoverage orders by (file_path, extractor_id): "chronicle-ast" < "chronicle-scan".
	if a.Extractors[0].ID != "chronicle-ast" || a.Extractors[1].ID != "chronicle-scan" {
		t.Errorf("svc/a.ts Extractors order = %+v, want [chronicle-ast, chronicle-scan]", a.Extractors)
	}

	b, ok := byPath["svc/b.ts"]
	if !ok {
		t.Fatal("missing coverage for svc/b.ts")
	}
	if len(b.Extractors) != 1 || b.Extractors[0].Status != "no_runtime_architecture" {
		t.Errorf("svc/b.ts Extractors = %+v, want one entry with status no_runtime_architecture", b.Extractors)
	}
}

// TestFromStore_FiltersEveryDerivedKind proves each closed-list value in
// derived.go is actually filtered, not merely "happened not to survive".
// The tom-and-jerry fixture only ever has evidence source_kind "file" or
// "synthetic" and extractor "chronicle:derive_flows" — none of the other 6
// signal-pipeline source_kinds, and neither derived edge type, occur in it,
// so TestFromStore_FixtureCounts's "no surviving evidence is derived"
// assertion would still pass even if IsDerivedEvidence/IsDerivedEdgeType
// were wrong for any of them. This constructs one fact for each value by
// hand — plus a non-derived control of each kind, so an over-eager filter
// would also be caught — and checks it is actually absent from the export.
func TestFromStore_FiltersEveryDerivedKind(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	nodeID, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:a", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "A", FilePath: "svc/a.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("UpsertNode a: %v", err)
	}
	otherID, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:b", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "B", FilePath: "svc/b.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("UpsertNode b: %v", err)
	}

	// One evidence row per derivedSourceKinds value, each with an ordinary
	// (non-derived) extractor id, so a bug in the source_kind branch can't
	// hide behind the extractor_id branch instead.
	derivedKinds := []string{
		"complexity-ast", "complexity-smells", "complexity-graph",
		"churn-git", "coupling-git", "test-link", "similarity-minhash",
	}
	for i, kind := range derivedKinds {
		if _, err := s.AddEvidence(store.EvidenceRow{
			TargetKind: "node", NodeID: nodeID, SourceKind: kind,
			FilePath: "svc/a.ts", LineStart: i + 1, ExtractorID: "chronicle-scan", ExtractorVersion: "1",
			Locator: kind, Confidence: 0.9, EvidenceStatus: "valid", EvidencePolarity: "positive",
		}); err != nil {
			t.Fatalf("AddEvidence %s: %v", kind, err)
		}
	}
	// Control: ordinary, non-derived evidence on the same node — proves the
	// node itself isn't dropped, and that filtering isn't over-eager.
	if _, err := s.AddEvidence(store.EvidenceRow{
		TargetKind: "node", NodeID: nodeID, SourceKind: "file",
		FilePath: "svc/a.ts", LineStart: 100, ExtractorID: "chronicle-scan", ExtractorVersion: "1",
		Locator: "control", Confidence: 0.9, EvidenceStatus: "valid", EvidencePolarity: "positive",
	}); err != nil {
		t.Fatalf("AddEvidence control: %v", err)
	}

	// One edge of each derivedEdgeTypes value, plus one ordinary edge as a
	// control so the export isn't vacuously "every edge is gone". "inferred"
	// is the real derivation_kind production code uses for both
	// (graph/gitsignals.go, graph/similarity.go) — also the only value in
	// the schema's CHECK constraint that fits an analytical, non-code edge.
	for _, edgeType := range []string{"CHANGES_WITH", "SIMILAR_TO"} {
		if _, err := s.UpsertEdge(store.EdgeRow{
			EdgeKey: "code:provider:d:a->code:provider:d:b:" + edgeType, FromNodeID: nodeID, ToNodeID: otherID,
			EdgeType: edgeType, DerivationKind: "inferred", Active: true,
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
		}); err != nil {
			t.Fatalf("UpsertEdge %s: %v", edgeType, err)
		}
	}
	if _, err := s.UpsertEdge(store.EdgeRow{
		EdgeKey: "code:provider:d:a->code:provider:d:b:INJECTS", FromNodeID: nodeID, ToNodeID: otherID,
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("UpsertEdge INJECTS (control): %v", err)
	}

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}

	if len(snap.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2 (neither node is itself derived)", len(snap.Nodes))
	}
	var nodeA *Node
	for i := range snap.Nodes {
		if snap.Nodes[i].ID == "code:provider:d:a" {
			nodeA = &snap.Nodes[i]
		}
	}
	if nodeA == nil {
		t.Fatal("node a is missing from the export")
	}
	gotByLocator := map[string]bool{}
	for _, ev := range nodeA.Evidence {
		gotByLocator[ev.Locator] = true
	}
	for _, kind := range derivedKinds {
		if gotByLocator[kind] {
			t.Errorf("evidence with source_kind %q survived export — IsDerivedEvidence should have filtered it", kind)
		}
	}
	if !gotByLocator["control"] {
		t.Error("the non-derived control evidence is missing — filtering is over-eager")
	}
	if len(nodeA.Evidence) != 1 {
		t.Errorf("node a Evidence = %+v, want exactly the control entry", nodeA.Evidence)
	}

	gotEdgeType := map[string]bool{}
	for _, e := range snap.Edges {
		gotEdgeType[e.Type] = true
	}
	if gotEdgeType["CHANGES_WITH"] {
		t.Error("a CHANGES_WITH edge survived export — IsDerivedEdgeType should have dropped it")
	}
	if gotEdgeType["SIMILAR_TO"] {
		t.Error("a SIMILAR_TO edge survived export — IsDerivedEdgeType should have dropped it")
	}
	if !gotEdgeType["INJECTS"] {
		t.Error("the non-derived control edge (INJECTS) is missing — filtering is over-eager")
	}
	if len(snap.Edges) != 1 {
		t.Errorf("len(Edges) = %d, want 1 (only the control edge)", len(snap.Edges))
	}
}

// TestFromStore_EncodeFillsHeaderCounts pins an invariant internal/cli/graph.go's
// runGraphUpdate depends on: Encode fills Header.Counts from a Snapshot's
// actual record slices as a documented side effect, so reading
// Header.Counts is only valid AFTER calling Encode, never before. Nothing
// else in this package's tests exercises Header.Counts at all — without
// this, a future change that reordered runGraphUpdate's Encode/read-Counts
// lines, or that stopped Encode from mutating Counts, would break silently.
func TestFromStore_EncodeFillsHeaderCounts(t *testing.T) {
	s := copyFixtureStore(t)

	snap, err := FromStore(s, ExportOptions{})
	if err != nil {
		t.Fatalf("FromStore: %v", err)
	}
	wantNode, wantEdge := len(snap.Nodes), len(snap.Edges)
	wantTerm, wantCoverage := len(snap.Terms), len(snap.Coverage)
	if wantNode == 0 || wantEdge == 0 {
		t.Fatal("fixture produced zero nodes/edges — test is not exercising anything")
	}

	// Before Encode, Header.Counts is whatever FromStore left it (Task 1
	// documents Encode, not FromStore, as owning Counts) — assert that too,
	// so this test would fail loud if FromStore ever started filling it in
	// some other, inconsistent way instead.
	if snap.Header.Counts != (Counts{}) {
		t.Errorf("before Encode, Header.Counts = %+v, want the zero value", snap.Header.Counts)
	}

	var buf bytes.Buffer
	if err := Encode(&buf, snap); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if snap.Header.Counts.Node != wantNode {
		t.Errorf("after Encode, Header.Counts.Node = %d, want %d", snap.Header.Counts.Node, wantNode)
	}
	if snap.Header.Counts.Edge != wantEdge {
		t.Errorf("after Encode, Header.Counts.Edge = %d, want %d", snap.Header.Counts.Edge, wantEdge)
	}
	if snap.Header.Counts.Term != wantTerm {
		t.Errorf("after Encode, Header.Counts.Term = %d, want %d", snap.Header.Counts.Term, wantTerm)
	}
	if snap.Header.Counts.Coverage != wantCoverage {
		t.Errorf("after Encode, Header.Counts.Coverage = %d, want %d", snap.Header.Counts.Coverage, wantCoverage)
	}

	// Deliberately no Decode round trip here: tom-and-jerry has one
	// pre-existing, unrelated node ("infra:broker:battle-result", status
	// stale) whose node_key has only 3 of the required 4 colon-separated
	// segments — predates this branch (present since commit 275bbca) and is
	// not something FromStore or Encode did. FromStore correctly keeps it
	// (only "deleted" is excluded), Encode correctly counts it, and Decode
	// correctly rejects the malformed key with a bad_key/counts_mismatch
	// warning on the way back in — all three exactly per spec. Round-tripping
	// this exact fixture through Decode would make this test assert "zero
	// warnings" against data known to legitimately produce one, coupling a
	// Header.Counts test to an unrelated fixture data-quality quirk. The
	// direct assertions above already pin the invariant this test exists
	// for: Header.Counts matches Nodes/Edges/Terms/Coverage's real lengths
	// once Encode has run.
}

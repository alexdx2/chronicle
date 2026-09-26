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

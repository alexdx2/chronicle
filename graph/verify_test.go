package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// newTestGraph opens a fresh store + graph in a temp dir and chdirs into it
// (restored automatically via t.Chdir) so evidence file paths can be relative,
// matching how VerifyFileEvidence resolves file_path against process cwd.
func newTestGraph(t *testing.T) (*Graph, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	g := New(s, reg)

	t.Chdir(dir)

	return g, dir
}

// mustCreateEdgeWithImportEvidence creates a from/to node pair, an edge between
// them, and a single import_specifier assertion-based evidence row on that
// edge (evaluated against filePath at creation time). Returns the edge key.
func mustCreateEdgeWithImportEvidence(t *testing.T, g *Graph, filePath, assertion string) string {
	t.Helper()

	fromID, err := g.Store().UpsertNode(store.NodeRow{
		NodeKey:   "code:test:consumer:Consumer",
		Layer:     "code",
		NodeType:  "class",
		DomainKey: "test",
		Name:      "Consumer",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("UpsertNode from: %v", err)
	}
	toID, err := g.Store().UpsertNode(store.NodeRow{
		NodeKey:   "package:test:auth-client:AuthClient",
		Layer:     "package",
		NodeType:  "library",
		DomainKey: "test",
		Name:      "AuthClient",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("UpsertNode to: %v", err)
	}

	edgeKey := "code:test:consumer:Consumer->package:test:auth-client:AuthClient:INJECTS"
	if _, err := g.Store().UpsertEdge(store.EdgeRow{
		EdgeKey:        edgeKey,
		FromNodeID:     fromID,
		ToNodeID:       toID,
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		Active:         true,
		Confidence:     1.0,
		Freshness:      1.0,
		TrustScore:     1.0,
		Metadata:       "{}",
	}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	if _, err := g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "file",
		FilePath:         filePath,
		LineStart:        1,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		Confidence:       0.95,
		AssertionKind:    "import_specifier",
		Assertion:        assertion,
	}); err != nil {
		t.Fatalf("AddEdgeEvidence: %v", err)
	}

	return edgeKey
}

// TestVerifyFileEvidence_ReexaminesRejected asserts that VerifyFileEvidence
// re-examines evidence rejected at creation time, not only evidence that has
// gone stale — rejected rows keep evidence_status='valid', so today they are
// never re-selected once the underlying file is fixed.
func TestVerifyFileEvidence_ReexaminesRejected(t *testing.T) {
	g, dir := newTestGraph(t)

	// File initially imports a DIFFERENT package -> creation verification rejects.
	fp := filepath.Join(dir, "src", "consumer.ts")
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(fp, []byte("import { a } from '@other/pkg'\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	edgeKey := mustCreateEdgeWithImportEvidence(t, g, "src/consumer.ts", `{"module":"@okeep/auth-client"}`)

	ev, err := g.Store().ListEvidenceByEdge(mustEdgeID(t, g, edgeKey))
	if err != nil {
		t.Fatalf("ListEvidenceByEdge: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("precondition: expected 1 evidence row, got %d", len(ev))
	}
	if ev[0].VerificationStatus != "rejected" {
		t.Fatalf("precondition: creation must reject, got %q", ev[0].VerificationStatus)
	}

	// Fix the file: now imports a subpath of the asserted package.
	if err := os.WriteFile(fp, []byte("import { a } from '@okeep/auth-client/server'\n"), 0o644); err != nil {
		t.Fatalf("WriteFile fix: %v", err)
	}

	res, err := g.VerifyFileEvidence("src/consumer.ts", 0, "")
	if err != nil {
		t.Fatalf("VerifyFileEvidence: %v", err)
	}
	if res.Summary.Checked == 0 {
		t.Fatal("rejected evidence was not re-examined")
	}

	ev, err = g.Store().ListEvidenceByEdge(mustEdgeID(t, g, edgeKey))
	if err != nil {
		t.Fatalf("ListEvidenceByEdge after verify: %v", err)
	}
	if ev[0].VerificationStatus != "verified" {
		t.Fatalf("want verified after fix, got %q (%s)", ev[0].VerificationStatus, ev[0].VerificationReason)
	}

	edge, err := g.Store().GetEdgeByKey(edgeKey)
	if err != nil {
		t.Fatalf("GetEdgeByKey: %v", err)
	}
	if edge.TrustScore <= 0 {
		t.Fatalf("trust must recover after reverification, got %f", edge.TrustScore)
	}
}

func mustEdgeID(t *testing.T, g *Graph, edgeKey string) int64 {
	t.Helper()
	edge, err := g.Store().GetEdgeByKey(edgeKey)
	if err != nil {
		t.Fatalf("GetEdgeByKey: %v", err)
	}
	return edge.EdgeID
}

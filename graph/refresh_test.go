package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// RefreshFromDiff is zero-token structural freshness: it re-verifies evidence on
// changed files mechanically, stale-marks evidence on deleted files, and surfaces
// files that need an LLM rescan — all by reusing InvalidateChanged.
func TestRefreshFromDiff(t *testing.T) {
	tmp := t.TempDir()
	s, err := store.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	// A real file whose import still exists → mechanical verifier confirms it.
	liveFile := filepath.Join(tmp, "order.ts")
	os.WriteFile(liveFile, []byte(`import { PricingEngine } from "./pricing";
export class OrderService {}
`), 0644)

	rev0, err := s.CreateRevision("test", "", "sha0", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	nodeID, err := s.UpsertNode(store.NodeRow{
		NodeKey: "service:service:test:order-api", Name: "order-api",
		Layer: "service", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, TrustScore: 1.0, Metadata: "{}",
		FirstSeenRevisionID: rev0, LastSeenRevisionID: rev0,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadNodeID, err := s.UpsertNode(store.NodeRow{
		NodeKey: "service:service:test:legacy-api", Name: "legacy-api",
		Layer: "service", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, TrustScore: 1.0, Metadata: "{}",
		FirstSeenRevisionID: rev0, LastSeenRevisionID: rev0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Evidence anchored to the live file (import_specifier the verifier can check).
	if _, err := s.AddEvidence(store.EvidenceRow{
		TargetKind: "node", NodeID: nodeID,
		SourceKind: "file", FilePath: liveFile, LineStart: 1,
		ExtractorID: "chronicle-ast", AssertionKind: "import_specifier",
		Assertion: `{"modules":["./pricing"]}`, Confidence: 0.85,
		EvidenceStatus: "valid", EvidencePolarity: "positive",
	}); err != nil {
		t.Fatal(err)
	}
	// Evidence anchored to a file that will be reported deleted.
	deletedFile := filepath.Join(tmp, "legacy.ts")
	if _, err := s.AddEvidence(store.EvidenceRow{
		TargetKind: "node", NodeID: deadNodeID,
		SourceKind: "file", FilePath: deletedFile, LineStart: 1,
		ExtractorID: "chronicle-ast", AssertionKind: "import_specifier",
		Assertion: `{"modules":["./gone"]}`, Confidence: 0.85,
		EvidenceStatus: "valid", EvidencePolarity: "positive",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := g.RefreshFromDiff("test", "sha1", []string{liveFile}, []string{deletedFile})
	if err != nil {
		t.Fatalf("RefreshFromDiff: %v", err)
	}

	if res.RevisionID == 0 || res.RevisionID == rev0 {
		t.Errorf("expected a fresh revision, got %d", res.RevisionID)
	}
	if res.HeadSHA != "sha1" {
		t.Errorf("head sha mismatch: %q", res.HeadSHA)
	}
	rev, _ := s.GetRevision(res.RevisionID)
	if rev == nil || !strings.Contains(rev.Metadata, "refresh") {
		t.Errorf("expected refresh marker in revision metadata, got %+v", rev)
	}
	if res.Invalidated == nil || res.Invalidated.StaleEvidence == 0 {
		t.Errorf("expected stale evidence marked, got %+v", res.Invalidated)
	}
	if res.ChangedFiles != 1 || res.DeletedFiles != 1 {
		t.Errorf("expected 1 changed + 1 deleted, got %d/%d", res.ChangedFiles, res.DeletedFiles)
	}
}

func TestRefreshFromDiffNoChanges(t *testing.T) {
	tmp := t.TempDir()
	s, _ := store.Open(filepath.Join(tmp, "t.db"))
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := New(s, reg)
	s.CreateRevision("test", "", "sha0", "manual", "full", "{}")

	res, err := g.RefreshFromDiff("test", "sha1", nil, nil)
	if err != nil {
		t.Fatalf("RefreshFromDiff: %v", err)
	}
	if res.ChangedFiles != 0 || res.DeletedFiles != 0 {
		t.Errorf("expected no-op, got %+v", res)
	}
}

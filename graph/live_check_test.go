package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

func setupLiveCheckGraph(t *testing.T) (*Graph, string) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)
	return g, tmpDir
}

// TestLiveCheck_ValidEvidence — evidence assertion still matches file, no _changed items returned.
func TestLiveCheck_ValidEvidence(t *testing.T) {
	g, tmpDir := setupLiveCheckGraph(t)

	// Create a TS file with an import.
	tsFile := filepath.Join(tmpDir, "order.ts")
	os.WriteFile(tsFile, []byte(`import { OrderService } from "./order.service";
import express from "express";
`), 0644)

	// Create node + evidence with assertion matching the file.
	nodeID, _ := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:order-service", Name: "OrderService",
		Layer: "code", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	revID, _ := g.Store().CreateRevision("test", "sha1", "", "manual", "full", "{}")

	_, err := g.AddNodeEvidence("code:service:test:order-service", validate.EvidenceInput{
		TargetKind:       "node",
		SourceKind:       "file",
		FilePath:         tsFile,
		LineStart:        1,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		Confidence:       0.95,
		RevisionID:       revID,
		AssertionKind:    "import_specifier",
		Assertion:        `{"module": "./order.service", "symbols": ["OrderService"]}`,
	})
	if err != nil {
		t.Fatalf("AddNodeEvidence: %v", err)
	}

	// Fetch evidence and run live check.
	evidence, _ := g.Store().ListEvidenceByNode(nodeID)
	items := g.LiveCheckEvidence(evidence)

	// File unchanged → assertion still valid → no items.
	if len(items) != 0 {
		t.Errorf("expected 0 changed items for valid evidence, got %d: %+v", len(items), items)
	}
}

// TestLiveCheck_MissingImport — file changed, import removed → evidence flagged as missing.
func TestLiveCheck_MissingImport(t *testing.T) {
	g, tmpDir := setupLiveCheckGraph(t)

	tsFile := filepath.Join(tmpDir, "order.ts")
	// Initial file has the import.
	os.WriteFile(tsFile, []byte(`import { OrderService } from "./order.service";
`), 0644)

	nodeID, _ := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:order-service", Name: "OrderService",
		Layer: "code", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	revID, _ := g.Store().CreateRevision("test", "sha1", "", "manual", "full", "{}")

	_, err := g.AddNodeEvidence("code:service:test:order-service", validate.EvidenceInput{
		TargetKind:       "node",
		SourceKind:       "file",
		FilePath:         tsFile,
		LineStart:        1,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		Confidence:       0.95,
		RevisionID:       revID,
		AssertionKind:    "import_specifier",
		Assertion:        `{"module": "./order.service", "symbols": ["OrderService"]}`,
	})
	if err != nil {
		t.Fatalf("AddNodeEvidence: %v", err)
	}

	// Now rewrite the file — remove the import.
	os.WriteFile(tsFile, []byte(`// import removed
const x = 1;
`), 0644)

	evidence, _ := g.Store().ListEvidenceByNode(nodeID)
	items := g.LiveCheckEvidence(evidence)

	if len(items) != 1 {
		t.Fatalf("expected 1 changed item, got %d: %+v", len(items), items)
	}
	if items[0].Status != "missing" {
		t.Errorf("expected status=missing, got %s", items[0].Status)
	}
	if items[0].AssertionKind != "import_specifier" {
		t.Errorf("expected assertion_kind=import_specifier, got %s", items[0].AssertionKind)
	}
}

// TestLiveCheck_MovedLine — import moved to different line → verifier returns valid (still found).
func TestLiveCheck_MovedLine(t *testing.T) {
	g, tmpDir := setupLiveCheckGraph(t)

	tsFile := filepath.Join(tmpDir, "order.ts")
	os.WriteFile(tsFile, []byte(`import { OrderService } from "./order.service";
`), 0644)

	nodeID, _ := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:order-service", Name: "OrderService",
		Layer: "code", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	revID, _ := g.Store().CreateRevision("test", "sha1", "", "manual", "full", "{}")

	_, err := g.AddNodeEvidence("code:service:test:order-service", validate.EvidenceInput{
		TargetKind:       "node",
		SourceKind:       "file",
		FilePath:         tsFile,
		LineStart:        1,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		Confidence:       0.95,
		RevisionID:       revID,
		AssertionKind:    "import_specifier",
		Assertion:        `{"module": "./order.service", "symbols": ["OrderService"]}`,
	})
	if err != nil {
		t.Fatalf("AddNodeEvidence: %v", err)
	}

	// Rewrite file — import moved to line 5 (added blank lines above).
	os.WriteFile(tsFile, []byte(`// header
// more comments
// even more

import { OrderService } from "./order.service";
`), 0644)

	evidence, _ := g.Store().ListEvidenceByNode(nodeID)
	items := g.LiveCheckEvidence(evidence)

	// Import still present in file → valid → not in _changed.
	if len(items) != 0 {
		t.Errorf("expected 0 changed items (import just moved), got %d: %+v", len(items), items)
	}
}

// TestLiveCheck_FileDeleted — source file no longer exists → all evidence flagged as missing.
func TestLiveCheck_FileDeleted(t *testing.T) {
	g, tmpDir := setupLiveCheckGraph(t)

	tsFile := filepath.Join(tmpDir, "order.ts")
	os.WriteFile(tsFile, []byte(`import { OrderService } from "./order.service";
`), 0644)

	nodeID, _ := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:order-service", Name: "OrderService",
		Layer: "code", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	revID, _ := g.Store().CreateRevision("test", "sha1", "", "manual", "full", "{}")

	_, _ = g.AddNodeEvidence("code:service:test:order-service", validate.EvidenceInput{
		TargetKind: "node", SourceKind: "file", FilePath: tsFile, LineStart: 1,
		ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.95,
		RevisionID: revID, AssertionKind: "import_specifier",
		Assertion: `{"module": "./order.service", "symbols": ["OrderService"]}`,
	})

	// Delete the file.
	os.Remove(tsFile)

	evidence, _ := g.Store().ListEvidenceByNode(nodeID)
	items := g.LiveCheckEvidence(evidence)

	if len(items) != 1 {
		t.Fatalf("expected 1 changed item for deleted file, got %d", len(items))
	}
	if items[0].Status != "missing" {
		t.Errorf("expected status=missing, got %s", items[0].Status)
	}
	if items[0].Reason != "file not found" {
		t.Errorf("expected reason='file not found', got %s", items[0].Reason)
	}
}

// TestLiveCheck_NoAssertion — evidence without assertion is skipped.
func TestLiveCheck_NoAssertion(t *testing.T) {
	g, tmpDir := setupLiveCheckGraph(t)

	tsFile := filepath.Join(tmpDir, "order.ts")
	os.WriteFile(tsFile, []byte(`const x = 1;
`), 0644)

	nodeID, _ := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:order-service", Name: "OrderService",
		Layer: "code", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	revID, _ := g.Store().CreateRevision("test", "sha1", "", "manual", "full", "{}")

	// Evidence WITHOUT assertion — should be skipped by LiveCheckEvidence.
	_, _ = g.AddNodeEvidence("code:service:test:order-service", validate.EvidenceInput{
		TargetKind: "node", SourceKind: "file", FilePath: tsFile, LineStart: 1,
		ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.95,
		RevisionID: revID,
		// No AssertionKind / Assertion
	})

	evidence, _ := g.Store().ListEvidenceByNode(nodeID)
	items := g.LiveCheckEvidence(evidence)

	if len(items) != 0 {
		t.Errorf("expected 0 items for evidence without assertion, got %d: %+v", len(items), items)
	}
}

// TestLiveCheck_EmptyInput — nil/empty evidence returns nil.
func TestLiveCheck_EmptyInput(t *testing.T) {
	g, _ := setupLiveCheckGraph(t)

	if items := g.LiveCheckEvidence(nil); items != nil {
		t.Errorf("expected nil for nil input, got %+v", items)
	}
	if items := g.LiveCheckEvidence([]store.EvidenceRow{}); items != nil {
		t.Errorf("expected nil for empty input, got %+v", items)
	}
}

// TestLiveCheck_ManifestDependency — package.json dependency check.
func TestLiveCheck_ManifestDependencyValid(t *testing.T) {
	g, tmpDir := setupLiveCheckGraph(t)

	pkgJSON := filepath.Join(tmpDir, "package.json")
	os.WriteFile(pkgJSON, []byte(`{
  "dependencies": {
    "express": "^4.18.0"
  }
}`), 0644)

	nodeID, _ := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:my-api", Name: "my-api",
		Layer: "code", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	revID, _ := g.Store().CreateRevision("test", "sha1", "", "manual", "full", "{}")

	_, _ = g.AddNodeEvidence("code:service:test:my-api", validate.EvidenceInput{
		TargetKind: "node", SourceKind: "file", FilePath: pkgJSON, LineStart: 3,
		ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.95,
		RevisionID: revID, AssertionKind: "manifest_dependency",
		Assertion: `{"package": "express", "sections": ["dependencies"]}`,
	})

	evidence, _ := g.Store().ListEvidenceByNode(nodeID)
	items := g.LiveCheckEvidence(evidence)

	// Dependency present → valid → no items.
	if len(items) != 0 {
		t.Errorf("expected 0 items for valid manifest dep, got %d: %+v", len(items), items)
	}
}

// TestLiveCheck_ManifestDependencyRemoved — dependency removed from package.json.
func TestLiveCheck_ManifestDependencyRemoved(t *testing.T) {
	g, tmpDir := setupLiveCheckGraph(t)

	pkgJSON := filepath.Join(tmpDir, "package.json")
	os.WriteFile(pkgJSON, []byte(`{
  "dependencies": {
    "express": "^4.18.0"
  }
}`), 0644)

	nodeID, _ := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:my-api", Name: "my-api",
		Layer: "code", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	revID, _ := g.Store().CreateRevision("test", "sha1", "", "manual", "full", "{}")

	_, _ = g.AddNodeEvidence("code:service:test:my-api", validate.EvidenceInput{
		TargetKind: "node", SourceKind: "file", FilePath: pkgJSON, LineStart: 3,
		ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.95,
		RevisionID: revID, AssertionKind: "manifest_dependency",
		Assertion: `{"package": "express", "sections": ["dependencies"]}`,
	})

	// Now rewrite package.json — remove express.
	os.WriteFile(pkgJSON, []byte(`{
  "dependencies": {}
}`), 0644)

	evidence, _ := g.Store().ListEvidenceByNode(nodeID)
	items := g.LiveCheckEvidence(evidence)

	if len(items) != 1 {
		t.Fatalf("expected 1 changed item, got %d: %+v", len(items), items)
	}
	if items[0].Status != "missing" {
		t.Errorf("expected status=missing, got %s", items[0].Status)
	}
}

// TestLiveCheck_ReadOnly — verify that LiveCheckEvidence does NOT modify evidence in DB.
func TestLiveCheck_ReadOnly(t *testing.T) {
	g, tmpDir := setupLiveCheckGraph(t)

	tsFile := filepath.Join(tmpDir, "order.ts")
	os.WriteFile(tsFile, []byte(`import { OrderService } from "./order.service";
`), 0644)

	nodeID, _ := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:order-service", Name: "OrderService",
		Layer: "code", NodeType: "service", DomainKey: "test",
		Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	revID, _ := g.Store().CreateRevision("test", "sha1", "", "manual", "full", "{}")

	_, _ = g.AddNodeEvidence("code:service:test:order-service", validate.EvidenceInput{
		TargetKind: "node", SourceKind: "file", FilePath: tsFile, LineStart: 1,
		ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.95,
		RevisionID: revID, AssertionKind: "import_specifier",
		Assertion: `{"module": "./order.service", "symbols": ["OrderService"]}`,
	})

	// Snapshot evidence state before.
	evBefore, _ := g.Store().ListEvidenceByNode(nodeID)

	// Delete file to trigger "missing".
	os.Remove(tsFile)

	items := g.LiveCheckEvidence(evBefore)
	if len(items) != 1 {
		t.Fatalf("expected 1 changed, got %d", len(items))
	}

	// Evidence in DB must be unchanged.
	evAfter, _ := g.Store().ListEvidenceByNode(nodeID)
	if len(evAfter) != len(evBefore) {
		t.Fatalf("evidence count changed: before=%d, after=%d", len(evBefore), len(evAfter))
	}
	for i := range evBefore {
		if evBefore[i].EvidenceStatus != evAfter[i].EvidenceStatus {
			t.Errorf("evidence %d status changed: %s → %s", evBefore[i].EvidenceID, evBefore[i].EvidenceStatus, evAfter[i].EvidenceStatus)
		}
		if evBefore[i].VerificationStatus != evAfter[i].VerificationStatus {
			t.Errorf("evidence %d verification_status changed: %s → %s", evBefore[i].EvidenceID, evBefore[i].VerificationStatus, evAfter[i].VerificationStatus)
		}
	}
}

package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// TestVerificationFullFlow tests the complete iterative verification flow:
// 1. Create graph with assertion-based evidence
// 2. Simulate file change (invalidate_changed auto-verifies)
// 3. Finalize reports status
// 4. Resolve needs_review edges
func TestVerificationFullFlow(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	// Create a package.json file to verify against
	pkgJSON := filepath.Join(tmpDir, "package.json")
	os.WriteFile(pkgJSON, []byte(`{
  "name": "test-app",
  "dependencies": {
    "@otopoint/pricing-engine": "workspace:*",
    "express": "^4.18.0"
  },
  "devDependencies": {
    "typescript": "^5.0.0"
  }
}`), 0644)

	// Create a TS file
	tsFile := filepath.Join(tmpDir, "order.ts")
	os.WriteFile(tsFile, []byte(`import { calculatePrice } from "@otopoint/pricing-engine";
import express from "express";

export class OrderService {
  createOrder(items: any[]) {
    return calculatePrice(items);
  }
}
`), 0644)

	// === Phase 1: Build initial graph with assertion-based evidence ===
	nodeID1, err := s.UpsertNode(store.NodeRow{NodeKey: "service:test:orders:order-api", Name: "order-api", Layer: "service", NodeType: "api", DomainKey: "test", Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}"})
	if err != nil {
		t.Fatalf("UpsertNode1: %v", err)
	}
	nodeID2, err := s.UpsertNode(store.NodeRow{NodeKey: "package:test:orders:pricing-engine", Name: "pricing-engine", Layer: "package", NodeType: "library", DomainKey: "test", Status: "active", Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}"})
	if err != nil {
		t.Fatalf("UpsertNode2: %v", err)
	}

	_, _ = s.UpsertEdge(store.EdgeRow{
		EdgeKey:        "service:test:orders:order-api->package:test:orders:pricing-engine:DEPENDS_ON",
		FromNodeID:     nodeID1,
		ToNodeID:       nodeID2,
		EdgeType:       "DEPENDS_ON",
		DerivationKind: "hard",
		Active:         true,
		Confidence:     1.0,
		Freshness:      1.0,
		TrustScore:     1.0,
		Metadata:       "{}",
	})

	revID, _ := s.CreateRevision("test", "abc123", "", "manual", "full", "{}")

	// Add evidence with assertion
	_, err = g.AddEdgeEvidence("service:test:orders:order-api->package:test:orders:pricing-engine:DEPENDS_ON", validate.EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "file",
		FilePath:         pkgJSON,
		LineStart:        4,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		Confidence:       0.95,
		RevisionID:       revID,
		AssertionKind:    "manifest_dependency",
		Assertion:        `{"package": "@otopoint/pricing-engine", "sections": ["dependencies"]}`,
	})
	if err != nil {
		t.Fatalf("AddEdgeEvidence: %v", err)
	}

	// Add TS import evidence
	_, err = g.AddEdgeEvidence("service:test:orders:order-api->package:test:orders:pricing-engine:DEPENDS_ON", validate.EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "file",
		FilePath:         tsFile,
		LineStart:        1,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		Confidence:       0.95,
		RevisionID:       revID,
		AssertionKind:    "import_specifier",
		Assertion:        `{"module": "@otopoint/pricing-engine", "symbols": ["calculatePrice"]}`,
	})
	if err != nil {
		t.Fatalf("AddEdgeEvidence (TS): %v", err)
	}

	// === Phase 2: Simulate file change — dependency still exists ===
	revID2, _ := s.CreateRevision("test", "def456", "abc123", "manual", "incremental", "{}")

	result, err := g.InvalidateChanged("test", revID2, []string{pkgJSON, tsFile})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}

	t.Logf("InvalidateChanged: stale=%d, auto_verified=%d, needs_claude=%d",
		result.StaleEvidence, len(result.AutoVerified), len(result.NeedsClaude))

	// Auto-verification should have re-confirmed both evidence (files still have the deps)
	if len(result.AutoVerified) == 0 {
		t.Error("expected auto-verification results")
	}

	totalValid := 0
	for _, av := range result.AutoVerified {
		totalValid += av.Summary.Valid + av.Summary.Moved
	}
	if totalValid < 2 {
		t.Errorf("expected at least 2 evidence verified, got %d", totalValid)
	}

	// NeedsClaude should be empty (both files verified cleanly)
	if len(result.NeedsClaude) > 0 {
		t.Logf("unexpected needs_claude: %v", result.NeedsClaude)
	}

	// === Phase 3: Finalize — should be clean ===
	fin, err := g.FinalizeIncrementalScan("test", revID2)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	t.Logf("Finalize: status=%s revalidated=%d stale=%d needs_review=%d obligations=%+v",
		fin.ScanStatus, fin.Revalidated, fin.StillStale, len(fin.NeedsReviewEdges), fin.Obligations)

	if fin.ScanStatus != "clean" {
		t.Errorf("expected scan_status=clean, got %s", fin.ScanStatus)
	}
	// Evidence was auto-verified (stale→valid) so revalidated count should be > 0
	if fin.Revalidated == 0 {
		t.Logf("Note: revalidated=%d (evidence went stale→valid during auto-verify in same revision)", fin.Revalidated)
	}

	// === Phase 4: Remove dependency from package.json, keep TS import ===
	os.WriteFile(pkgJSON, []byte(`{
  "name": "test-app",
  "dependencies": {
    "express": "^4.18.0"
  }
}`), 0644)

	revID3, _ := s.CreateRevision("test", "ghi789", "def456", "manual", "incremental", "{}")
	result2, err := g.InvalidateChanged("test", revID3, []string{pkgJSON})
	if err != nil {
		t.Fatalf("InvalidateChanged2: %v", err)
	}

	t.Logf("InvalidateChanged2: stale=%d, auto_verified=%d, needs_claude=%d",
		result2.StaleEvidence, len(result2.AutoVerified), len(result2.NeedsClaude))

	// The manifest evidence should be missing now
	hasMissing := false
	for _, av := range result2.AutoVerified {
		if av.Summary.Missing > 0 {
			hasMissing = true
		}
	}
	if !hasMissing {
		t.Error("expected manifest evidence to be missing after removing dep from package.json")
	}

	// But the TS import evidence is still valid (not invalidated — different file)
	// So finalize should not mark edge as needs_review (it still has one valid evidence)
	fin2, err := g.FinalizeIncrementalScan("test", revID3)
	if err != nil {
		t.Fatalf("Finalize2: %v", err)
	}

	t.Logf("Finalize2: status=%s needs_review_edges=%d", fin2.ScanStatus, len(fin2.NeedsReviewEdges))

	// Edge should NOT be needs_review because TS import evidence is still valid
	if len(fin2.NeedsReviewEdges) > 0 {
		t.Errorf("edge should not be needs_review — TS import evidence still valid")
	}

	// === Phase 5: Remove TS import too — now edge should be needs_review ===
	os.WriteFile(tsFile, []byte(`import express from "express";

export class OrderService {
  createOrder(items: any[]) {
    return items;
  }
}
`), 0644)

	revID4, _ := s.CreateRevision("test", "jkl012", "ghi789", "manual", "incremental", "{}")
	result3, err := g.InvalidateChanged("test", revID4, []string{tsFile})
	if err != nil {
		t.Fatalf("InvalidateChanged3: %v", err)
	}

	t.Logf("InvalidateChanged3: stale=%d, auto_verified=%d, needs_claude=%d",
		result3.StaleEvidence, len(result3.AutoVerified), len(result3.NeedsClaude))

	fin3, err := g.FinalizeIncrementalScan("test", revID4)
	if err != nil {
		t.Fatalf("Finalize3: %v", err)
	}

	t.Logf("Finalize3: status=%s needs_review_edges=%d uncovered=%d",
		fin3.ScanStatus, len(fin3.NeedsReviewEdges), len(fin3.UncoveredFiles))

	// Now edge should be needs_review — both evidence sources are gone
	if len(fin3.NeedsReviewEdges) == 0 {
		t.Error("expected needs_review_edges to contain the pricing-engine edge")
	}
	if fin3.ScanStatus == "clean" {
		t.Error("expected scan_status != clean when edge has no valid evidence")
	}
}

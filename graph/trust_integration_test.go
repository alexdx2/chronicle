package graph

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

func setupTrustGraph(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	return New(s, reg)
}

func TestTrustLifecycle(t *testing.T) {
	g := setupTrustGraph(t)
	s := g.Store()

	// === Phase 1: Full scan ===
	revID, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrdersService", FilePath: "src/orders/orders.service.ts"},
			{NodeKey: "code:provider:orders:paymentsservice", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "PaymentsService", FilePath: "src/payments/payments.service.ts"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:provider:orders:ordersservice", ToNodeKey: "code:provider:orders:paymentsservice", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "edge", EdgeKey: "code:provider:orders:ordersservice->code:provider:orders:paymentsservice:INJECTS",
				SourceKind: "file", FilePath: "src/orders/orders.service.ts", LineStart: 12,
				ExtractorID: "claude-code", ExtractorVersion: "1.0"},
		},
	}

	if _, err := g.ImportAll(payload, revID); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Verify: edge trust = 0.85 (code evidence cap), not 0.95
	edge, err := s.GetEdgeByKey("code:provider:orders:ordersservice->code:provider:orders:paymentsservice:INJECTS")
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if math.Abs(edge.TrustScore-0.85) > 0.01 {
		t.Errorf("Phase 1: edge trust = %v, want ~0.85 (code evidence cap)", edge.TrustScore)
	}
	if math.Abs(edge.Freshness-1.0) > 0.01 {
		t.Errorf("Phase 1: edge freshness = %v, want 1.0", edge.Freshness)
	}

	// === Phase 2: File changes, invalidate ===
	revID2, err := s.CreateRevision("orders", "abc123", "def456", "manual", "incremental", "{}")
	if err != nil {
		t.Fatalf("CreateRevision2: %v", err)
	}

	result, err := g.InvalidateChanged("orders", revID2, []string{"src/orders/orders.service.ts"})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}
	if result.StaleEvidence != 1 {
		t.Errorf("Phase 2: stale evidence = %d, want 1", result.StaleEvidence)
	}
	if result.AffectedEdges != 1 {
		t.Errorf("Phase 2: affected edges = %d, want 1", result.AffectedEdges)
	}

	// Verify: edge is now stale, trust dropped
	edge, _ = s.GetEdgeByKey("code:provider:orders:ordersservice->code:provider:orders:paymentsservice:INJECTS")
	if edge.TrustScore >= 0.9 {
		t.Errorf("Phase 2: edge trust = %v, should be < 0.9 after invalidation", edge.TrustScore)
	}

	// === Phase 3: Re-scan, find evidence again ===
	evInput := validate.EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "file",
		FilePath:         "src/orders/orders.service.ts",
		LineStart:        12,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		RevisionID:       revID2,
	}
	if _, err := g.AddEdgeEvidence("code:provider:orders:ordersservice->code:provider:orders:paymentsservice:INJECTS", evInput); err != nil {
		t.Fatalf("AddEdgeEvidence: %v", err)
	}

	// Verify: edge trust restored (capped at 0.85 by code evidence)
	edge, _ = s.GetEdgeByKey("code:provider:orders:ordersservice->code:provider:orders:paymentsservice:INJECTS")
	if math.Abs(edge.TrustScore-0.85) > 0.01 {
		t.Errorf("Phase 3: edge trust = %v, want ~0.85 (restored, code evidence cap)", edge.TrustScore)
	}

	// Check evidence status is revalidated
	evidence, _ := s.ListEvidenceByEdge(edge.EdgeID)
	if len(evidence) == 0 {
		t.Fatal("Phase 3: no evidence found")
	}
	if evidence[0].EvidenceStatus != "revalidated" {
		t.Errorf("Phase 3: evidence status = %q, want revalidated", evidence[0].EvidenceStatus)
	}

	// === Phase 4: Negative evidence kills the edge ===
	revID3, err := s.CreateRevision("orders", "def456", "ghi789", "manual", "incremental", "{}")
	if err != nil {
		t.Fatalf("CreateRevision3: %v", err)
	}

	// Invalidate again
	g.InvalidateChanged("orders", revID3, []string{"src/orders/orders.service.ts"})

	// Add negative evidence — relationship confirmed removed
	negInput := validate.EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "file",
		FilePath:         "src/orders/orders.service.ts",
		LineStart:        12,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		Polarity:         "negative",
		Confidence:       0.92,
		RevisionID:       revID3,
	}
	if _, err := g.AddEdgeEvidence("code:provider:orders:ordersservice->code:provider:orders:paymentsservice:INJECTS", negInput); err != nil {
		t.Fatalf("AddEdgeEvidence negative: %v", err)
	}

	// Verify: edge is contradicted, trust near 0
	edge, _ = s.GetEdgeByKey("code:provider:orders:ordersservice->code:provider:orders:paymentsservice:INJECTS")
	if edge.TrustScore > 0.1 {
		t.Errorf("Phase 4: edge trust = %v, want near 0 (contradicted)", edge.TrustScore)
	}
	if edge.Active {
		t.Error("Phase 4: edge should be inactive (contradicted)")
	}

	// === Phase 5: FinalizeIncrementalScan — stale stays stale ===
	finalResult, err := g.FinalizeIncrementalScan("orders", revID3)
	if err != nil {
		t.Fatalf("FinalizeIncrementalScan: %v", err)
	}
	// We should have some stats
	t.Logf("Finalize: revalidated=%d stale=%d contradicted=%d", finalResult.Revalidated, finalResult.StillStale, finalResult.Contradicted)

	// === Phase 6: Impact reflects trust ===
	// Add a third node to test impact chain
	payload2 := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrdersController", FilePath: "src/orders/orders.controller.ts"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:controller:orders:orderscontroller", ToNodeKey: "code:provider:orders:ordersservice", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "edge", EdgeKey: "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS",
				SourceKind: "file", FilePath: "src/orders/orders.controller.ts", LineStart: 5,
				ExtractorID: "claude-code", ExtractorVersion: "1.0"},
		},
	}
	g.ImportAll(payload2, revID3)

	// Query impact from PaymentsService — should show controller at depth 2
	// but with lower score because middle edge (ordersservice→paymentsservice) is contradicted
	impactResult, err := g.QueryImpact("code:provider:orders:paymentsservice", ImpactOptions{MaxDepth: 5})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}

	for _, imp := range impactResult.Impacts {
		t.Logf("Impact: %s depth=%d score=%.2f trust_chain=%.3f", imp.NodeKey, imp.Depth, imp.ImpactScore, imp.TrustChain)
		if imp.NodeKey == "code:controller:orders:orderscontroller" {
			// This goes through the contradicted edge, so trust_chain should be very low
			if imp.TrustChain > 0.1 {
				t.Errorf("Phase 6: trust_chain through contradicted edge = %v, want < 0.1", imp.TrustChain)
			}
		}
	}
}

// TestIncrementalDependencyAdded verifies that adding a new node and edge
// to an existing graph makes the new node reachable via deps traversal.
func TestIncrementalDependencyAdded(t *testing.T) {
	g := setupTrustGraph(t)
	s := g.Store()

	// Phase 1: import A→B
	revID1, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	payload1 := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:a", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "A"},
			{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:controller:orders:a", ToNodeKey: "code:provider:orders:b", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
	}
	if _, err := g.ImportAll(payload1, revID1); err != nil {
		t.Fatalf("ImportAll phase 1: %v", err)
	}

	// Verify: A has 1 dep (B)
	deps, err := g.QueryDeps("code:controller:orders:a", 5, nil)
	if err != nil {
		t.Fatalf("QueryDeps phase 1: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("Phase 1: deps = %d, want 1", len(deps))
	}

	// Phase 2: add C, import A→C edge
	revID2, err := s.CreateRevision("orders", "abc123", "def456", "manual", "incremental", "{}")
	if err != nil {
		t.Fatalf("CreateRevision2: %v", err)
	}

	payload2 := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:module:orders:c", Layer: "code", NodeType: "module", DomainKey: "orders", Name: "C"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:controller:orders:a", ToNodeKey: "code:module:orders:c", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
	}
	if _, err := g.ImportAll(payload2, revID2); err != nil {
		t.Fatalf("ImportAll phase 2: %v", err)
	}

	// Verify: A now has 2 deps (B and C)
	deps, err = g.QueryDeps("code:controller:orders:a", 5, nil)
	if err != nil {
		t.Fatalf("QueryDeps phase 2: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("Phase 2: deps = %d, want 2", len(deps))
	}

	foundB, foundC := false, false
	for _, d := range deps {
		switch d.NodeKey {
		case "code:provider:orders:b":
			foundB = true
		case "code:module:orders:c":
			foundC = true
		}
	}
	if !foundB {
		t.Error("Phase 2: B not found in deps")
	}
	if !foundC {
		t.Error("Phase 2: C not found in deps")
	}
}

// TestIncrementalFileDeleted verifies that invalidating a file marks
// evidence from that file as stale, causing affected edges to lose trust.
func TestIncrementalFileDeleted(t *testing.T) {
	g := setupTrustGraph(t)
	s := g.Store()

	// Phase 1: import nodes with file_path and evidence
	revID1, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:ctrl", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "Ctrl", FilePath: "src/ctrl.ts"},
			{NodeKey: "code:provider:orders:svc", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Svc", FilePath: "src/svc.ts"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:controller:orders:ctrl", ToNodeKey: "code:provider:orders:svc", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "edge", EdgeKey: "code:controller:orders:ctrl->code:provider:orders:svc:INJECTS",
				SourceKind: "file", FilePath: "src/ctrl.ts", LineStart: 10,
				ExtractorID: "claude-code", ExtractorVersion: "1.0"},
		},
	}
	if _, err := g.ImportAll(payload, revID1); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Verify edge has trust before invalidation
	edge, err := s.GetEdgeByKey("code:controller:orders:ctrl->code:provider:orders:svc:INJECTS")
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	trustBefore := edge.TrustScore
	if trustBefore < 0.5 {
		t.Fatalf("Phase 1: edge trust = %v, want >= 0.5", trustBefore)
	}

	// Phase 2: invalidate the file
	revID2, err := s.CreateRevision("orders", "abc123", "def456", "manual", "incremental", "{}")
	if err != nil {
		t.Fatalf("CreateRevision2: %v", err)
	}

	result, err := g.InvalidateChanged("orders", revID2, []string{"src/ctrl.ts"})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}
	if result.StaleEvidence != 1 {
		t.Errorf("Phase 2: stale evidence = %d, want 1", result.StaleEvidence)
	}
	if result.AffectedEdges != 1 {
		t.Errorf("Phase 2: affected edges = %d, want 1", result.AffectedEdges)
	}

	// Verify: edge trust dropped after invalidation
	edge, _ = s.GetEdgeByKey("code:controller:orders:ctrl->code:provider:orders:svc:INJECTS")
	if edge.TrustScore >= trustBefore {
		t.Errorf("Phase 2: edge trust = %v, should be < %v after invalidation", edge.TrustScore, trustBefore)
	}

	// Verify: the evidence is stale
	evidence, _ := s.ListEvidenceByEdge(edge.EdgeID)
	if len(evidence) == 0 {
		t.Fatal("Phase 2: no evidence found")
	}
	if evidence[0].EvidenceStatus != "stale" {
		t.Errorf("Phase 2: evidence status = %q, want stale", evidence[0].EvidenceStatus)
	}
}

// TestIncrementalDependencyUnchanged verifies that after invalidation,
// re-adding the same evidence restores trust to the original level.
func TestIncrementalDependencyUnchanged(t *testing.T) {
	g := setupTrustGraph(t)
	s := g.Store()

	// Phase 1: import with evidence
	revID1, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:x", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "X", FilePath: "src/x.ts"},
			{NodeKey: "code:provider:orders:y", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Y", FilePath: "src/y.ts"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:controller:orders:x", ToNodeKey: "code:provider:orders:y", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "edge", EdgeKey: "code:controller:orders:x->code:provider:orders:y:INJECTS",
				SourceKind: "file", FilePath: "src/x.ts", LineStart: 5,
				ExtractorID: "claude-code", ExtractorVersion: "1.0"},
		},
	}
	if _, err := g.ImportAll(payload, revID1); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Record original trust
	edge, _ := s.GetEdgeByKey("code:controller:orders:x->code:provider:orders:y:INJECTS")
	originalTrust := edge.TrustScore
	t.Logf("Original trust: %.4f", originalTrust)

	// Phase 2: invalidate file
	revID2, err := s.CreateRevision("orders", "abc123", "def456", "manual", "incremental", "{}")
	if err != nil {
		t.Fatalf("CreateRevision2: %v", err)
	}

	_, err = g.InvalidateChanged("orders", revID2, []string{"src/x.ts"})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}

	// Verify trust dropped
	edge, _ = s.GetEdgeByKey("code:controller:orders:x->code:provider:orders:y:INJECTS")
	if edge.TrustScore >= originalTrust {
		t.Errorf("Phase 2: trust should have dropped, got %v >= %v", edge.TrustScore, originalTrust)
	}

	// Phase 3: re-add same evidence (simulating re-scan found same relationship)
	evInput := validate.EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "file",
		FilePath:         "src/x.ts",
		LineStart:        5,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		RevisionID:       revID2,
	}
	if _, err := g.AddEdgeEvidence("code:controller:orders:x->code:provider:orders:y:INJECTS", evInput); err != nil {
		t.Fatalf("AddEdgeEvidence: %v", err)
	}

	// Verify: trust restored to original level
	edge, _ = s.GetEdgeByKey("code:controller:orders:x->code:provider:orders:y:INJECTS")
	if math.Abs(edge.TrustScore-originalTrust) > 0.02 {
		t.Errorf("Phase 3: trust = %v, want ~%v (restored)", edge.TrustScore, originalTrust)
	}

	// Verify: evidence is revalidated
	evidence, _ := s.ListEvidenceByEdge(edge.EdgeID)
	hasRevalidated := false
	for _, ev := range evidence {
		if ev.EvidenceStatus == "revalidated" {
			hasRevalidated = true
		}
	}
	if !hasRevalidated {
		t.Error("Phase 3: expected at least one revalidated evidence")
	}
}

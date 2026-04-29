package graph

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

func TestTrustLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	g := New(s, reg)

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

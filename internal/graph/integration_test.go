package graph

import (
	"path/filepath"
	"testing"

	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

func TestFullWorkflow(t *testing.T) {
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

	// 1. Create revision
	revID, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	// 2. Bulk import — simulates what Claude Code would send
	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrdersController", RepoName: "orders-api", FilePath: "src/orders/orders.controller.ts"},
			{NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrdersService", RepoName: "orders-api", FilePath: "src/orders/orders.service.ts"},
			{NodeKey: "code:provider:orders:paymentsservice", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "PaymentsService", RepoName: "orders-api", FilePath: "src/payments/payments.service.ts"},
			{NodeKey: "contract:endpoint:orders:post:/orders", Layer: "contract", NodeType: "endpoint", DomainKey: "orders", Name: "POST /orders"},
			{NodeKey: "contract:endpoint:orders:post:/orders/{id}/capture", Layer: "contract", NodeType: "endpoint", DomainKey: "orders", Name: "POST /orders/{id}/capture"},
			{NodeKey: "service:service:orders:payments-api", Layer: "service", NodeType: "service", DomainKey: "orders", Name: "payments-api"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:controller:orders:orderscontroller", ToNodeKey: "code:provider:orders:ordersservice", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:controller:orders:orderscontroller", ToNodeKey: "contract:endpoint:orders:post:/orders", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
			{FromNodeKey: "code:controller:orders:orderscontroller", ToNodeKey: "contract:endpoint:orders:post:/orders/{id}/capture", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
			{FromNodeKey: "code:provider:orders:ordersservice", ToNodeKey: "code:provider:orders:paymentsservice", EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:provider:orders:paymentsservice", ToNodeKey: "service:service:orders:payments-api", EdgeType: "CALLS_SERVICE", DerivationKind: "linked", FromLayer: "code", ToLayer: "service"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "node", NodeKey: "code:controller:orders:orderscontroller", SourceKind: "file", RepoName: "orders-api", FilePath: "src/orders/orders.controller.ts", LineStart: 1, LineEnd: 50, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.99},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated != 6 {
		t.Errorf("nodes = %d, want 6", result.NodesCreated)
	}
	if result.EdgesCreated != 5 {
		t.Errorf("edges = %d, want 5", result.EdgesCreated)
	}
	if result.EvidenceCreated != 1 {
		t.Errorf("evidence = %d, want 1", result.EvidenceCreated)
	}

	// 3. Query deps — controller should have 3 direct deps (ordersservice + 2 endpoints)
	deps, err := g.QueryDeps("code:controller:orders:orderscontroller", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	if len(deps) != 3 {
		t.Errorf("direct deps = %d, want 3", len(deps))
	}

	// 4. Query reverse deps — who depends on payments-api? (depth 2)
	rdeps, err := g.QueryReverseDeps("service:service:orders:payments-api", 2, nil)
	if err != nil {
		t.Fatalf("QueryReverseDeps: %v", err)
	}
	if len(rdeps) < 2 {
		t.Errorf("reverse deps = %d, want >= 2", len(rdeps))
	}

	// 5. Stats
	stats, err := g.QueryStats("orders")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.NodeCount != 6 {
		t.Errorf("node count = %d, want 6", stats.NodeCount)
	}

	// 6. Idempotent re-import — no duplicates
	result2, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("second ImportAll: %v", err)
	}
	_ = result2
	allNodes, _ := s.ListNodes(store.NodeFilter{})
	if len(allNodes) != 6 {
		t.Errorf("total nodes after re-import = %d, want 6", len(allNodes))
	}

	// 7. Create snapshot
	snapID, err := s.CreateSnapshot(store.SnapshotRow{
		RevisionID: revID, DomainKey: "orders", Kind: "full",
		NodeCount: stats.NodeCount, EdgeCount: stats.EdgeCount,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snapID <= 0 {
		t.Error("expected positive snapshot_id")
	}

	// 8. Stale marking — new revision, only re-import controller
	revID2, _ := s.CreateRevision("orders", "abc123", "def456", "manual", "full", "{}")
	g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController",
	}, revID2)

	staleCount, err := s.MarkStaleNodes("orders", revID2)
	if err != nil {
		t.Fatalf("MarkStaleNodes: %v", err)
	}
	if staleCount != 5 {
		t.Errorf("stale nodes = %d, want 5", staleCount)
	}

	t.Log("Full workflow passed!")
}

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

func loadGoldenPayload(t *testing.T) graph.ImportPayload {
	t.Helper()
	data, err := os.ReadFile("../fixtures/orders-domain/expected-graph.json")
	if err != nil {
		t.Fatalf("reading golden payload: %v", err)
	}
	var payload graph.ImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parsing golden payload: %v", err)
	}
	return payload
}

func setupE2E(t *testing.T) (*graph.Graph, graph.ImportPayload) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "e2e.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	return graph.New(s, reg), loadGoldenPayload(t)
}

// Phase 1: Validate golden payload itself
func TestGoldenPayloadValid(t *testing.T) {
	payload := loadGoldenPayload(t)

	// All node keys unique
	nodeKeys := make(map[string]bool)
	for _, n := range payload.Nodes {
		if nodeKeys[n.NodeKey] {
			t.Errorf("duplicate node_key: %s", n.NodeKey)
		}
		nodeKeys[n.NodeKey] = true
	}

	// All edge from/to reference existing nodes
	for i, e := range payload.Edges {
		if !nodeKeys[e.FromNodeKey] {
			t.Errorf("edge[%d] from_node_key %q not in nodes", i, e.FromNodeKey)
		}
		if !nodeKeys[e.ToNodeKey] {
			t.Errorf("edge[%d] to_node_key %q not in nodes", i, e.ToNodeKey)
		}
	}

	// All evidence targets exist
	for i, ev := range payload.Evidence {
		if ev.TargetKind == "node" && !nodeKeys[ev.NodeKey] {
			t.Errorf("evidence[%d] node_key %q not in nodes", i, ev.NodeKey)
		}
	}

	// Counts
	if len(payload.Nodes) < 15 {
		t.Errorf("nodes = %d, want >= 15", len(payload.Nodes))
	}
	if len(payload.Edges) < 12 {
		t.Errorf("edges = %d, want >= 12", len(payload.Edges))
	}
}

// Phase 2: Import and basic queries
func TestE2EImport(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated < 15 {
		t.Errorf("nodes = %d, want >= 15", result.NodesCreated)
	}
	if result.EdgesCreated < 12 {
		t.Errorf("edges = %d, want >= 12", result.EdgesCreated)
	}

	// Query deps of OrdersController
	deps, err := g.QueryDeps("code:controller:orders:orderscontroller", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	if len(deps) < 1 {
		t.Errorf("deps = %d, want >= 1", len(deps))
	}
}

// Phase 3: Path queries
func TestE2EPath(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	// Directed path: OrdersController -> payments-api (service)
	result, err := g.QueryPath(
		"code:controller:orders:orderscontroller",
		"service:service:orders:payments-api",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"},
	)
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) == 0 {
		t.Fatal("expected at least 1 directed path from controller to payments-api")
	}
	p := result.Paths[0]
	if p.Depth < 2 {
		t.Errorf("path depth = %d, want >= 2", p.Depth)
	}

	// No directed dependency path: OrderCreatedProducer -> PaymentsController
	result2, err := g.QueryPath(
		"code:provider:orders:ordercreatedproducer",
		"code:controller:orders:paymentscontroller",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"},
	)
	if err != nil {
		t.Fatalf("QueryPath no-path: %v", err)
	}
	if len(result2.Paths) != 0 {
		t.Errorf("expected 0 directed paths, got %d", len(result2.Paths))
	}

	// Derivation filter: hard only should NOT find path (CALLS_SERVICE is linked)
	result3, err := g.QueryPath(
		"code:controller:orders:orderscontroller",
		"service:service:orders:payments-api",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed", DerivationFilter: []string{"hard"}},
	)
	if err != nil {
		t.Fatalf("QueryPath hard-only: %v", err)
	}
	if len(result3.Paths) != 0 {
		t.Errorf("expected 0 paths with hard-only filter, got %d", len(result3.Paths))
	}

	// Derivation filter: hard+linked SHOULD find path
	result4, err := g.QueryPath(
		"code:controller:orders:orderscontroller",
		"service:service:orders:payments-api",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed", DerivationFilter: []string{"hard", "linked"}},
	)
	if err != nil {
		t.Fatalf("QueryPath hard+linked: %v", err)
	}
	if len(result4.Paths) == 0 {
		t.Error("expected at least 1 path with hard+linked filter")
	}
}

// Phase 4: Impact
func TestE2EImpact(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	// PaymentsService changes -> OrdersService impacted (INJECTS depth 1), then OrdersController (depth 2)
	result, err := g.QueryImpact("code:provider:orders:paymentsservice", graph.ImpactOptions{
		MaxDepth: 4, MinScore: 0.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if result.TotalImpacted < 2 {
		t.Errorf("impacted = %d, want >= 2", result.TotalImpacted)
	}

	// Verify CONTAINS doesn't pollute: OrdersModule should NOT appear
	for _, imp := range result.Impacts {
		if imp.NodeKey == "code:module:orders:ordersmodule" {
			t.Error("OrdersModule should NOT be impacted (CONTAINS is structural)")
		}
	}

	// Closer nodes have higher scores
	if len(result.Impacts) >= 2 {
		if result.Impacts[0].ImpactScore <= result.Impacts[1].ImpactScore {
			t.Errorf("closer should score higher: %f <= %f", result.Impacts[0].ImpactScore, result.Impacts[1].ImpactScore)
		}
	}

	// EXPOSES_ENDPOINT excluded: endpoint change should not impact controller
	result2, err := g.QueryImpact("contract:endpoint:orders:post:/orders", graph.ImpactOptions{
		MaxDepth: 4, MinScore: 0.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact endpoint: %v", err)
	}
	for _, imp := range result2.Impacts {
		if imp.NodeKey == "code:controller:orders:orderscontroller" {
			t.Error("controller should NOT be reverse-impacted via EXPOSES_ENDPOINT")
		}
	}
}

// Phase 5: Lifecycle
func TestE2EIdempotent(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	// Re-import same payload
	g.ImportAll(payload, revID)

	nodes, _ := g.Store().ListNodes(store.NodeFilter{})
	if len(nodes) != len(payload.Nodes) {
		t.Errorf("after re-import: %d nodes, want %d (no duplicates)", len(nodes), len(payload.Nodes))
	}
}

func TestE2EStaleMarking(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	// New revision, only re-import 1 node
	revID2, _ := g.Store().CreateRevision("orders", "abc123", "def456", "manual", "full", "{}")
	g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController",
	}, revID2)

	staleCount, _ := g.Store().MarkStaleNodes("orders", revID2)
	expectedStale := len(payload.Nodes) - 1
	if int(staleCount) != expectedStale {
		t.Errorf("stale = %d, want %d", staleCount, expectedStale)
	}
}

func TestE2EStats(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	stats, err := g.QueryStats("orders")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.NodeCount != len(payload.Nodes) {
		t.Errorf("node count = %d, want %d", stats.NodeCount, len(payload.Nodes))
	}
	if stats.NodesByLayer["code"] < 5 {
		t.Errorf("code nodes = %d, want >= 5", stats.NodesByLayer["code"])
	}
	if stats.EdgesByDerivation == nil {
		t.Error("EdgesByDerivation is nil")
	}
	if stats.EdgesByDerivation["hard"] < 5 {
		t.Errorf("hard edges = %d, want >= 5", stats.EdgesByDerivation["hard"])
	}
}

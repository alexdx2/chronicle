package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

// seedABC seeds nodes A -> B -> C using INJECTS edges (code->code).
func seedABC(t *testing.T, g *Graph) int64 {
	t.Helper()
	revID := makeRevision(t, g)

	nodes := []validate.NodeInput{
		{NodeKey: "code:controller:test-domain:nodea", Layer: "code", NodeType: "controller", DomainKey: "test-domain", Name: "A"},
		{NodeKey: "code:provider:test-domain:nodeb", Layer: "code", NodeType: "provider", DomainKey: "test-domain", Name: "B"},
		{NodeKey: "code:module:test-domain:nodec", Layer: "code", NodeType: "module", DomainKey: "test-domain", Name: "C"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("seedABC UpsertNode %s: %v", n.Name, err)
		}
	}

	edges := []validate.EdgeInput{
		{
			FromNodeKey:    "code:controller:test-domain:nodea",
			ToNodeKey:      "code:provider:test-domain:nodeb",
			EdgeType:       "INJECTS",
			DerivationKind: "hard",
			FromLayer:      "code",
			ToLayer:        "code",
		},
		{
			FromNodeKey:    "code:provider:test-domain:nodeb",
			ToNodeKey:      "code:module:test-domain:nodec",
			EdgeType:       "INJECTS",
			DerivationKind: "hard",
			FromLayer:      "code",
			ToLayer:        "code",
		},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("seedABC UpsertEdge %s->%s: %v", e.FromNodeKey, e.ToNodeKey, err)
		}
	}

	return revID
}

func TestQueryDepsDepth1(t *testing.T) {
	g := setupGraph(t)
	seedABC(t, g)

	deps, err := g.QueryDeps("code:controller:test-domain:nodea", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep at depth 1, got %d: %+v", len(deps), deps)
	}
	if deps[0].Name != "B" {
		t.Errorf("expected dep B, got %s", deps[0].Name)
	}
}

func TestQueryDepsDepth2(t *testing.T) {
	g := setupGraph(t)
	seedABC(t, g)

	deps, err := g.QueryDeps("code:controller:test-domain:nodea", 2, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps at depth 2, got %d: %+v", len(deps), deps)
	}
}

func TestQueryReverseDepsDepth1(t *testing.T) {
	g := setupGraph(t)
	seedABC(t, g)

	rdeps, err := g.QueryReverseDeps("code:module:test-domain:nodec", 1, nil)
	if err != nil {
		t.Fatalf("QueryReverseDeps: %v", err)
	}
	if len(rdeps) != 1 {
		t.Fatalf("expected 1 reverse dep at depth 1, got %d: %+v", len(rdeps), rdeps)
	}
	if rdeps[0].Name != "B" {
		t.Errorf("expected reverse dep B, got %s", rdeps[0].Name)
	}
}

func TestQueryReverseDepsDepth2(t *testing.T) {
	g := setupGraph(t)
	seedABC(t, g)

	rdeps, err := g.QueryReverseDeps("code:module:test-domain:nodec", 2, nil)
	if err != nil {
		t.Fatalf("QueryReverseDeps: %v", err)
	}
	if len(rdeps) != 2 {
		t.Fatalf("expected 2 reverse deps at depth 2, got %d: %+v", len(rdeps), rdeps)
	}
}

// seedGraphForQuery seeds nodes in the "orders" domain with 2 hard INJECTS edges.
func seedGraphForQuery(t *testing.T) *Graph {
	t.Helper()
	g := setupGraph(t)
	revID, err := g.store.CreateRevision("orders", "", "abc123", "full_scan", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	nodes := []validate.NodeInput{
		{NodeKey: "code:controller:orders:nodea", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "A"},
		{NodeKey: "code:provider:orders:nodeb", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B"},
		{NodeKey: "code:module:orders:nodec", Layer: "code", NodeType: "module", DomainKey: "orders", Name: "C"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("seedGraphForQuery UpsertNode %s: %v", n.Name, err)
		}
	}

	edges := []validate.EdgeInput{
		{
			FromNodeKey:    "code:controller:orders:nodea",
			ToNodeKey:      "code:provider:orders:nodeb",
			EdgeType:       "INJECTS",
			DerivationKind: "hard",
			FromLayer:      "code",
			ToLayer:        "code",
		},
		{
			FromNodeKey:    "code:provider:orders:nodeb",
			ToNodeKey:      "code:module:orders:nodec",
			EdgeType:       "INJECTS",
			DerivationKind: "hard",
			FromLayer:      "code",
			ToLayer:        "code",
		},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("seedGraphForQuery UpsertEdge %s->%s: %v", e.FromNodeKey, e.ToNodeKey, err)
		}
	}

	return g
}

func TestQueryStats(t *testing.T) {
	g := setupGraph(t)
	seedABC(t, g)

	stats, err := g.QueryStats("test-domain")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.NodeCount != 3 {
		t.Errorf("NodeCount = %d, want 3", stats.NodeCount)
	}
	if stats.EdgeCount != 2 {
		t.Errorf("EdgeCount = %d, want 2", stats.EdgeCount)
	}
	if stats.ActiveNodes != 3 {
		t.Errorf("ActiveNodes = %d, want 3", stats.ActiveNodes)
	}
}

func TestQueryStatsDerivation(t *testing.T) {
	g := seedGraphForQuery(t)
	stats, err := g.QueryStats("orders")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.EdgesByDerivation == nil {
		t.Fatal("EdgesByDerivation is nil")
	}
	if stats.EdgesByDerivation["hard"] != 2 {
		t.Errorf("hard edges = %d, want 2", stats.EdgesByDerivation["hard"])
	}
}

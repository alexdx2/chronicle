package graph

import (
	"fmt"
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

func seedImpactGraph(t *testing.T) *Graph {
	t.Helper()
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	g.UpsertNode(validate.NodeInput{NodeKey: "code:module:orders:m", Layer: "code", NodeType: "module", DomainKey: "orders", Name: "M"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:controller:orders:a", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "A"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:orders:c", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "service:service:orders:d", Layer: "service", NodeType: "service", DomainKey: "orders", Name: "D"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "contract:endpoint:orders:post:/x", Layer: "contract", NodeType: "endpoint", DomainKey: "orders", Name: "POST /x"}, revID)

	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:module:orders:m", ToNodeKey: "code:controller:orders:a", EdgeType: "CONTAINS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:controller:orders:a", ToNodeKey: "code:provider:orders:b", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:provider:orders:b", ToNodeKey: "code:provider:orders:c", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:provider:orders:c", ToNodeKey: "service:service:orders:d", EdgeType: "CALLS_SERVICE", DerivationKind: "linked", FromLayer: "code", ToLayer: "service"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:controller:orders:a", ToNodeKey: "contract:endpoint:orders:post:/x", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"}, revID)

	return g
}

func TestImpactBasic(t *testing.T) {
	g := seedImpactGraph(t)
	// C changes → B impacted (depth 1, via reverse INJECTS), A impacted (depth 2)
	result, err := g.QueryImpact("code:provider:orders:c", ImpactOptions{MaxDepth: 4, MinScore: 0.0})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if result.TotalImpacted < 2 {
		t.Errorf("impacted = %d, want >= 2", result.TotalImpacted)
	}
	if len(result.Impacts) < 2 {
		t.Fatalf("impacts = %d, want >= 2", len(result.Impacts))
	}
	if result.Impacts[0].NodeKey != "code:provider:orders:b" {
		t.Errorf("first = %q, want b", result.Impacts[0].NodeKey)
	}
	if result.Impacts[0].Depth != 1 {
		t.Errorf("first depth = %d, want 1", result.Impacts[0].Depth)
	}
	if result.Impacts[1].NodeKey != "code:controller:orders:a" {
		t.Errorf("second = %q, want a", result.Impacts[1].NodeKey)
	}
	if result.Impacts[0].ImpactScore <= result.Impacts[1].ImpactScore {
		t.Errorf("closer should score higher: %f <= %f", result.Impacts[0].ImpactScore, result.Impacts[1].ImpactScore)
	}
}

func TestImpactExcludesContains(t *testing.T) {
	g := seedImpactGraph(t)
	// A changes. M -CONTAINS-> A, but CONTAINS is structural, so M should NOT be impacted.
	result, _ := g.QueryImpact("code:controller:orders:a", ImpactOptions{MaxDepth: 4, MinScore: 0.0})
	for _, imp := range result.Impacts {
		if imp.NodeKey == "code:module:orders:m" {
			t.Error("M should NOT be impacted via CONTAINS")
		}
	}
}

func TestImpactExcludesExposesEndpoint(t *testing.T) {
	g := seedImpactGraph(t)
	// POST /x changes. A -EXPOSES_ENDPOINT-> POST /x. No reverse impact.
	result, _ := g.QueryImpact("contract:endpoint:orders:post:/x", ImpactOptions{MaxDepth: 4, MinScore: 0.0})
	for _, imp := range result.Impacts {
		if imp.NodeKey == "code:controller:orders:a" {
			t.Error("A should NOT be reverse-impacted via EXPOSES_ENDPOINT")
		}
	}
}

func TestImpactMinScore(t *testing.T) {
	g := seedImpactGraph(t)
	result, _ := g.QueryImpact("code:provider:orders:c", ImpactOptions{MaxDepth: 4, MinScore: 99.0})
	if result.TotalImpacted > 1 {
		t.Errorf("impacted = %d, want <= 1 with high min_score", result.TotalImpacted)
	}
}

func TestImpactDerivationFilter(t *testing.T) {
	g := seedImpactGraph(t)
	// D changes. Reverse needs CALLS_SERVICE (linked). With hard-only = no impact.
	result, _ := g.QueryImpact("service:service:orders:d", ImpactOptions{MaxDepth: 4, MinScore: 0.0, DerivationFilter: []string{"hard"}})
	if result.TotalImpacted != 0 {
		t.Errorf("impacted = %d, want 0", result.TotalImpacted)
	}
}

func TestImpactAffectedEndpoints(t *testing.T) {
	g := seedImpactGraph(t)
	// C changes → B impacted (depth 1) → A impacted (depth 2).
	// A -EXPOSES_ENDPOINT-> POST /x. POST /x should appear in affected_surface.endpoints.
	result, err := g.QueryImpact("code:provider:orders:c", ImpactOptions{MaxDepth: 4, MinScore: 0.0})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if len(result.AffectedSurface.Endpoints) != 1 {
		t.Fatalf("affected endpoints = %d, want 1", len(result.AffectedSurface.Endpoints))
	}
	ep := result.AffectedSurface.Endpoints[0]
	if ep.NodeKey != "contract:endpoint:orders:post:/x" {
		t.Errorf("endpoint = %q, want post:/x", ep.NodeKey)
	}
	if ep.ExposedBy != "code:controller:orders:a" {
		t.Errorf("exposed_by = %q, want controller a", ep.ExposedBy)
	}
}

func TestImpactAffectedTopics(t *testing.T) {
	g := seedImpactGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha2", "manual", "full", "{}")

	// Add a topic and PUBLISHES_TOPIC edge from B.
	g.UpsertNode(validate.NodeInput{NodeKey: "contract:topic:orders:events", Layer: "contract", NodeType: "topic", DomainKey: "orders", Name: "events"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:provider:orders:b", ToNodeKey: "contract:topic:orders:events", EdgeType: "PUBLISHES_TOPIC", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"}, revID)

	// C changes → B impacted → B PUBLISHES_TOPIC events → events in affected_surface.topics.
	result, err := g.QueryImpact("code:provider:orders:c", ImpactOptions{MaxDepth: 4, MinScore: 0.0})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if len(result.AffectedSurface.Topics) != 1 {
		t.Fatalf("affected topics = %d, want 1", len(result.AffectedSurface.Topics))
	}
	tp := result.AffectedSurface.Topics[0]
	if tp.NodeKey != "contract:topic:orders:events" {
		t.Errorf("topic = %q, want events", tp.NodeKey)
	}
}

func TestImpactSurfaceDedup(t *testing.T) {
	g := seedImpactGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha2", "manual", "full", "{}")

	// Add another controller that also exposes the same endpoint.
	g.UpsertNode(validate.NodeInput{NodeKey: "code:controller:orders:a2", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "A2"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:provider:orders:b", ToNodeKey: "code:controller:orders:a2", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:controller:orders:a2", ToNodeKey: "contract:endpoint:orders:post:/x", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"}, revID)

	// C changes → B → A and A2 both impacted, both expose POST /x.
	// POST /x should appear only once.
	result, err := g.QueryImpact("code:provider:orders:c", ImpactOptions{MaxDepth: 4, MinScore: 0.0})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if len(result.AffectedSurface.Endpoints) != 1 {
		t.Errorf("affected endpoints = %d, want 1 (deduped)", len(result.AffectedSurface.Endpoints))
	}
}

// TestImpactLargeGraph creates a 20-node chain and verifies impact traversal,
// TopK limiting, and score descending sort.
func TestImpactLargeGraph(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	// Create a chain of 20 nodes: n0 → n1 → n2 → ... → n19
	// All use INJECTS (code→code), alternating controller/provider/module types.
	nodeTypes := []string{"controller", "provider", "module"}
	nodeKeys := make([]string, 20)
	for i := 0; i < 20; i++ {
		nt := nodeTypes[i%3]
		key := fmt.Sprintf("code:%s:test-domain:n%d", nt, i)
		nodeKeys[i] = key
		_, err := g.UpsertNode(validate.NodeInput{
			NodeKey:   key,
			Layer:     "code",
			NodeType:  nt,
			DomainKey: "test-domain",
			Name:      fmt.Sprintf("N%d", i),
		}, revID)
		if err != nil {
			t.Fatalf("UpsertNode n%d: %v", i, err)
		}
	}

	for i := 0; i < 19; i++ {
		_, err := g.UpsertEdge(validate.EdgeInput{
			FromNodeKey:    nodeKeys[i],
			ToNodeKey:      nodeKeys[i+1],
			EdgeType:       "INJECTS",
			DerivationKind: "hard",
			FromLayer:      "code",
			ToLayer:        "code",
		}, revID)
		if err != nil {
			t.Fatalf("UpsertEdge n%d→n%d: %v", i, i+1, err)
		}
	}

	// Impact from last node (n19): reverse BFS should find n18, n17, ..., n0
	// up to depth limit.
	t.Run("AllNodesFoundUpToDepthLimit", func(t *testing.T) {
		result, err := g.QueryImpact(nodeKeys[19], ImpactOptions{MaxDepth: 25, MinScore: 0.0})
		if err != nil {
			t.Fatalf("QueryImpact: %v", err)
		}
		// All 19 upstream nodes should be impacted.
		if result.TotalImpacted != 19 {
			t.Errorf("TotalImpacted = %d, want 19", result.TotalImpacted)
		}
		if result.MaxDepthReached != 19 {
			t.Errorf("MaxDepthReached = %d, want 19", result.MaxDepthReached)
		}
	})

	t.Run("DepthLimitCutsResults", func(t *testing.T) {
		result, err := g.QueryImpact(nodeKeys[19], ImpactOptions{MaxDepth: 5, MinScore: 0.0})
		if err != nil {
			t.Fatalf("QueryImpact: %v", err)
		}
		if result.TotalImpacted != 5 {
			t.Errorf("TotalImpacted = %d, want 5 (depth limited)", result.TotalImpacted)
		}
	})

	t.Run("TopKLimitsOutput", func(t *testing.T) {
		result, err := g.QueryImpact(nodeKeys[19], ImpactOptions{MaxDepth: 25, MinScore: 0.0, TopK: 3})
		if err != nil {
			t.Fatalf("QueryImpact: %v", err)
		}
		if len(result.Impacts) != 3 {
			t.Errorf("Impacts count = %d, want 3 (TopK)", len(result.Impacts))
		}
	})

	t.Run("ResultsSortedByScoreDescending", func(t *testing.T) {
		result, err := g.QueryImpact(nodeKeys[19], ImpactOptions{MaxDepth: 25, MinScore: 0.0})
		if err != nil {
			t.Fatalf("QueryImpact: %v", err)
		}
		for i := 1; i < len(result.Impacts); i++ {
			if result.Impacts[i].ImpactScore > result.Impacts[i-1].ImpactScore {
				t.Errorf("Impacts not sorted descending: [%d].score=%v > [%d].score=%v",
					i, result.Impacts[i].ImpactScore, i-1, result.Impacts[i-1].ImpactScore)
			}
		}
		// First impact (highest score) should be at depth 1 (closest node)
		if result.Impacts[0].Depth != 1 {
			t.Errorf("First impact depth = %d, want 1 (closest)", result.Impacts[0].Depth)
		}
	})
}

// TestImpactAffectedSurfaceFromChangedNode verifies that when the changed node
// itself exposes an endpoint, that endpoint appears in affected_surface.
func TestImpactAffectedSurfaceFromChangedNode(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	// Create a controller that exposes an endpoint.
	g.UpsertNode(validate.NodeInput{NodeKey: "code:controller:test-domain:src", Layer: "code", NodeType: "controller", DomainKey: "test-domain", Name: "Src"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "contract:endpoint:test-domain:get:/health", Layer: "contract", NodeType: "endpoint", DomainKey: "test-domain", Name: "GET /health"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:controller:test-domain:src", ToNodeKey: "contract:endpoint:test-domain:get:/health", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"}, revID)

	// Also add a provider that injects into the controller, to have at least one impact.
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:test-domain:dep", Layer: "code", NodeType: "provider", DomainKey: "test-domain", Name: "Dep"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:controller:test-domain:src", ToNodeKey: "code:provider:test-domain:dep", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)

	// Impact from the controller itself — it exposes GET /health.
	// The changed node's own endpoints should appear in affected_surface.
	result, err := g.QueryImpact("code:controller:test-domain:src", ImpactOptions{MaxDepth: 4, MinScore: 0.0})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}

	if len(result.AffectedSurface.Endpoints) != 1 {
		t.Fatalf("affected endpoints = %d, want 1", len(result.AffectedSurface.Endpoints))
	}
	ep := result.AffectedSurface.Endpoints[0]
	if ep.NodeKey != "contract:endpoint:test-domain:get:/health" {
		t.Errorf("endpoint = %q, want get:/health", ep.NodeKey)
	}
}

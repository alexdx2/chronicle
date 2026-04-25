package graph

import (
	"testing"

	"github.com/anthropics/depbot/internal/validate"
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

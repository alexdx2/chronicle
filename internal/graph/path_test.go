package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/internal/validate"
)

// seedPathGraph creates: A -INJECTS-> B -INJECTS-> C -CALLS_SERVICE(linked)-> D
// Also: M -CONTAINS-> A (structural), A -EXPOSES_ENDPOINT-> E (no reverse impact)
func seedPathGraph(t *testing.T) *Graph {
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

func TestQueryPathDirected(t *testing.T) {
	g := seedPathGraph(t)
	result, err := g.QueryPath("code:controller:orders:a", "service:service:orders:d", PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("paths = %d, want 1", len(result.Paths))
	}
	p := result.Paths[0]
	if p.Depth != 3 {
		t.Errorf("depth = %d, want 3", p.Depth)
	}
	if len(p.Nodes) != 4 {
		t.Errorf("nodes = %d, want 4", len(p.Nodes))
	}
	if p.PathScore <= 0 || p.PathScore > 1 {
		t.Errorf("path_score = %f, want (0, 1]", p.PathScore)
	}
}

func TestQueryPathNoPath(t *testing.T) {
	g := seedPathGraph(t)
	result, err := g.QueryPath("service:service:orders:d", "code:controller:orders:a", PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 0 {
		t.Errorf("paths = %d, want 0", len(result.Paths))
	}
}

func TestQueryPathConnected(t *testing.T) {
	g := seedPathGraph(t)
	result, err := g.QueryPath("service:service:orders:d", "code:controller:orders:a", PathOptions{MaxDepth: 6, TopK: 3, Mode: "connected"})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) == 0 {
		t.Error("expected at least 1 path in connected mode")
	}
}

func TestQueryPathExcludesStructural(t *testing.T) {
	g := seedPathGraph(t)
	result, err := g.QueryPath("code:module:orders:m", "code:provider:orders:b", PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 0 {
		t.Errorf("paths = %d, want 0 (CONTAINS is structural)", len(result.Paths))
	}
}

func TestQueryPathIncludeStructural(t *testing.T) {
	g := seedPathGraph(t)
	result, err := g.QueryPath("code:module:orders:m", "code:provider:orders:b", PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed", IncludeStructural: true})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Errorf("paths = %d, want 1", len(result.Paths))
	}
}

func TestQueryPathDerivationFilter(t *testing.T) {
	g := seedPathGraph(t)
	// hard-only: no path A->D because CALLS_SERVICE is linked
	r1, _ := g.QueryPath("code:controller:orders:a", "service:service:orders:d", PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed", DerivationFilter: []string{"hard"}})
	if len(r1.Paths) != 0 {
		t.Errorf("hard-only paths = %d, want 0", len(r1.Paths))
	}
	// hard+linked: path exists
	r2, _ := g.QueryPath("code:controller:orders:a", "service:service:orders:d", PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed", DerivationFilter: []string{"hard", "linked"}})
	if len(r2.Paths) != 1 {
		t.Errorf("hard+linked paths = %d, want 1", len(r2.Paths))
	}
}

package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// seedInsights builds a graph with a clear hub, a low-trust cross-domain edge,
// a service without endpoints, and an unresolved external node.
func seedInsights(t *testing.T, g *Graph) {
	t.Helper()
	revID := makeRevision(t, g)

	nodes := []validate.NodeInput{
		{NodeKey: "service:service:orders:order-api", Layer: "service", NodeType: "service", DomainKey: "orders", Name: "order-api"},
		{NodeKey: "service:service:billing:billing-api", Layer: "service", NodeType: "service", DomainKey: "billing", Name: "billing-api"},
		{NodeKey: "code:provider:orders:order-service", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrderService"},
		{NodeKey: "code:controller:orders:order-controller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrderController"},
		{NodeKey: "code:module:orders:order-module", Layer: "code", NodeType: "module", DomainKey: "orders", Name: "OrderModule"},
		{NodeKey: "contract:endpoint:orders:post-orders", Layer: "contract", NodeType: "endpoint", DomainKey: "orders", Name: "POST /orders"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}
	// External (unresolved) node.
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "service:service:payments:payments-api", Layer: "service", NodeType: "service",
		DomainKey: "payments", Name: "payments-api", Status: "external",
	}, revID); err != nil {
		t.Fatal(err)
	}

	// order-service is the hub: connected to module, controller, endpoint.
	edges := []validate.EdgeInput{
		{FromNodeKey: "code:module:orders:order-module", ToNodeKey: "code:provider:orders:order-service", EdgeType: "CONTAINS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		{FromNodeKey: "code:controller:orders:order-controller", ToNodeKey: "code:provider:orders:order-service", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		{FromNodeKey: "code:provider:orders:order-service", ToNodeKey: "contract:endpoint:orders:post-orders", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
		{FromNodeKey: "service:service:orders:order-api", ToNodeKey: "contract:endpoint:orders:post-orders", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "service", ToLayer: "contract"},
		// Low-trust cross-domain edge: orders → billing, inferred (low derivation).
		{FromNodeKey: "code:provider:orders:order-service", ToNodeKey: "service:service:billing:billing-api", EdgeType: "CALLS_SERVICE", DerivationKind: "inferred", FromLayer: "code", ToLayer: "service"},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge %s->%s: %v", e.FromNodeKey, e.ToNodeKey, err)
		}
	}
}

func TestInsightsHubs(t *testing.T) {
	g := setupGraphDefaults(t)
	seedInsights(t, g)

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if len(ins.Hubs) == 0 {
		t.Fatal("expected hubs")
	}
	if ins.Hubs[0].NodeKey != "code:provider:orders:order-service" {
		t.Errorf("expected order-service as top hub, got %q (degree %d)", ins.Hubs[0].NodeKey, ins.Hubs[0].Degree)
	}
}

func TestInsightsCrossDomainLowTrustFirst(t *testing.T) {
	g := setupGraphDefaults(t)
	seedInsights(t, g)

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if len(ins.SuspiciousEdges) == 0 {
		t.Fatal("expected a suspicious cross-domain edge")
	}
	first := ins.SuspiciousEdges[0]
	if first.From != "code:provider:orders:order-service" || first.To != "service:service:billing:billing-api" {
		t.Errorf("expected orders→billing edge first, got %s→%s", first.From, first.To)
	}
	// Sorted ascending: the lowest-trust edge surfaces first.
	for i := 1; i < len(ins.SuspiciousEdges); i++ {
		if ins.SuspiciousEdges[i-1].Trust > ins.SuspiciousEdges[i].Trust {
			t.Errorf("suspicious edges not sorted by trust ascending")
		}
	}
}

func TestInsightsVerificationTargets(t *testing.T) {
	g := setupGraphDefaults(t)
	seedInsights(t, g)

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	for _, e := range ins.VerificationTargets {
		if e.Trust >= 0.7 {
			t.Errorf("verification target should be low-trust (<0.7), got %.2f for %s", e.Trust, e.EdgeKey)
		}
	}
}

func TestInsightsGaps(t *testing.T) {
	g := setupGraphDefaults(t)
	seedInsights(t, g)

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	kinds := map[string]bool{}
	for _, gap := range ins.Gaps {
		kinds[gap.Kind] = true
	}
	if !kinds["unresolved_external"] {
		t.Errorf("expected an unresolved_external gap, got %+v", ins.Gaps)
	}
}

func TestInsightsMarkdownDeterministic(t *testing.T) {
	g := setupGraphDefaults(t)
	seedInsights(t, g)

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	md1 := ins.Markdown()
	md2 := ins.Markdown()
	if md1 != md2 {
		t.Error("Markdown not deterministic")
	}
	if !strings.Contains(md1, "chronicle_node_search") {
		t.Error("suggested queries must reference chronicle_node_search")
	}
}

var _ = store.DomainFromNodeKey

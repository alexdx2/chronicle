package graph

import (
	"sort"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// setupFlowTestGraph creates a graph backed by the defaults registry (which has flow
// layer, use_case node type, TRIGGERS_FLOW and REQUIRES edge types).
func setupFlowTestGraph(t *testing.T) (*Graph, int64) {
	t.Helper()
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	return g, revID
}

// upsertTestNode is a helper to upsert a node in the test graph.
func upsertTestNode(t *testing.T, g *Graph, revID int64, key, layer, nodeType, domain, name string) {
	t.Helper()
	_, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   key,
		Layer:     layer,
		NodeType:  nodeType,
		DomainKey: domain,
		Name:      name,
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode(%q): %v", key, err)
	}
}

// upsertTestEdge is a helper to upsert an edge in the test graph.
func upsertTestEdge(t *testing.T, g *Graph, revID int64, fromKey, toKey, edgeType, fromLayer, toLayer string) {
	t.Helper()
	_, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey:    fromKey,
		ToNodeKey:      toKey,
		EdgeType:       edgeType,
		DerivationKind: "hard",
		FromLayer:      fromLayer,
		ToLayer:        toLayer,
	}, revID)
	if err != nil {
		t.Fatalf("UpsertEdge(%q->%q %s): %v", fromKey, toKey, edgeType, err)
	}
}

// listFlowNodes returns all flow:use_case nodes in the domain.
func listFlowNodes(t *testing.T, g *Graph, domain string) []store.NodeRow {
	t.Helper()
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: domain, NodeType: "use_case"})
	if err != nil {
		t.Fatalf("ListNodes(use_case): %v", err)
	}
	return nodes
}

// listEdgesOfType returns all active edges of a given type.
func listEdgesOfType(t *testing.T, g *Graph, edgeType string) []store.EdgeRow {
	t.Helper()
	active := true
	edges, err := g.store.ListEdges(store.EdgeFilter{EdgeType: edgeType, Active: &active})
	if err != nil {
		t.Fatalf("ListEdges(%s): %v", edgeType, err)
	}
	return edges
}

// edgeToNodeKeys is a set of (from->to) edge pairs for quick membership checks.
func edgeToSet(edges []store.EdgeRow) map[string]bool {
	out := make(map[string]bool, len(edges))
	for _, e := range edges {
		out[e.FromNodeKey+"->"+e.ToNodeKey] = true
	}
	return out
}

// --- Tests ---

// TestDeriveFlows_BasicCase: endpoint POST /arena/attack exposed by ArenaController which injects
// ArenaService; ArenaService injects PrismaService + TomClient.
// Expected: flow "attack (POST /arena/attack)", TRIGGERS_FLOW from endpoint,
// REQUIRES exactly {ArenaService, PrismaService, TomClient}.
func TestDeriveFlows_BasicCase(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "tom-and-jerry"

	// Endpoint
	epKey := "contract:endpoint:" + domain + ":post:/arena/attack"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "POST /arena/attack")

	// Controller
	ctrlKey := "code:controller:" + domain + ":arenacontroller"
	upsertTestNode(t, g, revID, ctrlKey, "code", "controller", domain, "ArenaController")

	// Providers
	arenaSvcKey := "code:provider:" + domain + ":arenaservice"
	upsertTestNode(t, g, revID, arenaSvcKey, "code", "provider", domain, "ArenaService")
	prismaKey := "code:provider:" + domain + ":prismaservice"
	upsertTestNode(t, g, revID, prismaKey, "code", "provider", domain, "PrismaService")
	tomClientKey := "code:provider:" + domain + ":tomclient"
	upsertTestNode(t, g, revID, tomClientKey, "code", "provider", domain, "TomClient")

	// Edges
	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")
	upsertTestEdge(t, g, revID, ctrlKey, arenaSvcKey, "INJECTS", "code", "code")
	upsertTestEdge(t, g, revID, arenaSvcKey, prismaKey, "INJECTS", "code", "code")
	upsertTestEdge(t, g, revID, arenaSvcKey, tomClientKey, "INJECTS", "code", "code")

	// Derive flows
	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}

	// Verify: exactly one flow node
	flows := listFlowNodes(t, g, domain)
	if len(flows) != 1 {
		t.Fatalf("want 1 flow node, got %d: %v", len(flows), flows)
	}

	flow := flows[0]
	wantName := "attack (POST /arena/attack)"
	if flow.Name != wantName {
		t.Errorf("flow name = %q, want %q", flow.Name, wantName)
	}
	wantKey := "flow:use_case:" + domain + ":post:/arena/attack"
	if flow.NodeKey != wantKey {
		t.Errorf("flow key = %q, want %q", flow.NodeKey, wantKey)
	}
	if flow.Layer != "flow" {
		t.Errorf("flow layer = %q, want \"flow\"", flow.Layer)
	}
	if flow.NodeType != "use_case" {
		t.Errorf("flow node_type = %q, want \"use_case\"", flow.NodeType)
	}

	// Verify: TRIGGERS_FLOW edge from endpoint to flow
	triggers := listEdgesOfType(t, g, "TRIGGERS_FLOW")
	triggerSet := edgeToSet(triggers)
	triggerEdge := epKey + "->" + wantKey
	if !triggerSet[triggerEdge] {
		t.Errorf("missing TRIGGERS_FLOW edge %q; got %v", triggerEdge, triggers)
	}

	// Verify: REQUIRES edges - flow → {ArenaService, PrismaService, TomClient}
	requires := listEdgesOfType(t, g, "REQUIRES")
	requiresTo := make(map[string]bool)
	for _, e := range requires {
		if e.FromNodeKey == wantKey {
			requiresTo[e.ToNodeKey] = true
		}
	}
	wantProviders := []string{arenaSvcKey, prismaKey, tomClientKey}
	for _, pKey := range wantProviders {
		if !requiresTo[pKey] {
			t.Errorf("flow missing REQUIRES edge to %q; got requires: %v", pKey, requiresTo)
		}
	}
	if len(requiresTo) != len(wantProviders) {
		t.Errorf("flow has %d REQUIRES edges, want %d; edges: %v", len(requiresTo), len(wantProviders), requiresTo)
	}
}

// TestDeriveFlows_Cycle: A→B→A (INJECTS cycle) → terminates, both included once.
func TestDeriveFlows_Cycle(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "cycle-domain"

	epKey := "contract:endpoint:" + domain + ":post:/do/thing"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "POST /do/thing")

	ctrlKey := "code:controller:" + domain + ":mycontroller"
	upsertTestNode(t, g, revID, ctrlKey, "code", "controller", domain, "MyController")

	svcAKey := "code:provider:" + domain + ":servicea"
	upsertTestNode(t, g, revID, svcAKey, "code", "provider", domain, "ServiceA")
	svcBKey := "code:provider:" + domain + ":serviceb"
	upsertTestNode(t, g, revID, svcBKey, "code", "provider", domain, "ServiceB")

	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")
	upsertTestEdge(t, g, revID, ctrlKey, svcAKey, "INJECTS", "code", "code")
	// Cycle: A→B→A
	upsertTestEdge(t, g, revID, svcAKey, svcBKey, "INJECTS", "code", "code")
	upsertTestEdge(t, g, revID, svcBKey, svcAKey, "INJECTS", "code", "code")

	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}

	flows := listFlowNodes(t, g, domain)
	if len(flows) != 1 {
		t.Fatalf("want 1 flow node, got %d", len(flows))
	}

	// Both ServiceA and ServiceB should appear exactly once in REQUIRES
	requires := listEdgesOfType(t, g, "REQUIRES")
	flowKey := flows[0].NodeKey
	requiresTo := make(map[string]bool)
	for _, e := range requires {
		if e.FromNodeKey == flowKey {
			requiresTo[e.ToNodeKey] = true
		}
	}

	if !requiresTo[svcAKey] {
		t.Errorf("ServiceA missing from REQUIRES; got %v", requiresTo)
	}
	if !requiresTo[svcBKey] {
		t.Errorf("ServiceB missing from REQUIRES; got %v", requiresTo)
	}
	if len(requiresTo) != 2 {
		t.Errorf("want 2 REQUIRES edges, got %d: %v", len(requiresTo), requiresTo)
	}
}

// TestDeriveFlows_Idempotent: re-running DeriveFlows must not create duplicate nodes or edges.
func TestDeriveFlows_Idempotent(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "idempotent-domain"

	epKey := "contract:endpoint:" + domain + ":get:/health"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "GET /health")
	ctrlKey := "code:controller:" + domain + ":healthcontroller"
	upsertTestNode(t, g, revID, ctrlKey, "code", "controller", domain, "HealthController")
	svcKey := "code:provider:" + domain + ":healthservice"
	upsertTestNode(t, g, revID, svcKey, "code", "provider", domain, "HealthService")

	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")
	upsertTestEdge(t, g, revID, ctrlKey, svcKey, "INJECTS", "code", "code")

	// Run twice
	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows (1st): %v", err)
	}
	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows (2nd): %v", err)
	}

	// Exactly 1 flow node
	flows := listFlowNodes(t, g, domain)
	if len(flows) != 1 {
		t.Errorf("want 1 flow node after 2 runs, got %d", len(flows))
	}

	// Exactly 1 TRIGGERS_FLOW edge
	triggers := listEdgesOfType(t, g, "TRIGGERS_FLOW")
	if len(triggers) != 1 {
		t.Errorf("want 1 TRIGGERS_FLOW edge after 2 runs, got %d: %v", len(triggers), triggers)
	}

	// Exactly 1 REQUIRES edge
	active := true
	requires, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "REQUIRES", Active: &active})
	if len(requires) != 1 {
		t.Errorf("want 1 REQUIRES edge after 2 runs, got %d: %v", len(requires), requires)
	}
}

// TestDeriveFlows_NoProviders: endpoint with controller that injects nothing →
// flow with 0 REQUIRES still created (but 1 TRIGGERS_FLOW).
func TestDeriveFlows_NoProviders(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "no-providers-domain"

	epKey := "contract:endpoint:" + domain + ":get:/ping"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "GET /ping")
	ctrlKey := "code:controller:" + domain + ":pingcontroller"
	upsertTestNode(t, g, revID, ctrlKey, "code", "controller", domain, "PingController")

	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")
	// No INJECTS edges from the controller

	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}

	flows := listFlowNodes(t, g, domain)
	if len(flows) != 1 {
		t.Fatalf("want 1 flow node, got %d", len(flows))
	}
	if flows[0].Name != "ping (GET /ping)" {
		t.Errorf("flow name = %q, want %q", flows[0].Name, "ping (GET /ping)")
	}

	triggers := listEdgesOfType(t, g, "TRIGGERS_FLOW")
	if len(triggers) != 1 {
		t.Errorf("want 1 TRIGGERS_FLOW edge, got %d", len(triggers))
	}

	active := true
	requires, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "REQUIRES", Active: &active})
	if len(requires) != 0 {
		t.Errorf("want 0 REQUIRES edges, got %d: %v", len(requires), requires)
	}
}

// TestDeriveFlows_MultipleEndpoints: multiple endpoints on one controller → one flow per endpoint.
func TestDeriveFlows_MultipleEndpoints(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "multi-ep-domain"

	ctrl := "code:controller:" + domain + ":arenacontroller"
	upsertTestNode(t, g, revID, ctrl, "code", "controller", domain, "ArenaController")

	svc := "code:provider:" + domain + ":arenaservice"
	upsertTestNode(t, g, revID, svc, "code", "provider", domain, "ArenaService")
	upsertTestEdge(t, g, revID, ctrl, svc, "INJECTS", "code", "code")

	// Three endpoints
	endpoints := []struct {
		key      string
		name     string
		wantFlow string
	}{
		{"contract:endpoint:" + domain + ":post:/arena/attack", "POST /arena/attack", "attack (POST /arena/attack)"},
		{"contract:endpoint:" + domain + ":get:/arena/history", "GET /arena/history", "history (GET /arena/history)"},
		{"contract:endpoint:" + domain + ":ws:/watch-battle", "WS /watch-battle", "watch-battle (WS /watch-battle)"},
	}
	for _, ep := range endpoints {
		upsertTestNode(t, g, revID, ep.key, "contract", "endpoint", domain, ep.name)
		upsertTestEdge(t, g, revID, ctrl, ep.key, "EXPOSES_ENDPOINT", "code", "contract")
	}

	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}

	flows := listFlowNodes(t, g, domain)
	if len(flows) != 3 {
		t.Fatalf("want 3 flow nodes, got %d: %v", len(flows), flows)
	}

	// Collect flow names for assertion
	names := make([]string, len(flows))
	for i, f := range flows {
		names[i] = f.Name
	}
	sort.Strings(names)

	wantNames := []string{
		"attack (POST /arena/attack)",
		"history (GET /arena/history)",
		"watch-battle (WS /watch-battle)",
	}
	sort.Strings(wantNames)

	for i, want := range wantNames {
		if names[i] != want {
			t.Errorf("flow[%d] name = %q, want %q", i, names[i], want)
		}
	}

	// Each flow should have exactly one REQUIRES (to ArenaService)
	active := true
	requires, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "REQUIRES", Active: &active})
	if len(requires) != 3 {
		t.Errorf("want 3 REQUIRES edges, got %d", len(requires))
	}
}

// TestDeriveFlowName tests the name derivation helper directly.
func TestDeriveFlowName(t *testing.T) {
	cases := []struct {
		endpointName string
		want         string
	}{
		{"POST /arena/attack", "attack (POST /arena/attack)"},
		{"GET /stats/leaderboard", "leaderboard (GET /stats/leaderboard)"},
		{"WS /watch-battle", "watch-battle (WS /watch-battle)"},
		{"GET /", "/ (GET /)"},
		{"DELETE /users/{id}", "{id} (DELETE /users/{id})"},
	}
	for _, tc := range cases {
		got := deriveFlowName(tc.endpointName)
		if got != tc.want {
			t.Errorf("deriveFlowName(%q) = %q, want %q", tc.endpointName, got, tc.want)
		}
	}
}

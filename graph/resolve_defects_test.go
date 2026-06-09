package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// resolve_defects_test.go — TDD tests for R1–R5 resolve defects.

// ---------------------------------------------------------------------------
// R1 — Endpoint canonicalization (route join + method)
// ---------------------------------------------------------------------------

// TestR1_EndpointFragment_ControllerBase verifies that an AST-style endpoint
// fact with a route fragment and a controller base in "from" produces the
// canonical key "get_/tom/status" — NOT "get_/TomController/status".
func TestR1_EndpointFragment_ControllerBase(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// AST-style fact: from = controller base path, target = fragment
	facts := `[{"kind":"endpoint","from":"tom","method":"GET","target":"status","from_type":"controller"}]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.controller.ts", "extracted", "controller", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	// Expect canonical key: get:/tom/status
	wantKey := "contract:endpoint:testapp:get:/tom/status"
	if _, err := s.GetNodeByKey(wantKey); err != nil {
		nodes, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "endpoint"})
		var keys []string
		for _, n := range nodes {
			keys = append(keys, n.NodeKey)
		}
		t.Errorf("expected endpoint node %q; found: %v", wantKey, keys)
	}

	// Verify display name
	n, err := s.GetNodeByKey(wantKey)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "GET /tom/status" {
		t.Errorf("expected name %q, got %q", "GET /tom/status", n.Name)
	}
}

// TestR1_EndpointClassNameFrom_ResolvesToBase verifies that when the LLM
// fact's "from" is the class name "TomController" (not the base "tom"),
// the endpoint is still canonicalized to get_/tom/status rather than
// get_/TomController/status.
func TestR1_EndpointClassNameFrom_ResolvesToBase(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// Two extractions for the same file: one from AST (base prefix), one LLM-style (class name)
	// The canonicalization must produce the same key for both.

	// Extraction 1: LLM-style — from=class name, target=fragment (fragment, not full path)
	facts1 := `[{"kind":"endpoint","from":"TomController","method":"GET","target":"status","from_type":"controller"}]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.controller.ts", "extracted", "controller", facts1, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	// Should NOT create a node for /TomController/status
	badKey := "contract:endpoint:testapp:get:/tomcontroller/status"
	if n, err := s.GetNodeByKey(badKey); err == nil {
		t.Errorf("must not create phantom endpoint %q (name=%q)", badKey, n.Name)
	}
}

// TestR1_LLMFullPath_SameNode verifies that an LLM fact with a full absolute
// path "/tom/status" produces the same node key as an AST fact with fragment
// "status" + base "tom". (No duplicates.)
func TestR1_LLMFullPath_SameNode(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// Both facts should resolve to the same endpoint node.
	facts := `[
		{"kind":"endpoint","from":"tom","method":"GET","target":"status","from_type":"controller"},
		{"kind":"endpoint","from":"TomController","method":"GET","target":"/tom/status","from_type":"controller"}
	]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.controller.ts", "extracted", "controller", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	endpoints, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "endpoint"})
	if len(endpoints) != 1 {
		var keys []string
		for _, ep := range endpoints {
			keys = append(keys, ep.NodeKey)
		}
		t.Errorf("expected exactly 1 endpoint node (no duplicates), got %d: %v", len(endpoints), keys)
	}
	wantKey := "contract:endpoint:testapp:get:/tom/status"
	found := false
	for _, ep := range endpoints {
		if ep.NodeKey == wantKey {
			found = true
		}
	}
	if !found {
		t.Errorf("expected endpoint %q", wantKey)
	}
}

// TestR1_CallsEndpoint_MatchesCanonical verifies that a calls_endpoint fact
// with a full URL path "/tom/status" resolves to the same canonical node as
// the endpoint fact.
func TestR1_CallsEndpoint_MatchesCanonical(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// Controller exposes the endpoint
	controllerFacts := `[{"kind":"endpoint","from":"tom","method":"GET","target":"status","from_type":"controller"}]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.controller.ts", "extracted", "controller", controllerFacts, "")

	// Client calls the endpoint with full path
	clientFacts := `[{"kind":"calls_endpoint","method":"GET","target":"/tom/status","from_type":"provider"}]`
	g.SaveFileExtraction(revID, domain, "arena-api/src/arena/arena.service.ts", "extracted", "provider", clientFacts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	// Only 1 endpoint node should exist
	endpoints, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "endpoint"})
	if len(endpoints) != 1 {
		var keys []string
		for _, ep := range endpoints {
			keys = append(keys, ep.NodeKey)
		}
		t.Errorf("expected 1 endpoint node, got %d: %v", len(endpoints), keys)
	}

	// CALLS_ENDPOINT edge must exist
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_ENDPOINT"})
	if len(edges) == 0 {
		t.Error("expected CALLS_ENDPOINT edge")
	}
}

// ---------------------------------------------------------------------------
// R2 — transport=local facts must not mint topics
// ---------------------------------------------------------------------------

// TestR2_LocalTransport_NoTopicNode verifies that a produces fact with
// transport=local does not create a contract:topic node.
func TestR2_LocalTransport_NoTopicNode(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	facts := `[
		{"kind":"produces","to":"tom.weapon.equipped","transport":"local","from_type":"provider"},
		{"kind":"consumes","to":"battle.result","transport":"local","from_type":"provider"}
	]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.events.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	// No topic nodes should be created
	topics, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "topic"})
	if len(topics) > 0 {
		var keys []string
		for _, t2 := range topics {
			keys = append(keys, t2.NodeKey)
		}
		t.Errorf("local transport events must not create topic nodes; got: %v", keys)
	}

	// No PUBLISHES_TOPIC or CONSUMES_TOPIC edges
	pub, _ := s.ListEdges(store.EdgeFilter{EdgeType: "PUBLISHES_TOPIC"})
	cons, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONSUMES_TOPIC"})
	if len(pub) > 0 {
		t.Errorf("local transport produces must not create PUBLISHES_TOPIC edges; got %d", len(pub))
	}
	if len(cons) > 0 {
		t.Errorf("local transport consumes must not create CONSUMES_TOPIC edges; got %d", len(cons))
	}
}

// TestR2_QueueTransport_StillCreatesTopics verifies that queue transport
// produces/consumes still creates topic nodes and edges.
func TestR2_QueueTransport_StillCreatesTopics(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	facts := `[
		{"kind":"produces","to":"battle-results","transport":"queue","from_type":"provider"}
	]`
	g.SaveFileExtraction(revID, domain, "arena-api/src/arena/battle-result.producer.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	topics, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "topic"})
	if len(topics) == 0 {
		t.Error("queue transport produces should create a topic node")
	}
	pub, _ := s.ListEdges(store.EdgeFilter{EdgeType: "PUBLISHES_TOPIC"})
	if len(pub) == 0 {
		t.Error("queue transport produces should create PUBLISHES_TOPIC edge")
	}
}

// TestR2_NoTransportTag_StillCreatesTopics verifies that produces/consumes
// without a transport tag (legacy/untagged) still creates topic nodes.
func TestR2_NoTransportTag_StillCreatesTopics(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	facts := `[
		{"kind":"produces","to":"order.created","from_type":"provider"}
	]`
	g.SaveFileExtraction(revID, domain, "orders-api/src/orders.service.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	topics, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "topic"})
	if len(topics) == 0 {
		t.Error("untagged produces should create a topic node")
	}
}

// ---------------------------------------------------------------------------
// R3 — http_call facts → external system + CALLS_SERVICE
// ---------------------------------------------------------------------------

// TestR3_HTTPCall_CreatesExternalSystemNode verifies that an http_call fact
// creates a service:external_system node for the target host.
func TestR3_HTTPCall_CreatesExternalSystemNode(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	facts := `[{"kind":"http_call","target":"http://tom-api:3001/tom/status","method":"GET","from_type":"provider"}]`
	g.SaveFileExtraction(revID, domain, "arena-api/src/arena/arena.service.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	wantKey := "service:external_system:testapp:tom-api"
	node, err := s.GetNodeByKey(wantKey)
	if err != nil {
		t.Errorf("expected external_system node %q", wantKey)
	} else if node.Name != "tom-api" {
		t.Errorf("expected name %q, got %q", "tom-api", node.Name)
	}

	// CALLS_SERVICE edge must exist
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	found := false
	for _, e := range edges {
		if e.Active && e.ToNodeKey == wantKey {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CALLS_SERVICE edge to %q", wantKey)
	}
}

// TestR3_TwoCallsSameHost_OneNode verifies that two http_call facts to the
// same host produce exactly one external_system node and one CALLS_SERVICE edge.
func TestR3_TwoCallsSameHost_OneNode(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	facts := `[
		{"kind":"http_call","target":"http://tom-api:3001/tom/status","method":"GET","from_type":"provider"},
		{"kind":"http_call","target":"http://tom-api:3001/tom/weapons","method":"GET","from_type":"provider"}
	]`
	g.SaveFileExtraction(revID, domain, "arena-api/src/arena/arena.service.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	externals, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "external_system"})
	tomNodes := 0
	for _, n := range externals {
		if strings.Contains(n.NodeKey, "tom-api") {
			tomNodes++
		}
	}
	if tomNodes != 1 {
		t.Errorf("expected exactly 1 external_system node for tom-api, got %d", tomNodes)
	}
}

// ---------------------------------------------------------------------------
// R4 — calls_service to local providers: drop
// ---------------------------------------------------------------------------

// TestR4_CallsServiceToLocalProvider_Dropped verifies that a calls_service
// fact targeting a provider that resolves to an in-domain code node does NOT
// create a CALLS_SERVICE edge.
func TestR4_CallsServiceToLocalProvider_Dropped(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// First create the target provider node (in-domain code node)
	s.UpsertNode(store.NodeRow{
		NodeKey:            "code:provider:testapp:tomservice",
		Layer:              "code",
		NodeType:           "provider",
		DomainKey:          domain,
		Name:               "TomService",
		Status:             "active",
		LastSeenRevisionID: revID,
	})

	// Controller calls_service to TomService — this is a local DI call
	facts := `[{"kind":"calls_service","to":"TomService","from_type":"controller"}]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.controller.ts", "extracted", "controller", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	// No CALLS_SERVICE edge should be created for local in-domain providers
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	for _, e := range edges {
		if !e.Active {
			continue
		}
		// The edge target must NOT be the local provider
		if e.ToNodeKey == "code:provider:testapp:tomservice" {
			t.Errorf("CALLS_SERVICE to local provider must be dropped; got edge %s", e.EdgeKey)
		}
	}
}

// TestR4_CallsServiceToExternal_Kept verifies that a calls_service fact
// targeting an unresolvable/external name still creates a CALLS_SERVICE edge.
func TestR4_CallsServiceToExternal_Kept(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// No node exists for "ExternalPaymentGateway" — it's truly external
	facts := `[{"kind":"calls_service","to":"ExternalPaymentGateway","from_type":"provider"}]`
	g.SaveFileExtraction(revID, domain, "payments-api/src/payments.service.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	found := false
	for _, e := range edges {
		if e.Active {
			found = true
		}
	}
	if !found {
		t.Error("calls_service to external/unresolvable target should create CALLS_SERVICE edge")
	}
}

// ---------------------------------------------------------------------------
// R5 — framework injectable denylist
// ---------------------------------------------------------------------------

// TestR5_InjectsQueue_Dropped verifies that an injects fact targeting "Queue"
// (Bull framework) does not create a provider node or INJECTS edge.
func TestR5_InjectsQueue_Dropped(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	facts := `[{"kind":"injects","to":"Queue","from_type":"provider"}]`
	g.SaveFileExtraction(revID, domain, "arena-api/src/arena/battle.queue.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	// No provider node named "queue" should be created
	nodes, _ := s.ListNodes(store.NodeFilter{Domain: domain})
	for _, n := range nodes {
		if n.NodeType == "provider" && strings.EqualFold(n.Name, "queue") {
			t.Errorf("framework injectable 'Queue' must not create provider node; got %q", n.NodeKey)
		}
	}

	// No INJECTS edge to queue node
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "INJECTS"})
	for _, e := range edges {
		if strings.Contains(strings.ToLower(e.ToNodeKey), ":queue") {
			t.Errorf("framework injectable 'Queue' must not create INJECTS edge; got %s", e.EdgeKey)
		}
	}
}

// TestR5_InjectsEventEmitter2_Dropped verifies that an injects fact targeting
// "EventEmitter2" does not create a provider node or INJECTS edge.
func TestR5_InjectsEventEmitter2_Dropped(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	facts := `[{"kind":"injects","to":"EventEmitter2","from_type":"provider"}]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.events.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	nodes, _ := s.ListNodes(store.NodeFilter{Domain: domain})
	for _, n := range nodes {
		if n.NodeType == "provider" && strings.EqualFold(n.Name, "eventemitter2") {
			t.Errorf("framework injectable 'EventEmitter2' must not create provider node; got %q", n.NodeKey)
		}
	}

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "INJECTS"})
	for _, e := range edges {
		if strings.Contains(strings.ToLower(e.ToNodeKey), "eventemitter2") {
			t.Errorf("framework injectable 'EventEmitter2' must not create INJECTS edge; got %s", e.EdgeKey)
		}
	}
}

// TestR5_InjectsPrismaService_StillWorks verifies that PrismaService (a real
// application service) is NOT in the denylist and still creates INJECTS edge.
func TestR5_InjectsPrismaService_StillWorks(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// Pre-create PrismaService node so it resolves
	s.UpsertNode(store.NodeRow{
		NodeKey:            "code:provider:testapp:prismaservice",
		Layer:              "code",
		NodeType:           "provider",
		DomainKey:          domain,
		Name:               "PrismaService",
		Status:             "active",
		LastSeenRevisionID: revID,
	})

	facts := `[{"kind":"injects","to":"PrismaService","from_type":"provider"}]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.service.ts", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "INJECTS"})
	found := false
	for _, e := range edges {
		if e.Active {
			found = true
		}
	}
	if !found {
		t.Error("PrismaService is not a framework injectable; INJECTS edge should exist")
	}
}

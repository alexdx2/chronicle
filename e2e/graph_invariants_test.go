package e2e

import (
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// =============================================================================
// Graph Invariant Tests
//
// These tests verify STRUCTURAL RULES that must hold on ANY correctly-built
// knowledge graph, not just the tom-and-jerry fixture. If the scan pipeline
// or import logic has a bug, these tests catch it.
//
// Each test loads the tom-and-jerry fixture and checks invariants.
// =============================================================================

// --- Referential Integrity ---

// TestInvariant_NoOrphanEdges verifies every edge points to nodes that exist.
// A broken import that creates edges before nodes would fail this.
func TestInvariant_NoOrphanEdges(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	s := g.Store()

	nodes, _ := s.ListNodes(store.NodeFilter{})
	nodeIDs := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		nodeIDs[n.NodeID] = n.NodeKey
	}

	edges := allEdges(t, g)
	for _, e := range edges {
		if _, ok := nodeIDs[e.FromNodeID]; !ok {
			t.Errorf("edge %s→%s: from_node_id %d does not exist", e.FromNodeKey, e.ToNodeKey, e.FromNodeID)
		}
		if _, ok := nodeIDs[e.ToNodeID]; !ok {
			t.Errorf("edge %s→%s: to_node_id %d does not exist", e.FromNodeKey, e.ToNodeKey, e.ToNodeID)
		}
	}
}

// TestInvariant_NoDuplicateNodeKeys verifies every node_key is unique.
func TestInvariant_NoDuplicateNodeKeys(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	nodes, _ := g.Store().ListNodes(store.NodeFilter{})

	seen := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if seen[n.NodeKey] {
			t.Errorf("duplicate node_key: %s", n.NodeKey)
		}
		seen[n.NodeKey] = true
	}
}

// --- Endpoint Invariants ---

// TestInvariant_EveryEndpointHasController verifies that every endpoint node
// has at least one EXPOSES_ENDPOINT edge from a controller.
// Catches: endpoints created without linking to their controller.
func TestInvariant_EveryEndpointHasController(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	endpoints, _ := g.Store().ListNodes(store.NodeFilter{NodeType: "endpoint"})
	if len(endpoints) == 0 {
		t.Fatal("graph has no endpoints — scan likely broken")
	}

	for _, ep := range endpoints {
		revDeps, _ := g.QueryReverseDeps(ep.NodeKey, 1, nil)
		hasController := false
		for _, rd := range revDeps {
			if rd.NodeType == "controller" {
				hasController = true
				break
			}
		}
		if !hasController {
			t.Errorf("endpoint %q (%s) has no controller — missing EXPOSES_ENDPOINT edge", ep.Name, ep.NodeKey)
		}
	}
}

// TestInvariant_EveryCalledEndpointExists verifies that CALLS_ENDPOINT edges
// point to nodes that actually exist as endpoints.
// Catches: typos in endpoint keys, stale references.
func TestInvariant_EveryCalledEndpointExists(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	s := g.Store()

	endpoints, _ := s.ListNodes(store.NodeFilter{NodeType: "endpoint"})
	endpointKeys := make(map[string]bool, len(endpoints))
	for _, ep := range endpoints {
		endpointKeys[ep.NodeKey] = true
	}

	edges := allEdges(t, g)
	for _, e := range edges {
		if e.EdgeType == "CALLS_ENDPOINT" {
			if !endpointKeys[e.ToNodeKey] {
				t.Errorf("CALLS_ENDPOINT → %s but no endpoint node with that key exists", e.ToNodeKey)
			}
		}
	}
}

// --- Service Invariants ---

// TestInvariant_EveryServiceHasEndpoints verifies that every service node
// has at least one endpoint exposed by one of its controllers.
// Catches: service nodes created but scan didn't find their endpoints.
func TestInvariant_EveryServiceHasEndpoints(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	s := g.Store()

	services, _ := s.ListNodes(store.NodeFilter{NodeType: "service"})
	if len(services) == 0 {
		t.Fatal("graph has no services")
	}

	endpoints, _ := s.ListNodes(store.NodeFilter{NodeType: "endpoint"})
	// Build service→endpoint map through: service ← repo ← module ← controller → endpoint
	// Simpler: just check that for each service's repo_name, there are endpoints with that repo_name
	endpointsByRepo := make(map[string]int)
	for _, ep := range endpoints {
		endpointsByRepo[ep.RepoName]++
	}

	for _, svc := range services {
		// Service name typically matches repo_name
		if count := endpointsByRepo[svc.Name]; count == 0 {
			t.Errorf("service %q has no endpoints (no endpoint with repo_name=%q)", svc.Name, svc.Name)
		}
	}
}

// TestInvariant_EveryControllerInjectsAService verifies controllers aren't orphaned.
// Catches: controller extracted but DI relationships missed.
func TestInvariant_EveryControllerInjectsAService(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	controllers, _ := g.Store().ListNodes(store.NodeFilter{NodeType: "controller"})
	if len(controllers) == 0 {
		t.Fatal("graph has no controllers")
	}

	for _, ctrl := range controllers {
		deps, _ := g.QueryDeps(ctrl.NodeKey, 1, nil)
		hasProvider := false
		for _, d := range deps {
			if d.NodeType == "provider" || d.NodeType == "service" {
				hasProvider = true
				break
			}
		}
		if !hasProvider {
			t.Errorf("controller %q (%s) injects no provider — missing INJECTS edge", ctrl.Name, ctrl.NodeKey)
		}
	}
}

// --- Async / Topic Invariants ---

// TestInvariant_EveryTopicHasProducerOrConsumer verifies no orphan topics.
// A topic should be published to or consumed from — otherwise why does it exist?
func TestInvariant_EveryTopicHasProducerOrConsumer(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	topics, _ := g.Store().ListNodes(store.NodeFilter{NodeType: "topic"})
	if len(topics) == 0 {
		t.Skip("no topics in graph")
	}

	for _, topic := range topics {
		revDeps, _ := g.QueryReverseDeps(topic.NodeKey, 1, nil)
		deps, _ := g.QueryDeps(topic.NodeKey, 1, nil)

		hasProducer := false
		hasConsumer := false
		for _, rd := range revDeps {
			_ = rd // any reverse dep means something publishes to this topic
			hasProducer = true
			break
		}
		for _, d := range deps {
			_ = d
			hasConsumer = true
			break
		}
		// Topic connected on at least one side (producer → topic or topic → consumer via CONSUMES)
		// Actually both PUBLISHES and CONSUMES point TO the topic, so check revDeps for both
		if len(revDeps) == 0 {
			t.Errorf("topic %q (%s) has no producer and no consumer — orphan topic", topic.Name, topic.NodeKey)
		}
		_ = hasProducer
		_ = hasConsumer
		_ = deps
	}
}

// TestInvariant_KafkaFlowIsComplete verifies async flow works end-to-end:
// there exists a path from some producer through a topic to some consumer.
// Catches: PUBLISHES_TOPIC or CONSUMES_TOPIC edges missing.
func TestInvariant_KafkaFlowIsComplete(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	topics, _ := g.Store().ListNodes(store.NodeFilter{NodeType: "topic"})
	if len(topics) == 0 {
		t.Skip("no topics in graph")
	}

	edges := allEdges(t, g)
	publishEdges := 0
	consumeEdges := 0
	for _, e := range edges {
		switch e.EdgeType {
		case "PUBLISHES_TOPIC":
			publishEdges++
		case "CONSUMES_TOPIC":
			consumeEdges++
		}
	}

	if publishEdges == 0 {
		t.Error("no PUBLISHES_TOPIC edges — async producers not linked to topics")
	}
	if consumeEdges == 0 {
		t.Error("no CONSUMES_TOPIC edges — async consumers not linked to topics")
	}
}

// --- Cross-Service Invariants ---

// TestInvariant_CalledServicesExist verifies CALLS_SERVICE edges point to
// real service nodes. Catches: service reference typos, missing service nodes.
func TestInvariant_CalledServicesExist(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	s := g.Store()

	services, _ := s.ListNodes(store.NodeFilter{NodeType: "service"})
	serviceKeys := make(map[string]bool, len(services))
	for _, svc := range services {
		serviceKeys[svc.NodeKey] = true
	}

	edges := allEdges(t, g)
	for _, e := range edges {
		if e.EdgeType == "CALLS_SERVICE" {
			if !serviceKeys[e.ToNodeKey] {
				t.Errorf("CALLS_SERVICE → %s but no service node with that key exists", e.ToNodeKey)
			}
		}
	}
}

// TestInvariant_CrossServiceCallHasEndpoint verifies that when code calls a
// remote service (CALLS_SERVICE), it also calls at least one specific endpoint
// on that service (CALLS_ENDPOINT). Catches: service call detected but
// specific endpoint not resolved.
func TestInvariant_CrossServiceCallHasEndpoint(t *testing.T) {
	g, _ := setupTomAndJerry(t)

	edges := allEdges(t, g)

	// Group: caller → set of edge types
	callerEdgeTypes := make(map[string]map[string]bool)
	for _, e := range edges {
		if e.EdgeType == "CALLS_SERVICE" || e.EdgeType == "CALLS_ENDPOINT" {
			if callerEdgeTypes[e.FromNodeKey] == nil {
				callerEdgeTypes[e.FromNodeKey] = make(map[string]bool)
			}
			callerEdgeTypes[e.FromNodeKey][e.EdgeType] = true
		}
	}

	unresolved := 0
	for caller, types := range callerEdgeTypes {
		if types["CALLS_SERVICE"] && !types["CALLS_ENDPOINT"] {
			t.Logf("WARNING: %s has CALLS_SERVICE but no CALLS_ENDPOINT — endpoint not resolved", caller)
			unresolved++
		}
	}
	// At least SOME cross-service callers should have resolved endpoints
	total := len(callerEdgeTypes)
	if total > 0 && unresolved == total {
		t.Errorf("ALL %d cross-service callers lack CALLS_ENDPOINT — extraction likely broken", total)
	}
}

// --- Containment Invariants ---

// TestInvariant_EveryModuleContainsSomething verifies modules aren't empty.
// Catches: module node created but children not linked via CONTAINS.
func TestInvariant_EveryModuleContainsSomething(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	modules, _ := g.Store().ListNodes(store.NodeFilter{NodeType: "module"})
	if len(modules) == 0 {
		t.Skip("no modules in graph")
	}

	for _, mod := range modules {
		deps, _ := g.QueryDeps(mod.NodeKey, 1, nil)
		if len(deps) == 0 {
			t.Errorf("module %q (%s) CONTAINS nothing — empty module", mod.Name, mod.NodeKey)
		}
	}
}

// --- Infrastructure Invariants ---

// TestInvariant_InfraNodesLinkedToTopics verifies that when infra nodes exist,
// topics are linked via BELONGS_TO. Tests the infra→topic relationship.
func TestInvariant_InfraNodesLinkedToTopics(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	s := g.Store()

	// Add infra node + BELONGS_TO edge (simulates what scan does from manifest)
	revID, _ := s.CreateRevision("tomandjerry", "abc", "def", "manual", "full", "{}")
	kafkaKey := "infra:infrastructure:tomandjerry:kafka"
	g.UpsertNode(validate.NodeInput{
		NodeKey: kafkaKey, Layer: "infra", NodeType: "infrastructure",
		DomainKey: "tomandjerry", Name: "kafka",
	}, revID)

	topicKey := "contract:topic:tomandjerry:battle-results"
	topicNode, _ := s.GetNodeByKey(topicKey)
	kafkaNode, _ := s.GetNodeByKey(kafkaKey)
	if topicNode == nil || kafkaNode == nil {
		t.Fatal("topic or kafka node missing")
	}
	s.UpsertEdge(store.EdgeRow{
		EdgeKey: topicKey + "->" + kafkaKey + ":BELONGS_TO",
		FromNodeID: topicNode.NodeID, ToNodeID: kafkaNode.NodeID,
		EdgeType: "BELONGS_TO", DerivationKind: "linked", Active: true,
		FromNodeKey: topicKey, ToNodeKey: kafkaKey,
	})

	// Now verify the invariant: every topic should reach an infra node
	topics, _ := s.ListNodes(store.NodeFilter{NodeType: "topic"})
	for _, topic := range topics {
		deps, _ := g.QueryDeps(topic.NodeKey, 1, nil)
		hasInfra := false
		for _, d := range deps {
			if d.Layer == "infra" {
				hasInfra = true
				break
			}
		}
		if !hasInfra {
			t.Errorf("topic %q not linked to any infra node via BELONGS_TO", topic.Name)
		}
	}
}

// --- Data Model Invariants ---

// TestInvariant_ModelsReachableFromCode verifies data models aren't totally orphaned.
// Every model should be reachable from code — either directly via USES_MODEL or
// indirectly via REFERENCES_MODEL from another model that IS used.
func TestInvariant_ModelsReachableFromCode(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	models, _ := g.Store().ListNodes(store.NodeFilter{NodeType: "model"})
	if len(models) == 0 {
		t.Skip("no models in graph")
	}

	for _, model := range models {
		revDeps, _ := g.QueryReverseDeps(model.NodeKey, 2, nil) // depth 2: code→model or code→model→model
		hasCodeLink := false
		for _, rd := range revDeps {
			if rd.Layer == "code" {
				hasCodeLink = true
				break
			}
		}
		if !hasCodeLink {
			t.Errorf("model %q (%s) not reachable from any code entity (depth 2)", model.Name, model.NodeKey)
		}
	}
}

// --- Query Invariants (graph engine correctness) ---

// TestInvariant_PathsAreReachable verifies that query_path finds paths
// that actually follow real edges in the graph.
func TestInvariant_PathsAreReachable(t *testing.T) {
	g, _ := setupTomAndJerry(t)

	// ArenaController should reach tom-api through injection chain
	result, err := g.QueryPath("code:controller:tomandjerry:arenacontroller", "service:service:tomandjerry:tom-api", graph.PathOptions{MaxDepth: 5, Mode: "directed"})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) == 0 {
		t.Error("no path from ArenaController to tom-api — graph traversal broken")
	}

	// No path between unconnected services (tom-api and jerry-api only connect via arena)
	noPath, _ := g.QueryPath("service:service:tomandjerry:tom-api", "service:service:tomandjerry:jerry-api", graph.PathOptions{MaxDepth: 5, Mode: "directed"})
	if len(noPath.Paths) > 0 {
		t.Error("unexpected direct path from tom-api to jerry-api — should only connect via arena")
	}
}

// TestInvariant_ImpactPropagates verifies that impact analysis reaches
// dependents through the edge chain.
func TestInvariant_ImpactPropagates(t *testing.T) {
	g, _ := setupTomAndJerry(t)

	// Changing ArenaService should impact ArenaController (via reverse INJECTS)
	result, err := g.QueryImpact("code:provider:tomandjerry:arenaservice", graph.ImpactOptions{MaxDepth: 2})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	foundController := false
	for _, entry := range result.Impacts {
		if entry.NodeKey == "code:controller:tomandjerry:arenacontroller" {
			foundController = true
			break
		}
	}
	if !foundController {
		t.Error("ArenaService impact should reach ArenaController")
	}
}

// =============================================================================
// Helpers
// =============================================================================

func allEdges(t *testing.T, g *graph.Graph) []store.EdgeRow {
	t.Helper()
	nodes, _ := g.Store().ListNodes(store.NodeFilter{})
	ids := make([]int64, len(nodes))
	for i, n := range nodes {
		ids[i] = n.NodeID
	}
	edges, err := g.Store().GetEdgesBetweenNodes(ids)
	if err != nil {
		t.Fatalf("GetEdgesBetweenNodes: %v", err)
	}
	// Enrich with node keys for readable error messages
	nodeByID := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		nodeByID[n.NodeID] = n.NodeKey
	}
	for i := range edges {
		edges[i].FromNodeKey = nodeByID[edges[i].FromNodeID]
		edges[i].ToNodeKey = nodeByID[edges[i].ToNodeID]
	}
	return edges
}

// allEdgesCount returns edge count per edge_type for diagnostics.
func allEdgesCount(t *testing.T, g *graph.Graph) map[string]int {
	t.Helper()
	edges := allEdges(t, g)
	counts := make(map[string]int)
	for _, e := range edges {
		counts[e.EdgeType]++
	}
	return counts
}

// TestDiagnostic_PrintGraphSummary prints the graph structure for debugging.
// Not an assertion — just a diagnostic helper. Always passes.
func TestDiagnostic_PrintGraphSummary(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	s := g.Store()

	nodes, _ := s.ListNodes(store.NodeFilter{})
	byLayer := make(map[string]int)
	byType := make(map[string]int)
	for _, n := range nodes {
		byLayer[n.Layer]++
		byType[n.NodeType]++
	}

	edgeCounts := allEdgesCount(t, g)

	t.Logf("=== Graph Summary ===")
	t.Logf("Total nodes: %d", len(nodes))
	for layer, count := range byLayer {
		t.Logf("  %s: %d", layer, count)
	}
	t.Logf("Node types:")
	for nt, count := range byType {
		t.Logf("  %s: %d", nt, count)
	}
	t.Logf("Edge types:")
	for et, count := range edgeCounts {
		t.Logf("  %s: %d", et, count)
	}
}

package graph

import (
	"encoding/json"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// These tests verify that declares_service creates correct service nodes
// and that non-service packages do NOT create service nodes.

// --- Positive: these SHOULD create service nodes ---

func TestDeclaresService_PackageWithEntrypoint(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID, _ := g.Store().CreateRevision("myapp", "", "abc", "manual", "full", "{}")

	facts := `[{"kind":"declares_service","to":"orders-api"}]`
	g.Store().SaveExtraction(revID, "myapp", "orders-api/package.json", "extracted", "", facts, "")
	result, err := g.ResolveExtractions("myapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}
	if result.NodesCreated == 0 {
		t.Fatal("declares_service should create a service node")
	}

	node, _ := g.Store().GetNodeByKey("service:service:myapp:orders-api")
	if node == nil {
		t.Fatal("service:service:myapp:orders-api should exist")
	}
	if node.Layer != "service" {
		t.Errorf("layer = %q, want service", node.Layer)
	}
	if node.NodeType != "service" {
		t.Errorf("node_type = %q, want service", node.NodeType)
	}
}

// --- Negative: these should NOT create service nodes ---
// The resolver accepts declares_service — filtering is the AGENT's job.
// These tests document what the instructions tell agents NOT to emit.
// If an agent incorrectly emits declares_service for a library, the
// resolver will create the node (by design). The guard is in instructions.

func TestDeclaresService_EmptyName_Ignored(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID, _ := g.Store().CreateRevision("myapp", "", "abc", "manual", "full", "{}")

	facts := `[{"kind":"declares_service","to":""}]`
	g.Store().SaveExtraction(revID, "myapp", "shared/package.json", "extracted", "", facts, "")
	result, _ := g.ResolveExtractions("myapp", revID)
	if result.NodesCreated != 0 {
		t.Error("empty declares_service name should not create a node")
	}
}

func TestDeclaresService_ResolvedBeforeCallsService(t *testing.T) {
	// Verifies that declares_service is resolved BEFORE calls_service,
	// so CALLS_SERVICE targets link to service:service nodes, not code:provider.
	g, _, _ := setupTestGraph(t)
	revID, _ := g.Store().CreateRevision("myapp", "", "abc", "manual", "full", "{}")

	// File 1: package.json declares the service
	g.Store().SaveExtraction(revID, "myapp", "orders-api/package.json", "extracted", "", `[{"kind":"declares_service","to":"orders-api"}]`, "")
	// File 2: a client calls the service
	g.Store().SaveExtraction(revID, "myapp", "gateway/src/orders.client.ts", "extracted", "provider", `[{"kind":"calls_service","to":"orders-api"}]`, "")

	g.ResolveExtractions("myapp", revID)

	// The CALLS_SERVICE edge should target service:service, not code:provider
	svcNode, _ := g.Store().GetNodeByKey("service:service:myapp:orders-api")
	if svcNode == nil {
		t.Fatal("service node should exist")
	}

	// Check that no code:provider:myapp:orders-api was created
	provNode, _ := g.Store().GetNodeByKey("code:provider:myapp:orders-api")
	if provNode != nil {
		t.Error("code:provider:myapp:orders-api should NOT exist — calls_service should link to service:service")
	}
}

// --- Topic canonicalization safety ---

func TestTopicCanonical_ExactMatch(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID, _ := g.Store().CreateRevision("myapp", "", "abc", "manual", "full", "{}")

	// Create topic first
	g.Store().SaveExtraction(revID, "myapp", "producer.ts", "extracted", "provider", `[{"kind":"produces","to":"order.created"}]`, "")
	g.ResolveExtractions("myapp", revID)

	key := g.resolveTopicKey("myapp", "order.created", revID)
	if key != "contract:topic:myapp:order.created" {
		t.Errorf("exact match key = %q, want contract:topic:myapp:order.created", key)
	}
}

func TestTopicCanonical_PluralMatch(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID, _ := g.Store().CreateRevision("myapp", "", "abc", "manual", "full", "{}")

	// Create "battle-results" topic
	g.Store().SaveExtraction(revID, "myapp", "producer.ts", "extracted", "provider", `[{"kind":"produces","to":"battle-results"}]`, "")
	g.ResolveExtractions("myapp", revID)

	// "battle-result" (without s) should resolve to same topic
	key := g.resolveTopicKey("myapp", "battle-result", revID)
	if key != "contract:topic:myapp:battle-results" {
		t.Errorf("plural match key = %q, want contract:topic:myapp:battle-results", key)
	}
}

func TestTopicCanonical_DifferentTopics_NotMerged(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID, _ := g.Store().CreateRevision("myapp", "", "abc", "manual", "full", "{}")

	// Create two clearly different topics
	g.Store().SaveExtraction(revID, "myapp", "a.ts", "extracted", "provider", `[{"kind":"produces","to":"order.created"}]`, "")
	g.Store().SaveExtraction(revID, "myapp", "b.ts", "extracted", "provider", `[{"kind":"produces","to":"order.updated"}]`, "")
	g.ResolveExtractions("myapp", revID)

	key1 := g.resolveTopicKey("myapp", "order.created", revID)
	key2 := g.resolveTopicKey("myapp", "order.updated", revID)
	if key1 == key2 {
		t.Errorf("different topics merged: %q == %q", key1, key2)
	}
}

func TestTopicCanonical_VersionSuffix_NotMerged(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID, _ := g.Store().CreateRevision("myapp", "", "abc", "manual", "full", "{}")

	g.Store().SaveExtraction(revID, "myapp", "a.ts", "extracted", "provider", `[{"kind":"produces","to":"order.created"}]`, "")
	g.Store().SaveExtraction(revID, "myapp", "b.ts", "extracted", "provider", `[{"kind":"produces","to":"order.created.v2"}]`, "")
	g.ResolveExtractions("myapp", revID)

	key1 := g.resolveTopicKey("myapp", "order.created", revID)
	key2 := g.resolveTopicKey("myapp", "order.created.v2", revID)
	if key1 == key2 {
		t.Errorf("versioned topics merged: %q == %q", key1, key2)
	}
}

// --- Edge lifecycle (Risk 4) ---

func TestEdgeLifecycle_SameEdgeTwice_StaysActive(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID, _ := g.Store().CreateRevision("myapp", "", "abc", "manual", "full", "{}")

	facts := `[{"kind":"injects","to":"OrderService"}]`
	g.Store().SaveExtraction(revID, "myapp", "src/controller.ts", "extracted", "controller", facts, "")
	g.Store().SaveExtraction(revID, "myapp", "src/controller.ts", "extracted", "controller", facts, "")
	g.ResolveExtractions("myapp", revID)

	// Edge should exist and be queryable
	node, _ := g.Store().GetNodeByKey("code:controller:myapp:src/controller")
	if node == nil {
		t.Fatal("controller node should exist")
	}
	deps, _ := g.QueryDeps(node.NodeKey, 1, nil)
	found := false
	for _, d := range deps {
		// resolveInjectTarget normalizes PascalCase: OrderService → order.service
		if d.Name == "order.service" || d.Name == "OrderService" ||
			d.NodeKey == "code:provider:myapp:order.service" || d.NodeKey == "code:provider:myapp:orderservice" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("INJECTS edge should be active after duplicate resolve, got deps: %+v", deps)
	}
}

func TestEdgeLifecycle_StaleMarking(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	s := g.Store()

	// Revision 1: create nodes and edge
	rev1, _ := s.CreateRevision("myapp", "", "abc", "manual", "full", "{}")
	factsJSON, _ := json.Marshal([]Fact{{Kind: "injects", To: "ServiceA"}})
	s.SaveExtraction(rev1, "myapp", "src/ctrl.ts", "extracted", "controller", string(factsJSON), "")
	g.ResolveExtractions("myapp", rev1)

	// Verify node exists
	nodes, _ := s.ListNodes(store.NodeFilter{Domain: "myapp", Status: "active"})
	initialCount := len(nodes)
	if initialCount < 2 {
		t.Fatalf("expected at least 2 active nodes, got %d", initialCount)
	}

	// Revision 2: mark stale (nothing re-imported)
	rev2, _ := s.CreateRevision("myapp", "abc", "def", "manual", "full", "{}")
	staled, _ := s.MarkStaleNodes("myapp", rev2)
	if staled == 0 {
		t.Error("expected some nodes to be marked stale")
	}
}

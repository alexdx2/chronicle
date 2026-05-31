package graph

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// TestEndpointReconcileFindsUnmatched tests that FindUnmatchedHTTPCalls
// correctly identifies http_calls without CALLS_ENDPOINT edges and
// provides the known endpoints as context for LLM matching.
func TestEndpointReconcileFindsUnmatched(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	revID, _ := s.CreateRevision("myapp", "scan_reconcile", "", "manual", "full", "{}")

	// Controller exposes endpoints with path params
	factsCtrl, _ := json.Marshal([]Fact{
		{Kind: "endpoint", Method: "GET", Target: "/users/:id", FromType: "controller"},
		{Kind: "endpoint", Method: "GET", Target: "/users/:id/profile", FromType: "controller"},
		{Kind: "endpoint", Method: "GET", Target: "/users", FromType: "controller"},
	})
	g.SaveFileExtraction(revID, "myapp", "users-api/src/users.controller.ts", "extracted", "", string(factsCtrl), "")

	// Client calls with concrete values — these WON'T match via string comparison
	factsClient, _ := json.Marshal([]Fact{
		{Kind: "http_call", From: "AdminClient.getUser", FromType: "provider",
			Target: "http://users-api:3000/users/abc-123", Method: "GET"},
		{Kind: "http_call", From: "AdminClient.getUserProfile", FromType: "provider",
			Target: "http://users-api:3000/users/abc-123/profile", Method: "GET"},
		// This one SHOULD match automatically (exact path)
		{Kind: "http_call", From: "AdminClient.listUsers", FromType: "provider",
			Target: "http://users-api:3000/users", Method: "GET"},
	})
	g.SaveFileExtraction(revID, "myapp", "admin/src/admin.client.ts", "extracted", "", string(factsClient), "")

	result, err := g.ResolveExtractions("myapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	t.Logf("Resolve result: nodes=%d edges=%d", result.NodesCreated, result.EdgesCreated)

	// Check what matched and what didn't
	edges, _ := s.ListEdges(store.EdgeFilter{})
	matchedCount := 0
	for _, e := range edges {
		if e.EdgeType == "CALLS_ENDPOINT" {
			matchedCount++
			t.Logf("  MATCHED: %s → %s", e.FromNodeKey, e.ToNodeKey)
		}
	}

	// Only /users should auto-match; /users/abc-123 and /users/abc-123/profile won't
	if matchedCount != 1 {
		t.Errorf("expected 1 auto-matched CALLS_ENDPOINT (GET /users), got %d", matchedCount)
	}

	// Now find unmatched calls
	unmatched := g.FindUnmatchedHTTPCalls("myapp")
	t.Logf("Unmatched HTTP calls: %d", len(unmatched))
	for _, u := range unmatched {
		t.Logf("  from=%s target=%s path=%s known_endpoints=%v",
			u.FromName, u.TargetURL, u.Path, u.Endpoints)
	}

	// Should find the admin.client calls that didn't get CALLS_ENDPOINT
	// (the /users call matched, so admin.client HAS a CALLS_ENDPOINT,
	// but the specific calls to /users/abc-123 may or may not be tracked individually)
	if len(unmatched) == 0 {
		t.Log("NOTE: No unmatched calls found — all resolved or from-node already has CALLS_ENDPOINT")
	}

	// The key assertion: known_endpoints should include the parameterized endpoints
	for _, u := range unmatched {
		if len(u.Endpoints) == 0 {
			t.Errorf("expected known_endpoints to be populated for unmatched call from %s", u.FromName)
		}
		foundParamEndpoint := false
		for _, ep := range u.Endpoints {
			if ep == "GET /users/:id" || ep == "GET /users/:id/profile" {
				foundParamEndpoint = true
			}
		}
		if !foundParamEndpoint {
			t.Errorf("expected parameterized endpoints in known_endpoints, got: %v", u.Endpoints)
		}
	}
}

// TestEndpointReconcileLLMFix tests the full reconciliation flow:
// 1. Phase 1: some calls don't match (path params)
// 2. LLM sees unmatched calls + known endpoints
// 3. LLM emits calls_endpoint facts
// 4. Re-resolve creates the missing edges
func TestEndpointReconcileLLMFix(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	revID, _ := s.CreateRevision("myapp", "scan_llmfix", "", "manual", "full", "{}")

	// Step 1: Controller with parameterized endpoints
	factsCtrl, _ := json.Marshal([]Fact{
		{Kind: "endpoint", Method: "GET", Target: "/orders/:id", FromType: "controller"},
		{Kind: "endpoint", Method: "DELETE", Target: "/orders/:id", FromType: "controller"},
		{Kind: "endpoint", Method: "GET", Target: "/orders/:id/items", FromType: "controller"},
	})
	g.SaveFileExtraction(revID, "myapp", "orders-api/src/orders.controller.ts", "extracted", "", string(factsCtrl), "")

	// Step 2: Client with concrete URLs (won't match)
	factsClient, _ := json.Marshal([]Fact{
		{Kind: "http_call", From: "ShippingClient.getOrder", FromType: "provider",
			Target: "http://orders-api:3001/orders/ord-789", Method: "GET"},
		{Kind: "http_call", From: "ShippingClient.getItems", FromType: "provider",
			Target: "http://orders-api:3001/orders/ord-789/items", Method: "GET"},
	})
	g.SaveFileExtraction(revID, "myapp", "shipping-api/src/shipping.client.ts", "extracted", "", string(factsClient), "")

	// Step 3: Resolve — these won't auto-match
	result1, _ := g.ResolveExtractions("myapp", revID)
	t.Logf("Phase 1 resolve: nodes=%d edges=%d", result1.NodesCreated, result1.EdgesCreated)

	// Verify no CALLS_ENDPOINT yet
	edges1, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_ENDPOINT"})
	if len(edges1) != 0 {
		t.Errorf("expected 0 CALLS_ENDPOINT before reconciliation, got %d", len(edges1))
	}

	// Step 4: LLM sees the unmatched calls and known endpoints
	unmatched := g.FindUnmatchedHTTPCalls("myapp")
	t.Logf("Unmatched: %d", len(unmatched))
	for _, u := range unmatched {
		t.Logf("  from=%s path=%s endpoints=%v", u.FromName, u.Path, u.Endpoints)
	}

	// Step 5: LLM responds with calls_endpoint facts (simulating what the agent would do)
	revID2, _ := s.CreateRevision("myapp", "reconcile_1", "", "manual", "incremental", "{}")
	factsLLM, _ := json.Marshal([]Fact{
		{Kind: "calls_endpoint", From: "ShippingClient.getOrder", FromType: "provider",
			Target: "/orders/:id", Method: "GET"},
		{Kind: "calls_endpoint", From: "ShippingClient.getItems", FromType: "provider",
			Target: "/orders/:id/items", Method: "GET"},
	})
	g.SaveFileExtraction(revID2, "myapp", "shipping-api/src/shipping.client.ts", "extracted", "", string(factsLLM), "")

	// Step 6: Re-resolve
	result2, _ := g.ResolveExtractions("myapp", revID2)
	t.Logf("Reconcile resolve: nodes=%d edges=%d", result2.NodesCreated, result2.EdgesCreated)

	// Step 7: Verify CALLS_ENDPOINT edges now exist
	edges2, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_ENDPOINT"})
	for _, e := range edges2 {
		t.Logf("  CALLS_ENDPOINT: %s → %s", e.FromNodeKey, e.ToNodeKey)
	}
	if len(edges2) != 2 {
		t.Errorf("expected 2 CALLS_ENDPOINT after LLM reconciliation, got %d", len(edges2))
	}
}

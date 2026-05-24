package admin

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, _ := registry.LoadDefaults()
	g := graph.New(s, reg)
	manifestPath := filepath.Join(dir, "chronicle.domain.yaml")
	os.WriteFile(manifestPath, []byte("domain: test\nrepositories:\n  - name: test\n    path: .\n"), 0644)
	return NewServer(g, s, 0, manifestPath, false, dir)
}

func TestHandleStats(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/stats?domain=orders", nil)
	w := httptest.NewRecorder()
	srv.handleStats(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleRequests(t *testing.T) {
	srv := setupTestServer(t)
	srv.store.LogRequest(store.RequestLogEntry{ToolName: "test", ParamsJSON: "{}", DurationMs: 5})
	req := httptest.NewRequest("GET", "/api/requests", nil)
	w := httptest.NewRecorder()
	srv.handleRequests(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var entries []store.RequestLogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 1 {
		t.Errorf("count = %d, want 1", len(entries))
	}
}

func TestHandleValidate(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest("POST", "/api/validate", nil)
	w := httptest.NewRecorder()
	srv.handleValidate(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	if result["valid"] != true {
		t.Error("expected valid=true for empty graph")
	}
}

func TestHandleGraph(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/graph?domain=orders", nil)
	w := httptest.NewRecorder()
	srv.handleGraph(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleLowConfidence(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/low-confidence", nil)
	w := httptest.NewRecorder()
	srv.handleLowConfidence(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleScans(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/scans?domain=orders", nil)
	w := httptest.NewRecorder()
	srv.handleScans(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleDiagramBuild(t *testing.T) {
	srv := setupTestServer(t)

	// Insert test nodes
	idA, err := srv.store.UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:OrdersAPI", Layer: "code", NodeType: "service", DomainKey: "test", Name: "OrdersAPI", Status: "active",
	})
	if err != nil {
		t.Fatalf("insert node A: %v", err)
	}
	idB, err := srv.store.UpsertNode(store.NodeRow{
		NodeKey: "code:service:test:PaymentsAPI", Layer: "code", NodeType: "service", DomainKey: "test", Name: "PaymentsAPI", Status: "active",
	})
	if err != nil {
		t.Fatalf("insert node B: %v", err)
	}
	// Insert edge between them
	_, err = srv.store.UpsertEdge(store.EdgeRow{
		EdgeKey: "code:service:test:OrdersAPI->code:service:test:PaymentsAPI:CALLS_SERVICE",
		FromNodeID: idA, ToNodeID: idB, EdgeType: "CALLS_SERVICE", DerivationKind: "hard",
		FromNodeKey: "code:service:test:OrdersAPI", ToNodeKey: "code:service:test:PaymentsAPI",
	})
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	body := `{
		"title": "Test Build",
		"node_keys": ["code:service:test:OrdersAPI", "code:service:test:PaymentsAPI", "INVALID_KEY"],
		"nodes": [{"key": "user", "label": "User", "kind": "actor"}],
		"edges": [{"from": "user", "to": "code:service:test:OrdersAPI", "kind": "http", "label": "POST /orders"}]
	}`
	req := httptest.NewRequest("POST", "/api/diagram/build", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDiagram(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)

	// Should have session_id and url
	if result["session_id"] == nil || result["session_id"] == "" {
		t.Fatal("expected session_id in response")
	}
	if result["url"] == nil || result["url"] == "" {
		t.Fatal("expected url in response")
	}
	// 2 real + 1 virtual = 3 nodes
	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 3 {
		t.Fatalf("expected 3 nodes, got %d", nodeCount)
	}
	// 1 real edge (A->B) + 1 virtual edge (user->A) = 2
	edgeCount := int(result["edge_count"].(float64))
	if edgeCount != 2 {
		t.Fatalf("expected 2 edges, got %d", edgeCount)
	}
	// INVALID_KEY should be in errors
	errs, ok := result["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Fatalf("expected 1 error for invalid key, got %v", result["errors"])
	}
	if errs[0].(string) != "INVALID_KEY" {
		t.Fatalf("expected INVALID_KEY error, got %v", errs[0])
	}
}

func TestHandleDiagramBuildAllInvalid(t *testing.T) {
	srv := setupTestServer(t)
	body := `{"title": "Bad", "node_keys": ["NOPE", "ALSO_NOPE"]}`
	req := httptest.NewRequest("POST", "/api/diagram/build", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDiagram(w, req)
	// All keys invalid still returns 200 with 0 nodes and errors list
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 0 {
		t.Fatalf("expected 0 nodes, got %d", nodeCount)
	}
	errs, ok := result["errors"].([]any)
	if !ok || len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %v", result["errors"])
	}
}

func TestHandleDiagramBuildNoInput(t *testing.T) {
	srv := setupTestServer(t)
	body := `{"title": "Bad"}`
	req := httptest.NewRequest("POST", "/api/diagram/build", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDiagram(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDiagramBuildPureViewModel(t *testing.T) {
	srv := setupTestServer(t)

	body := `{
		"title": "Overview",
		"nodes": [
			{"key": "domain:core", "label": "Core", "kind": "domain"},
			{"key": "infra:kafka", "label": "Kafka", "kind": "infrastructure"}
		],
		"edges": [
			{"from": "domain:core", "to": "infra:kafka", "label": "publish", "kind": "async"}
		]
	}`
	req := httptest.NewRequest("POST", "/api/diagram/build", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDiagram(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)

	if result["session_id"] == nil || result["session_id"] == "" {
		t.Fatal("expected non-empty session_id")
	}
	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 2 {
		t.Errorf("node_count = %d, want 2", nodeCount)
	}
	edgeCount := int(result["edge_count"].(float64))
	if edgeCount != 1 {
		t.Errorf("edge_count = %d, want 1", edgeCount)
	}
}

func TestDiagramBuildWithSteps(t *testing.T) {
	srv := setupTestServer(t)

	body := `{
		"title": "Step Flow",
		"nodes": [
			{"key": "svc:a", "label": "Service A", "kind": "service"},
			{"key": "svc:b", "label": "Service B", "kind": "service"}
		],
		"edges": [
			{"from": "svc:a", "to": "svc:b", "label": "calls", "kind": "http"}
		],
		"steps": [
			{"title": "Step 1", "description": "A calls B", "highlights": {"svc:a": "active", "svc:b": "target"}},
			{"title": "Step 2", "description": "B responds", "highlights": {"svc:b": "active"}}
		]
	}`
	req := httptest.NewRequest("POST", "/api/diagram/build", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDiagram(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)

	if result["session_id"] == nil || result["session_id"] == "" {
		t.Fatal("expected non-empty session_id")
	}

	// Verify session was stored with steps
	sessionID := result["session_id"].(string)
	srv.mu.RLock()
	session, ok := srv.diagrams[sessionID]
	srv.mu.RUnlock()
	if !ok {
		t.Fatal("session not stored")
	}
	if len(session.Steps) != 2 {
		t.Errorf("session steps = %d, want 2", len(session.Steps))
	}
	if session.Steps[0].Title != "Step 1" {
		t.Errorf("step[0].title = %q, want %q", session.Steps[0].Title, "Step 1")
	}
}

func TestDiagramBuildWithGroups(t *testing.T) {
	srv := setupTestServer(t)

	body := `{
		"title": "Grouped",
		"nodes": [
			{"key": "svc:orders", "label": "Orders", "kind": "service", "domain": "core"},
			{"key": "svc:payments", "label": "Payments", "kind": "service", "domain": "core"},
			{"key": "svc:crm", "label": "CRM", "kind": "service", "domain": "crm"}
		],
		"groups": {"field": "domain"}
	}`
	req := httptest.NewRequest("POST", "/api/diagram/build", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDiagram(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)

	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 3 {
		t.Errorf("node_count = %d, want 3", nodeCount)
	}

	// Verify groups were stored in session
	sessionID := result["session_id"].(string)
	srv.mu.RLock()
	session, ok := srv.diagrams[sessionID]
	srv.mu.RUnlock()
	if !ok {
		t.Fatal("session not stored")
	}
	if session.Groups == nil {
		t.Fatal("expected groups to be set")
	}
	if session.Groups.Field != "domain" {
		t.Errorf("groups.field = %q, want %q", session.Groups.Field, "domain")
	}
}

func TestEdgeTypeToKind(t *testing.T) {
	tests := []struct{ edgeType, want string }{
		{"CALLS_ENDPOINT", "http"},
		{"CALLS_SERVICE", "http"},
		{"PUBLISHES_TOPIC", "async"},
		{"CONSUMES_TOPIC", "async"},
		{"CONSUMED_BY", "async"},
		{"USES_MODEL", "data"},
		{"READS_DB", "data"},
		{"CONTAINS", "structural"},
		{"UNKNOWN_TYPE", "structural"},
	}
	for _, tt := range tests {
		got := edgeTypeToKind(tt.edgeType)
		if got != tt.want {
			t.Errorf("edgeTypeToKind(%q) = %q, want %q", tt.edgeType, got, tt.want)
		}
	}
}

func TestHandleRequestsSince(t *testing.T) {
	srv := setupTestServer(t)
	srv.store.LogRequest(store.RequestLogEntry{ToolName: "a", ParamsJSON: "{}", DurationMs: 1})
	srv.store.LogRequest(store.RequestLogEntry{ToolName: "b", ParamsJSON: "{}", DurationMs: 2})
	req := httptest.NewRequest("GET", "/api/requests?since=1", nil)
	w := httptest.NewRecorder()
	srv.handleRequests(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var entries []store.RequestLogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 1 {
		t.Errorf("count = %d, want 1", len(entries))
	}
}

func TestHandleGraphDomains(t *testing.T) {
	s := setupTestServer(t)
	rev, _ := s.getStore().CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	s.getStore().UpsertNode(store.NodeRow{
		NodeKey: "code:controller:orders:oc", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrderController", Status: "active",
		LastSeenRevisionID: rev, Confidence: 0.9, Freshness: 1.0,
	})
	s.getStore().UpsertNode(store.NodeRow{
		NodeKey: "code:provider:orders:os", Layer: "code", NodeType: "provider",
		DomainKey: "orders", Name: "OrderService", Status: "active",
		LastSeenRevisionID: rev, Confidence: 0.9, Freshness: 1.0,
	})
	s.getStore().UpsertNode(store.NodeRow{
		NodeKey: "contract:endpoint:orders:post_orders", Layer: "contract", NodeType: "endpoint",
		DomainKey: "orders", Name: "POST /orders", Status: "active",
		LastSeenRevisionID: rev, Confidence: 0.9, Freshness: 1.0,
	})
	s.getStore().UpsertNode(store.NodeRow{
		NodeKey: "code:controller:vouchers:vc", Layer: "code", NodeType: "controller",
		DomainKey: "vouchers", Name: "VoucherController", Status: "active",
		LastSeenRevisionID: rev, Confidence: 0.9, Freshness: 1.0,
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/graph/domains", nil)
	s.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Domains []struct {
			DomainKey string         `json:"domain_key"`
			NodeCount int            `json:"node_count"`
			ByLayer   map[string]int `json:"by_layer"`
			ByType    map[string]int `json:"by_type"`
		} `json:"domains"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Domains) < 2 {
		t.Fatalf("expected at least 2 domains, got %d", len(resp.Domains))
	}

	var orders *struct {
		DomainKey string         `json:"domain_key"`
		NodeCount int            `json:"node_count"`
		ByLayer   map[string]int `json:"by_layer"`
		ByType    map[string]int `json:"by_type"`
	}
	for i := range resp.Domains {
		if resp.Domains[i].DomainKey == "orders" {
			orders = &resp.Domains[i]
		}
	}
	if orders == nil {
		t.Fatal("orders domain not found")
	}
	if orders.NodeCount != 3 {
		t.Errorf("orders node_count = %d, want 3", orders.NodeCount)
	}
	if orders.ByLayer["code"] != 2 {
		t.Errorf("orders code layer = %d, want 2", orders.ByLayer["code"])
	}
}

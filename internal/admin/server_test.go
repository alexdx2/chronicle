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
		"virtual_nodes": [{"key": "user", "name": "User", "type": "actor"}],
		"virtual_edges": [{"from": "user", "to": "code:service:test:OrdersAPI", "edge_type": "http_request", "label": "POST /orders"}]
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
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
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

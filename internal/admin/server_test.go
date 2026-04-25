package admin

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
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
	manifestPath := filepath.Join(dir, "oracle.domain.yaml")
	os.WriteFile(manifestPath, []byte("domain: test\nrepositories:\n  - name: test\n    path: .\n"), 0644)
	return NewServer(g, s, 0, manifestPath)
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

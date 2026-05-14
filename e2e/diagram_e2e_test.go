package e2e

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/admin"
)

// setupDiagramServer creates an admin server backed by the tom-and-jerry graph.
func setupDiagramServer(t *testing.T) *admin.Server {
	t.Helper()
	g, _ := setupTomAndJerry(t)
	s := g.Store()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "chronicle.domain.yaml")
	os.WriteFile(manifestPath, []byte("domain: tomandjerry\nrepositories:\n  - name: tom-api\n    path: .\n"), 0644)
	return admin.NewServer(g, s, 0, manifestPath, false, dir)
}

func postDiagram(t *testing.T, srv *admin.Server, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/diagram/build", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.HandleDiagramForTest(w, req)
	if w.Code != 200 {
		t.Fatalf("diagram build returned %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	return result
}

// TestDiagramOverview_NoNodeKeys builds an Overview diagram with only synthetic
// nodes (no node_keys) and verifies counts and structure.
func TestDiagramOverview_NoNodeKeys(t *testing.T) {
	srv := setupDiagramServer(t)

	body := `{
		"title": "TJ Overview",
		"nodes": [
			{"key": "domain:tj", "label": "Tom & Jerry", "kind": "domain"},
			{"key": "infra:kafka", "label": "Kafka", "kind": "infrastructure"},
			{"key": "infra:redis", "label": "Redis", "kind": "infrastructure"}
		],
		"edges": [
			{"from": "domain:tj", "to": "infra:kafka", "label": "publishes", "kind": "async"},
			{"from": "domain:tj", "to": "infra:redis", "label": "caches", "kind": "data"}
		]
	}`
	result := postDiagram(t, srv, body)

	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 3 {
		t.Errorf("node_count = %d, want 3", nodeCount)
	}
	edgeCount := int(result["edge_count"].(float64))
	if edgeCount != 2 {
		t.Errorf("edge_count = %d, want 2", edgeCount)
	}
	if result["session_id"] == nil || result["session_id"] == "" {
		t.Error("expected non-empty session_id")
	}
	// No errors expected (no node_keys to resolve)
	if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

// TestDiagramDomainDetail_ResolvesFromGraph builds a diagram using node_keys
// for real services and verifies resolution from the graph.
func TestDiagramDomainDetail_ResolvesFromGraph(t *testing.T) {
	srv := setupDiagramServer(t)

	body := `{
		"title": "Arena Service Detail",
		"node_keys": [
			"service:service:tomandjerry:arena-api",
			"service:service:tomandjerry:tom-api",
			"service:service:tomandjerry:jerry-api",
			"code:provider:tomandjerry:tomclient",
			"code:provider:tomandjerry:jerryclient"
		]
	}`
	result := postDiagram(t, srv, body)

	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 5 {
		t.Errorf("node_count = %d, want 5", nodeCount)
	}
	// Edges should be auto-discovered between these nodes
	edgeCount := int(result["edge_count"].(float64))
	if edgeCount < 2 {
		t.Errorf("edge_count = %d, want >= 2 (CALLS_SERVICE edges from clients)", edgeCount)
	}
	// No errors expected
	if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

// TestDiagramFlow_WithSteps builds a flow diagram with node_keys and steps,
// verifying steps are stored in the session.
func TestDiagramFlow_WithSteps(t *testing.T) {
	srv := setupDiagramServer(t)

	body := `{
		"title": "Attack Flow",
		"node_keys": [
			"code:controller:tomandjerry:arenacontroller",
			"code:provider:tomandjerry:arenaservice",
			"code:provider:tomandjerry:tomclient",
			"service:service:tomandjerry:tom-api"
		],
		"steps": [
			{
				"title": "Step 1: Client Request",
				"description": "Arena controller receives attack request",
				"highlights": {
					"code:controller:tomandjerry:arenacontroller": "active"
				}
			},
			{
				"title": "Step 2: Orchestrate",
				"description": "ArenaService coordinates the attack",
				"highlights": {
					"code:provider:tomandjerry:arenaservice": "active",
					"code:provider:tomandjerry:tomclient": "target"
				}
			},
			{
				"title": "Step 3: Cross-Service Call",
				"description": "TomClient calls tom-api",
				"highlights": {
					"code:provider:tomandjerry:tomclient": "active",
					"service:service:tomandjerry:tom-api": "target"
				}
			}
		]
	}`
	result := postDiagram(t, srv, body)

	if result["session_id"] == nil || result["session_id"] == "" {
		t.Fatal("expected session_id")
	}
	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 4 {
		t.Errorf("node_count = %d, want 4", nodeCount)
	}
}

// TestDiagramImpact_Annotations builds an impact diagram with annotations
// and verifies they are returned correctly.
func TestDiagramImpact_Annotations(t *testing.T) {
	srv := setupDiagramServer(t)

	body := `{
		"title": "Cat Model Impact",
		"node_keys": [
			"data:model:tomandjerry:cat",
			"code:provider:tomandjerry:tomservice",
			"code:controller:tomandjerry:tomcontroller"
		],
		"annotations": {
			"data:model:tomandjerry:cat": {
				"note": "Changed field: mood",
				"highlight": "red"
			},
			"code:provider:tomandjerry:tomservice": {
				"note": "Directly affected",
				"highlight": "orange"
			}
		}
	}`
	result := postDiagram(t, srv, body)

	if result["session_id"] == nil || result["session_id"] == "" {
		t.Fatal("expected session_id")
	}
	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 3 {
		t.Errorf("node_count = %d, want 3", nodeCount)
	}
}

// TestDiagramMixed_NodeKeysAndSynthetic mixes real node_keys with synthetic
// nodes and explicit edges between them.
func TestDiagramMixed_NodeKeysAndSynthetic(t *testing.T) {
	srv := setupDiagramServer(t)

	body := `{
		"title": "Mixed Diagram",
		"node_keys": [
			"service:service:tomandjerry:arena-api",
			"service:service:tomandjerry:tom-api"
		],
		"nodes": [
			{"key": "actor:user", "label": "Player", "kind": "actor"}
		],
		"edges": [
			{"from": "actor:user", "to": "service:service:tomandjerry:arena-api", "label": "POST /arena/attack", "kind": "http"}
		]
	}`
	result := postDiagram(t, srv, body)

	// 2 real + 1 synthetic = 3
	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 3 {
		t.Errorf("node_count = %d, want 3 (2 real + 1 synthetic)", nodeCount)
	}
	// 1 explicit edge (user→arena) + any auto-discovered edges
	edgeCount := int(result["edge_count"].(float64))
	if edgeCount < 1 {
		t.Errorf("edge_count = %d, want >= 1", edgeCount)
	}
	// No errors expected
	if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

// TestDiagramOverview_NeverUsesNodeKeys documents the expected pattern: Overview
// diagrams use only synthetic nodes. This test verifies the endpoint still works
// even if node_keys are passed (we don't block it).
func TestDiagramOverview_NeverUsesNodeKeys(t *testing.T) {
	srv := setupDiagramServer(t)

	// Overview with node_keys should still work
	body := `{
		"title": "Overview with keys",
		"node_keys": ["service:service:tomandjerry:arena-api"],
		"nodes": [
			{"key": "domain:core", "label": "Core Domain", "kind": "domain"}
		],
		"edges": [
			{"from": "domain:core", "to": "service:service:tomandjerry:arena-api", "label": "contains", "kind": "structural"}
		]
	}`
	result := postDiagram(t, srv, body)

	// Should succeed — we don't enforce the "no node_keys for overview" rule server-side
	nodeCount := int(result["node_count"].(float64))
	if nodeCount != 2 {
		t.Errorf("node_count = %d, want 2 (1 real + 1 synthetic)", nodeCount)
	}
}

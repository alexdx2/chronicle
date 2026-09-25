package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/live"
)

func TestDashboardState_Closed(t *testing.T) {
	g := newLabTestGraph(t)
	SetAdminPort(0)
	res, err := dashboardStateHandler(g)(context.Background(), makeRevisionRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	out := toolResultJSON(t, res)
	if out["open"] != false || out["admin_url"] != "http://localhost:4200" {
		t.Fatalf("closed state = %v", out)
	}
	if v, ok := out["last_seen_seconds_ago"]; !ok || v != nil {
		t.Fatalf("never seen → null: %v", out)
	}
}

func TestDashboardState_ReportsTheScreen(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, _ := seedTomJerry(t, g)
	spec := viewmodel.PresetSpec("c2", "test", "")
	if _, err := live.ReportScreen(g.Store(), live.Screen{ClientID: "tab1", Tab: "graph", Title: "C2 Containers", Spec: &spec,
		Selected: &live.Selection{Kind: "node", Key: tomKey}, Breadcrumbs: []string{"C2 Containers"}, Visible: true}); err != nil {
		t.Fatal(err)
	}
	res, _ := dashboardStateHandler(g)(context.Background(), makeRevisionRequest(map[string]any{}))
	out := toolResultJSON(t, res)
	if out["open"] != true || out["preset"] != "c2" || out["domain"] != "test" || !strings.Contains(out["url"].(string), "#v/") {
		t.Fatalf("state = %v", out)
	}
	sel := out["selected"].(map[string]any)
	if sel["name"] != "tom" || sel["type"] != "service" {
		t.Fatalf("selected = %v", sel)
	}
}

func TestDashboardState_NoAdminInThisProcess(t *testing.T) {
	g := newLabTestGraph(t)
	SetAdminPort(AdminPortNone)
	defer SetAdminPort(0)
	res, _ := dashboardStateHandler(g)(context.Background(), makeRevisionRequest(map[string]any{}))
	out := toolResultJSON(t, res)
	if msg, _ := out["admin"].(string); !strings.Contains(msg, "No admin dashboard") {
		t.Fatalf("state = %v", out)
	}
}

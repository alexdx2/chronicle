package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// chronicle_admin_url must not claim a dashboard is running when the host
// process starts none (chronicle-pro's single-repo serve, core's --no-admin).
// AdminPortNone is the sentinel for that mode; SetAdminURLNote lets the host
// say how a dashboard CAN be started.

func resetAdminState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		SetAdminPort(0)
		SetAdminURLNote("")
	})
}

func callAdminURL(t *testing.T) map[string]any {
	t.Helper()
	res, err := adminURLHandler()(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("adminURLHandler: %v", err)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestAdminURL_SentinelReportsNoDashboard(t *testing.T) {
	resetAdminState(t)
	SetAdminPort(AdminPortNone)
	SetAdminURLNote("Run `chronicle-pro admin` to start the dashboard.")

	out := callAdminURL(t)
	msg, _ := out["message"].(string)
	if strings.Contains(msg, "is running at") {
		t.Fatalf("sentinel mode still claims a running dashboard: %q", msg)
	}
	if !strings.Contains(msg, "No admin dashboard is running") {
		t.Fatalf("sentinel mode must say no dashboard is running, got: %q", msg)
	}
	if !strings.Contains(msg, "chronicle-pro admin") {
		t.Fatalf("host note not included: %q", msg)
	}
	if running, ok := out["running"].(bool); !ok || running {
		t.Fatalf("expected running=false, got %v", out["running"])
	}
	if _, hasURL := out["url"]; hasURL {
		t.Fatalf("sentinel mode must not fabricate a url, got %v", out["url"])
	}
}

func TestAdminURL_SentinelWithoutNoteStaysNeutral(t *testing.T) {
	resetAdminState(t)
	SetAdminPort(AdminPortNone)

	out := callAdminURL(t)
	msg, _ := out["message"].(string)
	if !strings.Contains(msg, "No admin dashboard is running") {
		t.Fatalf("expected neutral no-dashboard message, got: %q", msg)
	}
}

func TestAdminURL_DefaultBehaviorUnchanged(t *testing.T) {
	resetAdminState(t)
	SetAdminPort(0) // unset — core's serve default resolves to 4200

	out := callAdminURL(t)
	if url, _ := out["url"].(string); url != "http://localhost:4200" {
		t.Fatalf("default url changed: %q", url)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "running at http://localhost:4200") {
		t.Fatalf("default message changed: %q", msg)
	}
}

func TestScanStatus_AdminDashboardHonestUnderSentinel(t *testing.T) {
	resetAdminState(t)
	SetAdminPort(AdminPortNone)
	SetAdminURLNote("Run `chronicle-pro admin` to start the dashboard.")

	g := newLabTestGraph(t)
	res, err := scanStatusHandler(g)(context.Background(), makeRevisionRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("scanStatusHandler: %v", err)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	dash, _ := out["admin_dashboard"].(string)
	if strings.Contains(dash, "http://localhost") {
		t.Fatalf("scan_status still reports a dashboard URL in sentinel mode: %q", dash)
	}
	if !strings.Contains(dash, "No admin dashboard is running") {
		t.Fatalf("scan_status admin_dashboard must say no dashboard runs, got: %q", dash)
	}
}

func TestScanStatus_AdminDashboardURLWhenPortSet(t *testing.T) {
	resetAdminState(t)
	SetAdminPort(4321)

	g := newLabTestGraph(t)
	res, err := scanStatusHandler(g)(context.Background(), makeRevisionRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("scanStatusHandler: %v", err)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	if !strings.Contains(text, "http://localhost:4321") {
		t.Fatalf("scan_status lost the dashboard URL with a real port: %s", text)
	}
}

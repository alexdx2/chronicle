package admin

import (
	"strings"
	"testing"
)

func TestRenderDashboard_DefaultShowsAllTabs(t *testing.T) {
	html := RenderDashboard(DashboardSlots{})
	for _, tabID := range []string{"overview", "graph", "language", "settings"} {
		btnMarker := "<!-- TAB_BTN:" + tabID + ":start -->"
		if !strings.Contains(html, btnMarker) {
			t.Errorf("default render missing tab button %q", tabID)
		}
		panelMarker := "<!-- TAB_PANEL:" + tabID + ":start -->"
		if !strings.Contains(html, panelMarker) {
			t.Errorf("default render missing tab panel %q", tabID)
		}
	}
}

func TestRenderDashboard_EnabledTabsFilters(t *testing.T) {
	html := RenderDashboard(DashboardSlots{
		EnabledTabs: []string{"graph", "language"},
	})
	if !strings.Contains(html, "<!-- TAB_BTN:graph:start -->") {
		t.Error("filtered render missing graph button")
	}
	if !strings.Contains(html, "<!-- TAB_PANEL:graph:start -->") {
		t.Error("filtered render missing graph panel")
	}
	if !strings.Contains(html, "<!-- TAB_BTN:language:start -->") {
		t.Error("filtered render missing language button")
	}
	if strings.Contains(html, "<!-- TAB_BTN:overview:start -->") {
		t.Error("overview button should be removed")
	}
	if strings.Contains(html, "<!-- TAB_PANEL:overview:start -->") {
		t.Error("overview panel should be removed")
	}
	if strings.Contains(html, "<!-- TAB_BTN:settings:start -->") {
		t.Error("settings button should be removed")
	}
	if strings.Contains(html, "<!-- TAB_PANEL:settings:start -->") {
		t.Error("settings panel should be removed")
	}
}

func TestRenderDashboard_DefaultTab(t *testing.T) {
	html := RenderDashboard(DashboardSlots{DefaultTab: "graph"})
	if !strings.Contains(html, `data-default-tab="graph"`) {
		t.Error("expected data-default-tab='graph'")
	}

	html = RenderDashboard(DashboardSlots{EnabledTabs: []string{"language", "graph"}})
	if !strings.Contains(html, `data-default-tab="language"`) {
		t.Error("expected data-default-tab='language' from first EnabledTabs entry")
	}

	html = RenderDashboard(DashboardSlots{})
	if !strings.Contains(html, `data-default-tab=""`) {
		t.Error("expected empty data-default-tab")
	}
}

func TestRenderDashboard_TitleAndSubtitle(t *testing.T) {
	html := RenderDashboard(DashboardSlots{Title: "Chronicle Playground", Subtitle: "Demo"})
	if !strings.Contains(html, "<title>Chronicle Playground</title>") {
		t.Error("title not replaced")
	}
	if !strings.Contains(html, "Demo") {
		t.Error("subtitle not replaced")
	}
}

func TestRemoveBetweenMarkers(t *testing.T) {
	html := "before<!-- START -->middle<!-- END -->after"
	result := removeBetweenMarkers(html, "<!-- START -->", "<!-- END -->")
	if result != "beforeafter" {
		t.Errorf("got %q, want %q", result, "beforeafter")
	}
	result = removeBetweenMarkers(html, "<!-- MISSING -->", "<!-- END -->")
	if result != html {
		t.Error("should not modify when start marker missing")
	}
}

func TestRemoveAllBetweenMarkers(t *testing.T) {
	html := "a<!-- S -->1<!-- E -->b<!-- S -->2<!-- E -->c"
	if got := removeAllBetweenMarkers(html, "<!-- S -->", "<!-- E -->"); got != "abc" {
		t.Fatalf("got %q, want every region removed", got)
	}
	if got := removeAllBetweenMarkers("a<!-- S -->x", "<!-- S -->", "<!-- E -->"); got != "a<!-- S -->x" {
		t.Fatalf("an unterminated region must be left alone: %q", got)
	}
}

func TestRenderDashboard_AgentLiveSections(t *testing.T) {
	full := RenderDashboard(DashboardSlots{})
	for _, want := range []string{`data-testid="agent-menu"`, `data-testid="agent-cards"`, `import('/js/live-client.js')`} {
		if !strings.Contains(full, want) {
			t.Fatalf("dashboard lacks %s", want)
		}
	}
	hidden := RenderDashboard(DashboardSlots{HideSections: []string{"agent_live"}})
	for _, gone := range []string{`data-testid="agent-menu"`, `data-testid="agent-cards"`, "SECTION:agent_live"} {
		if strings.Contains(hidden, gone) {
			t.Errorf("agent_live hidden but %s remains", gone)
		}
	}
}

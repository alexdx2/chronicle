// Package admin provides the Chronicle dashboard HTML with extension slots.
// Pro imports this package to inject federation UI without duplicating the base dashboard.
package admin

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed static/*
var staticFS embed.FS

// StaticFS returns the embedded static filesystem (favicon, logo, etc).
func StaticFS() embed.FS { return staticFS }

// DashboardSlots defines content to inject into the dashboard HTML.
// All fields are optional — empty strings leave the default content.
type DashboardSlots struct {
	// Title replaces "Chronicle" in <h1> and <title>.
	Title string
	// Subtitle replaces the version subtitle line.
	Subtitle string
	// CSS is appended before </style>.
	CSS string
	// ExtraTabs is inserted after the Settings tab button.
	ExtraTabs string
	// AfterHeader is inserted after the </header> tag.
	AfterHeader string
	// SettingsTop is inserted at the top of the Settings tab container.
	SettingsTop string
	// ExtraContent is inserted before <script> (federation tab, modals, etc).
	ExtraContent string
	// ExtraState is inserted into the Alpine data object.
	ExtraState string
	// ExtraRoutes is inserted into the URL routing in init().
	ExtraRoutes string
	// ExtraInit is inserted into init() after version fetch.
	ExtraInit string
	// ExtraMethods is inserted into the Alpine methods.
	ExtraMethods string
	// EnabledTabs is a list of tab IDs to include. Empty means show all.
	EnabledTabs []string
	// DefaultTab overrides the initially active tab.
	DefaultTab string
	// HideSections is a list of section IDs to remove (e.g. "mcp_requests").
	HideSections []string
}

var titleRe = regexp.MustCompile(`<!-- SLOT:TITLE_START -->.*?<!-- SLOT:TITLE_END -->`)
var subtitleRe = regexp.MustCompile(`<!-- SLOT:SUBTITLE_START -->.*?<!-- SLOT:SUBTITLE_END -->`)

// RenderDashboard returns the dashboard HTML with slots filled in.
func RenderDashboard(slots DashboardSlots) string {
	raw, _ := staticFS.ReadFile("static/index.html")
	html := string(raw)

	if slots.Title != "" {
		html = strings.Replace(html, "<title>Chronicle</title>", "<title>"+slots.Title+"</title>", 1)
		html = titleRe.ReplaceAllString(html, slots.Title)
	}
	if slots.Subtitle != "" {
		html = subtitleRe.ReplaceAllString(html, slots.Subtitle)
	}

	html = strings.Replace(html, "<!-- SLOT:CSS -->", slots.CSS, 1)
	html = strings.Replace(html, "<!-- SLOT:EXTRA_TABS -->", slots.ExtraTabs, 1)
	html = strings.Replace(html, "<!-- SLOT:AFTER_HEADER -->", slots.AfterHeader, 1)
	html = strings.Replace(html, "<!-- SLOT:SETTINGS_TOP -->", slots.SettingsTop, 1)
	html = strings.Replace(html, "<!-- SLOT:EXTRA_CONTENT -->", slots.ExtraContent, 1)
	html = strings.Replace(html, "// SLOT:EXTRA_STATE", slots.ExtraState, 1)
	html = strings.Replace(html, "// SLOT:EXTRA_ROUTES", slots.ExtraRoutes, 1)
	html = strings.Replace(html, "// SLOT:EXTRA_INIT", slots.ExtraInit, 1)
	html = strings.Replace(html, "// SLOT:EXTRA_METHODS", slots.ExtraMethods, 1)

	// Default tab
	if slots.DefaultTab != "" {
		html = strings.Replace(html, "<!-- SLOT:DEFAULT_TAB -->", slots.DefaultTab, 1)
	} else if len(slots.EnabledTabs) > 0 {
		html = strings.Replace(html, "<!-- SLOT:DEFAULT_TAB -->", slots.EnabledTabs[0], 1)
	} else {
		html = strings.Replace(html, "<!-- SLOT:DEFAULT_TAB -->", "", 1)
	}

	// Filter tabs
	if len(slots.EnabledTabs) > 0 {
		allowed := make(map[string]bool, len(slots.EnabledTabs))
		for _, id := range slots.EnabledTabs {
			allowed[id] = true
		}
		allTabs := []string{"overview", "graph", "language", "settings"}
		for _, tabID := range allTabs {
			if allowed[tabID] {
				continue
			}
			btnStart := "<!-- TAB_BTN:" + tabID + ":start -->"
			btnEnd := "<!-- TAB_BTN:" + tabID + ":end -->"
			html = removeBetweenMarkers(html, btnStart, btnEnd)
			panelStart := "<!-- TAB_PANEL:" + tabID + ":start -->"
			panelEnd := "<!-- TAB_PANEL:" + tabID + ":end -->"
			html = removeBetweenMarkers(html, panelStart, panelEnd)
		}
	}

	// Hide sections
	for _, id := range slots.HideSections {
		start := "<!-- SECTION:" + id + ":start -->"
		end := "<!-- SECTION:" + id + ":end -->"
		html = removeBetweenMarkers(html, start, end)
	}

	return html
}

func removeBetweenMarkers(html, startMarker, endMarker string) string {
	start := strings.Index(html, startMarker)
	if start == -1 {
		return html
	}
	end := strings.Index(html[start:], endMarker)
	if end == -1 {
		return html
	}
	return html[:start] + html[start+end+len(endMarker):]
}

// StaticFile returns the content of a static asset (favicon, logos).
func StaticFile(name string) ([]byte, error) {
	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		return nil, fmt.Errorf("static file %s: %w", name, err)
	}
	return data, nil
}

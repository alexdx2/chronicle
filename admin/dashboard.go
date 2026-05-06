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

	return html
}

// StaticFile returns the content of a static asset (favicon, logos).
func StaticFile(name string) ([]byte, error) {
	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		return nil, fmt.Errorf("static file %s: %w", name, err)
	}
	return data, nil
}

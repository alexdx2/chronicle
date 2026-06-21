package admin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

func TestReadHandlers_SmokeOK(t *testing.T) {
	s := setupTestServer(t)
	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		path string
	}{
		{"info", s.handleInfo, "/api/info"},
		{"projects", s.handleProjects, "/api/projects"},
		{"metrics", s.handleMetrics, "/api/metrics"},
		{"discoveries", s.handleDiscoveries, "/api/discoveries?domain=test"},
		{"glossary", s.handleGlossary, "/api/glossary?domain=test"},
		{"languageCheck", s.handleLanguageCheck, "/api/language/check?domain=test"},
		{"registry", s.handleRegistry, "/api/registry"},
		{"manifestGET", s.handleManifest, "/api/manifest"},
		{"evidenceNoKey", s.handleEvidence, "/api/evidence"},
		{"diagramList", s.handleDiagramList, "/api/diagram/list"},
		{"diagramLatest", s.handleDiagramLatest, "/api/diagram/latest"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", c.path, nil)
			w := httptest.NewRecorder()
			c.fn(w, req)
			if w.Code >= 500 {
				t.Errorf("%s returned %d (server error): %s", c.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleView_GET(t *testing.T) {
	s := setupTestServer(t)

	// Missing spec → 400.
	req := httptest.NewRequest("GET", "/api/view", nil)
	w := httptest.NewRecorder()
	s.handleView(w, req)
	if w.Code != 400 {
		t.Errorf("missing spec: want 400, got %d", w.Code)
	}

	// Valid base64url spec → 200.
	spec := viewmodel.ViewSpec{Scope: viewmodel.ScopeSpec{Domain: "test"}, Layout: viewmodel.LayoutSpec{Preset: "c2"}}
	b, _ := json.Marshal(spec)
	enc := base64.RawURLEncoding.EncodeToString(b)
	req = httptest.NewRequest("GET", "/api/view?spec="+enc, nil)
	w = httptest.NewRecorder()
	s.handleView(w, req)
	if w.Code != 200 {
		t.Errorf("valid spec GET: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Invalid base64 → 400.
	req = httptest.NewRequest("GET", "/api/view?spec=!!!notb64!!!", nil)
	w = httptest.NewRecorder()
	s.handleView(w, req)
	if w.Code != 400 {
		t.Errorf("bad base64: want 400, got %d", w.Code)
	}
}

func TestHandleView_POST(t *testing.T) {
	s := setupTestServer(t)
	spec := viewmodel.ViewSpec{Scope: viewmodel.ScopeSpec{Domain: "test"}, Layout: viewmodel.LayoutSpec{Preset: "c1"}}
	b, _ := json.Marshal(spec)
	req := httptest.NewRequest("POST", "/api/view", strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	s.handleView(w, req)
	if w.Code != 200 {
		t.Errorf("view POST: want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPureHelpers(t *testing.T) {
	if portFromPath("/some/dir") < 4200 || portFromPath("/some/dir") >= 5000 {
		t.Error("portFromPath out of expected range")
	}
	// Deterministic.
	if portFromPath("/x") != portFromPath("/x") {
		t.Error("portFromPath not deterministic")
	}
	if findModuleRoot() == "" {
		t.Error("findModuleRoot empty")
	}
	// diagramSessionCounts parses a diagram JSON payload into kind + counts.
	kind, n, e := diagramSessionCounts(`{"kind":"c2","nodes":[{},{}],"edges":[{}]}`)
	if kind != "c2" || n != 2 || e != 1 {
		t.Errorf("diagramSessionCounts: kind=%q n=%d e=%d", kind, n, e)
	}
}

func TestMutationHandlers(t *testing.T) {
	s := setupTestServer(t)
	do := func(name string, fn func(http.ResponseWriter, *http.Request), method, path, body string) int {
		var rdr *strings.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		} else {
			rdr = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, rdr)
		w := httptest.NewRecorder()
		fn(w, req)
		if w.Code >= 500 {
			t.Errorf("%s -> %d: %s", name, w.Code, w.Body.String())
		}
		return w.Code
	}

	// Save a term with an anti-pattern.
	do("saveTerm", s.handleSaveTerm, "POST", "/api/language/term?domain=test",
		`{"term":"Battle","description":"a battle","aliases":"fight","anti_patterns":"brawl","examples":"x"}`)
	// 405 path for wrong method.
	if code := do("saveTermGET", s.handleSaveTerm, "GET", "/api/language/term", ""); code != 405 {
		t.Errorf("saveTerm GET should be 405, got %d", code)
	}
	// Glossary now contains the term.
	{
		req := httptest.NewRequest("GET", "/api/glossary?domain=test", nil)
		w := httptest.NewRecorder()
		s.handleGlossary(w, req)
		if !strings.Contains(w.Body.String(), "Battle") {
			t.Errorf("glossary missing saved term: %s", w.Body.String())
		}
	}
	// Dismiss the anti-pattern.
	do("dismiss", s.handleDismissViolation, "POST", "/api/language/dismiss?domain=test&term=Battle&anti=brawl", "")
	// Missing params → 400.
	if code := do("dismissBad", s.handleDismissViolation, "POST", "/api/language/dismiss?domain=test", ""); code != 400 {
		t.Errorf("dismiss missing params should be 400, got %d", code)
	}
	// Delete the term.
	do("deleteTerm", s.handleDeleteTerm, "POST", "/api/language/term?domain=test&term=Battle", "")
	if code := do("deleteBad", s.handleDeleteTerm, "POST", "/api/language/term?domain=test", ""); code != 400 {
		t.Errorf("deleteTerm missing term should be 400, got %d", code)
	}

	// Edge category settings: GET, PUT, DELETE.
	do("edgeCatsGET", s.handleEdgeCategorySettings, "GET", "/api/settings/edge-categories", "")
	do("edgeCatsPUT", s.handleEdgeCategorySettings, "PUT", "/api/settings/edge-categories", `{"calls":{"label":"Calls","color":"#fff"}}`)
	do("edgeCatsDELETE", s.handleEdgeCategorySettings, "DELETE", "/api/settings/edge-categories", "")

	// Prompt settings GET (merged defaults + overrides).
	do("promptGET", s.handlePromptSetting, "GET", "/api/settings/prompts", "")
}

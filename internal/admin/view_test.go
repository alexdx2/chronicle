package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

func postViewSpec(t *testing.T, srv *Server, spec string) viewmodel.View {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/view", strings.NewReader(spec))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleView(w, req)
	if w.Code != 200 {
		t.Fatalf("/api/view status=%d body=%s", w.Code, w.Body.String())
	}
	var v viewmodel.View
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

// /api/view is the only diagram endpoint. The C4 questions on the mini
// fixture (see setupC4Graph): C1 counts, C2 services + lifted arrows,
// C3 components of one service, stale nodes excluded everywhere.
func TestView_C1_SystemBoxAndExternals(t *testing.T) {
	srv, _ := setupC4Graph(t)
	v := postViewSpec(t, srv, `{"scope":{"domain":"test-domain"},"group":{"by":"domain"},"collapse":true,"layout":{"preset":"c1"}}`)
	if len(v.Groups) != 1 || v.Groups[0].Kind != "domain" {
		t.Fatalf("C1 wants exactly the system box: %+v", v.Groups)
	}
	st := v.Groups[0].Stats
	if st["services"] != 2 || st["endpoints"] < 3 {
		t.Errorf("system box stats=%v want 2 services, >=3 endpoints", st)
	}
	names := map[string]bool{}
	for _, n := range v.Nodes {
		names[n.Name] = true
		if n.Layer == "code" || n.Type == "topic" {
			t.Errorf("C1 must not show code or internal topics: %s", n.Name)
		}
	}
	if !names["notifications"] {
		t.Errorf("external system missing at C1: %v", names)
	}
}

func TestView_C2_ServicesAndLiftedArrows(t *testing.T) {
	srv, _ := setupC4Graph(t)
	v := postViewSpec(t, srv, `{"scope":{"domain":"test-domain"},"filter":{"edge_kinds":["sync","async"],"layers":["code","contract","service","infra"]},"group":{"by":"service"},"collapse":true,"layout":{"preset":"c2"}}`)
	services := map[string]bool{}
	for _, g := range v.Groups {
		if g.Kind == "service" {
			services[g.Name] = true
		}
	}
	if !services["svc-a"] || !services["svc-b"] {
		t.Fatalf("C2 service cards=%v", services)
	}
	var svcAB, pub, sub bool
	for _, e := range v.Edges {
		switch {
		case e.From == "service:service:test:svc-b" && e.To == "service:service:test:svc-a" && e.Kind == "CALLS_SERVICE":
			svcAB = true
		case e.Kind == "PUBLISHES_TOPIC" && e.From == "service:service:test:svc-a":
			pub = true
		case e.Kind == "CONSUMES_TOPIC" && e.To == "service:service:test:svc-b":
			sub = true // flow direction: topic → consumer
		}
	}
	if !svcAB || !pub || !sub {
		t.Errorf("lifted arrows: svc-b→svc-a=%v pub=%v sub=%v; edges=%+v", svcAB, pub, sub, v.Edges)
	}
	for _, n := range v.Nodes {
		if strings.Contains(n.Name, "stale") {
			t.Errorf("stale node leaked into C2: %s", n.Name)
		}
	}
}

func TestView_C3_OneService(t *testing.T) {
	srv, _ := setupC4Graph(t)
	v := postViewSpec(t, srv, `{"scope":{"domain":"test-domain","service":"svc-a"},"group":{"by":"module"},"layout":{"preset":"c3"}}`)
	got := map[string]viewmodel.VNode{}
	for _, n := range v.Nodes {
		got[n.Name] = n
	}
	for _, want := range []string{"arena.controller", "arena.service"} {
		n, ok := got[want]
		if !ok {
			t.Errorf("C3 svc-a missing %s: %v", want, got)
			continue
		}
		if n.Group != "code:module:test:arena.module" {
			t.Errorf("%s group=%q want arena.module", want, n.Group)
		}
	}
	if n, ok := got["arena.controller"]; ok && len(n.Endpoints) != 2 {
		t.Errorf("arena.controller endpoints=%v want 2 pills", n.Endpoints)
	}
	if _, ok := got["stats.controller"]; ok {
		t.Errorf("svc-b's controller must not be a component of svc-a")
	}
	if v.Boundary == nil || len(v.Boundary.Out) == 0 {
		t.Errorf("svc-a publishes battle-results: expected an outgoing boundary entry, got %+v", v.Boundary)
	}
}

func TestView_C3_UnknownServiceIs404(t *testing.T) {
	srv, _ := setupC4Graph(t)
	req := httptest.NewRequest("POST", "/api/view", strings.NewReader(`{"scope":{"domain":"test-domain","service":"nope"},"group":{"by":"module"},"layout":{"preset":"c3"}}`))
	w := httptest.NewRecorder()
	srv.handleView(w, req)
	if w.Code != 404 {
		t.Errorf("status=%d want 404", w.Code)
	}
}

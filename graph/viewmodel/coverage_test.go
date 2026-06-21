package viewmodel

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

func TestParseRoleClaim_Branches(t *testing.T) {
	// assertion JSON wins, role_reason preferred.
	r, reason := parseRoleClaim(`{"role":"entity","role_reason":"persisted"}`, "")
	if r != "entity" || reason != "persisted" {
		t.Errorf("assertion: got %q/%q", r, reason)
	}
	// fall back to metadata blob; "reason" alias accepted.
	r, reason = parseRoleClaim("", `{"role":"dto","reason":"payload"}`)
	if r != "dto" || reason != "payload" {
		t.Errorf("metadata: got %q/%q", r, reason)
	}
	// no role anywhere → empty.
	if r, _ := parseRoleClaim(`{"x":1}`, `{"y":2}`); r != "" {
		t.Errorf("no role: got %q", r)
	}
	// invalid json in first, valid in second.
	if r, _ := parseRoleClaim("not json", `{"role":"helper"}`); r != "helper" {
		t.Errorf("invalid+valid: got %q", r)
	}
	// both empty.
	if r, _ := parseRoleClaim("", ""); r != "" {
		t.Error("empty should yield empty role")
	}
}

func TestNotFoundError(t *testing.T) {
	st := openLiveDBCopy(t)
	_, err := BuildC3(st, "tom-and-jerry", "no-such-service-xyz")
	if err == nil {
		t.Fatal("expected NotFoundError")
	}
	if _, ok := err.(*NotFoundError); !ok {
		t.Fatalf("want *NotFoundError, got %T", err)
	}
	if err.Error() == "" {
		t.Error("NotFoundError.Error() should be non-empty")
	}
}

func TestBuildPreset_AllLevels(t *testing.T) {
	st := openLiveDBCopy(t)
	const domain = "tom-and-jerry"

	// Discover a service name and two node keys for target-bearing presets.
	nodes, err := st.ListNodes(store.NodeFilter{Domain: domain})
	if err != nil {
		t.Fatal(err)
	}
	var svc string
	var keys []string
	for _, n := range nodes {
		if n.Status != "active" {
			continue
		}
		if svc == "" && n.Layer == "service" && n.NodeType == "service" {
			svc = n.Name
		}
		if len(keys) < 2 && n.Layer == "code" {
			keys = append(keys, n.NodeKey)
		}
	}
	if svc == "" || len(keys) < 2 {
		t.Fatalf("fixture lacks service/code nodes (svc=%q keys=%d)", svc, len(keys))
	}

	// Domain-only presets.
	for _, lvl := range []string{"c1", "c2", "data", "api", "services", "flows"} {
		if _, err := BuildPreset(st, lvl, domain, ""); err != nil {
			t.Errorf("BuildPreset(%s): %v", lvl, err)
		}
	}
	// Service-scoped.
	if _, err := BuildPreset(st, "c3", domain, svc); err != nil {
		t.Errorf("BuildPreset(c3,%s): %v", svc, err)
	}
	// Node-scoped: deps / impact.
	for _, lvl := range []string{"deps", "impact"} {
		if _, err := BuildPreset(st, lvl, domain, keys[0]); err != nil {
			t.Errorf("BuildPreset(%s): %v", lvl, err)
		}
	}
}

func TestBuildView_TransportAndEdgeKindFilter(t *testing.T) {
	st := openLiveDBCopy(t)
	// Exercise the transport / edge-kind filter path (transportMatches, edgeTransport).
	spec := ViewSpec{
		Scope:  ScopeSpec{Domain: "tom-and-jerry"},
		Filter: &FilterSpec{EdgeKinds: []string{"async"}, Transports: []string{"broker"}, Layers: []string{"service", "contract", "code"}},
		Group:  GroupSpec{By: "service"},
		Layout: LayoutSpec{Preset: "c2"},
	}
	if _, err := BuildView(st, spec); err != nil {
		t.Fatalf("BuildView transport filter: %v", err)
	}
	// sync filter too.
	spec.Filter.EdgeKinds = []string{"sync"}
	spec.Filter.Transports = []string{"http"}
	if _, err := BuildView(st, spec); err != nil {
		t.Fatalf("BuildView sync filter: %v", err)
	}
}

func TestBuildSelection_FromKeys(t *testing.T) {
	st := openLiveDBCopy(t)
	const domain = "tom-and-jerry"
	nodes, _ := st.ListNodes(store.NodeFilter{Domain: domain})
	var keys []string
	for _, n := range nodes {
		if n.Status == "active" && (n.NodeType == "controller" || n.NodeType == "provider") {
			keys = append(keys, n.NodeKey)
		}
		if len(keys) >= 3 {
			break
		}
	}
	if len(keys) == 0 {
		t.Skip("no component nodes in fixture")
	}
	// Include a bogus key to exercise the Missing path.
	sel, err := BuildSelection(st, domain, append(keys, "code:provider:tom-and-jerry:does/not/exist"), "My Selection")
	if err != nil {
		t.Fatalf("BuildSelection: %v", err)
	}
	if sel.Level != "custom" || sel.Title != "My Selection" {
		t.Errorf("selection meta: %+v", sel)
	}
	if len(sel.Components) == 0 {
		t.Error("expected resolved components")
	}
	if len(sel.Missing) == 0 {
		t.Error("expected the bogus key in Missing")
	}
}

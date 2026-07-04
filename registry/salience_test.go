package registry

import (
	"strings"
	"testing"
)

func TestSaliencePolicy_RenderRuleLookup(t *testing.T) {
	p := &SaliencePolicy{
		RenderPolicy: map[string]map[string]RenderRule{
			"type:data.dto": {
				"default": {RenderMode: "hidden"},
				"focus":   {RenderMode: "expandable_detail"},
			},
		},
		Roles: map[string]RoleRule{
			"request_dto": {Promotable: true, MaxTier: "secondary"},
		},
	}

	rule, ok := p.Rule("type:data.dto", "default")
	if !ok || rule.RenderMode != "hidden" {
		t.Fatalf("default rule: got %+v ok=%v", rule, ok)
	}
	rule, ok = p.Rule("type:data.dto", "focus")
	if !ok || rule.RenderMode != "expandable_detail" {
		t.Fatalf("focus rule: got %+v ok=%v", rule, ok)
	}
	if _, ok := p.Rule("type:data.dto", "c3"); ok {
		t.Fatalf("c3 rule should not exist")
	}

	role, ok := p.Role("request_dto")
	if !ok || !role.Promotable || role.MaxTier != "secondary" {
		t.Fatalf("role: got %+v ok=%v", role, ok)
	}
}

func TestLoad_ParsesSalienceSection(t *testing.T) {
	yaml := []byte(`
version: "1"
layers: [data]
node_types:
  data: [dto, entity]
salience:
  render_policy:
    "type:data.dto":
      default: { render_mode: hidden }
    "role:entity":
      default: { tier: primary, render_mode: box }
  roles:
    entity: { promotable: true }
  noise_roles: [generated]
`)
	r, err := Load(yaml)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := r.SaliencePolicy()
	if p == nil {
		t.Fatal("SaliencePolicy() == nil")
	}
	if rule, ok := p.Rule("role:entity", "default"); !ok || rule.Tier != "primary" || rule.RenderMode != "box" {
		t.Fatalf("role:entity rule: %+v ok=%v", rule, ok)
	}
	if !p.IsNoiseRole("generated") {
		t.Fatal("generated should be a noise role")
	}
}

func TestLoad_NoSalienceSection_FallsBackToDefaultSalience(t *testing.T) {
	// A project chronicle.types.yaml that customizes types but has no
	// salience: section must inherit the built-in salience defaults —
	// otherwise every diagram collapses to hidden (real regression).
	yaml := []byte(`
version: "1"
layers: [data]
node_types:
  data: [dto]
`)
	r, err := Load(yaml)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := r.SaliencePolicy()
	if p == nil {
		t.Fatal("SaliencePolicy() must be non-nil even when absent")
	}
	if rule, ok := p.Rule("type:contract.endpoint", "default"); !ok || rule.Tier != "primary" {
		t.Fatalf("endpoint must inherit default salience: %+v ok=%v", rule, ok)
	}
	if !p.IsNoiseRole("generated") {
		t.Fatal("default noise_roles must be inherited")
	}
}

func TestLoad_PartialSalience_MergesOverDefaults(t *testing.T) {
	yaml := []byte(`
version: "1"
layers: [data]
node_types:
  data: [dto]
salience:
  render_policy:
    "type:data.dto": { default: { tier: secondary, render_mode: badge } }
  roles:
    helper: { promotable: true }
`)
	r, err := Load(yaml)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := r.SaliencePolicy()
	// User entry wins for its key.
	if rule, ok := p.Rule("type:data.dto", "default"); !ok || rule.RenderMode != "badge" || rule.Tier != "secondary" {
		t.Fatalf("user dto rule must win: %+v ok=%v", rule, ok)
	}
	// User key replaces the WHOLE level-map for that key: the default
	// focus-level dto rule is gone (predictable per-key granularity).
	if _, ok := p.Rule("type:data.dto", "focus"); ok {
		t.Fatal("user key must replace the whole level-map for that key")
	}
	// Untouched default keys survive.
	if rule, ok := p.Rule("type:contract.endpoint", "default"); !ok || rule.Tier != "primary" {
		t.Fatalf("default endpoint rule must survive merge: %+v ok=%v", rule, ok)
	}
	// User role overrides that role; default roles survive.
	if rr, ok := p.Role("helper"); !ok || !rr.Promotable {
		t.Fatalf("user helper role must win: %+v ok=%v", rr, ok)
	}
	if rr, ok := p.Role("entity"); !ok || !rr.Promotable {
		t.Fatalf("default entity role must survive: %+v ok=%v", rr, ok)
	}
	// noise_roles not set by user -> defaults inherited.
	if !p.IsNoiseRole("test_fixture") {
		t.Fatal("default noise_roles must be inherited when user omits them")
	}
}

func TestLoad_MergeDoesNotMutateDefaults(t *testing.T) {
	yaml := []byte(`
version: "1"
layers: [data]
node_types:
  data: [dto]
salience:
  render_policy:
    "type:contract.endpoint": { default: { tier: detail, render_mode: hidden } }
`)
	if _, err := Load(yaml); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A later independent load of the defaults must be unaffected.
	r2, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	if rule, ok := r2.SaliencePolicy().Rule("type:contract.endpoint", "default"); !ok || rule.Tier != "primary" {
		t.Fatalf("defaults were mutated by a prior merge: %+v ok=%v", rule, ok)
	}
}

func TestLoadDefaults_HasSaliencePolicy(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	p := r.SaliencePolicy()
	if rule, ok := p.Rule("type:contract.endpoint", "default"); !ok || rule.Tier != "primary" {
		t.Fatalf("endpoint should default to primary: %+v ok=%v", rule, ok)
	}
	if rule, ok := p.Rule("type:service.service", "default"); !ok || rule.Tier != "primary" {
		t.Fatalf("service should default to primary: %+v ok=%v", rule, ok)
	}
	if rule, ok := p.Rule("type:data.field", "default"); !ok || rule.RenderMode != "hidden" {
		t.Fatalf("field should default to hidden: %+v ok=%v", rule, ok)
	}
}

func TestSaliencePolicy_KnownRoles(t *testing.T) {
	p := &SaliencePolicy{
		RenderPolicy: map[string]map[string]RenderRule{
			"type:data.dto": {"default": {RenderMode: "hidden"}},
			"role:entity":   {"default": {Tier: "primary"}},
			"role:port":     {"default": {Tier: "primary"}},
		},
		Roles:      map[string]RoleRule{"request_dto": {Promotable: true}, "entity": {Promotable: true}},
		NoiseRoles: []string{"generated"},
	}
	got := p.KnownRoles()
	// union of role: render keys + Roles keys + noise roles + "unknown", sorted, deduped
	want := map[string]bool{"entity": true, "port": true, "request_dto": true, "generated": true, "unknown": true}
	if len(got) != len(want) {
		t.Fatalf("KnownRoles=%v want keys %v", got, want)
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("unexpected role %q", r)
		}
	}
	// must be sorted
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("not sorted: %v", got)
		}
	}
}

func TestLoad_RejectsInvalidSalienceValues(t *testing.T) {
	cases := []struct {
		name     string
		salience string
		wantSub  string
	}{
		{"bad tier", `
  render_policy:
    "type:data.dto": { default: { tier: primry } }`, "tier"},
		{"bad render_mode", `
  render_policy:
    "type:data.dto": { default: { render_mode: boxx } }`, "render_mode"},
		{"unnamespaced key", `
  render_policy:
    "data.dto": { default: { render_mode: hidden } }`, "namespaced"},
		{"bad max_tier", `
  roles:
    helper: { promotable: false, max_tier: primry }`, "max_tier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y := []byte("version: \"1\"\nlayers: [data]\nnode_types:\n  data: [dto]\nsalience:" + tc.salience + "\n")
			_, err := Load(y)
			if err == nil {
				t.Fatalf("Load should reject %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q should mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestLoadDefaults_KnownRolesNonEmpty(t *testing.T) {
	r, _ := LoadDefaults()
	roles := r.SaliencePolicy().KnownRoles()
	if len(roles) < 5 {
		t.Fatalf("expected several default roles, got %v", roles)
	}
	found := map[string]bool{}
	for _, x := range roles {
		found[x] = true
	}
	for _, must := range []string{"entity", "request_dto", "helper", "unknown"} {
		if !found[must] {
			t.Errorf("default roles missing %q (got %v)", must, roles)
		}
	}
}

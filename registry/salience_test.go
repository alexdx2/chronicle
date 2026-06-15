package registry

import "testing"

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

func TestLoad_NoSalienceSection_ReturnsEmptyPolicy(t *testing.T) {
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
	if _, ok := p.Rule("type:data.dto", "default"); ok {
		t.Fatal("no rules expected")
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

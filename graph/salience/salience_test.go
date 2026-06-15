package salience

import (
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
)

func basePolicy() *registry.SaliencePolicy {
	return &registry.SaliencePolicy{
		RenderPolicy: map[string]map[string]registry.RenderRule{
			"type:data.dto":          {"default": {Tier: "detail", RenderMode: "hidden"}, "focus": {RenderMode: "expandable_detail"}},
			"type:contract.endpoint": {"default": {Tier: "primary", RenderMode: "box"}},
			"role:entity":            {"default": {Tier: "primary", RenderMode: "box"}},
			"role:request_dto":       {"default": {Tier: "detail", RenderMode: "hidden"}, "focus": {RenderMode: "attached_detail"}},
		},
		Roles: map[string]registry.RoleRule{
			"entity":      {Promotable: true},
			"request_dto": {Promotable: true, MaxTier: "secondary"},
			"helper":      {Promotable: false},
		},
		NoiseRoles: []string{"generated", "test_fixture"},
	}
}

func TestResolve_BaseTypeOnly(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", Level: "default"})
	if d.Tier != TierPrimary || d.RenderMode != RenderBox {
		t.Fatalf("endpoint: got tier=%s mode=%s", d.Tier, d.RenderMode)
	}
}

func TestResolve_UnknownType_DefaultsDetailHidden(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "mystery", Layer: "code", Level: "default"})
	if d.Tier != TierDetail || d.RenderMode != RenderHidden {
		t.Fatalf("unknown: got tier=%s mode=%s", d.Tier, d.RenderMode)
	}
}

func TestResolve_RoleRefinesType(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "model", Layer: "data", Role: "entity", Level: "default"})
	if d.Tier != TierPrimary || d.RenderMode != RenderBox {
		t.Fatalf("entity role: got tier=%s mode=%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
}
func TestResolve_LevelOverride(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "dto", Layer: "data", Role: "request_dto", Level: "focus"})
	if d.RenderMode != RenderAttachedDetail {
		t.Fatalf("focus request_dto: got mode=%s trace=%v", d.RenderMode, d.Trace)
	}
}
func TestResolve_UnknownRoleIgnored(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "dto", Layer: "data", Role: "unknown", Level: "default"})
	if d.Tier != TierDetail || d.RenderMode != RenderHidden {
		t.Fatalf("unknown role: got tier=%s mode=%s", d.Tier, d.RenderMode)
	}
}

func TestResolve_PromotionRespectsPromotable(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "model", Layer: "data", Role: "entity", Level: "default", BoundaryCrossing: true})
	if d.Tier != TierPrimary {
		t.Fatalf("promotable entity should reach primary: got %s trace=%v", d.Tier, d.Trace)
	}
}
func TestResolve_PromotionBlockedForNonPromotable(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "symbol", Layer: "code", Role: "helper", Level: "default", BoundaryCrossing: true})
	if d.Tier != TierDetail {
		t.Fatalf("helper must stay detail: got %s trace=%v", d.Tier, d.Trace)
	}
}
func TestResolve_PromotionCappedByMaxTier(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "dto", Layer: "data", Role: "request_dto", Level: "default", BoundaryCrossing: true})
	if d.Tier != TierSecondary {
		t.Fatalf("request_dto capped at secondary: got %s trace=%v", d.Tier, d.Trace)
	}
}

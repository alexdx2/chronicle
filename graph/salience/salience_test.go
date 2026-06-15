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
			"role:aggregate":         {"default": {Tier: "secondary", RenderMode: "collapsed_group"}},
		},
		Roles: map[string]registry.RoleRule{
			"entity":      {Promotable: true},
			"request_dto": {Promotable: true, MaxTier: "secondary"},
			"helper":      {Promotable: false},
			"aggregate":   {Promotable: true},
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

func TestResolve_NoiseDemotes(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", Level: "default", NoiseClass: "generated"})
	if d.Tier != TierDetail || d.RenderMode != RenderHidden {
		t.Fatalf("noise should demote: got tier=%s mode=%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
}
func TestResolve_UserOverrideWins(t *testing.T) {
	box := RenderBox
	d := Resolve(basePolicy(), Input{NodeType: "dto", Layer: "data", Role: "request_dto", Level: "default", UserOverride: &Override{RenderMode: &box}})
	if d.RenderMode != RenderBox {
		t.Fatalf("user override should win: got mode=%s trace=%v", d.RenderMode, d.Trace)
	}
}
func TestResolve_ReconcileFloorsRenderMode(t *testing.T) {
	d := Resolve(basePolicy(), Input{NodeType: "model", Layer: "data", Role: "entity", Level: "default", BoundaryCrossing: true})
	if d.Tier != TierPrimary || d.RenderMode != RenderBox {
		t.Fatalf("reconcile: got tier=%s mode=%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
}
func TestResolve_ReconcileDoesNotOverridePinnedMode(t *testing.T) {
	badge := RenderBadge
	d := Resolve(basePolicy(), Input{NodeType: "model", Layer: "data", Role: "entity", Level: "default", BoundaryCrossing: true, UserOverride: &Override{RenderMode: &badge}})
	if d.RenderMode != RenderBadge {
		t.Fatalf("pinned mode must survive reconcile: got %s", d.RenderMode)
	}
}

func TestResolve_NoiseByRoleFromPolicy(t *testing.T) {
	// No explicit NoiseClass, but role "generated" IS in the policy's noise_roles.
	// It must be demoted to detail/hidden even though the base type is primary.
	d := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", Role: "generated", Level: "default"})
	if d.Tier != TierDetail || d.RenderMode != RenderHidden {
		t.Fatalf("policy noise role should demote: got tier=%s mode=%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
	if d.NoiseClass != "generated" {
		t.Fatalf("NoiseClass should record the role: got %q", d.NoiseClass)
	}
}

func TestResolve_PromotionRaisesBelowPrimary(t *testing.T) {
	// aggregate base tier is secondary. Without boundary crossing it stays secondary;
	// with boundary crossing (and promotable, no cap) it must be raised to primary.
	noBoundary := Resolve(basePolicy(), Input{NodeType: "model", Layer: "data", Role: "aggregate", Level: "default"})
	if noBoundary.Tier != TierSecondary {
		t.Fatalf("aggregate without boundary should stay secondary: got %s trace=%v", noBoundary.Tier, noBoundary.Trace)
	}
	promoted := Resolve(basePolicy(), Input{NodeType: "model", Layer: "data", Role: "aggregate", Level: "default", BoundaryCrossing: true})
	if promoted.Tier != TierPrimary {
		t.Fatalf("aggregate with boundary should be promoted to primary: got %s trace=%v", promoted.Tier, promoted.Trace)
	}
	// And render_mode must be reconciled up to box (floor for primary).
	if promoted.RenderMode != RenderBox {
		t.Fatalf("promoted aggregate render_mode should floor to box: got %s", promoted.RenderMode)
	}
}

func TestResolve_DefaultPolicy_DtoHiddenInC3_DetailInFocus(t *testing.T) {
	r, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	p := r.SaliencePolicy()

	d := Resolve(p, Input{NodeType: "dto", Layer: "data", Role: "request_dto", Level: "default"})
	if d.RenderMode != RenderHidden {
		t.Fatalf("dto in default: want hidden got %s trace=%v", d.RenderMode, d.Trace)
	}
	d = Resolve(p, Input{NodeType: "dto", Layer: "data", Role: "request_dto", Level: "focus"})
	if d.RenderMode != RenderAttachedDetail {
		t.Fatalf("dto in focus: want attached_detail got %s trace=%v", d.RenderMode, d.Trace)
	}
	d = Resolve(p, Input{NodeType: "endpoint", Layer: "contract", Level: "default"})
	if d.RenderMode != RenderBox || d.Tier != TierPrimary {
		t.Fatalf("endpoint: want primary/box got %s/%s", d.Tier, d.RenderMode)
	}
	d = Resolve(p, Input{NodeType: "dto", Layer: "data", Role: "generated", Level: "default", BoundaryCrossing: true})
	if d.RenderMode == RenderBox {
		t.Fatalf("generated dto must not be promoted to box; trace=%v", d.Trace)
	}
}

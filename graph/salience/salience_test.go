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
			"role:helper":            {"default": {RenderMode: "hidden"}},
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

func TestResolve_LowConfidenceRoleCannotDemote(t *testing.T) {
	// A wrong LLM "helper" claim at 0.4 must not hide a node whose type
	// policy says primary/box. Promotion is cheap to be wrong about;
	// hiding is not.
	d := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", Role: "helper", RoleConfidence: 0.4, Level: "default"})
	if d.Tier != TierPrimary || d.RenderMode != RenderBox {
		t.Fatalf("low-confidence helper must not demote: got tier=%s mode=%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
}

func TestResolve_HighConfidenceRoleDemotes(t *testing.T) {
	// At 0.9 the helper claim clears the gate — and the tier floor must NOT
	// resurrect the node to box (tier stayed primary but was never raised).
	d := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", Role: "helper", RoleConfidence: 0.9, Level: "default"})
	if d.RenderMode != RenderHidden {
		t.Fatalf("high-confidence helper must hide: got mode=%s trace=%v", d.RenderMode, d.Trace)
	}
}

func TestResolve_LowConfidenceRoleCanPromote(t *testing.T) {
	// Promotion direction is allowed at any confidence.
	d := Resolve(basePolicy(), Input{NodeType: "dto", Layer: "data", Role: "entity", RoleConfidence: 0.3, Level: "default"})
	if d.Tier != TierPrimary || d.RenderMode != RenderBox {
		t.Fatalf("low-confidence entity may promote: got tier=%s mode=%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
}

func TestResolve_UnsetConfidenceTreatedAsManual(t *testing.T) {
	// RoleConfidence <= 0 means "no recorded confidence" (manually set role)
	// and is trusted — existing behavior preserved.
	d := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", Role: "helper", Level: "default"})
	if d.RenderMode != RenderHidden {
		t.Fatalf("manual helper role must hide: got mode=%s trace=%v", d.RenderMode, d.Trace)
	}
}

func TestResolve_NoiseRoleInferenceGated(t *testing.T) {
	low := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", Role: "generated", RoleConfidence: 0.4, Level: "default"})
	if low.RenderMode != RenderBox {
		t.Fatalf("low-confidence generated must not demote: got mode=%s trace=%v", low.RenderMode, low.Trace)
	}
	high := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", Role: "generated", RoleConfidence: 0.9, Level: "default"})
	if high.Tier != TierDetail || high.RenderMode != RenderHidden {
		t.Fatalf("high-confidence generated must demote: got tier=%s mode=%s trace=%v", high.Tier, high.RenderMode, high.Trace)
	}
}

func TestResolve_ExplicitNoiseClassBypassesGate(t *testing.T) {
	// A caller-supplied NoiseClass (e.g. deterministic path detection) is not
	// an LLM claim and must demote regardless of role confidence.
	d := Resolve(basePolicy(), Input{NodeType: "endpoint", Layer: "contract", NoiseClass: "generated", RoleConfidence: 0.1, Level: "default"})
	if d.Tier != TierDetail || d.RenderMode != RenderHidden {
		t.Fatalf("explicit noise class must demote: got tier=%s mode=%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
}

func TestResolve_DemoteThresholdFromPolicy(t *testing.T) {
	p := basePolicy()
	v := 0.95
	p.MinDemoteConfidence = &v
	d := Resolve(p, Input{NodeType: "endpoint", Layer: "contract", Role: "helper", RoleConfidence: 0.9, Level: "default"})
	if d.RenderMode != RenderBox {
		t.Fatalf("0.9 < policy threshold 0.95 must gate demotion: got mode=%s trace=%v", d.RenderMode, d.Trace)
	}
}

func TestResolve_UserTierRaiseFloorsMode(t *testing.T) {
	// Raising the tier via user override without pinning render_mode must
	// still surface the node (floor applies to user-raised tiers too).
	tier := TierPrimary
	d := Resolve(basePolicy(), Input{NodeType: "dto", Layer: "data", Level: "default", UserOverride: &Override{Tier: &tier}})
	if d.RenderMode != RenderBox {
		t.Fatalf("user tier raise must floor mode to box: got %s trace=%v", d.RenderMode, d.Trace)
	}
}

func TestNoiseClassForPath(t *testing.T) {
	p := &registry.SaliencePolicy{NoisePaths: map[string][]string{
		"generated": {"generated/", "*.pb.go"},
		"test":      {"__tests__/", "*.spec.*", "*_test.go"},
		"vendor":    {"node_modules/", "vendor/"},
	}}
	cases := []struct{ path, want string }{
		{"src/api/generated/client.ts", "generated"},
		{"proto/orders.pb.go", "generated"},
		{"src/battle/__tests__/battle.ts", "test"},
		{"src/battle/battle.controller.spec.ts", "test"},
		{"graph/salience/salience_test.go", "test"},
		{"node_modules/lodash/index.js", "vendor"},
		{"src/battle/battle.controller.ts", ""},
		{"", ""},
		// A directory pattern must match path segments, not substrings:
		// "vendor/" must not match a file named vendors.ts.
		{"src/vendors.ts", ""},
	}
	for _, tc := range cases {
		if got := NoiseClassForPath(p, tc.path); got != tc.want {
			t.Errorf("NoiseClassForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
	if got := NoiseClassForPath(nil, "node_modules/x.js"); got != "" {
		t.Errorf("nil policy must yield no noise class, got %q", got)
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

func TestResolve_DefaultPolicy_FlowsShown(t *testing.T) {
	r, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	p := r.SaliencePolicy()
	// use_case is a real flow node type in the registry; it must be a box, not hidden.
	for _, nt := range []string{"flow", "use_case", "usecase"} {
		d := Resolve(p, Input{NodeType: nt, Layer: "flow", Level: "default"})
		if d.RenderMode != RenderBox {
			t.Fatalf("flow type %q should be box, got %s trace=%v", nt, d.RenderMode, d.Trace)
		}
	}
}

func TestResolve_DefaultPolicy_ProviderLevelAware(t *testing.T) {
	r, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	p := r.SaliencePolicy()
	// At service-level views (default/c2) a provider is collapsed.
	d := Resolve(p, Input{NodeType: "provider", Layer: "code", Level: "default"})
	if d.RenderMode != RenderCollapsedGroup {
		t.Fatalf("provider at default should collapse, got %s trace=%v", d.RenderMode, d.Trace)
	}
	// Inside a single-service component view (c3) it becomes a first-class box.
	d = Resolve(p, Input{NodeType: "provider", Layer: "code", Level: "c3"})
	if d.RenderMode != RenderBox || d.Tier != TierPrimary {
		t.Fatalf("provider at c3 should be primary/box, got %s/%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
}

func TestResolve_DataLensPromotesModels(t *testing.T) {
	r, err := registry.LoadDefaults()
	if err != nil { t.Fatalf("LoadDefaults: %v", err) }
	p := r.SaliencePolicy()
	// In C2/C3 a model is collapsed background...
	d := Resolve(p, Input{NodeType: "model", Layer: "data", Level: "c3"})
	if d.RenderMode != RenderCollapsedGroup {
		t.Fatalf("model at c3: want collapsed_group, got %s", d.RenderMode)
	}
	// ...but in the Data lens the model is the subject → a primary box.
	d = Resolve(p, Input{NodeType: "model", Layer: "data", Level: "data"})
	if d.RenderMode != RenderBox || d.Tier != TierPrimary {
		t.Fatalf("model in data lens: want primary/box, got %s/%s trace=%v", d.Tier, d.RenderMode, d.Trace)
	}
	// enum: badge by default, but a labeled box in the data lens.
	d = Resolve(p, Input{NodeType: "enum", Layer: "data", Level: "data"})
	if d.RenderMode != RenderBox {
		t.Fatalf("enum in data lens: want box, got %s", d.RenderMode)
	}
}

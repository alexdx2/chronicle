package registry

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Closed vocabularies (spec principle 4). Typos in a user chronicle.types.yaml
// must fail Load loudly instead of silently degrading to hidden.
var (
	validTiers = map[string]bool{"primary": true, "secondary": true, "detail": true}

	validRenderModes = map[string]bool{
		"box": true, "collapsed_group": true, "badge": true,
		"attached_detail": true, "expandable_detail": true, "hidden": true,
	}
)

// Validate checks the salience section against the closed vocabularies.
// Level keys are intentionally NOT validated (the set of diagram levels /
// layout presets is open); policy keys, tiers and render modes are closed.
func (p *SaliencePolicy) Validate() error {
	if p == nil {
		return nil
	}
	for key, byLevel := range p.RenderPolicy {
		if !strings.HasPrefix(key, "type:") && !strings.HasPrefix(key, "role:") {
			return fmt.Errorf("salience: render_policy key %q must be namespaced (type:<layer>.<type> or role:<role>)", key)
		}
		for level, rule := range byLevel {
			if rule.Tier != "" && !validTiers[rule.Tier] {
				return fmt.Errorf("salience: render_policy %q level %q: invalid tier %q (want primary|secondary|detail)", key, level, rule.Tier)
			}
			if rule.RenderMode != "" && !validRenderModes[rule.RenderMode] {
				return fmt.Errorf("salience: render_policy %q level %q: invalid render_mode %q (want box|collapsed_group|badge|attached_detail|expandable_detail|hidden)", key, level, rule.RenderMode)
			}
		}
	}
	for role, rr := range p.Roles {
		if rr.MaxTier != "" && !validTiers[rr.MaxTier] {
			return fmt.Errorf("salience: role %q: invalid max_tier %q (want primary|secondary|detail)", role, rr.MaxTier)
		}
	}
	return nil
}

// RenderRule is one (level-scoped) salience override for a policy key.
// Empty fields mean "this layer does not touch that field".
type RenderRule struct {
	Tier       string `yaml:"tier" json:"tier,omitempty"`               // primary|secondary|detail
	RenderMode string `yaml:"render_mode" json:"render_mode,omitempty"` // box|collapsed_group|badge|attached_detail|expandable_detail|hidden
}

// RoleRule holds the promotion caps for a semantic role.
type RoleRule struct {
	Promotable bool   `yaml:"promotable" json:"promotable"`
	MaxTier    string `yaml:"max_tier" json:"max_tier,omitempty"` // "" = no cap
}

// SaliencePolicy is the additive `salience:` section of the registry.
// RenderPolicy is keyed by namespaced policy key ("type:<layer>.<type>" or
// "role:<role>"), then by diagram level ("default","focus","c3",...).
type SaliencePolicy struct {
	RenderPolicy map[string]map[string]RenderRule `yaml:"render_policy" json:"render_policy"`
	Roles        map[string]RoleRule              `yaml:"roles" json:"roles"`
	NoiseRoles   []string                         `yaml:"noise_roles" json:"noise_roles"`
}

// Rule returns the render rule for a policy key at a given level.
func (p *SaliencePolicy) Rule(key, level string) (RenderRule, bool) {
	if p == nil {
		return RenderRule{}, false
	}
	byLevel, ok := p.RenderPolicy[key]
	if !ok {
		return RenderRule{}, false
	}
	r, ok := byLevel[level]
	return r, ok
}

// Role returns the cap rule for a role.
func (p *SaliencePolicy) Role(role string) (RoleRule, bool) {
	if p == nil {
		return RoleRule{}, false
	}
	r, ok := p.Roles[role]
	return r, ok
}

// IsNoiseRole reports whether a role is flagged as noise (demoted).
func (p *SaliencePolicy) IsNoiseRole(role string) bool {
	if p == nil {
		return false
	}
	for _, r := range p.NoiseRoles {
		if r == role {
			return true
		}
	}
	return false
}

// KnownRoles returns the closed vocabulary of semantic roles the policy knows:
// the union of role: render-policy keys, the roles: caps section, and noise_roles,
// plus the sentinel "unknown". Sorted and deduped. This is the list the scan
// agent must classify into (surfaced via chronicle_schema).
func (p *SaliencePolicy) KnownRoles() []string {
	if p == nil {
		return []string{"unknown"}
	}
	set := map[string]bool{"unknown": true}
	for key := range p.RenderPolicy {
		if strings.HasPrefix(key, "role:") {
			set[strings.TrimPrefix(key, "role:")] = true
		}
	}
	for r := range p.Roles {
		set[r] = true
	}
	for _, r := range p.NoiseRoles {
		set[r] = true
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// SaliencePolicy returns the registry's salience policy (never nil).
func (r *Registry) SaliencePolicy() *SaliencePolicy {
	if r.salience == nil {
		return &SaliencePolicy{}
	}
	return r.salience
}

var (
	defaultSalienceOnce sync.Once
	defaultSalienceVal  *SaliencePolicy
)

// defaultSaliencePolicy parses the built-in defaults.yaml salience section
// once. Parsed directly (not via Load) to avoid recursion with Load's merge.
func defaultSaliencePolicy() *SaliencePolicy {
	defaultSalienceOnce.Do(func() {
		var f RegistryFile
		if err := yaml.Unmarshal(DefaultRegistryYAML, &f); err != nil || f.Salience == nil {
			defaultSalienceVal = &SaliencePolicy{}
			return
		}
		defaultSalienceVal = f.Salience
	})
	return defaultSalienceVal
}

// mergeSalience overlays user onto base at per-key granularity and returns a
// new policy (neither input is mutated). A user chronicle.types.yaml therefore
// only overrides the entries it names; everything else keeps the defaults —
// a types-only project file no longer wipes out salience entirely.
// render_policy: a user key replaces that key's whole level-map.
// roles: per role name. noise_roles: replaced only when explicitly set.
func mergeSalience(base, user *SaliencePolicy) *SaliencePolicy {
	out := &SaliencePolicy{
		RenderPolicy: make(map[string]map[string]RenderRule),
		Roles:        make(map[string]RoleRule),
	}
	if base != nil {
		for k, v := range base.RenderPolicy {
			out.RenderPolicy[k] = v
		}
		for k, v := range base.Roles {
			out.Roles[k] = v
		}
		out.NoiseRoles = append([]string(nil), base.NoiseRoles...)
	}
	if user != nil {
		for k, v := range user.RenderPolicy {
			out.RenderPolicy[k] = v
		}
		for k, v := range user.Roles {
			out.Roles[k] = v
		}
		if user.NoiseRoles != nil {
			out.NoiseRoles = append([]string(nil), user.NoiseRoles...)
		}
	}
	return out
}


package registry

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

// SaliencePolicy returns the registry's salience policy (never nil).
func (r *Registry) SaliencePolicy() *SaliencePolicy {
	if r.salience == nil {
		return &SaliencePolicy{}
	}
	return r.salience
}


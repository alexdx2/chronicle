// Package salience computes view-specific diagram salience deterministically.
// Given a node + view context and a registry SaliencePolicy, Resolve returns a
// Decision: an internal salience tier, the final UI render_mode, and a trace.
//
// Invariant: tier is an INTERNAL bucket used only for promotion/cap math;
// render_mode is the single source of truth for the UI. "hidden" lives only in
// render_mode.
package salience

import (
	"fmt"

	"github.com/alexdx2/chronicle-core/registry"
)

type Tier string

const (
	TierDetail    Tier = "detail"
	TierSecondary Tier = "secondary"
	TierPrimary   Tier = "primary"
)

func tierRank(t Tier) int {
	switch t {
	case TierPrimary:
		return 2
	case TierSecondary:
		return 1
	default:
		return 0
	}
}

type RenderMode string

const (
	RenderHidden           RenderMode = "hidden"
	RenderBadge            RenderMode = "badge"
	RenderAttachedDetail   RenderMode = "attached_detail"
	RenderExpandableDetail RenderMode = "expandable_detail"
	RenderCollapsedGroup   RenderMode = "collapsed_group"
	RenderBox              RenderMode = "box"
)

// Decision is the resolved salience for one node in one view.
type Decision struct {
	Tier       Tier
	RenderMode RenderMode
	Promotable bool
	MaxTier    Tier // "" = no cap
	NoiseClass string
	Trace      []string
}

// Override is a user-supplied pin that beats policy (last layer).
type Override struct {
	Tier       *Tier
	RenderMode *RenderMode
}

// Input is the node + view context for resolution.
type Input struct {
	NodeType string
	Layer    string
	Role     string // winning_role; "" or "unknown" treated as no role
	// RoleConfidence is the winning claim's confidence. <= 0 means "no
	// recorded confidence" (manually set role) and is treated as trusted.
	// Claims below the policy's demote threshold may promote but not demote.
	RoleConfidence   float64
	Level            string // diagram level: default|focus|c3|...
	Lens             string // v1: "" (no-op)
	BoundaryCrossing bool
	NoiseClass       string // "" => none; explicit values are deterministic (not gated)
	UserOverride     *Override
}

func typeKey(layer, nodeType string) string { return "type:" + layer + "." + nodeType }
func roleKey(role string) string            { return "role:" + role }

// Resolve runs the ordered override chain and returns the final Decision.
func Resolve(p *registry.SaliencePolicy, in Input) Decision {
	// Start: safe default (lowest bucket, hidden).
	d := Decision{Tier: TierDetail, RenderMode: RenderHidden}

	// Layer 1: base policy by type.
	applyKey(p, &d, typeKey(in.Layer, in.NodeType), in.Level)

	// Effective claim confidence: <= 0 means "manually set role" => trusted.
	conf := in.RoleConfidence
	if conf <= 0 {
		conf = 1
	}
	threshold := p.DemoteConfidenceThreshold()

	// Layer 2: role override (winning_role). "unknown"/"" => skip.
	// Asymmetric confidence gate: a role claim below the demote threshold may
	// refine visibility UP (promote) but not DOWN — a wrong promotion costs
	// one extra box, a wrong demotion silently hides architecture.
	if in.Role != "" && in.Role != "unknown" {
		preTier, preMode := d.Tier, d.RenderMode
		applyKey(p, &d, roleKey(in.Role), in.Level)
		// Caps come from the roles: section (single source of truth).
		if rr, ok := p.Role(in.Role); ok {
			d.Promotable = rr.Promotable
			d.MaxTier = Tier(rr.MaxTier)
			d.Trace = append(d.Trace, fmt.Sprintf("role:%s -> promotable=%v max_tier=%q", in.Role, rr.Promotable, rr.MaxTier))
		}
		demoted := tierRank(d.Tier) < tierRank(preTier) || renderRank(d.RenderMode) < renderRank(preMode)
		if demoted && conf < threshold {
			d.Tier, d.RenderMode = preTier, preMode
			d.Trace = append(d.Trace, fmt.Sprintf("role:%s demote_gated (confidence %.2f < %.2f) -> tier/mode restored", in.Role, conf, threshold))
		}
	}

	// (A future lens override layer slots between role and promotion; v1 no-op.)
	// Layer 3: bounded topology promotion. Only if the role allows it.
	tierRaised := false
	if in.BoundaryCrossing && d.Promotable {
		target := TierPrimary
		if d.MaxTier != "" && tierRank(d.MaxTier) < tierRank(target) {
			target = d.MaxTier
		}
		if tierRank(target) > tierRank(d.Tier) {
			d.Tier = target
			tierRaised = true
			d.Trace = append(d.Trace, fmt.Sprintf("promotion:boundary -> tier=%s (cap=%q)", target, d.MaxTier))
		} else {
			d.Trace = append(d.Trace, "promotion:noop(already >= target)")
		}
	} else if in.BoundaryCrossing {
		d.Trace = append(d.Trace, "promotion:skipped(not promotable)")
	} else {
		d.Trace = append(d.Trace, "promotion:skipped(no boundary crossing)")
	}

	// Layer 4: noise demotion (generated/test/vendor). Symmetric to promotion.
	// NoiseClass may be supplied explicitly by a caller (deterministic, e.g.
	// path-based detection — never gated), or inferred from a noise role, in
	// which case it is an LLM claim and subject to the same demote gate.
	noiseClass := in.NoiseClass
	noiseFromRole := false
	if noiseClass == "" && in.Role != "" && in.Role != "unknown" && p.IsNoiseRole(in.Role) {
		noiseClass = in.Role
		noiseFromRole = true
	}
	if noiseClass != "" && noiseClass != "none" {
		if noiseFromRole && conf < threshold {
			d.Trace = append(d.Trace, fmt.Sprintf("noise:%s gated (confidence %.2f < %.2f)", noiseClass, conf, threshold))
		} else {
			d.NoiseClass = noiseClass
			d.Tier = TierDetail
			d.RenderMode = RenderHidden
			tierRaised = false
			d.Trace = append(d.Trace, "noise:"+noiseClass+" -> tier=detail mode=hidden")
		}
	} else {
		d.Trace = append(d.Trace, "noise:none")
	}

	// Layer 5: user override — last word.
	pinned := false
	if in.UserOverride != nil {
		if in.UserOverride.Tier != nil {
			if tierRank(*in.UserOverride.Tier) > tierRank(d.Tier) {
				tierRaised = true
			}
			d.Tier = *in.UserOverride.Tier
			d.Trace = append(d.Trace, "user_override:tier="+string(*in.UserOverride.Tier))
		}
		if in.UserOverride.RenderMode != nil {
			d.RenderMode = *in.UserOverride.RenderMode
			pinned = true
			d.Trace = append(d.Trace, "user_override:mode="+string(*in.UserOverride.RenderMode))
		}
	} else {
		d.Trace = append(d.Trace, "user_override:none")
	}

	// Layer 6: reconcile tier -> render_mode floor. The floor exists so a
	// RAISED tier (promotion or user override) isn't visually lost; it must
	// not resurrect nodes whose render_mode was deliberately lowered by a
	// policy layer while their tier stayed put (e.g. role:helper hiding a
	// primary-typed node). Hence: only when the tier was raised, never over
	// a user-pinned mode.
	if !pinned && tierRaised {
		floor := floorMode(d.Tier)
		if renderRank(floor) > renderRank(d.RenderMode) {
			d.Trace = append(d.Trace, fmt.Sprintf("reconcile:floor %s->%s for tier=%s", d.RenderMode, floor, d.Tier))
			d.RenderMode = floor
		}
	}

	return d
}

// floorMode is the minimum render_mode implied by a tier.
func floorMode(t Tier) RenderMode {
	switch t {
	case TierPrimary:
		return RenderBox
	case TierSecondary:
		return RenderCollapsedGroup
	default:
		return RenderHidden
	}
}

// renderRank orders render modes from least to most visible for floor comparison.
func renderRank(m RenderMode) int {
	switch m {
	case RenderBox:
		return 5
	case RenderCollapsedGroup:
		return 4
	case RenderExpandableDetail:
		return 3
	case RenderAttachedDetail:
		return 2
	case RenderBadge:
		return 1
	default: // hidden
		return 0
	}
}

// applyKey merges the "default" rule then the level-specific rule for a policy
// key into d (per-field, last-writer-wins for tier/render_mode).
func applyKey(p *registry.SaliencePolicy, d *Decision, key, level string) {
	if rule, ok := p.Rule(key, "default"); ok {
		mergeRule(d, key+":default", rule)
	}
	if level != "" && level != "default" {
		if rule, ok := p.Rule(key, level); ok {
			mergeRule(d, key+":"+level, rule)
		}
	}
}

// mergeRule applies a RenderRule's non-empty fields onto d and records a trace line.
func mergeRule(d *Decision, src string, rule registry.RenderRule) {
	changed := ""
	if rule.Tier != "" {
		d.Tier = Tier(rule.Tier)
		changed += fmt.Sprintf(" tier=%s", rule.Tier)
	}
	if rule.RenderMode != "" {
		d.RenderMode = RenderMode(rule.RenderMode)
		changed += fmt.Sprintf(" mode=%s", rule.RenderMode)
	}
	if changed != "" {
		d.Trace = append(d.Trace, src+" ->"+changed)
	}
}

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
	NodeType         string
	Layer            string
	Role             string // winning_role; "" or "unknown" treated as no role
	Level            string // diagram level: default|focus|c3|...
	Lens             string // v1: "" (no-op)
	BoundaryCrossing bool
	NoiseClass       string // "" => none
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

	// Layer 2: role override (winning_role). "unknown"/"" => skip.
	if in.Role != "" && in.Role != "unknown" {
		applyKey(p, &d, roleKey(in.Role), in.Level)
		// Caps come from the roles: section (single source of truth).
		if rr, ok := p.Role(in.Role); ok {
			d.Promotable = rr.Promotable
			d.MaxTier = Tier(rr.MaxTier)
			d.Trace = append(d.Trace, fmt.Sprintf("role:%s -> promotable=%v max_tier=%q", in.Role, rr.Promotable, rr.MaxTier))
		}
	}

	// Layer 4: bounded topology promotion. Only if the role allows it.
	if in.BoundaryCrossing && d.Promotable {
		target := TierPrimary
		if d.MaxTier != "" && tierRank(d.MaxTier) < tierRank(target) {
			target = d.MaxTier
		}
		if tierRank(target) > tierRank(d.Tier) {
			d.Tier = target
			d.Trace = append(d.Trace, fmt.Sprintf("promotion:boundary -> tier=%s (cap=%q)", target, d.MaxTier))
		} else {
			d.Trace = append(d.Trace, "promotion:noop(already >= target)")
		}
	} else if in.BoundaryCrossing {
		d.Trace = append(d.Trace, "promotion:skipped(not promotable)")
	} else {
		d.Trace = append(d.Trace, "promotion:skipped(no boundary crossing)")
	}

	return d
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

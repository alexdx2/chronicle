package viewmodel

import (
	"fmt"

	"github.com/alexdx2/chronicle-core/store"
)

// DepthUnbounded is "deep enough" to exhaust any real graph: the expand BFS
// stops as soon as the frontier empties, so the constant only bounds
// pathological cycles while staying compact in encoded view URLs.
const DepthUnbounded = 99

// PresetSpec returns the ViewSpec for a named preset level. C1/C2/C3 are
// presets of the view algebra, not separate engines (design §2), and
// deps/impact are the same algebra pointed at a single node (design §2,
// "deps as views"):
//
//	c1     — scope:domain · group:domain · collapse (the legacy BuildC1 stays
//	         for the artful /api/c4/c1 endpoint)
//	c2     — scope:domain · filter:{edge_kinds:[sync,async]} · group:service · collapse
//	c3     — scope:service · group:module · node-level edges + boundary
//	         (empty target: scope:domain · group:service — components of
//	         EVERY service in one view, no per-service boundary pass)
//	deps   — scope:{nodes:[target]} · expand:{out,∞} · group:service · collapse
//	impact — scope:{nodes:[target]} · expand:{in,∞} · group:service · collapse
//
// Lens presets (the dashboard view bar, replacing the old client-side
// graph-presets.js filtering):
//
//	data     — filter:{layers:[data]} · group:none
//	api      — filter:{layers:[contract,service]} · group:service · no collapse
//	services — filter:{layers:[service,code]} · group:service · collapse
//	flows    — filter:{layers:[flow]} · group:none ("flow" in filter.layers
//	           opts the otherwise-hidden flow layer in; flows render alone,
//	           each carrying its trigger endpoint as VNode.Detail)
//
// target is the service key/name for c3 and the node key for deps/impact.
func PresetSpec(level, domain, target string) ViewSpec {
	switch level {
	case "data":
		return ViewSpec{
			Scope:  ScopeSpec{Domain: domain},
			Filter: &FilterSpec{Layers: []string{"data"}},
			Group:  GroupSpec{By: "none"},
			Layout: LayoutSpec{Preset: "data"},
		}
	case "api":
		return ViewSpec{
			Scope:    ScopeSpec{Domain: domain},
			Filter:   &FilterSpec{Layers: []string{"contract", "service"}},
			Group:    GroupSpec{By: "service"},
			Collapse: false,
			Layout:   LayoutSpec{Preset: "api"},
		}
	case "services":
		return ViewSpec{
			Scope:    ScopeSpec{Domain: domain},
			Filter:   &FilterSpec{Layers: []string{"service", "code"}},
			Group:    GroupSpec{By: "service"},
			Collapse: true,
			Layout:   LayoutSpec{Preset: "services"},
		}
	case "flows":
		// Flows LIST view: flows only — no endpoints, no code targets. Each
		// flow VNode carries its trigger endpoint as Detail ("GET /api/score");
		// dblclick drills into the flow-scoped composition (scope.Flow).
		return ViewSpec{
			Scope:  ScopeSpec{Domain: domain},
			Filter: &FilterSpec{Layers: []string{"flow"}},
			Group:  GroupSpec{By: "none"},
			Layout: LayoutSpec{Preset: "flows"},
		}
	case "c1":
		return ViewSpec{
			Scope:    ScopeSpec{Domain: domain},
			Group:    GroupSpec{By: "domain"},
			Collapse: true,
			Layout:   LayoutSpec{Preset: "c1"},
		}
	case "c3":
		if target == "" {
			// C3 across ALL services: domain-wide component view grouped by
			// service (each service a group frame holding its components).
			// Boundary materialization is per-service only (scope.Service),
			// so cross-service component edges render directly here.
			return ViewSpec{
				Scope:    ScopeSpec{Domain: domain},
				Group:    GroupSpec{By: "service"},
				Collapse: false,
				Layout:   LayoutSpec{Preset: "c3"},
			}
		}
		return ViewSpec{
			Scope:    ScopeSpec{Domain: domain, Service: target},
			Group:    GroupSpec{By: "module"},
			Collapse: false,
			Layout:   LayoutSpec{Preset: "c3"},
		}
	case "deps":
		return ViewSpec{
			Scope:    ScopeSpec{Domain: domain, Nodes: []string{target}},
			Expand:   &ExpandSpec{Direction: "out", Depth: DepthUnbounded},
			Group:    GroupSpec{By: "service"},
			Collapse: true,
			Layout:   LayoutSpec{Preset: "deps"},
		}
	case "impact":
		return ViewSpec{
			Scope:    ScopeSpec{Domain: domain, Nodes: []string{target}},
			Expand:   &ExpandSpec{Direction: "in", Depth: DepthUnbounded},
			Group:    GroupSpec{By: "service"},
			Collapse: true,
			Layout:   LayoutSpec{Preset: "impact"},
		}
	default: // "c2"
		return ViewSpec{
			Scope: ScopeSpec{Domain: domain},
			// Containers level: services + channels only. Data models/enums
			// belong to C3 component views, not the container landscape.
			Filter:   &FilterSpec{EdgeKinds: []string{"sync", "async"}, Layers: []string{"code", "contract", "service"}},
			Group:    GroupSpec{By: "service"},
			Collapse: true,
			Layout:   LayoutSpec{Preset: "c2"},
		}
	}
}

// BuildPreset builds the View for a named preset level via BuildView.
func BuildPreset(st *store.Store, level, domain, target string) (*View, error) {
	v, err := BuildView(st, PresetSpec(level, domain, target))
	if err != nil {
		return nil, err
	}
	switch level {
	case "c1":
		v.Title = fmt.Sprintf("C1 — %s", domain)
	case "c3":
		if target == "" {
			v.Title = fmt.Sprintf("C3 — %s (all services)", domain)
		} else {
			v.Title = fmt.Sprintf("C3 — %s", target)
		}
	case "deps":
		v.Title = fmt.Sprintf("Dependencies — %s", target)
	case "impact":
		v.Title = fmt.Sprintf("Impact — %s", target)
	case "data":
		v.Title = fmt.Sprintf("Data Models — %s", domain)
	case "api":
		v.Title = fmt.Sprintf("API Surface — %s", domain)
	case "services":
		v.Title = fmt.Sprintf("Services — %s", domain)
	case "flows":
		v.Title = fmt.Sprintf("Flows — %s", domain)
	default:
		v.Title = fmt.Sprintf("C2 — %s", domain)
	}
	return v, nil
}

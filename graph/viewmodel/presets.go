package viewmodel

import (
	"fmt"
	"strings"

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
//	c1     — scope:domain · group:domain · collapse
//	c2     — scope:domain · filter:{edge_kinds:[sync,async], layers:[code,contract,service,infra]} · group:service · collapse
//	c3     — scope:service · group:module · node-level edges + boundary
//	         (empty target: scope:domain · group:service — components of
//	         EVERY service in one view, no per-service boundary pass)
//	deps   — scope:{nodes:[target]} · expand:{out,∞} · group:service · collapse
//	impact — scope:{nodes:[target]} · expand:{in,∞} · group:service · collapse
//	path   — scope:{nodes:[A,B]} · expand:{mode:path} · group:none (target "A,B")
//	flow   — scope:{flow:target} · group:none (the flow's composition)
//
// Lens presets (the dashboard view bar):
//
//	data     — filter:{layers:[data]} · group:service (owner; "owner unknown" frame)
//	api      — filter:{layers:[contract], exclude_types:[topic], callers:[CALLS_ENDPOINT]}
//	           · group:service (endpoints by provider, their callers by their
//	           own service; topics belong to the C2 broker frame)
//	services — filter:{layers:[service,code]} · group:service · collapse
//	flows    — filter:{layers:[flow]} · group:none · View.Table (a list:
//	           service → endpoint → handler → flow; "flow" in filter.layers
//	           opts the otherwise-hidden flow layer in)
//
// target is the service key/name for c3 and the node key for deps/impact.
// Presets lists every named preset PresetSpec accepts, in menu order.
var Presets = []string{"c1", "c2", "c3", "deps", "impact", "path", "flow", "data", "api", "services", "flows"}

// IsPreset reports whether level names a known preset.
func IsPreset(level string) bool {
	for _, p := range Presets {
		if p == level {
			return true
		}
	}
	return false
}

func PresetSpec(level, domain, target string) ViewSpec {
	switch level {
	case "path":
		// target = "A,B" (two node keys); the shortest path between them.
		var nodes []string
		for _, k := range strings.Split(target, ",") {
			if k = strings.TrimSpace(k); k != "" {
				nodes = append(nodes, k)
			}
		}
		return ViewSpec{
			Scope:  ScopeSpec{Domain: domain, Nodes: nodes},
			Expand: &ExpandSpec{Mode: "path"},
			Group:  GroupSpec{By: "none"},
			Layout: LayoutSpec{Preset: "path"},
		}
	case "flow":
		// target = flow node key; the flow's composition (trigger + REQUIRES).
		return ViewSpec{
			Scope:  ScopeSpec{Domain: domain, Flow: target},
			Group:  GroupSpec{By: "none"},
			Layout: LayoutSpec{Preset: "flow"},
		}
	case "data":
		// Models framed by the service that owns them (schema path, the
		// one component that uses them, their referrers); the rest in an
		// explicit "owner unknown" frame. Arrows: model references.
		return ViewSpec{
			Scope:  ScopeSpec{Domain: domain},
			Filter: &FilterSpec{Layers: []string{"data"}},
			Group:  GroupSpec{By: "service"},
			Layout: LayoutSpec{Preset: "data"},
		}
	case "api":
		// Who provides each endpoint (its service frames it) and who calls
		// it (the calling components, framed by THEIR service, with the
		// CALLS_ENDPOINT arrows). Nothing else from the code layer.
		return ViewSpec{
			Scope:    ScopeSpec{Domain: domain},
			Filter:   &FilterSpec{Layers: []string{"contract"}, ExcludeTypes: []string{"topic"}, Callers: []string{"CALLS_ENDPOINT"}, EdgeKinds: []string{"CALLS_ENDPOINT"}},
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
		// Flows are a LIST, not a picture: View.Table holds one row per
		// flow (service → endpoint → handler → flow); a row drills into the
		// flow-scoped composition (scope.Flow). Flow VNodes ride along
		// (provenance, hidden summary) and still carry the trigger as Detail.
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
			Scope:  ScopeSpec{Domain: domain, Nodes: []string{target}},
			Expand: &ExpandSpec{Direction: "in", Depth: DepthUnbounded},
			Group:  GroupSpec{By: "service"},
			// Expanded by default: the whole point of impact is WHICH
			// components break — collapsing them into service boxes hides
			// the answer behind a far-away checkbox.
			Collapse: false,
			Layout:   LayoutSpec{Preset: "impact"},
		}
	default: // "c2"
		return ViewSpec{
			Scope: ScopeSpec{Domain: domain},
			// Containers level: services + channels only. Data models/enums
			// belong to C3 component views, not the container landscape.
			// infra is in: brokers frame their topics, and isolated infra
			// (nothing touches it at this altitude) hides itself with a reason.
			Filter:   &FilterSpec{EdgeKinds: []string{"sync", "async"}, Layers: []string{"code", "contract", "service", "infra"}},
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
	case "path":
		if v.Title == "" {
			v.Title = fmt.Sprintf("Path — %s", target)
		}
	case "flow":
		if v.Title == "" {
			v.Title = fmt.Sprintf("Flow — %s", target)
		}
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

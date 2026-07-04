package viewmodel

// ViewSpec is the declarative "view algebra" specification: any diagram is
// scope + (optional) expand + (optional) filter + group + collapse + layout.
// See docs/superpowers/specs/2026-06-10-view-algebra-design.md §2.
type ViewSpec struct {
	Scope    ScopeSpec   `json:"scope"`
	Expand   *ExpandSpec `json:"expand,omitempty"`
	Filter   *FilterSpec `json:"filter,omitempty"`
	Group    GroupSpec   `json:"group"`
	Collapse bool        `json:"collapse"`
	Layout   LayoutSpec  `json:"layout"`
	// Pin holds node keys dropped onto an existing view (palette drag / "+").
	// Pinned nodes join the view unconditionally: they bypass node filters
	// and endpoint absorption, always render as VNodes, and bring their
	// connections (edges to in-view nodes plus 1-hop out-of-view neighbors
	// materialized as dimmed Boundary nodes).
	Pin []string `json:"pin,omitempty"`
	// SalienceOverrides pins per-node salience (keyed by node key) — the
	// user's last word in the resolve chain. Session/ViewSpec-scoped by
	// design: user decisions do not pollute the graph.
	SalienceOverrides map[string]SalienceOverrideSpec `json:"salience_overrides,omitempty"`
}

// SalienceOverrideSpec pins a node's tier and/or render_mode. Values follow
// the closed salience vocabularies; empty fields are left to policy.
type SalienceOverrideSpec struct {
	Tier       string `json:"tier,omitempty"`
	RenderMode string `json:"render_mode,omitempty"`
}

// ScopeSpec selects the seed set of nodes. Domain is always required in V1;
// Service/Flow/Nodes narrow the seed set within the domain.
// Precedence when several are set: Flow > Nodes > Service > whole domain.
type ScopeSpec struct {
	Domain  string   `json:"domain,omitempty"`
	Service string   `json:"service,omitempty"`
	Flow    string   `json:"flow,omitempty"`
	Nodes   []string `json:"nodes,omitempty"`
}

// ExpandSpec grows the seed set by BFS over dependency edges.
// Direction is out|in|both; Depth 0 means no expansion; Edges is an allowlist
// of edge types or semantic classes (empty = all non-hierarchy kinds);
// Mode is neighbors|path. Mode "path" requires scope.nodes=[A,B] and finds
// the shortest undirected path between them (View.Trace holds the order);
// Direction/Depth are ignored in path mode.
type ExpandSpec struct {
	Direction string   `json:"direction,omitempty"`
	Depth     int      `json:"depth,omitempty"`
	Edges     []string `json:"edges,omitempty"`
	Mode      string   `json:"mode,omitempty"`
}

// FilterSpec holds node predicates (Layers, ExcludeTypes, Name glob,
// MinConfidence) and edge predicates (EdgeKinds semantic classes
// sync|async|data|hierarchy or raw edge types; Transports). Filters run
// BEFORE grouping/collapse (design §5.4); dropping a node drops its edges.
type FilterSpec struct {
	Layers        []string `json:"layers,omitempty"`
	EdgeKinds     []string `json:"edge_kinds,omitempty"`
	Transports    []string `json:"transports,omitempty"`
	ExcludeTypes  []string `json:"exclude_types,omitempty"`
	MinConfidence float64  `json:"min_confidence,omitempty"`
	Name          string   `json:"name,omitempty"`
	// BypassTypes/BypassLayers are removed-with-bypass selectors (the "via"
	// tristate of the dashboard Filters panel): a node whose type is in
	// BypassTypes or whose layer is in BypassLayers is hidden, but
	// connectivity THROUGH it is preserved — surviving in-neighbors are
	// connected to surviving out-neighbors reachable through bypass-removed
	// nodes only, via synthesized "via <node>" edges. The bypass check runs
	// BEFORE the plain node filters above, so a bypassed type keeps its via
	// semantics even when its layer is absent from Layers. Nodes removed by
	// Layers-absence/ExcludeTypes/Name/MinConfidence stay plain-hidden: they
	// sever paths.
	BypassTypes  []string `json:"bypass_types,omitempty"`
	BypassLayers []string `json:"bypass_layers,omitempty"`
}

// GroupSpec selects the grouping key: service|module|layer|domain|none.
type GroupSpec struct {
	By string `json:"by"`
}

// LayoutSpec is only a rendering hint (e.g. "c1", "c2", "c3", "flow").
type LayoutSpec struct {
	Preset string `json:"preset,omitempty"`
}

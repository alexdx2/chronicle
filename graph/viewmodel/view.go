package viewmodel

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/graph/salience"
	"github.com/alexdx2/chronicle-core/store"
)

// ---------- View: the unified response of the view algebra ----------

// View is the single response shape for any ViewSpec. Groups are frames,
// Nodes are cards, Edges are arrows; Boundary lists connections crossing the
// scope. The spec is echoed back so views are self-describing (shareable
// URLs, back-navigation).
type View struct {
	Spec     ViewSpec   `json:"spec"`
	Title    string     `json:"title,omitempty"`
	Groups   []VGroup   `json:"groups"`
	Nodes    []VNode    `json:"nodes"`
	Edges    []VEdge    `json:"edges"`
	Boundary *VBoundary `json:"boundary,omitempty"`
	// Trace is the ordered node-key sequence of a path view
	// (expand.mode="path"): scope.nodes[0] … scope.nodes[1]. Empty when no
	// path exists (the view then holds just the two seeds) or for non-path
	// views.
	Trace   []string `json:"trace,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

// VGroup is a grouping frame (service, module, layer or domain).
type VGroup struct {
	Key   string         `json:"key"`
	Name  string         `json:"name"`
	Kind  string         `json:"kind"`
	Stats map[string]int `json:"stats"`
}

// VNode is a node card. Endpoint pills come from EXPOSES_ENDPOINT edges and
// model pills from USES_MODEL edges (hierarchy is rendered as decoration,
// never as arrows).
type VNode struct {
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Layer      string     `json:"layer"`
	Group      string     `json:"group"`
	Endpoints  []Endpoint `json:"endpoints,omitempty"`
	UsesModels []string   `json:"uses_models,omitempty"`
	// Detail is a short context label rendered as the node subtitle. Flow
	// nodes carry their TRIGGERS_FLOW source endpoint name here
	// (e.g. "GET /api/score") so the flows LIST view stays meaningful
	// without materializing the trigger endpoints as nodes.
	Detail string `json:"detail,omitempty"`
	// Tier and RenderMode are view-specific salience annotations (registry-
	// driven, resolved at the view's preset level). RenderMode is the UI source
	// of truth (box|collapsed_group|badge|attached_detail|expandable_detail|
	// hidden); Tier is diagnostic. Renderers style/hide nodes accordingly.
	Tier       string `json:"tier,omitempty"`
	RenderMode string `json:"render_mode,omitempty"`
	// Boundary marks a materialized 1-hop boundary target of a
	// service-scoped view (C3): the node sits OUTSIDE the scope but is
	// included so in-view components whose only edges cross the boundary
	// don't render as orphans. Renderers draw boundary nodes dimmed.
	Boundary bool `json:"boundary,omitempty"`
}

// VEdge is a rendered arrow. With collapse=true, From/To are group keys (or
// node keys for ungrouped free-standing nodes), Weight counts the component
// edges and CollapsedFrom holds their edge keys for drill-in.
type VEdge struct {
	From          string   `json:"from"`
	To            string   `json:"to"`
	Kind          string   `json:"kind"`
	Transport     string   `json:"transport,omitempty"`
	Label         string   `json:"label,omitempty"`
	Weight        int      `json:"weight"`
	CollapsedFrom []string `json:"collapsed_from,omitempty"`
}

// VBoundary lists edges crossing the view boundary, reusing the C3
// BoundaryIn/BoundaryOut semantics.
type VBoundary struct {
	In  []BoundaryIn  `json:"in"`
	Out []BoundaryOut `json:"out"`
}

// ---------- edge taxonomy ----------

// edgeClasses are the semantic classes accepted in FilterSpec.EdgeKinds and
// ExpandSpec.Edges. Raw edge type names are accepted as well.
var edgeClasses = map[string][]string{
	"sync":      {"CALLS_SERVICE", "CALLS_ENDPOINT", "INJECTS"},
	"async":     {"PUBLISHES_TOPIC", "CONSUMES_TOPIC"},
	"data":      {"USES_MODEL", "REFERENCES_MODEL"},
	"hierarchy": {"CONTAINS", "EXPOSES_ENDPOINT"},
}

// hierarchyKinds are never traversed or rendered as dependency arrows: they
// feed grouping (CONTAINS) and endpoint pills (EXPOSES_ENDPOINT).
var hierarchyKinds = map[string]bool{"CONTAINS": true, "EXPOSES_ENDPOINT": true}

// flowKinds are skipped unless the flow layer is visible: a flow-scoped view
// (scope.Flow) or an explicit filter.layers entry containing "flow" (the
// Flows preset) opts them in.
var flowKinds = map[string]bool{"TRIGGERS_FLOW": true, "REQUIRES": true}

// expandEdgeKinds expands classes/raw types to a set of raw edge types.
// Returns nil for an empty input (meaning "no restriction").
func expandEdgeKinds(kinds []string) map[string]bool {
	if len(kinds) == 0 {
		return nil
	}
	out := make(map[string]bool)
	for _, k := range kinds {
		if types, ok := edgeClasses[strings.ToLower(k)]; ok {
			for _, t := range types {
				out[t] = true
			}
			continue
		}
		out[strings.ToUpper(k)] = true
	}
	return out
}

// brokerTransports are matched by the "broker" transport class.
var brokerTransports = map[string]bool{
	"kafka": true, "queue": true, "rabbitmq": true, "amqp": true,
	"nats": true, "sqs": true, "sns": true, "redis": true,
}

func transportMatches(wanted []string, transport string) bool {
	for _, w := range wanted {
		w = strings.ToLower(w)
		if w == transport {
			return true
		}
		if w == "broker" && brokerTransports[transport] {
			return true
		}
	}
	return false
}

// edgeTransport derives the transport of an edge: explicit metadata wins,
// then the edge type implies it (topic edges use topicTransport of the topic
// node — same fallback chain as the legacy C2 builder).
func edgeTransport(e *store.EdgeRow, nodeByID map[int64]*store.NodeRow) string {
	if e.Metadata != "" && e.Metadata != "{}" {
		var meta map[string]any
		if json.Unmarshal([]byte(e.Metadata), &meta) == nil {
			if t, ok := meta["transport"].(string); ok && t != "" {
				return strings.ToLower(t)
			}
		}
	}
	switch e.EdgeType {
	case "CALLS_SERVICE", "CALLS_ENDPOINT":
		return "http"
	case "PUBLISHES_TOPIC", "CONSUMES_TOPIC":
		if tn := nodeByID[e.ToNodeID]; tn != nil && tn.NodeType == "topic" {
			return topicTransport(*tn)
		}
		if fn := nodeByID[e.FromNodeID]; fn != nil && fn.NodeType == "topic" {
			return topicTransport(*fn)
		}
		return "broker"
	case "INJECTS", "USES_MODEL", "REFERENCES_MODEL", "CONTAINS", "EXPOSES_ENDPOINT":
		return "local"
	}
	return ""
}

func globMatch(pattern, name string) bool {
	ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(name))
	return err == nil && ok
}

// ---------- BuildView ----------

// BuildView evaluates a ViewSpec over one domain's active nodes and edges.
// Pipeline: load → scope seeds → expand → filter → group → collapse →
// boundary. Pure in-memory; the only store access is the two list calls.
func BuildView(st *store.Store, spec ViewSpec) (*View, error) {
	domain := spec.Scope.Domain
	if domain == "" {
		return nil, fmt.Errorf("view spec: scope.domain is required in V1")
	}
	pathMode := false
	if spec.Expand != nil {
		switch spec.Expand.Mode {
		case "", "neighbors":
		case "path":
			pathMode = true
			if len(spec.Scope.Nodes) != 2 {
				return nil, fmt.Errorf("view spec: expand.mode \"path\" requires scope.nodes with exactly two keys")
			}
			if spec.Scope.Flow != "" {
				return nil, fmt.Errorf("view spec: expand.mode \"path\" cannot combine with scope.flow")
			}
		default:
			return nil, fmt.Errorf("view spec: expand.mode %q not supported (only \"neighbors\" and \"path\")", spec.Expand.Mode)
		}
	}
	flowScoped := spec.Scope.Flow != ""
	// The flow layer is hidden by default; a flow-scoped view or an explicit
	// filter.layers entry containing "flow" opts it in (the "Flows" preset).
	flowVisible := flowScoped
	if spec.Filter != nil {
		for _, l := range spec.Filter.Layers {
			if l == "flow" {
				flowVisible = true
			}
		}
	}
	groupBy := spec.Group.By
	if groupBy == "" {
		groupBy = "none"
	}

	// ---- a. load: active domain nodes + active edges within the domain ----
	allNodes, err := st.ListNodes(store.NodeFilter{Domain: domain})
	if err != nil {
		return nil, err
	}
	// Stale nodes stay renderable: stale = "awaiting re-verification", the
	// architecture still references them (e.g. shared infra at C1). Only
	// deleted/rejected nodes are dropped.
	nodes := make([]store.NodeRow, 0, len(allNodes))
	for _, n := range allNodes {
		if n.Status == "active" || n.Status == "stale" {
			nodes = append(nodes, n)
		}
	}

	allEdges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		return nil, err
	}
	active := activeEdges(allEdges)

	nodeByID := make(map[int64]*store.NodeRow, len(nodes))
	nodeByKey := make(map[string]*store.NodeRow, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		if !flowVisible && n.Layer == "flow" {
			continue // flow layer hidden unless flow-scoped or filter opts it in
		}
		nodeByID[n.NodeID] = n
		nodeByKey[n.NodeKey] = n
	}

	// Domain edges: both endpoints must resolve to loaded domain nodes
	// (equivalent to from_node_key containing ":"+domain+":" for V1
	// single-domain stores). Flow edges skipped unless flow-scoped.
	domEdges := make([]store.EdgeRow, 0, len(active))
	for _, e := range active {
		if flowKinds[e.EdgeType] && !flowVisible {
			continue
		}
		if nodeByID[e.FromNodeID] == nil || nodeByID[e.ToNodeID] == nil {
			continue
		}
		domEdges = append(domEdges, e)
	}

	owned := ownershipMap(nodes)

	// Hierarchy indices: endpoint → exposing component, node → parent module.
	exposerOf := make(map[int64]int64)
	parentModule := make(map[int64]int64)
	for i := range domEdges {
		e := &domEdges[i]
		switch e.EdgeType {
		case "EXPOSES_ENDPOINT":
			exposerOf[e.ToNodeID] = e.FromNodeID
		case "CONTAINS":
			if fn := nodeByID[e.FromNodeID]; fn != nil && fn.NodeType == "module" {
				parentModule[e.ToNodeID] = e.FromNodeID
			}
		}
	}

	// Pills (decoration from hierarchy/data edges, like the legacy C3).
	endpointPills := make(map[int64][]Endpoint)
	modelPills := make(map[int64][]string)
	for i := range domEdges {
		e := &domEdges[i]
		switch e.EdgeType {
		case "EXPOSES_ENDPOINT":
			if tn := nodeByID[e.ToNodeID]; tn != nil {
				endpointPills[e.FromNodeID] = append(endpointPills[e.FromNodeID], Endpoint{Key: tn.NodeKey, Name: tn.Name})
			}
		case "USES_MODEL":
			if tn := nodeByID[e.ToNodeID]; tn != nil {
				modelPills[e.FromNodeID] = append(modelPills[e.FromNodeID], tn.Name)
			}
		}
	}

	// Flow trigger labels: each flow node carries its TRIGGERS_FLOW source
	// endpoint name as VNode.Detail ("GET /api/score") — the flows LIST view
	// renders flows alone, so the trigger rides along as a subtitle instead
	// of a materialized endpoint node.
	flowTrigger := make(map[int64]string)
	if flowVisible {
		for i := range domEdges {
			e := &domEdges[i]
			if e.EdgeType != "TRIGGERS_FLOW" {
				continue
			}
			if fn := nodeByID[e.FromNodeID]; fn != nil && flowTrigger[e.ToNodeID] == "" {
				flowTrigger[e.ToNodeID] = fn.Name
			}
		}
	}

	// ---- b. scope: seed set ----
	view := &View{Spec: spec}
	seeds := make(map[int64]bool)
	flowTitle := ""
	switch {
	case flowScoped:
		var flowNode *store.NodeRow
		for _, n := range nodeByID {
			if n.Layer == "flow" && (n.NodeKey == spec.Scope.Flow || n.Name == spec.Scope.Flow || n.QualifiedName == spec.Scope.Flow) {
				flowNode = n
				break
			}
		}
		if flowNode == nil {
			return nil, notFoundf("flow %q not found", spec.Scope.Flow)
		}
		flowTitle = flowNode.Name
		seeds[flowNode.NodeID] = true
		for i := range domEdges {
			e := &domEdges[i]
			if e.EdgeType == "REQUIRES" && e.FromNodeID == flowNode.NodeID {
				seeds[e.ToNodeID] = true // flow dependencies
			}
			if e.EdgeType == "TRIGGERS_FLOW" && e.ToNodeID == flowNode.NodeID {
				seeds[e.FromNodeID] = true // trigger endpoint
			}
		}
	case spec.Scope.Nodes != nil:
		// Nodes PRESENT but empty ([]) is a deliberate blank canvas (the
		// dashboard "New canvas" Custom view): render an empty view instead
		// of falling back to the whole domain.
		for _, k := range spec.Scope.Nodes {
			if n := nodeByKey[k]; n != nil {
				seeds[n.NodeID] = true
			} else {
				view.Missing = append(view.Missing, k)
			}
		}
	case spec.Scope.Service != "":
		// flattenIdent: lowercase alnum-only comparison form.
		flattenIdent := func(s string) string {
			var b strings.Builder
			for _, r := range strings.ToLower(s) {
				if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
					b.WriteRune(r)
				}
			}
			return b.String()
		}
		// Tolerant matching: users address services by dir name ("scoreboard-api")
		// while the node may carry the assembly name ("ScoreboardApi") — fall back
		// to flattened (lowercase alnum) comparison.
		want := flattenIdent(spec.Scope.Service)
		var svc *store.NodeRow
		for _, n := range nodeByID {
			if n.Layer != "service" || n.NodeType != "service" {
				continue
			}
			if n.NodeKey == spec.Scope.Service || n.Name == spec.Scope.Service ||
				flattenIdent(n.Name) == want {
				svc = n
				break
			}
		}
		if svc == nil {
			return nil, notFoundf("service %q not found", spec.Scope.Service)
		}
		seeds[svc.NodeID] = true
		for nid, owner := range owned {
			if owner.NodeID == svc.NodeID {
				seeds[nid] = true
			}
		}
	default: // whole domain
		for id := range nodeByID {
			seeds[id] = true
		}
	}

	// ---- c. expand: BFS over dependency edges ----
	inView := make(map[int64]bool, len(seeds))
	for id := range seeds {
		inView[id] = true
	}
	// pathPairs holds the unordered consecutive node-id pairs of the found
	// path: in path mode only edges along the path are rendered.
	var pathPairs map[[2]int64]bool
	if pathMode {
		var allow map[string]bool
		if spec.Expand != nil {
			allow = expandEdgeKinds(spec.Expand.Edges)
		}
		a := nodeByKey[spec.Scope.Nodes[0]]
		b := nodeByKey[spec.Scope.Nodes[1]]
		if a != nil && b != nil {
			order, pairs := shortestPath(a.NodeID, b.NodeID, domEdges, nodeByID, allow)
			for _, id := range order {
				inView[id] = true
				view.Trace = append(view.Trace, nodeByID[id].NodeKey)
			}
			pathPairs = pairs
		}
		// No path (or unresolved seed): the view holds just the seeds with an
		// empty Trace — deliberately not an error.
	} else if ex := spec.Expand; ex != nil && ex.Depth > 0 {
		allow := expandEdgeKinds(ex.Edges)
		dir := ex.Direction
		if dir == "" {
			dir = "out"
		}
		frontier := seeds
		for d := 0; d < ex.Depth && len(frontier) > 0; d++ {
			next := make(map[int64]bool)
			for i := range domEdges {
				e := &domEdges[i]
				if hierarchyKinds[e.EdgeType] {
					continue // hierarchy is never traversed as a dependency
				}
				if allow != nil && !allow[e.EdgeType] {
					continue
				}
				if (dir == "out" || dir == "both") && frontier[e.FromNodeID] && !inView[e.ToNodeID] {
					next[e.ToNodeID] = true
				}
				if (dir == "in" || dir == "both") && frontier[e.ToNodeID] && !inView[e.FromNodeID] {
					next[e.FromNodeID] = true
				}
			}
			for id := range next {
				inView[id] = true
			}
			frontier = next
		}
	}

	// ---- d. filter: node predicates (before grouping, design §5.4) ----
	f := spec.Filter
	nodePasses := func(n *store.NodeRow) bool {
		if f == nil {
			return true
		}
		if len(f.Layers) > 0 {
			ok := false
			for _, l := range f.Layers {
				if n.Layer == l {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
		for _, t := range f.ExcludeTypes {
			if n.NodeType == t {
				return false
			}
		}
		if f.MinConfidence > 0 && n.Confidence < f.MinConfidence {
			return false
		}
		if f.Name != "" && !globMatch(f.Name, n.Name) {
			return false
		}
		return true
	}
	// Bypass selectors (filter.bypass_types / filter.bypass_layers): matching
	// nodes are removed-with-bypass — hidden, but the connectivity pass below
	// routes around exactly these. The check runs BEFORE the plain filters so
	// a bypassed type keeps via semantics even when its layer is filtered out.
	bypassTypes := make(map[string]bool)
	bypassLayers := make(map[string]bool)
	if f != nil {
		for _, t := range f.BypassTypes {
			bypassTypes[t] = true
		}
		for _, l := range f.BypassLayers {
			bypassLayers[l] = true
		}
	}
	// removedBypass: nodes cut by the bypass selectors. Nodes cut by the
	// plain filters (nodePasses) are simply dropped — they sever paths.
	removedBypass := make(map[int64]bool)
	for id := range inView {
		n := nodeByID[id]
		if bypassTypes[n.NodeType] || bypassLayers[n.Layer] {
			removedBypass[id] = true
			delete(inView, id)
			continue
		}
		if !nodePasses(n) {
			delete(inView, id)
		}
	}

	// Pinned keys (palette drops onto an existing view) join the view
	// unconditionally: they bypass node filters and endpoint absorption and
	// always render as VNodes.
	pinned := make(map[int64]bool)
	for _, k := range spec.Pin {
		n := nodeByKey[k]
		if n == nil {
			view.Missing = append(view.Missing, k)
			continue
		}
		pinned[n.NodeID] = true
		inView[n.NodeID] = true
		delete(removedBypass, n.NodeID)
	}
	// Hand-selected nodes of a custom view (scope.nodes without expand) are
	// focus nodes too: dropping an entity must show its links — the selected
	// node gets the same neighborhood materialization and absorption bypass
	// as a pin. Expand-driven views (deps/impact/path) stay untouched: their
	// neighborhood IS the expansion.
	if len(spec.Scope.Nodes) > 0 && spec.Expand == nil {
		for id := range seeds {
			pinned[id] = true
		}
	}

	edgeKindAllow := map[string]bool(nil)
	if f != nil {
		edgeKindAllow = expandEdgeKinds(f.EdgeKinds)
	}
	edgePasses := func(e *store.EdgeRow) bool {
		if f == nil {
			return true
		}
		if edgeKindAllow != nil && !edgeKindAllow[e.EdgeType] {
			return false
		}
		if len(f.Transports) > 0 && !transportMatches(f.Transports, edgeTransport(e, nodeByID)) {
			return false
		}
		return true
	}

	// Absorbed endpoints: an endpoint whose exposing component is in view is
	// rendered as an Endpoint pill on that component (never a VNode); edges
	// touching it are retargeted to the exposer. Path mode skips absorption:
	// every Trace node must render as a real VNode.
	attached := make(map[int64]int64) // endpoint id → exposer id (in view)
	if !pathMode {
		for epID, exID := range exposerOf {
			if pinned[epID] {
				continue // pinned endpoints render as real VNodes, never pills
			}
			if inView[exID] {
				attached[epID] = exID
				delete(inView, epID)
			}
		}
	}
	retarget := func(id int64) int64 {
		if ex, ok := attached[id]; ok {
			return ex
		}
		return id
	}

	// ---- e. group assignment ----
	// Pinned service spotlight (carve-out): when grouping by domain (C1) a
	// pinned service node must not dissolve into the domain box as an orphan
	// card — it is carved out as its OWN group frame (like the C2 frame for
	// that one service): its owned code nodes leave the domain box and join
	// the carved VGroup, so edges lift naturally between the carved service
	// group, the box, topics absorbed in the box, infra and externals. The
	// domain box stats shrink accordingly.
	carvedService := make(map[int64]bool)
	if groupBy == "domain" {
		for id := range pinned {
			if !inView[id] {
				continue
			}
			if n := nodeByID[id]; n != nil && n.Layer == "service" && n.NodeType == "service" {
				carvedService[id] = true
			}
		}
	}
	// structuralKind: "group" → the node IS a grouping frame (VGroup, never a
	// VNode); "hidden" → suppressed (e.g. the service node itself when
	// grouping by module: it is the scope boundary, not a component).
	structuralKind := func(n *store.NodeRow) string {
		isSvc := n.Layer == "service" && n.NodeType == "service"
		switch groupBy {
		case "service":
			if isSvc {
				return "group"
			}
		case "module":
			if n.NodeType == "module" {
				return "group"
			}
			if isSvc {
				return "hidden"
			}
		case "domain":
			if carvedService[n.NodeID] {
				return "group" // pinned service carved out of the domain box
			}
		}
		return ""
	}
	groupOf := func(n *store.NodeRow) string {
		switch groupBy {
		case "service":
			if svc := owned[n.NodeID]; svc != nil {
				return svc.NodeKey
			}
		case "module":
			if mid, ok := parentModule[n.NodeID]; ok {
				if mn := nodeByID[mid]; mn != nil {
					return mn.NodeKey
				}
			}
			if svc := owned[n.NodeID]; svc != nil {
				return svc.NodeKey // fallback: owning service
			}
		case "layer":
			return n.Layer
		case "domain":
			// C1-style: external systems and shared infra stay outside the
			// system box; in-domain topics are internal channels — absorbed
			// into the box (they must not leak into the context diagram).
			if n.NodeType == "external_system" || n.Layer == "infra" {
				return ""
			}
			// Members of a carved (pinned) service join its spotlight group
			// instead of the domain box.
			if svc := owned[n.NodeID]; svc != nil && carvedService[svc.NodeID] {
				return svc.NodeKey
			}
			return domain
		}
		return ""
	}

	// Subjects of expand-driven views (the deps/impact root, path endpoints)
	// must stay visible in collapsed views — answering "deps of X" with a
	// service box that swallows X reads as the wrong answer.
	focusFree := make(map[int64]bool)
	if spec.Expand != nil {
		for id := range seeds {
			focusFree[id] = true
		}
	}

	memberGroup := make(map[int64]string) // regular in-view nodes → group key ("" = free-standing)
	for id := range inView {
		n := nodeByID[id]
		if structuralKind(n) != "" {
			continue
		}
		g := groupOf(n)
		// Pinned members stay free-standing in collapsed views: their group
		// renders as a collapsed card, so nesting the pin inside would turn
		// the card into a frame and dangle the group-keyed edges.
		if (pinned[id] || focusFree[id]) && spec.Collapse {
			g = ""
		}
		memberGroup[id] = g
	}

	groupsByKey := make(map[string]*VGroup)
	addGroup := func(key, name, kind string) {
		if groupsByKey[key] == nil {
			groupsByKey[key] = &VGroup{Key: key, Name: name, Kind: kind, Stats: map[string]int{}}
		}
	}
	for id := range inView {
		n := nodeByID[id]
		if structuralKind(n) == "group" {
			addGroup(n.NodeKey, n.Name, n.NodeType)
		}
	}
	for _, g := range memberGroup {
		if g == "" || groupsByKey[g] != nil {
			continue
		}
		if gn := nodeByKey[g]; gn != nil {
			addGroup(g, gn.Name, gn.NodeType)
		} else {
			addGroup(g, g, groupBy)
		}
	}
	for id, g := range memberGroup {
		if g == "" {
			continue
		}
		vg := groupsByKey[g]
		vg.Stats["nodes"]++
		vg.Stats["endpoints"] += len(endpointPills[id])
		vg.Stats["models"] += len(modelPills[id])
		if n := nodeByID[id]; n.Layer == "service" && n.NodeType == "service" {
			vg.Stats["services"]++ // C1 system-box label: "N services · M endpoints"
		}
	}

	// repKey: the rendered endpoint of a collapsed edge — group key for
	// grouped members and for group nodes themselves, node key otherwise.
	repKey := func(id int64) string {
		n := nodeByID[id]
		if pinned[id] || focusFree[id] {
			return n.NodeKey // pinned/focus nodes always render themselves
		}
		if structuralKind(n) == "group" {
			return n.NodeKey
		}
		if g := memberGroup[id]; g != "" {
			return g
		}
		return n.NodeKey
	}

	// ---- f. edges (+ collapse) and g. boundary ----
	type edgeKeyT struct{ from, to, kind, transport string }
	edgeAcc := make(map[edgeKeyT]*VEdge)
	var edgeOrder []edgeKeyT

	type outKeyT struct{ toName, kind string }
	type inKeyT struct{ fromName, kind string }
	outSeen := make(map[outKeyT]bool)
	inSeen := make(map[inKeyT]bool)
	var bOut []BoundaryOut
	var bIn []BoundaryIn

	addOut := func(toName, toKind, via, kind string) {
		k := outKeyT{toName, kind}
		if outSeen[k] {
			return
		}
		outSeen[k] = true
		bOut = append(bOut, BoundaryOut{ToName: toName, ToKind: toKind, Via: via, Kind: kind})
	}
	addIn := func(fromName, kind string) {
		k := inKeyT{fromName, kind}
		if inSeen[k] {
			return
		}
		inSeen[k] = true
		bIn = append(bIn, BoundaryIn{FromName: fromName, Kind: kind})
	}

	// explicitlyRemoved: the user explicitly removed this node type via
	// exclude_types ("hidden") or the bypass selectors ("via"). Such nodes
	// must not resurface as materialized boundary/pin neighbors — unlike
	// layer-absence, which is level semantics (pins still bring that
	// context as dimmed nodes).
	explicitlyRemoved := func(n *store.NodeRow) bool {
		if n == nil {
			return false
		}
		if bypassTypes[n.NodeType] || bypassLayers[n.Layer] {
			return true
		}
		if f != nil {
			for _, t := range f.ExcludeTypes {
				if n.NodeType == t {
					return true
				}
			}
		}
		return false
	}

	// Service-scoped views (C3) materialize the 1-hop targets of
	// boundary-crossing edges as Boundary VNodes + connecting VEdges, so
	// components whose only edges leave the scope stay visibly connected.
	matBoundary := spec.Scope.Service != "" && !pathMode
	boundaryIDs := make(map[int64]bool)
	renderedKey := func(id int64) string {
		if spec.Collapse {
			return repKey(id)
		}
		return nodeByID[id].NodeKey
	}
	// kind: usually e.EdgeType; CALLS_ENDPOINT targets are lifted to their
	// owning service, so those edges pass CALLS_SERVICE and dedupe with the
	// parallel direct CALLS_SERVICE edge (weight accumulates instead).
	boundaryHandled := make(map[string]bool) // edge keys already rendered as boundary edges
	addBoundaryEdge := func(inViewID int64, bn *store.NodeRow, e *store.EdgeRow, kind string, incoming bool) {
		if !matBoundary || bn == nil || inView[bn.NodeID] || explicitlyRemoved(bn) {
			return
		}
		boundaryHandled[e.EdgeKey] = true
		boundaryIDs[bn.NodeID] = true
		fromKey, toKey := renderedKey(inViewID), bn.NodeKey
		if incoming {
			fromKey, toKey = bn.NodeKey, renderedKey(inViewID)
		}
		k := edgeKeyT{fromKey, toKey, kind, edgeTransport(e, nodeByID)}
		ve := edgeAcc[k]
		if ve == nil {
			ve = &VEdge{From: fromKey, To: toKey, Kind: kind, Transport: k.transport}
			edgeAcc[k] = ve
			edgeOrder = append(edgeOrder, k)
		}
		ve.Weight++
	}

	for i := range domEdges {
		e := &domEdges[i]
		if hierarchyKinds[e.EdgeType] {
			continue // hierarchy is decoration (groups + pills), never an arrow
		}
		if flowKinds[e.EdgeType] && !flowVisible {
			continue
		}
		fid := retarget(e.FromNodeID)
		tid := retarget(e.ToNodeID)
		// Pinned nodes bring ALL their edges: edge-level filters don't apply
		// to edges touching an in-view pinned node.
		pinTouch := (pinned[fid] && inView[fid]) || (pinned[tid] && inView[tid])
		if !edgePasses(e) && !pinTouch {
			continue
		}
		fIn := inView[fid]
		tIn := inView[tid]

		switch {
		case fIn && tIn:
			if fid == tid {
				continue // e.g. a controller calling its own endpoint
			}
			if pathMode {
				a, b := fid, tid
				if a > b {
					a, b = b, a
				}
				if !pathPairs[[2]int64{a, b}] {
					continue // only edges along the found path are rendered
				}
			}
			var fromKey, toKey string
			if spec.Collapse {
				fromKey, toKey = repKey(fid), repKey(tid)
				if fromKey == toKey {
					continue // intra-group edges hidden when collapsed (§5.2)
				}
			} else {
				fromKey, toKey = nodeByID[fid].NodeKey, nodeByID[tid].NodeKey
			}
			k := edgeKeyT{fromKey, toKey, e.EdgeType, edgeTransport(e, nodeByID)}
			ve := edgeAcc[k]
			if ve == nil {
				ve = &VEdge{From: fromKey, To: toKey, Kind: e.EdgeType, Transport: k.transport, Weight: 0}
				edgeAcc[k] = ve
				edgeOrder = append(edgeOrder, k)
			}
			ve.Weight++
			if spec.Collapse {
				ve.CollapsedFrom = append(ve.CollapsedFrom, e.EdgeKey)
			}

		case fIn && !tIn:
			if pathMode {
				continue // a path view has no boundary entries
			}
			// Outgoing boundary (same semantics as legacy C3 / Selection).
			fromNode := nodeByID[fid]
			toNode := nodeByID[e.ToNodeID]
			if fromNode == nil || toNode == nil {
				continue
			}
			switch e.EdgeType {
			case "CALLS_SERVICE":
				if toNode.NodeType == "external_system" {
					addOut(toNode.Name, "external_system", fromNode.Name, "http")
					addBoundaryEdge(fid, toNode, e, e.EdgeType, false)
				} else if toNode.Layer == "service" && toNode.NodeType == "service" {
					addOut(toNode.Name, "service", fromNode.Name, "http")
					addBoundaryEdge(fid, toNode, e, e.EdgeType, false)
				}
			case "CALLS_ENDPOINT":
				// Lift the called endpoint to its owning service so the
				// boundary entry dedupes with the parallel CALLS_SERVICE.
				if exID, ok := exposerOf[e.ToNodeID]; ok {
					if svc := owned[exID]; svc != nil {
						addOut(svc.Name, "service", fromNode.Name, "http")
						addBoundaryEdge(fid, svc, e, "CALLS_SERVICE", false)
					}
				}
			case "PUBLISHES_TOPIC":
				addOut(toNode.Name, "topic", fromNode.Name, "async")
				addBoundaryEdge(fid, toNode, e, e.EdgeType, false)
			case "CONSUMES_TOPIC":
				// Consumption flows from the channel to the consumer.
				addIn(toNode.Name, "async")
				addBoundaryEdge(fid, toNode, e, e.EdgeType, false)
			}
			// Other kinds crossing the boundary (INJECTS, USES_MODEL, …) are
			// intentionally not boundary entries: models show as pills.

		case !fIn && tIn:
			if pathMode {
				continue // a path view has no boundary entries
			}
			// Incoming boundary.
			fromNode := nodeByID[e.FromNodeID]
			if fromNode == nil {
				continue
			}
			switch e.EdgeType {
			case "CALLS_SERVICE", "CALLS_ENDPOINT":
				callerSvc := owned[e.FromNodeID]
				if callerSvc == nil && fromNode.Layer == "service" && fromNode.NodeType == "service" {
					callerSvc = fromNode
				}
				if callerSvc != nil {
					addIn(callerSvc.Name, "http")
					addBoundaryEdge(tid, callerSvc, e, "CALLS_SERVICE", true)
				}
			case "PUBLISHES_TOPIC":
				if pubSvc := owned[e.FromNodeID]; pubSvc != nil {
					addIn(pubSvc.Name, "async")
					addBoundaryEdge(tid, pubSvc, e, e.EdgeType, true)
				}
			}
		}
	}

	// ---- pinned connections: dropped nodes bring their neighborhood ----
	// 1-hop neighbors of a pinned node that are OUTSIDE the view materialize
	// as dimmed Boundary nodes with their connecting edges (same mechanism as
	// the C3 boundary materialization) — the "show linked entities" feel.
	addPinEdge := func(fromKey, toKey, kind, transport string) {
		k := edgeKeyT{fromKey, toKey, kind, transport}
		ve := edgeAcc[k]
		if ve == nil {
			ve = &VEdge{From: fromKey, To: toKey, Kind: kind, Transport: transport}
			edgeAcc[k] = ve
			edgeOrder = append(edgeOrder, k)
		}
		ve.Weight++
	}
	if len(pinned) > 0 {
		for i := range domEdges {
			e := &domEdges[i]
			if hierarchyKinds[e.EdgeType] || (flowKinds[e.EdgeType] && !flowVisible) {
				continue
			}
			if boundaryHandled[e.EdgeKey] {
				continue // already drawn by the C3 boundary mechanism
			}
			fid := retarget(e.FromNodeID)
			tid := retarget(e.ToNodeID)
			if pinned[fid] && inView[fid] && !inView[tid] && !explicitlyRemoved(nodeByID[tid]) {
				boundaryIDs[tid] = true
				addPinEdge(nodeByID[fid].NodeKey, nodeByID[tid].NodeKey, e.EdgeType, edgeTransport(e, nodeByID))
			}
			if pinned[tid] && inView[tid] && !inView[fid] && !explicitlyRemoved(nodeByID[fid]) {
				boundaryIDs[fid] = true
				addPinEdge(nodeByID[fid].NodeKey, nodeByID[tid].NodeKey, e.EdgeType, edgeTransport(e, nodeByID))
			}
		}
		// Pinned endpoints render as nodes; their EXPOSES_ENDPOINT hierarchy
		// edge to the owning component (lifted to its group when collapsed)
		// keeps them visually attached while the owner is in view.
		for epID, exID := range exposerOf {
			if !pinned[epID] || !inView[epID] || !inView[exID] {
				continue
			}
			addPinEdge(renderedKey(exID), nodeByID[epID].NodeKey, "EXPOSES_ENDPOINT", "local")
		}
	}

	// NOTE: layer-opted flow views (the Flows preset) deliberately do NOT
	// materialize out-of-view TRIGGERS_FLOW/REQUIRES endpoints anymore — the
	// flows LIST view shows flows alone, each carrying its trigger endpoint
	// as VNode.Detail; the composition (trigger + REQUIRES targets + edges)
	// lives in the flow-scoped drill-down view (scope.Flow).

	// ---- bypass: keep connectivity through removed-with-bypass nodes ----
	// Nodes cut by filter.bypass_types/bypass_layers don't sever paths: a
	// directed BFS through the removed-node subgraph connects each surviving
	// in-neighbor to every surviving out-neighbor reachable through removed
	// nodes only. CONSUMES_TOPIC is traversed in semantic flow direction
	// (topic → consumer), matching the C2/boundary builders. The synthesized
	// edge keeps the FIRST removed-crossing edge's Kind, is labelled
	// "via <removed node name>" (or "via N nodes" for longer chains),
	// aggregates Weight and carries the underlying edge keys in
	// CollapsedFrom. Fan-out per removed entry node is capped at 16.
	var synEdges []VEdge
	if len(removedBypass) > 0 {
		type bEdge struct {
			from, to int64
			key      string
			kind     string
		}
		sem := func(e *store.EdgeRow) (int64, int64) {
			if e.EdgeType == "CONSUMES_TOPIC" {
				return retarget(e.ToNodeID), retarget(e.FromNodeID)
			}
			return retarget(e.FromNodeID), retarget(e.ToNodeID)
		}
		var entries []bEdge
		outOf := make(map[int64][]bEdge)
		for i := range domEdges {
			e := &domEdges[i]
			if hierarchyKinds[e.EdgeType] || (flowKinds[e.EdgeType] && !flowVisible) || !edgePasses(e) {
				continue
			}
			sf, st := sem(e)
			if sf == st {
				continue
			}
			be := bEdge{from: sf, to: st, key: e.EdgeKey, kind: e.EdgeType}
			if inView[sf] && removedBypass[st] {
				entries = append(entries, be)
			}
			if removedBypass[sf] {
				outOf[sf] = append(outOf[sf], be)
			}
		}
		const maxFanOut = 16
		fanOut := make(map[int64]int) // removed entry node → synthesized edges
		type synKeyT struct{ from, to, kind, label string }
		synAcc := make(map[synKeyT]*VEdge)
		var synOrder []synKeyT
		for _, en := range entries {
			type pathState struct {
				chain int      // removed nodes traversed so far
				keys  []string // underlying edge keys of the path
			}
			visited := map[int64]pathState{en.to: {1, []string{en.key}}}
			queue := []int64{en.to}
			for len(queue) > 0 {
				cur := queue[0]
				queue = queue[1:]
				cs := visited[cur]
				for _, be := range outOf[cur] {
					if removedBypass[be.to] {
						if _, seen := visited[be.to]; seen {
							continue
						}
						keys := append(append([]string{}, cs.keys...), be.key)
						visited[be.to] = pathState{cs.chain + 1, keys}
						queue = append(queue, be.to)
						continue
					}
					if !inView[be.to] {
						continue
					}
					fromKey, toKey := renderedKey(en.from), renderedKey(be.to)
					if fromKey == toKey {
						continue
					}
					label := "via " + nodeByID[en.to].Name
					if cs.chain > 1 {
						label = fmt.Sprintf("via %d nodes", cs.chain)
					}
					sk := synKeyT{fromKey, toKey, en.kind, label}
					ve := synAcc[sk]
					if ve == nil {
						if fanOut[en.to] >= maxFanOut {
							continue
						}
						fanOut[en.to]++
						ve = &VEdge{From: fromKey, To: toKey, Kind: en.kind, Label: label}
						synAcc[sk] = ve
						synOrder = append(synOrder, sk)
					}
					ve.Weight++
					ve.CollapsedFrom = append(ve.CollapsedFrom, append(append([]string{}, cs.keys...), be.key)...)
				}
			}
		}
		for _, sk := range synOrder {
			synEdges = append(synEdges, *synAcc[sk])
		}
	}

	// ---- assemble (deterministic order) ----
	for key, vg := range groupsByKey {
		_ = key
		view.Groups = append(view.Groups, *vg)
	}
	sort.Slice(view.Groups, func(i, j int) bool { return view.Groups[i].Key < view.Groups[j].Key })

	salPol := saliencePolicyFor(st)
	salLevel := spec.Layout.Preset
	roleByNode := resolveRolesByNode(st)
	for id, g := range memberGroup {
		if spec.Collapse && g != "" {
			continue // grouped members fold into their VGroup
		}
		n := nodeByID[id]
		role, roleConf := effectiveRoleClaim(n, roleByNode)
		sal := salience.Resolve(salPol, salience.Input{
			NodeType:       n.NodeType,
			Layer:          n.Layer,
			Role:           role,
			RoleConfidence: roleConf,
			Level:          salLevel,
		})
		view.Nodes = append(view.Nodes, VNode{
			Key:        n.NodeKey,
			Name:       n.Name,
			Type:       n.NodeType,
			Layer:      n.Layer,
			Group:      g,
			Endpoints:  endpointPills[id],
			UsesModels: modelPills[id],
			Detail:     flowTrigger[id],
			Tier:       string(sal.Tier),
			RenderMode: string(sal.RenderMode),
		})
	}
	// Materialized boundary targets (service-scoped views): free-standing,
	// marked Boundary so renderers can dim them.
	for id := range boundaryIDs {
		if inView[id] {
			continue
		}
		n := nodeByID[id]
		view.Nodes = append(view.Nodes, VNode{
			Key:      n.NodeKey,
			Name:     n.Name,
			Type:     n.NodeType,
			Layer:    n.Layer,
			Boundary: true,
		})
	}
	sort.Slice(view.Nodes, func(i, j int) bool { return view.Nodes[i].Key < view.Nodes[j].Key })

	for _, k := range edgeOrder {
		view.Edges = append(view.Edges, *edgeAcc[k])
	}
	view.Edges = append(view.Edges, synEdges...)
	sort.Slice(view.Edges, func(i, j int) bool {
		a, b := view.Edges[i], view.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Label < b.Label
	})

	// C1 context (group:domain + collapse): mirror /api/graph's manifest-infra
	// synthesis. USES_INFRA edges exist only as /api/graph virtual edges (built
	// from the manifest), never as store edges, so the domain-collapsed view
	// connects the system box to each free-standing infra node here: kind
	// "uses_infra", labelled by the infra role ("events" for the broker,
	// "cache" for the cache — /api/graph renders the same edges with its
	// USES_INFRA category label).
	if groupBy == "domain" && spec.Collapse && groupsByKey[domain] != nil {
		var infraEdges []VEdge
		for id := range inView {
			n := nodeByID[id]
			if n.Layer != "infra" || memberGroup[id] != "" {
				continue
			}
			label := n.NodeType
			switch n.NodeType {
			case "broker":
				label = "events"
			case "cache":
				label = "cache"
			case "database":
				label = "db"
			}
			infraEdges = append(infraEdges, VEdge{
				From: domain, To: n.NodeKey, Kind: "uses_infra", Label: label, Weight: 1,
			})
		}
		sort.Slice(infraEdges, func(i, j int) bool { return infraEdges[i].To < infraEdges[j].To })
		view.Edges = append(view.Edges, infraEdges...)
	}

	if len(bIn) > 0 || len(bOut) > 0 {
		sortIncoming(bIn)
		sortOutgoing(bOut)
		view.Boundary = &VBoundary{In: bIn, Out: bOut}
	}

	if view.Title == "" {
		switch {
		case flowScoped:
			view.Title = flowTitle
			if view.Title == "" {
				view.Title = spec.Scope.Flow
			}
		case pathMode:
			view.Title = fmt.Sprintf("%s → %s", spec.Scope.Nodes[0], spec.Scope.Nodes[1])
		case spec.Scope.Nodes != nil:
			view.Title = fmt.Sprintf("%d selected nodes", len(spec.Scope.Nodes))
		case spec.Scope.Service != "":
			view.Title = spec.Scope.Service
		default:
			view.Title = domain
		}
	}
	return view, nil
}

// ---------- path search ----------

// shortestPath finds the shortest undirected path from→to over non-hierarchy
// dependency edges (optionally restricted to the allow set of raw edge
// types). Plain BFS; among multiple shortest paths the choice is
// deterministic: each node's neighbors are visited in lexicographic node-key
// order, and the first parent to discover a node wins. Returns the ordered
// node ids and the set of unordered consecutive pairs, or (nil, nil) when no
// path exists.
func shortestPath(from, to int64, edges []store.EdgeRow, nodeByID map[int64]*store.NodeRow, allow map[string]bool) ([]int64, map[[2]int64]bool) {
	if from == to {
		return []int64{from}, map[[2]int64]bool{}
	}

	adj := make(map[int64][]int64)
	for i := range edges {
		e := &edges[i]
		if hierarchyKinds[e.EdgeType] || flowKinds[e.EdgeType] {
			continue
		}
		if allow != nil && !allow[e.EdgeType] {
			continue
		}
		adj[e.FromNodeID] = append(adj[e.FromNodeID], e.ToNodeID)
		adj[e.ToNodeID] = append(adj[e.ToNodeID], e.FromNodeID)
	}
	for id := range adj {
		ns := adj[id]
		sort.Slice(ns, func(i, j int) bool { return nodeByID[ns[i]].NodeKey < nodeByID[ns[j]].NodeKey })
	}

	parent := map[int64]int64{from: from}
	queue := []int64{from}
	found := false
	for len(queue) > 0 && !found {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if _, seen := parent[nb]; seen {
				continue
			}
			parent[nb] = cur
			if nb == to {
				found = true
				break
			}
			queue = append(queue, nb)
		}
	}
	if !found {
		return nil, nil
	}

	var rev []int64
	for id := to; ; id = parent[id] {
		rev = append(rev, id)
		if id == from {
			break
		}
	}
	order := make([]int64, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		order = append(order, rev[i])
	}
	pairs := make(map[[2]int64]bool, len(order)-1)
	for i := 0; i+1 < len(order); i++ {
		a, b := order[i], order[i+1]
		if a > b {
			a, b = b, a
		}
		pairs[[2]int64{a, b}] = true
	}
	return order, pairs
}

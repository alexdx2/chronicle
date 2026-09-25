package viewmodel

import (
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// unownedGroupKey frames data models no service owns ("owner unknown").
const unownedGroupKey = "unowned"

// groupStatsFor fills every frame's Stats in the language of what the
// frame IS:
//
//	service   — endpoints · components · models · publishes · consumes · calls
//	broker    — topics · publishers · subscribers (distinct services)
//	domain    — services · endpoints (the C1 system box)
//	module    — components · endpoints · models
//	otherwise — components (+ endpoints/models when any)
//
// "publishes"/"consumes" count distinct topics the frame's members touch;
// "calls" counts distinct services (or external systems) they call,
// endpoint calls lifted to the endpoint's owner. The counts describe the
// graph, not the view: a filtered-out edge kind still counts — except a
// hidden MEMBER (rev3 task 6c, the hidden param): a frame's own membership
// count (e.g. a broker's "N topics", and who publishes/subscribes THROUGH
// it) must match what this view actually draws, so a member with
// render_mode=hidden (private, isolated, …) and any edge touching it (post
// retarget) are excluded from the member-derived stats below. A frame
// member's OWN publishes/consumes/calls stat (bump(fromG, …) — what that
// member DOES, not what some other frame carries) is intentionally NOT
// gated on hidden: that is graph truth about the member itself (rev3 task
// 3 decision), independent of whether some other node it talks to is
// individually drawn.
func groupStatsFor(
	groups map[string]*VGroup,
	memberGroup map[int64]string,
	nodeByID map[int64]*store.NodeRow,
	edges []store.EdgeRow,
	owned map[int64]*store.NodeRow,
	brokerOf map[int64]int64,
	endpointPills map[int64][]Endpoint,
	modelPills map[int64][]string,
	retarget func(int64) int64,
	hidden map[int64]bool,
) {
	type set = map[string]bool
	distinct := make(map[string]map[string]set) // group → stat → keys
	bump := func(g, stat, key string) {
		if distinct[g] == nil {
			distinct[g] = make(map[string]set)
		}
		if distinct[g][stat] == nil {
			distinct[g][stat] = make(set)
		}
		distinct[g][stat][key] = true
	}
	isSvc := func(n *store.NodeRow) bool {
		return n != nil && n.Layer == "service" && (n.NodeType == "service" || n.NodeType == "external_system")
	}
	for id, g := range memberGroup {
		if g == "" {
			continue
		}
		if hidden[id] {
			continue // rev3 task 6c: not drawn, does not count as a member
		}
		vg := groups[g]
		n := nodeByID[id]
		switch {
		case isSvc(n) && n.NodeType == "service":
			vg.Stats["services"]++
		case n.Layer == "code" && n.NodeType != "module" && n.NodeType != "file" && n.NodeType != "repository" && n.NodeType != "package":
			vg.Stats["components"]++
		case n.NodeType == "topic":
			vg.Stats["topics"]++
		case n.Layer == "data" && (n.NodeType == "model" || n.NodeType == "entity" || n.NodeType == "table"):
			bump(g, "models", n.NodeKey)
		case n.NodeType == "endpoint":
			vg.Stats["endpoints"]++
		}
		vg.Stats["endpoints"] += len(endpointPills[id])
		// Models a member uses count once, whether the model is a member
		// too (expanded views) or only a pill (collapsed ones).
		for _, m := range modelPills[id] {
			bump(g, "models", "pill:"+m)
		}
	}
	// Conversations: what each frame's members publish, consume and call;
	// for a broker frame, which services publish into / subscribe from it.
	svcGroupOf := func(id int64) string {
		if svc := owned[id]; svc != nil {
			return svc.NodeKey
		}
		if n := nodeByID[id]; isSvc(n) {
			return n.NodeKey
		}
		return ""
	}
	for i := range edges {
		e := &edges[i]
		fid, tid := retarget(e.FromNodeID), retarget(e.ToNodeID)
		fromG, toG := memberGroup[fid], memberGroup[tid]
		fn, tn := nodeByID[fid], nodeByID[tid]
		if fn == nil || tn == nil {
			continue
		}
		switch e.EdgeType {
		case "PUBLISHES_TOPIC":
			if fromG != "" && groups[fromG] != nil && groups[fromG].Kind == "service" {
				bump(fromG, "publishes", tn.NodeKey)
			}
			// The broker-side "publishers" count is about who talks
			// through THIS topic (a member of the broker frame): a hidden
			// topic (rev3 task 6c) is not a conversation this view shows.
			if bid, ok := brokerOf[tid]; ok && !hidden[tid] {
				if bg := groups[nodeByID[bid].NodeKey]; bg != nil {
					if svc := svcGroupOf(fid); svc != "" {
						bump(bg.Key, "publishers", svc)
					}
				}
			}
		case "CONSUMES_TOPIC":
			if fromG != "" && groups[fromG] != nil && groups[fromG].Kind == "service" {
				bump(fromG, "consumes", tn.NodeKey)
			}
			// Same reasoning as "publishers" above: a hidden topic is not
			// a conversation this view shows, so it does not make its
			// consumer a counted "subscriber" of the broker frame either.
			if bid, ok := brokerOf[tid]; ok && !hidden[tid] {
				if bg := groups[nodeByID[bid].NodeKey]; bg != nil {
					if svc := svcGroupOf(fid); svc != "" {
						bump(bg.Key, "subscribers", svc)
					}
				}
			}
		case "CALLS_SERVICE":
			if fromG != "" && groups[fromG] != nil && groups[fromG].Kind == "service" && isSvc(tn) && tn.NodeKey != fromG {
				bump(fromG, "calls", strings.ToLower(tn.Name))
			}
		case "CALLS_ENDPOINT":
			if fromG != "" && groups[fromG] != nil && groups[fromG].Kind == "service" {
				if svc := owned[e.ToNodeID]; svc != nil && svc.NodeKey != fromG {
					bump(fromG, "calls", strings.ToLower(svc.Name))
				}
			}
		}
		_ = toG
	}
	for g, stats := range distinct {
		for stat, keys := range stats {
			if stat == "models" {
				// A model both a member and a pill is one model.
				names := make(set)
				for k := range keys {
					if strings.HasPrefix(k, "pill:") {
						names[strings.TrimPrefix(k, "pill:")] = true
					} else if n := nodeByID[nodeIDByKey(nodeByKeyIndex(nodeByID), k)]; n != nil {
						names[n.Name] = true
					}
				}
				groups[g].Stats[stat] = len(names)
				continue
			}
			groups[g].Stats[stat] = len(keys)
		}
	}
	for _, vg := range groups {
		// The system box (C1) speaks in services and endpoints only.
		if vg.Kind == "domain" {
			for k := range vg.Stats {
				if k != "services" && k != "endpoints" {
					delete(vg.Stats, k)
				}
			}
		}
		for k, v := range vg.Stats {
			if v == 0 {
				delete(vg.Stats, k)
			}
		}
	}
}

// nodeIDByKey resolves a node key to its id (0 when unknown).
func nodeIDByKey(byKey map[string]*store.NodeRow, key string) int64 {
	if n := byKey[key]; n != nil {
		return n.NodeID
	}
	return 0
}

// nodeByKeyIndex builds a key index over an id index.
func nodeByKeyIndex(byID map[int64]*store.NodeRow) map[string]*store.NodeRow {
	out := make(map[string]*store.NodeRow, len(byID))
	for _, n := range byID {
		out[n.NodeKey] = n
	}
	return out
}

// infraTouched reports whether anything in the domain graph touches the
// infra node: an edge to or from it, or a topic framed inside it.
func infraTouched(id int64, edges []store.EdgeRow, brokerOf map[int64]int64) bool {
	for _, bid := range brokerOf {
		if bid == id {
			return true
		}
	}
	for i := range edges {
		if edges[i].FromNodeID == id || edges[i].ToNodeID == id {
			return true
		}
	}
	return false
}

// flowsTable lists every in-view flow as a row: the service it enters
// through, the trigger endpoint, the component exposing that endpoint,
// and the flow. Sorted by service, endpoint, flow; unknown cells read "—".
func flowsTable(inView map[int64]bool, nodeByID map[int64]*store.NodeRow, edges []store.EdgeRow, exposerOf map[int64]int64, owned map[int64]*store.NodeRow) *VTable {
	trigger := make(map[int64]int64) // flow → endpoint
	for i := range edges {
		e := &edges[i]
		if e.EdgeType == "TRIGGERS_FLOW" {
			if _, seen := trigger[e.ToNodeID]; !seen {
				trigger[e.ToNodeID] = e.FromNodeID
			}
		}
	}
	type row struct {
		cells [4]string
		key   string
	}
	var rows []row
	for id := range inView {
		n := nodeByID[id]
		if n == nil || n.Layer != "flow" {
			continue
		}
		r := row{cells: [4]string{"—", "—", "—", n.Name}, key: n.NodeKey}
		if epID, ok := trigger[id]; ok {
			if ep := nodeByID[epID]; ep != nil {
				r.cells[1] = ep.Name
				// "score (GET /api/score)" repeats the endpoint column.
				r.cells[3] = strings.TrimSuffix(n.Name, " ("+ep.Name+")")
				if svc := owned[epID]; svc != nil {
					r.cells[0] = svc.Name
				}
				if exID, ok := exposerOf[epID]; ok {
					if ex := nodeByID[exID]; ex != nil {
						r.cells[2] = ex.Name
						if r.cells[0] == "—" {
							if svc := owned[exID]; svc != nil {
								r.cells[0] = svc.Name
							}
						}
					}
				}
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		for c := 0; c < 4; c++ {
			x, y := strings.ToLower(a.cells[c]), strings.ToLower(b.cells[c])
			if x != y {
				// Unknown service sorts last.
				if x == "—" || y == "—" {
					return y == "—"
				}
				return x < y
			}
		}
		return a.key < b.key
	})
	t := &VTable{Columns: []string{"service", "endpoint", "handler", "flow"}, Rows: [][]string{}, Keys: []string{}}
	for _, r := range rows {
		t.Rows = append(t.Rows, r.cells[:])
		t.Keys = append(t.Keys, r.key)
	}
	return t
}

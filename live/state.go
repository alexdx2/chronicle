package live

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

// StateNote tells the agent which parts of the state are verified.
const StateNote = "screen state is reported by the dashboard; keys are verified against the graph, titles are not"

const agentDiagramsInState = 5

type NodeRef struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Layer string `json:"layer"`
}

type EdgeRef struct {
	Key  string `json:"key"`
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// ResolvedSelection is the human's selection with names and types from the
// graph.
type ResolvedSelection struct {
	Kind  string    `json:"kind"`
	Node  *NodeRef  `json:"node,omitempty"`
	Edges []EdgeRef `json:"edges,omitempty"`
}

// DiagramRef is one of the agent's recent diagrams.
type DiagramRef struct {
	At    string             `json:"at"`
	Title string             `json:"title"`
	Repo  string             `json:"repo,omitempty"`
	Spec  viewmodel.ViewSpec `json:"spec"`
}

// DashboardState is what the human's dashboard shows, without URLs.
type DashboardState struct {
	Open           bool
	Visible        bool
	SeenSecondsAgo *int // nil: no screen was ever reported
	Clients        int  // every live client
	Repo           string
	Tab            string
	Title          string
	Spec           *viewmodel.ViewSpec
	Selected       *ResolvedSelection
	Unresolved     []string
	Breadcrumbs    []string
	Marks          int
	AgentDiagrams  []DiagramRef
}

type clientScreen struct {
	at     time.Time
	id     int64
	screen Screen
	src    Source
}

// State reads the latest screen report of every client across sources. A
// client is alive while its last report is younger than OpenWindow and not
// closed; the state follows the freshest visible live client, or the
// freshest live one when none is visible.
func State(sources []Source, now time.Time) DashboardState {
	latest := map[string]clientScreen{}
	var lastSeen time.Time
	for _, src := range sources {
		if src.Store == nil {
			continue
		}
		// The newest report of any client, even one older than OpenWindow, so
		// SeenSecondsAgo still reflects it once every client has gone quiet.
		// Read separately from the alive-candidate loop below (F1): a busy
		// client's volume must not be able to push this out of the table's
		// newest-per-kind window, and RecentLiveEvents(kind, 1) never can —
		// it always returns the single newest row regardless of age or count.
		if recent, err := src.Store.RecentLiveEvents(KindScreen, 1); err == nil {
			for _, ev := range recent {
				if at, err := time.Parse(time.RFC3339Nano, ev.At); err == nil && at.After(lastSeen) {
					lastSeen = at
				}
			}
		}
		// Alive candidates: every report within OpenWindow, not just the
		// newest liveKeepPerKind rows of the kind — a busy client's volume
		// must not push a quiet client's still-alive report out of this read
		// (F1; store/live.go's prune keeps such rows for the same reason).
		evs, err := src.Store.LiveEventsSince(KindScreen, now.Add(-OpenWindow))
		if err != nil {
			continue
		}
		for _, ev := range evs {
			var sc Screen
			if json.Unmarshal(ev.Payload, &sc) != nil || sc.ClientID == "" {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, ev.At)
			if err != nil {
				continue
			}
			cur, ok := latest[sc.ClientID]
			if !ok || at.After(cur.at) || (at.Equal(cur.at) && ev.ID > cur.id) {
				latest[sc.ClientID] = clientScreen{at: at, id: ev.ID, screen: sc, src: src}
			}
		}
	}
	st := DashboardState{AgentDiagrams: recentDiagrams(sources, agentDiagramsInState)}
	if !lastSeen.IsZero() {
		secs := secondsSince(now, lastSeen)
		st.SeenSecondsAgo = &secs
	}
	var alive []clientScreen
	for _, c := range latest {
		if !c.screen.Closed && now.Sub(c.at) <= OpenWindow {
			alive = append(alive, c)
		}
	}
	st.Clients = len(alive)
	if len(alive) == 0 {
		return st
	}
	pick := freshest(alive, true)
	if pick == nil {
		pick = freshest(alive, false)
	}
	secs := secondsSince(now, pick.at)
	st.Open = true
	st.SeenSecondsAgo = &secs
	st.Visible = pick.screen.Visible
	st.Repo = pick.src.Repo
	st.Tab = pick.screen.Tab
	st.Title = pick.screen.Title
	st.Spec = pick.screen.Spec
	st.Breadcrumbs = pick.screen.Breadcrumbs
	if st.Spec != nil {
		st.Marks = len(st.Spec.Marks)
	}
	st.Selected, st.Unresolved = resolveSelection(pick.screen.Selected, pick.src, sources)
	return st
}

func freshest(cs []clientScreen, visibleOnly bool) *clientScreen {
	var best *clientScreen
	for i := range cs {
		c := &cs[i]
		if visibleOnly && !c.screen.Visible {
			continue
		}
		if best == nil || c.at.After(best.at) || (c.at.Equal(best.at) && c.id > best.id) {
			best = c
		}
	}
	return best
}

func secondsSince(now, t time.Time) int {
	d := now.Sub(t)
	if d < 0 {
		return 0
	}
	return int(d / time.Second)
}

// resolveSelection looks the selection up in the graph, the screen's own
// repo first; keys the graph lacks come back unresolved.
func resolveSelection(sel *Selection, first Source, sources []Source) (*ResolvedSelection, []string) {
	if sel == nil {
		return nil, nil
	}
	order := append([]Source{first}, sources...)
	switch sel.Kind {
	case "node":
		for _, s := range order {
			if s.Store == nil {
				continue
			}
			if n, err := s.Store.GetNodeByKey(sel.Key); err == nil {
				return &ResolvedSelection{Kind: "node", Node: &NodeRef{Key: n.NodeKey, Name: n.Name, Type: n.NodeType, Layer: n.Layer}}, nil
			}
		}
		return nil, []string{sel.Key}
	case "edge":
		rs := &ResolvedSelection{Kind: "edge"}
		var unresolved []string
		for _, k := range sel.Keys {
			if ref, ok := findEdge(order, k); ok {
				rs.Edges = append(rs.Edges, ref)
			} else {
				unresolved = append(unresolved, k)
			}
		}
		if len(rs.Edges) == 0 {
			return nil, unresolved
		}
		return rs, unresolved
	}
	return nil, nil
}

func findEdge(order []Source, key string) (EdgeRef, bool) {
	for _, s := range order {
		if s.Store == nil {
			continue
		}
		e, err := s.Store.GetEdgeByKey(key)
		if err != nil || e == nil {
			continue
		}
		ref := EdgeRef{Key: e.EdgeKey, Type: e.EdgeType}
		if n, err := s.Store.GetNodeByID(e.FromNodeID); err == nil {
			ref.From = n.Name
		}
		if n, err := s.Store.GetNodeByID(e.ToNodeID); err == nil {
			ref.To = n.Name
		}
		return ref, true
	}
	return EdgeRef{}, false
}

// recentDiagrams are the newest n diagram events across sources.
func recentDiagrams(sources []Source, n int) []DiagramRef {
	type item struct {
		ref DiagramRef
		id  int64
	}
	var all []item
	for _, src := range sources {
		if src.Store == nil {
			continue
		}
		evs, err := src.Store.RecentLiveEvents(KindDiagram, n)
		if err != nil {
			continue
		}
		for _, ev := range evs {
			var d Diagram
			if json.Unmarshal(ev.Payload, &d) != nil {
				continue
			}
			all = append(all, item{DiagramRef{At: ev.At, Title: d.Title, Repo: src.Repo, Spec: d.Spec}, ev.ID})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].ref.At != all[j].ref.At {
			return all[i].ref.At > all[j].ref.At
		}
		return all[i].id > all[j].id
	})
	out := []DiagramRef{}
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].ref)
	}
	return out
}

// StateJSON shapes the chronicle_dashboard_state result. url builds a
// dashboard link for a spec: the caller knows the admin address.
func StateJSON(s DashboardState, url func(viewmodel.ViewSpec) string) map[string]any {
	diagrams := make([]map[string]any, 0, len(s.AgentDiagrams))
	for _, d := range s.AgentDiagrams {
		m := map[string]any{"at": d.At, "title": d.Title, "url": url(d.Spec)}
		if d.Repo != "" {
			m["repo"] = d.Repo
		}
		diagrams = append(diagrams, m)
	}
	out := map[string]any{"open": s.Open, "clients": s.Clients, "agent_diagrams": diagrams}
	if !s.Open {
		if s.SeenSecondsAgo != nil {
			out["last_seen_seconds_ago"] = *s.SeenSecondsAgo
		} else {
			out["last_seen_seconds_ago"] = nil
		}
		return out
	}
	crumbs := s.Breadcrumbs
	if crumbs == nil {
		crumbs = []string{}
	}
	unresolved := s.Unresolved
	if unresolved == nil {
		unresolved = []string{}
	}
	out["visible"] = s.Visible
	out["seen_seconds_ago"] = *s.SeenSecondsAgo
	out["tab"] = s.Tab
	out["title"] = s.Title
	out["breadcrumbs"] = crumbs
	out["marks"] = s.Marks
	out["unresolved"] = unresolved
	out["note"] = StateNote
	if s.Repo != "" {
		out["repo"] = s.Repo
	}
	if s.Spec != nil {
		out["spec"] = s.Spec
		out["preset"] = s.Spec.Layout.Preset
		out["domain"] = s.Spec.Scope.Domain
		out["url"] = url(*s.Spec)
	}
	switch {
	case s.Selected == nil:
		out["selected"] = nil
	case s.Selected.Kind == "node" && s.Selected.Node != nil:
		n := s.Selected.Node
		out["selected"] = map[string]any{"kind": "node", "key": n.Key, "name": n.Name, "type": n.Type, "layer": n.Layer}
	default:
		out["selected"] = map[string]any{"kind": "edge", "edges": s.Selected.Edges}
	}
	return out
}

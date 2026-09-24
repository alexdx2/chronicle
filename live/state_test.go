package live

import (
	"strings"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/store"
)

func seedPair(t *testing.T, st *store.Store) (string, string, string) {
	t.Helper()
	mk := func(key, name string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: "service", NodeType: "service", DomainKey: "d", Name: name,
			Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := mk("service:service:d:a", "a"), mk("service:service:d:b", "b")
	ek := "service:service:d:a->service:service:d:b:CALLS_SERVICE"
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: ek, FromNodeID: a, ToNodeID: b, EdgeType: "CALLS_SERVICE", DerivationKind: "hard",
		Active: true, Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}"}); err != nil {
		t.Fatal(err)
	}
	return "service:service:d:a", "service:service:d:b", ek
}

func TestState_EmptyStoreIsClosed(t *testing.T) {
	st := openStore(t)
	s := State([]Source{{Store: st}}, time.Now())
	if s.Open || s.Clients != 0 || s.SeenSecondsAgo != nil {
		t.Fatalf("state = %+v", s)
	}
}

func TestState_LiveClientWithSelection(t *testing.T) {
	st := openStore(t)
	aKey, _, _ := seedPair(t, st)
	spec := viewmodel.PresetSpec("c2", "d", "")
	spec.Marks = map[string]viewmodel.Mark{aKey: {Kind: "focus"}}
	if _, err := ReportScreen(st, Screen{ClientID: "tab1", Tab: "graph", Title: "C2", Spec: &spec,
		Selected: &Selection{Kind: "node", Key: aKey}, Breadcrumbs: []string{"C2"}, Visible: true}); err != nil {
		t.Fatal(err)
	}
	s := State([]Source{{Store: st}}, time.Now())
	if !s.Open || !s.Visible || s.Clients != 1 || s.Tab != "graph" || s.Marks != 1 || s.Spec == nil || s.Spec.Layout.Preset != "c2" {
		t.Fatalf("state = %+v", s)
	}
	if s.Selected == nil || s.Selected.Node == nil || s.Selected.Node.Name != "a" || s.Selected.Node.Type != "service" {
		t.Fatalf("selected = %+v", s.Selected)
	}
}

func TestState_StaleAndClosedClientsAreNotOpen(t *testing.T) {
	st := openStore(t)
	ReportScreen(st, Screen{ClientID: "old", Visible: true})
	s := State([]Source{{Store: st}}, time.Now().Add(2*time.Minute))
	if s.Open || s.SeenSecondsAgo == nil || *s.SeenSecondsAgo < 119 {
		t.Fatalf("stale state = %+v", s)
	}
	st2 := openStore(t)
	ReportScreen(st2, Screen{ClientID: "tab", Visible: true})
	ReportScreen(st2, Screen{ClientID: "tab", Closed: true})
	if s := State([]Source{{Store: st2}}, time.Now()); s.Open {
		t.Fatalf("a closed tab is not open: %+v", s)
	}
}

// Two tabs: the human works in A, B is hidden but its heartbeat is newer.
func TestState_PrefersTheVisibleTab(t *testing.T) {
	st := openStore(t)
	ReportScreen(st, Screen{ClientID: "A", Tab: "graph", Visible: true})
	time.Sleep(5 * time.Millisecond)
	ReportScreen(st, Screen{ClientID: "B", Tab: "overview", Visible: false})
	s := State([]Source{{Store: st}}, time.Now())
	if !s.Open || !s.Visible || s.Tab != "graph" || s.Clients != 2 {
		t.Fatalf("state = %+v", s)
	}
	st2 := openStore(t)
	ReportScreen(st2, Screen{ClientID: "A", Tab: "graph", Visible: false})
	time.Sleep(5 * time.Millisecond)
	ReportScreen(st2, Screen{ClientID: "B", Tab: "overview", Visible: false})
	if s := State([]Source{{Store: st2}}, time.Now()); !s.Open || s.Visible || s.Tab != "overview" {
		t.Fatalf("all hidden → the freshest: %+v", s)
	}
}

func TestState_ResolvesEdgesAndReportsUnknownKeys(t *testing.T) {
	st := openStore(t)
	_, _, ek := seedPair(t, st)
	ReportScreen(st, Screen{ClientID: "t", Visible: true, Selected: &Selection{Kind: "edge", Keys: []string{ek, "x->y:GONE"}}})
	s := State([]Source{{Store: st}}, time.Now())
	if s.Selected == nil || len(s.Selected.Edges) != 1 || s.Selected.Edges[0].From != "a" || s.Selected.Edges[0].To != "b" {
		t.Fatalf("edges = %+v", s.Selected)
	}
	if len(s.Unresolved) != 1 || s.Unresolved[0] != "x->y:GONE" {
		t.Fatalf("unresolved = %v", s.Unresolved)
	}
	st2 := openStore(t)
	ReportScreen(st2, Screen{ClientID: "t", Visible: true, Selected: &Selection{Kind: "node", Key: "service:service:d:gone"}})
	if s := State([]Source{{Store: st2}}, time.Now()); s.Selected != nil || len(s.Unresolved) != 1 {
		t.Fatalf("a vanished node: %+v", s)
	}
}

func TestState_AcrossRepos(t *testing.T) {
	a, b := openStore(t), openStore(t)
	ReportScreen(a, Screen{ClientID: "t1", Tab: "graph", Visible: true})
	time.Sleep(5 * time.Millisecond)
	ReportScreen(b, Screen{ClientID: "t1", Tab: "overview", Visible: true})
	s := State([]Source{{Repo: "a", Store: a}, {Repo: "b", Store: b}}, time.Now())
	if s.Repo != "b" || s.Tab != "overview" || s.Clients != 1 {
		t.Fatalf("the newest report of a client wins across repos: %+v", s)
	}
}

func TestState_AgentDiagramsNewestFive(t *testing.T) {
	st := openStore(t)
	for i := 0; i < 7; i++ {
		PublishDiagram(st, SourceMCP, Diagram{Title: string(rune('A' + i))})
	}
	s := State([]Source{{Store: st}}, time.Now())
	if len(s.AgentDiagrams) != 5 || s.AgentDiagrams[0].Title != "G" || s.AgentDiagrams[4].Title != "C" {
		t.Fatalf("agent diagrams = %+v", s.AgentDiagrams)
	}
}

func TestStateJSON_URLsComeFromTheCaller(t *testing.T) {
	st := openStore(t)
	spec := viewmodel.PresetSpec("c2", "d", "")
	PublishDiagram(st, SourceMCP, Diagram{Title: "C2", Spec: spec})
	ReportScreen(st, Screen{ClientID: "t", Tab: "graph", Spec: &spec, Visible: true})
	url := func(s viewmodel.ViewSpec) string { return "http://h/#v/" + s.Layout.Preset }
	out := StateJSON(State([]Source{{Store: st}}, time.Now()), url)
	if out["url"] != "http://h/#v/c2" || out["preset"] != "c2" || out["domain"] != "d" || out["open"] != true {
		t.Fatalf("open shape = %v", out)
	}
	diagrams := out["agent_diagrams"].([]map[string]any)
	if len(diagrams) != 1 || diagrams[0]["url"] != "http://h/#v/c2" {
		t.Fatalf("agent diagrams = %v", diagrams)
	}
	if note, _ := out["note"].(string); !strings.Contains(note, "titles are not") {
		t.Fatalf("note = %q", note)
	}
	closed := StateJSON(State([]Source{{Store: openStore(t)}}, time.Now()), url)
	if closed["open"] != false || closed["last_seen_seconds_ago"] != nil || closed["spec"] != nil {
		t.Fatalf("closed shape = %v", closed)
	}
}

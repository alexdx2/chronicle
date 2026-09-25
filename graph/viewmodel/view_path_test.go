package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// TestBuildViewPathLiveArenaToBattleResults: expand.mode="path" on the live
// demo DB. The shortest dependency path from arena.service to the
// battle-results topic runs through battle-result.producer (INJECTS, then
// PUBLISHES_TOPIC). Trace must hold the keys in path order.
func TestBuildViewPathLiveArenaToBattleResults(t *testing.T) {
	st := openLiveDBCopy(t)

	// Find the endpoints by suffix scan — keys embed fixture paths.
	allNodes, err := st.ListNodes(store.NodeFilter{Domain: "tom-and-jerry"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	var fromKey, toKey, viaKey string
	for _, n := range activeOnly(allNodes) {
		switch {
		case strings.HasSuffix(n.NodeKey, "/arena.service"):
			fromKey = n.NodeKey
		case strings.HasPrefix(n.NodeKey, "contract:topic:") && strings.HasSuffix(n.NodeKey, ":battle-results"):
			toKey = n.NodeKey
		case strings.HasSuffix(n.NodeKey, "/battle-result.producer"):
			viaKey = n.NodeKey
		}
	}
	if fromKey == "" || toKey == "" || viaKey == "" {
		t.Fatalf("expected nodes not found: from=%q to=%q via=%q", fromKey, toKey, viaKey)
	}

	v, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "tom-and-jerry", Nodes: []string{fromKey, toKey}},
		Expand: &ExpandSpec{Mode: "path"},
		Group:  GroupSpec{By: "none"},
	})
	if err != nil {
		t.Fatalf("BuildView(path): %v", err)
	}

	// --- Trace order: arena.service → battle-result.producer → topic ---
	want := []string{fromKey, viaKey, toKey}
	if len(v.Trace) != len(want) {
		t.Fatalf("trace = %v, want %v", v.Trace, want)
	}
	for i := range want {
		if v.Trace[i] != want[i] {
			t.Errorf("trace[%d] = %q, want %q", i, v.Trace[i], want[i])
		}
	}

	// --- Nodes: exactly the path nodes (group:none, no collapse) ---
	if len(v.Nodes) != 3 {
		t.Errorf("nodes = %d, want 3: %+v", len(v.Nodes), v.Nodes)
	}

	// --- Edges: only path edges; PUBLISHES_TOPIC hop must be present ---
	foundPublish := false
	for _, e := range v.Edges {
		if e.Kind == "PUBLISHES_TOPIC" && e.From == viaKey && e.To == toKey {
			foundPublish = true
		}
		// Every edge must connect consecutive trace nodes.
		pos := map[string]int{fromKey: 0, viaKey: 1, toKey: 2}
		pf, okF := pos[e.From]
		pt, okT := pos[e.To]
		if !okF || !okT || abs(pf-pt) != 1 {
			t.Errorf("edge %s -> %s (%s) is not a consecutive path hop", e.From, e.To, e.Kind)
		}
	}
	if !foundPublish {
		t.Errorf("PUBLISHES_TOPIC %s -> %s edge missing; edges: %+v", viaKey, toKey, v.Edges)
	}

	if v.Boundary != nil {
		t.Errorf("path view must have no boundary, got %+v", v.Boundary)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestBuildViewPathNoPath: two unconnected nodes — the view holds the two
// seeds with an empty Trace, and is NOT an error.
func TestBuildViewPathNoPath(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	keys := []string{"service:service:test:alpha", "service:service:test:omega"}
	for i, k := range keys {
		name := []string{"alpha", "omega"}[i]
		if _, err := st.UpsertNode(store.NodeRow{
			NodeKey: k, Layer: "service", NodeType: "service",
			DomainKey: "test", Name: name, Status: "active",
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
		}); err != nil {
			t.Fatalf("upsert %s: %v", k, err)
		}
	}

	v, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "test", Nodes: keys},
		Expand: &ExpandSpec{Mode: "path"},
		Group:  GroupSpec{By: "none"},
	})
	if err != nil {
		t.Fatalf("BuildView(no path) must not error: %v", err)
	}
	if len(v.Trace) != 0 {
		t.Errorf("trace = %v, want empty", v.Trace)
	}
	if len(v.Nodes) != 2 {
		t.Errorf("nodes = %d, want the 2 seeds: %+v", len(v.Nodes), v.Nodes)
	}
	if len(v.Edges) != 0 {
		t.Errorf("edges = %v, want none", v.Edges)
	}
	// rev3 task 2: a path view with no route says so, by display name.
	want := "No path between alpha and omega in the graph."
	if len(v.Notes) != 1 || v.Notes[0] != want {
		t.Errorf("notes = %v, want [%q]", v.Notes, want)
	}
	if !strings.Contains(RenderText(v), "note: "+want) {
		t.Errorf("RenderText should carry the note:\n%s", RenderText(v))
	}
}

// TestBuildViewPathRequiresTwoNodes: mode "path" with the wrong scope shape
// is a spec error.
func TestBuildViewPathRequiresTwoNodes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	_, err = BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "test", Nodes: []string{"only-one"}},
		Expand: &ExpandSpec{Mode: "path"},
	})
	if err == nil || !strings.Contains(err.Error(), "exactly two") {
		t.Errorf("expected exactly-two-keys error, got %v", err)
	}
}

// TestBuildViewPathEndpointToOtherService (rev3 task 1): an endpoint is no
// longer a path island. synthWithEndpoint (marks_test.go) wires
// contract:endpoint mkEndpoint <-EXPOSES_ENDPOINT- mkShopCtl -INJECTS->
// mkShopProv -CALLS_SERVICE-> mkMailSvc (a different service than the one
// that exposes the endpoint) on top of synthStore. The path must reach
// through the endpoint's handler, in request order, and the EXPOSES_ENDPOINT
// hop must draw the way the request actually travels (endpoint -> handler,
// "handled by") rather than the graph's stored hierarchy direction
// (handler -> endpoint).
func TestBuildViewPathEndpointToOtherService(t *testing.T) {
	st := synthWithEndpoint(t)

	v, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "d", Nodes: []string{mkEndpoint, mkMailSvc}},
		Expand: &ExpandSpec{Mode: "path"},
		Group:  GroupSpec{By: "none"},
	})
	if err != nil {
		t.Fatalf("BuildView(path): %v", err)
	}

	want := []string{mkEndpoint, mkShopCtl, mkShopProv, mkMailSvc}
	if len(v.Trace) != len(want) {
		t.Fatalf("trace = %v, want %v", v.Trace, want)
	}
	for i := range want {
		if v.Trace[i] != want[i] {
			t.Errorf("trace[%d] = %q, want %q", i, v.Trace[i], want[i])
		}
	}

	pos := map[string]int{}
	for i, k := range want {
		pos[k] = i
	}
	var handled *VEdge
	for i := range v.Edges {
		e := &v.Edges[i]
		pf, okF := pos[e.From]
		pt, okT := pos[e.To]
		if !okF || !okT || abs(pf-pt) != 1 {
			t.Errorf("edge %s -> %s (%s) is not a consecutive path hop", e.From, e.To, e.Kind)
		}
		if e.Kind == "EXPOSES_ENDPOINT" {
			handled = e
		}
	}
	if handled == nil {
		t.Fatalf("no EXPOSES_ENDPOINT arrow on the path; edges: %+v", v.Edges)
	}
	if handled.From != mkEndpoint || handled.To != mkShopCtl {
		t.Errorf("EXPOSES_ENDPOINT should draw endpoint -> handler, got %s -> %s", handled.From, handled.To)
	}
	if handled.Label != "handled by" {
		t.Errorf("EXPOSES_ENDPOINT label = %q, want %q", handled.Label, "handled by")
	}

	// This path is directionally honest end to end: no false "point back".
	if v.PathBackward || strings.Contains(v.Title, "↔") {
		t.Errorf("expected an honest one-way path, got PathBackward=%v title=%q", v.PathBackward, v.Title)
	}
	if strings.Contains(RenderText(v), "note: some arrows on this path point back") {
		t.Errorf("no backward note expected:\n%s", RenderText(v))
	}
}

// TestBuildViewPathContainsOnlyRouteNotFound (rev3 task 1): CONTAINS stays
// out of path search even though EXPOSES_ENDPOINT is now in it — a module
// and a member it only reaches via CONTAINS is not a path a request travels.
func TestBuildViewPathContainsOnlyRouteNotFound(t *testing.T) {
	st := synthStore(t)
	rev, err := st.CreateRevision("d", "", "contains-only", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	modKey := "code:module:d:isolated-mod"
	leafKey := "code:provider:d:isolated-mod/leaf"
	modID, err := st.UpsertNode(store.NodeRow{NodeKey: modKey, Layer: "code", NodeType: "module", DomainKey: "d",
		Name: "isolated-mod", Status: "active", Confidence: 0.8, Freshness: 1, TrustScore: 0.8, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	leafID, err := st.UpsertNode(store.NodeRow{NodeKey: leafKey, Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "leaf", Status: "active", Confidence: 0.8, Freshness: 1, TrustScore: 0.8, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: modKey + "->" + leafKey + ":CONTAINS", FromNodeID: modID, ToNodeID: leafID,
		EdgeType: "CONTAINS", DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9,
		Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}

	v, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "d", Nodes: []string{modKey, leafKey}},
		Expand: &ExpandSpec{Mode: "path"},
		Group:  GroupSpec{By: "none"},
	})
	if err != nil {
		t.Fatalf("BuildView(path): %v", err)
	}
	if len(v.Trace) != 0 {
		t.Errorf("trace = %v, want empty: CONTAINS must not be a path edge", v.Trace)
	}
	if len(v.Edges) != 0 {
		t.Errorf("edges = %+v, want none", v.Edges)
	}
}

// TestBuildViewPathBackwardEdgeIsHonest (rev3 task 1, rev2 follow-up): the
// only edge between the two path nodes is stored against the direction of
// travel (B depends on A, but the query asks path(A, B)). The default title
// must read "A <-> B" (not the dishonest "A -> B"), and RenderText must
// carry a note right after the header line.
func TestBuildViewPathBackwardEdgeIsHonest(t *testing.T) {
	st := synthStore(t)
	rev, err := st.CreateRevision("d", "", "backward-edge", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	aKey := "code:provider:d:back-a"
	bKey := "code:provider:d:back-b"
	aID, err := st.UpsertNode(store.NodeRow{NodeKey: aKey, Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "back-a", Status: "active", Confidence: 0.8, Freshness: 1, TrustScore: 0.8, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	bID, err := st.UpsertNode(store.NodeRow{NodeKey: bKey, Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "back-b", Status: "active", Confidence: 0.8, Freshness: 1, TrustScore: 0.8, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	// Stored B -> A (B depends on A); the query below asks path(A, B).
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: bKey + "->" + aKey + ":INJECTS", FromNodeID: bID, ToNodeID: aID,
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9,
		Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}

	v, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "d", Nodes: []string{aKey, bKey}},
		Expand: &ExpandSpec{Mode: "path"},
		Group:  GroupSpec{By: "none"},
	})
	if err != nil {
		t.Fatalf("BuildView(path): %v", err)
	}
	if len(v.Trace) != 2 || v.Trace[0] != aKey || v.Trace[1] != bKey {
		t.Fatalf("trace = %v, want [%s %s]", v.Trace, aKey, bKey)
	}
	if !v.PathBackward {
		t.Errorf("PathBackward should be true: the only edge points B -> A against an A -> B query")
	}
	if !strings.Contains(v.Title, "↔") {
		t.Errorf("title should read A <-> B, got %q", v.Title)
	}
	txt := RenderText(v)
	lines := strings.Split(txt, "\n")
	if len(lines) < 2 || lines[1] != "note: some arrows on this path point back" {
		t.Errorf("RenderText should carry the note right after the header line:\n%s", txt)
	}
}

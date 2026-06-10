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

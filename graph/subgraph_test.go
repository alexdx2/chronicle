package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

func TestSubgraphDepthBoth(t *testing.T) {
	g := setupGraphDefaults(t)
	seedABC(t, g) // a -> b -> c (INJECTS)

	res, err := g.Subgraph("code:provider:test-domain:nodeb", SubgraphOptions{Depth: 1, Direction: "both"})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if len(res.Nodes) != 3 {
		t.Fatalf("expected 3 nodes (a,b,c), got %d: %+v", len(res.Nodes), res.Nodes)
	}
	if len(res.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(res.Edges))
	}
	if res.Root != "code:provider:test-domain:nodeb" {
		t.Errorf("root mismatch: %q", res.Root)
	}
}

func TestSubgraphDirectionOut(t *testing.T) {
	g := setupGraphDefaults(t)
	seedABC(t, g)

	res, err := g.Subgraph("code:provider:test-domain:nodeb", SubgraphOptions{Depth: 2, Direction: "out"})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	keys := map[string]bool{}
	for _, n := range res.Nodes {
		keys[n.NodeKey] = true
	}
	if !keys["code:provider:test-domain:nodeb"] || !keys["code:module:test-domain:nodec"] {
		t.Fatalf("expected b and c, got %+v", keys)
	}
	if keys["code:controller:test-domain:nodea"] {
		t.Fatalf("direction=out must not include upstream node a: %+v", keys)
	}
}

func TestSubgraphMaxNodesTruncatesLowestTrust(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	root := validate.NodeInput{NodeKey: "code:module:test-domain:root", Layer: "code", NodeType: "module", DomainKey: "test-domain", Name: "Root"}
	if _, err := g.UpsertNode(root, revID); err != nil {
		t.Fatal(err)
	}
	// 5 children with varying edge confidence via derivation kinds.
	children := []struct {
		key  string
		kind string
	}{
		{"code:provider:test-domain:c1", "hard"},
		{"code:provider:test-domain:c2", "hard"},
		{"code:provider:test-domain:c3", "inferred"},
		{"code:provider:test-domain:c4", "inferred"},
		{"code:provider:test-domain:c5", "inferred"},
	}
	for _, c := range children {
		if _, err := g.UpsertNode(validate.NodeInput{NodeKey: c.key, Layer: "code", NodeType: "provider", DomainKey: "test-domain", Name: c.key[len(c.key)-2:]}, revID); err != nil {
			t.Fatal(err)
		}
		if _, err := g.UpsertEdge(validate.EdgeInput{
			FromNodeKey: root.NodeKey, ToNodeKey: c.key,
			EdgeType: "INJECTS", DerivationKind: c.kind,
			FromLayer: "code", ToLayer: "code",
		}, revID); err != nil {
			t.Fatal(err)
		}
	}

	res, err := g.Subgraph(root.NodeKey, SubgraphOptions{Depth: 1, Direction: "out", MaxNodes: 3})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if len(res.Nodes) != 3 { // root + 2 admitted children
		t.Fatalf("expected 3 nodes after truncation, got %d", len(res.Nodes))
	}
	if !res.Truncated {
		t.Fatal("expected Truncated=true")
	}
	if !strings.Contains(res.TruncatedNote, "3") {
		t.Errorf("note should state 3 dropped nodes, got %q", res.TruncatedNote)
	}
	// The two admitted children must be the hard (higher trust) ones.
	keys := map[string]bool{}
	for _, n := range res.Nodes {
		keys[n.NodeKey] = true
	}
	if !keys["code:provider:test-domain:c1"] || !keys["code:provider:test-domain:c2"] {
		t.Errorf("expected hard-derivation children kept, got %+v", keys)
	}
}

func TestSubgraphUnknownRoot(t *testing.T) {
	g := setupGraphDefaults(t)
	_, err := g.Subgraph("code:provider:test-domain:nope", SubgraphOptions{})
	if err == nil || !strings.Contains(err.Error(), "chronicle_node_search") {
		t.Fatalf("expected helpful not-found error, got %v", err)
	}
}

func TestSubgraphDefaults(t *testing.T) {
	g := setupGraphDefaults(t)
	seedABC(t, g)
	res, err := g.Subgraph("code:controller:test-domain:nodea", SubgraphOptions{})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	// Defaults: depth 2, both → a,b,c all reachable.
	if len(res.Nodes) != 3 {
		t.Fatalf("expected 3 nodes with defaults, got %d", len(res.Nodes))
	}
	if res.Truncated {
		t.Error("no truncation expected")
	}
}

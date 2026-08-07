package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// SQ-Contract 2 axis 2: edge type alone is insufficient evidence of runtime
// use. Dependency edges carry a dependency_source (Task 3): "code" (AST/
// evidence — runtime), "manifest" (declared but not necessarily used at
// runtime), "manifest_peer" (declared as a peer requirement — never treated
// as a real dependency by default). Consumers of DEPENDS_ON-shaped edges must
// gate on it. This file proves the two package-level helpers (IsRuntimeDep,
// IsDeclaredDep) are wired into every listed traversal/listing.

// TestIsRuntimeDepAndIsDeclaredDep proves the helpers' truth table directly,
// independent of any graph traversal: pro's Task 12 calls these two
// functions verbatim, so their behavior must be pinned regardless of how
// core happens to use them.
func TestIsRuntimeDepAndIsDeclaredDep(t *testing.T) {
	cases := []struct {
		source       string
		wantRuntime  bool
		wantDeclared bool
	}{
		{"", true, true}, // pre-column rows / non-dependency edges default to code
		{"code", true, true},
		{"manifest", false, true},
		{"manifest_peer", false, false},
	}
	for _, c := range cases {
		e := store.EdgeRow{DependencySource: c.source}
		if got := IsRuntimeDep(e); got != c.wantRuntime {
			t.Errorf("IsRuntimeDep(%q) = %v, want %v", c.source, got, c.wantRuntime)
		}
		if got := IsDeclaredDep(e); got != c.wantDeclared {
			t.Errorf("IsDeclaredDep(%q) = %v, want %v", c.source, got, c.wantDeclared)
		}
	}
}

// seedEligibilityFanIn builds: codeDep, manifestDep, peerDep --DEPENDS_ON--> target,
// one edge per dependency_source. Used to prove reverse-impact-shaped queries
// (QueryImpact) admit only the code-sourced dependent.
func seedEligibilityFanIn(t *testing.T) *Graph {
	t.Helper()
	g := setupGraphDefaults(t)
	revID, err := g.Store().CreateRevision("elig", "", "sha1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	nodes := []validate.NodeInput{
		{NodeKey: "code:module:elig:target", Layer: "code", NodeType: "module", DomainKey: "elig", Name: "Target"},
		{NodeKey: "code:module:elig:codedep", Layer: "code", NodeType: "module", DomainKey: "elig", Name: "CodeDep"},
		{NodeKey: "code:module:elig:manifestdep", Layer: "code", NodeType: "module", DomainKey: "elig", Name: "ManifestDep"},
		{NodeKey: "code:module:elig:peerdep", Layer: "code", NodeType: "module", DomainKey: "elig", Name: "PeerDep"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}

	edges := []validate.EdgeInput{
		{FromNodeKey: "code:module:elig:codedep", ToNodeKey: "code:module:elig:target", EdgeType: "DEPENDS_ON", DerivationKind: "hard", FromLayer: "code", ToLayer: "code", DependencySource: "code"},
		{FromNodeKey: "code:module:elig:manifestdep", ToNodeKey: "code:module:elig:target", EdgeType: "DEPENDS_ON", DerivationKind: "hard", FromLayer: "code", ToLayer: "code", DependencySource: "manifest"},
		{FromNodeKey: "code:module:elig:peerdep", ToNodeKey: "code:module:elig:target", EdgeType: "DEPENDS_ON", DerivationKind: "hard", FromLayer: "code", ToLayer: "code", DependencySource: "manifest_peer"},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge %s->%s: %v", e.FromNodeKey, e.ToNodeKey, err)
		}
	}
	return g
}

// seedEligibilityFanOut builds: consumer --DEPENDS_ON--> {codeTarget, manifestTarget,
// peerTarget}, one edge per dependency_source. Used to prove forward-shaped
// queries (QueryDeps, QueryPath, Subgraph) admit code+manifest, not peer.
func seedEligibilityFanOut(t *testing.T) *Graph {
	t.Helper()
	g := setupGraphDefaults(t)
	revID, err := g.Store().CreateRevision("elig", "", "sha1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	nodes := []validate.NodeInput{
		{NodeKey: "code:module:elig:consumer", Layer: "code", NodeType: "module", DomainKey: "elig", Name: "Consumer"},
		{NodeKey: "code:module:elig:codetarget", Layer: "code", NodeType: "module", DomainKey: "elig", Name: "CodeTarget"},
		{NodeKey: "code:module:elig:manifesttarget", Layer: "code", NodeType: "module", DomainKey: "elig", Name: "ManifestTarget"},
		{NodeKey: "code:module:elig:peertarget", Layer: "code", NodeType: "module", DomainKey: "elig", Name: "PeerTarget"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}

	edges := []validate.EdgeInput{
		{FromNodeKey: "code:module:elig:consumer", ToNodeKey: "code:module:elig:codetarget", EdgeType: "DEPENDS_ON", DerivationKind: "hard", FromLayer: "code", ToLayer: "code", DependencySource: "code"},
		{FromNodeKey: "code:module:elig:consumer", ToNodeKey: "code:module:elig:manifesttarget", EdgeType: "DEPENDS_ON", DerivationKind: "hard", FromLayer: "code", ToLayer: "code", DependencySource: "manifest"},
		{FromNodeKey: "code:module:elig:consumer", ToNodeKey: "code:module:elig:peertarget", EdgeType: "DEPENDS_ON", DerivationKind: "hard", FromLayer: "code", ToLayer: "code", DependencySource: "manifest_peer"},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge %s->%s: %v", e.FromNodeKey, e.ToNodeKey, err)
		}
	}
	return g
}

// (a) QueryImpact from the target reaches ONLY the code dependent.
func TestQueryImpactExcludesDeclaredOnlyDeps(t *testing.T) {
	g := seedEligibilityFanIn(t)
	result, err := g.QueryImpact("code:module:elig:target", ImpactOptions{MaxDepth: 4, MinScore: 0.0})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}

	seen := map[string]bool{}
	for _, imp := range result.Impacts {
		seen[imp.NodeKey] = true
	}
	if !seen["code:module:elig:codedep"] {
		t.Errorf("code-sourced dependent must be impacted; impacts: %+v", result.Impacts)
	}
	if seen["code:module:elig:manifestdep"] {
		t.Errorf("manifest-sourced dependent must NOT be impacted (declared-only, not runtime); impacts: %+v", result.Impacts)
	}
	if seen["code:module:elig:peerdep"] {
		t.Errorf("peer-sourced dependent must NOT be impacted; impacts: %+v", result.Impacts)
	}
	if len(result.Impacts) != 1 {
		t.Errorf("total impacted = %d, want exactly 1 (code only); impacts: %+v", len(result.Impacts), result.Impacts)
	}
}

// (b) QueryDeps from a consumer shows code+manifest, not peer; the manifest
// DepNode carries DependencySource "manifest".
func TestQueryDepsAdmitsDeclaredExcludesPeer(t *testing.T) {
	g := seedEligibilityFanOut(t)
	deps, err := g.QueryDeps("code:module:elig:consumer", 2, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}

	byKey := map[string]DepNode{}
	for _, d := range deps {
		byKey[d.NodeKey] = d
	}

	if _, ok := byKey["code:module:elig:codetarget"]; !ok {
		t.Errorf("code target must appear in deps; got %+v", deps)
	}
	manifestNode, ok := byKey["code:module:elig:manifesttarget"]
	if !ok {
		t.Fatalf("manifest target must appear in deps (declared, admitted); got %+v", deps)
	}
	if manifestNode.DependencySource != "manifest" {
		t.Errorf("manifest DepNode.DependencySource = %q, want %q", manifestNode.DependencySource, "manifest")
	}
	if _, ok := byKey["code:module:elig:peertarget"]; ok {
		t.Errorf("peer target must NOT appear in deps (peer excluded by default); got %+v", deps)
	}
}

// (c) QueryPath across the manifest edge returns a path whose PathEdge
// carries dependency_source "manifest".
func TestQueryPathCarriesDependencySource(t *testing.T) {
	g := seedEligibilityFanOut(t)
	result, err := g.QueryPath("code:module:elig:consumer", "code:module:elig:manifesttarget", PathOptions{MaxDepth: 4, TopK: 3, Mode: "directed"})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("paths = %d, want 1", len(result.Paths))
	}
	p := result.Paths[0]
	if len(p.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(p.Edges))
	}
	if p.Edges[0].DependencySource != "manifest" {
		t.Errorf("PathEdge.DependencySource = %q, want %q", p.Edges[0].DependencySource, "manifest")
	}

	// Path to the peer target must not exist at all: the per-edge gate
	// excludes it from traversal entirely, not merely from results.
	noPath, err := g.QueryPath("code:module:elig:consumer", "code:module:elig:peertarget", PathOptions{MaxDepth: 4, TopK: 3, Mode: "directed"})
	if err != nil {
		t.Fatalf("QueryPath to peer target: %v", err)
	}
	if len(noPath.Paths) != 0 {
		t.Errorf("path to peer target must not exist, got %+v", noPath.Paths)
	}
}

// (d) Subgraph excludes the peer edge (and the node it alone would reach).
func TestSubgraphExcludesPeerEdge(t *testing.T) {
	g := seedEligibilityFanOut(t)
	res, err := g.Subgraph("code:module:elig:consumer", SubgraphOptions{Depth: 1, Direction: "out"})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	nodeKeys := map[string]bool{}
	for _, n := range res.Nodes {
		nodeKeys[n.NodeKey] = true
	}
	if !nodeKeys["code:module:elig:codetarget"] || !nodeKeys["code:module:elig:manifesttarget"] {
		t.Errorf("code+manifest targets must be in subgraph; got nodes %+v", res.Nodes)
	}
	if nodeKeys["code:module:elig:peertarget"] {
		t.Errorf("peer target must NOT be reachable via the excluded peer edge; got nodes %+v", res.Nodes)
	}

	for _, e := range res.Edges {
		if e.To == "code:module:elig:peertarget" {
			t.Errorf("peer edge must be excluded from subgraph edges; got %+v", e)
		}
	}
}

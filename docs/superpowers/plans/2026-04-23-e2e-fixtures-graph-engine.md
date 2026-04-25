# E2E Fixtures + Graph Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add traversal policy, path queries, impact analysis, fixture project with golden graph, and comprehensive E2E tests to the Domain Oracle CLI.

**Architecture:** Extend the Registry with a TraversalPolicy that governs which edge types participate in path/impact. Add path.go (BFS directed/connected) and impact.go (reverse BFS with scoring) to internal/graph/. Create fixtures/orders-domain/ with NestJS source files and expected-graph.json. E2E test validates the full pipeline.

**Tech Stack:** Go 1.22+, existing packages (store, graph, registry, validate, cli, mcp)

---

### Task 1: Extend Registry with Traversal Policy

**Files:**
- Modify: `internal/registry/registry.go`
- Modify: `internal/registry/defaults.yaml`
- Modify: `internal/registry/registry_test.go`
- Modify: `testdata/registry/valid.yaml`

- [ ] **Step 1: Update testdata/registry/valid.yaml with traversal_policy**

Add to the end of `testdata/registry/valid.yaml`:

```yaml
traversal_policy:
  structural_edge_types:
    - CONTAINS
  no_reverse_impact:
    - EXPOSES_ENDPOINT
    - PUBLISHES_TOPIC
```

Note: The test fixture only has INJECTS, EXPOSES_ENDPOINT, PUBLISHES_TOPIC as edge types. CONTAINS is not yet an edge type in this test fixture — add it:

Under `edge_types:` in `testdata/registry/valid.yaml`, add:

```yaml
  CONTAINS:
    from_layers: [code, service]
    to_layers: [code, service, contract]
  CALLS_ENDPOINT:
    from_layers: [code, service]
    to_layers: [contract]
  CALLS_SERVICE:
    from_layers: [code, service]
    to_layers: [service]
```

- [ ] **Step 2: Write failing tests for TraversalPolicy**

Add to `internal/registry/registry_test.go`:

```go
func TestTraversalPolicy(t *testing.T) {
	r, err := LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	policy := r.TraversalPolicy()

	// CONTAINS is structural
	if !policy.IsStructural("CONTAINS") {
		t.Error("expected CONTAINS to be structural")
	}
	// INJECTS is not structural
	if policy.IsStructural("INJECTS") {
		t.Error("expected INJECTS to not be structural")
	}
	// EXPOSES_ENDPOINT has no reverse impact
	if policy.AllowsReverseImpact("EXPOSES_ENDPOINT") {
		t.Error("expected EXPOSES_ENDPOINT to not allow reverse impact")
	}
	// PUBLISHES_TOPIC has no reverse impact
	if policy.AllowsReverseImpact("PUBLISHES_TOPIC") {
		t.Error("expected PUBLISHES_TOPIC to not allow reverse impact")
	}
	// INJECTS allows reverse impact
	if !policy.AllowsReverseImpact("INJECTS") {
		t.Error("expected INJECTS to allow reverse impact")
	}
	// INJECTS allows forward path
	if !policy.AllowsForwardPath("INJECTS") {
		t.Error("expected INJECTS to allow forward path")
	}
	// CONTAINS does not allow forward path (structural)
	if policy.AllowsForwardPath("CONTAINS") {
		t.Error("expected CONTAINS to not allow forward path")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/registry/ -v -run TestTraversalPolicy
```

Expected: FAIL — `TraversalPolicy` method doesn't exist.

- [ ] **Step 4: Implement TraversalPolicy in registry.go**

Add to `internal/registry/registry.go`:

```go
type TraversalPolicyDef struct {
	StructuralEdgeTypes []string `yaml:"structural_edge_types"`
	NoReverseImpact     []string `yaml:"no_reverse_impact"`
}

type TraversalPolicy struct {
	structural      map[string]bool
	noReverseImpact map[string]bool
}

// IsStructural returns true if the edge type is structural (excluded from path/impact by default).
func (p *TraversalPolicy) IsStructural(edgeType string) bool {
	return p.structural[edgeType]
}

// AllowsForwardPath returns true if the edge type can be traversed in forward direction for path queries.
// Structural edges return false.
func (p *TraversalPolicy) AllowsForwardPath(edgeType string) bool {
	return !p.structural[edgeType]
}

// AllowsReverseImpact returns true if the edge type participates in reverse impact analysis.
// Structural edges and no_reverse_impact edges return false.
func (p *TraversalPolicy) AllowsReverseImpact(edgeType string) bool {
	return !p.structural[edgeType] && !p.noReverseImpact[edgeType]
}
```

Update `RegistryFile` to include:

```go
type RegistryFile struct {
	// ... existing fields ...
	TraversalPolicyDef *TraversalPolicyDef `yaml:"traversal_policy"`
}
```

Update `Registry` to store the policy:

```go
type Registry struct {
	// ... existing fields ...
	traversalPolicy *TraversalPolicy
}
```

In `Load()`, after existing parsing, add:

```go
policy := &TraversalPolicy{
	structural:      make(map[string]bool),
	noReverseImpact: make(map[string]bool),
}
if f.TraversalPolicyDef != nil {
	policy.structural = toSet(f.TraversalPolicyDef.StructuralEdgeTypes)
	policy.noReverseImpact = toSet(f.TraversalPolicyDef.NoReverseImpact)
}
r.traversalPolicy = policy
```

Add method:

```go
func (r *Registry) TraversalPolicy() *TraversalPolicy {
	return r.traversalPolicy
}
```

- [ ] **Step 5: Update defaults.yaml**

Add to the end of `internal/registry/defaults.yaml`:

```yaml
traversal_policy:
  structural_edge_types:
    - CONTAINS
    - DEPLOYS_AS
    - SELECTS_PODS
    - OWNED_BY
    - MAINTAINED_BY
    - DEPENDS_ON_DOMAIN
    - PART_OF_FLOW
    - BUILDS_ARTIFACT
    - DEPLOYS_RESOURCE
  no_reverse_impact:
    - EXPOSES_ENDPOINT
    - PUBLISHES_TOPIC
    - TRIGGERS_ANALYSIS
    - EMITS_AFTER
```

- [ ] **Step 6: Run all registry tests**

```bash
go test ./internal/registry/ -v
```

Expected: all tests PASS including TestTraversalPolicy and TestLoadDefaults.

- [ ] **Step 7: Commit**

```bash
git add internal/registry/ testdata/registry/
git commit -m "feat: add traversal policy to type registry"
```

---

### Task 2: Path Query

**Files:**
- Create: `internal/graph/path.go`
- Create: `internal/graph/path_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/graph/path_test.go`:

```go
package graph

import (
	"testing"

	"github.com/anthropics/depbot/internal/validate"
)

// seedPathGraph creates: A -INJECTS-> B -INJECTS-> C -CALLS_SERVICE(linked)-> D
// Also adds structural edge: M -CONTAINS-> A
func seedPathGraph(t *testing.T) *Graph {
	t.Helper()
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	g.UpsertNode(validate.NodeInput{NodeKey: "code:module:orders:m", Layer: "code", NodeType: "module", DomainKey: "orders", Name: "M"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:controller:orders:a", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "A"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:orders:c", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "service:service:orders:d", Layer: "service", NodeType: "service", DomainKey: "orders", Name: "D"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "contract:endpoint:orders:post:/x", Layer: "contract", NodeType: "endpoint", DomainKey: "orders", Name: "POST /x"}, revID)

	// Structural edge (CONTAINS) — should be excluded by default
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:module:orders:m", ToNodeKey: "code:controller:orders:a", EdgeType: "CONTAINS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	// Dependency edges
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:controller:orders:a", ToNodeKey: "code:provider:orders:b", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:provider:orders:b", ToNodeKey: "code:provider:orders:c", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:provider:orders:c", ToNodeKey: "service:service:orders:d", EdgeType: "CALLS_SERVICE", DerivationKind: "linked", FromLayer: "code", ToLayer: "service"}, revID)
	// No-reverse-impact edge
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:controller:orders:a", ToNodeKey: "contract:endpoint:orders:post:/x", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"}, revID)

	return g
}

func TestQueryPathDirected(t *testing.T) {
	g := seedPathGraph(t)

	result, err := g.QueryPath("code:controller:orders:a", "service:service:orders:d", PathOptions{
		MaxDepth: 6,
		TopK:     3,
		Mode:     "directed",
	})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("paths = %d, want 1", len(result.Paths))
	}
	p := result.Paths[0]
	if p.Depth != 3 {
		t.Errorf("depth = %d, want 3", p.Depth)
	}
	if len(p.Nodes) != 4 {
		t.Errorf("nodes = %d, want 4", len(p.Nodes))
	}
	if p.PathScore <= 0 || p.PathScore > 1 {
		t.Errorf("path_score = %f, want (0, 1]", p.PathScore)
	}
}

func TestQueryPathNoPath(t *testing.T) {
	g := seedPathGraph(t)

	// D -> A has no directed path
	result, err := g.QueryPath("service:service:orders:d", "code:controller:orders:a", PathOptions{
		MaxDepth: 6,
		TopK:     3,
		Mode:     "directed",
	})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 0 {
		t.Errorf("paths = %d, want 0 (no directed path)", len(result.Paths))
	}
}

func TestQueryPathConnected(t *testing.T) {
	g := seedPathGraph(t)

	// D -> A has no directed path, but in connected mode it should find one
	result, err := g.QueryPath("service:service:orders:d", "code:controller:orders:a", PathOptions{
		MaxDepth: 6,
		TopK:     3,
		Mode:     "connected",
	})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) == 0 {
		t.Error("expected at least 1 path in connected mode")
	}
}

func TestQueryPathExcludesStructural(t *testing.T) {
	g := seedPathGraph(t)

	// M -CONTAINS-> A. Path from M to B should NOT exist (CONTAINS is structural)
	result, err := g.QueryPath("code:module:orders:m", "code:provider:orders:b", PathOptions{
		MaxDepth: 6,
		TopK:     3,
		Mode:     "directed",
	})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 0 {
		t.Errorf("paths = %d, want 0 (CONTAINS is structural)", len(result.Paths))
	}
}

func TestQueryPathIncludeStructural(t *testing.T) {
	g := seedPathGraph(t)

	// With IncludeStructural, M -> A -> B should work
	result, err := g.QueryPath("code:module:orders:m", "code:provider:orders:b", PathOptions{
		MaxDepth:           6,
		TopK:               3,
		Mode:               "directed",
		IncludeStructural:  true,
	})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Errorf("paths = %d, want 1 with structural included", len(result.Paths))
	}
}

func TestQueryPathDerivationFilter(t *testing.T) {
	g := seedPathGraph(t)

	// A -> D path has a "linked" edge (CALLS_SERVICE). Filter to hard only = no path.
	result, err := g.QueryPath("code:controller:orders:a", "service:service:orders:d", PathOptions{
		MaxDepth:         6,
		TopK:             3,
		Mode:             "directed",
		DerivationFilter: []string{"hard"},
	})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) != 0 {
		t.Errorf("paths = %d, want 0 (linked edge filtered out)", len(result.Paths))
	}

	// With hard+linked = path exists
	result2, err := g.QueryPath("code:controller:orders:a", "service:service:orders:d", PathOptions{
		MaxDepth:         6,
		TopK:             3,
		Mode:             "directed",
		DerivationFilter: []string{"hard", "linked"},
	})
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result2.Paths) != 1 {
		t.Errorf("paths = %d, want 1", len(result2.Paths))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/graph/ -v -run TestQueryPath
```

Expected: FAIL — QueryPath doesn't exist.

- [ ] **Step 3: Implement path query**

Create `internal/graph/path.go`:

```go
package graph

import (
	"math"
	"sort"

	"github.com/anthropics/depbot/internal/store"
)

type PathOptions struct {
	MaxDepth          int
	TopK              int
	Mode              string   // "directed" or "connected"
	DerivationFilter  []string
	IncludeStructural bool
}

type PathEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	EdgeType   string `json:"type"`
	Derivation string `json:"derivation"`
}

type Path struct {
	Nodes     []string   `json:"nodes"`
	Edges     []PathEdge `json:"edges"`
	Depth     int        `json:"depth"`
	PathScore float64    `json:"path_score"`
	PathCost  float64    `json:"path_cost"`
}

type PathResult struct {
	From            string `json:"from"`
	To              string `json:"to"`
	Mode            string `json:"mode"`
	Paths           []Path `json:"paths"`
	TotalPathsFound int    `json:"total_paths_found"`
}

func (g *Graph) QueryPath(fromKey, toKey string, opts PathOptions) (*PathResult, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 6
	}
	if opts.TopK <= 0 {
		opts.TopK = 3
	}
	if opts.Mode == "" {
		opts.Mode = "directed"
	}

	fromNode, err := g.store.GetNodeByKey(fromKey)
	if err != nil {
		return nil, err
	}
	toNode, err := g.store.GetNodeByKey(toKey)
	if err != nil {
		return nil, err
	}

	policy := g.reg.TraversalPolicy()
	filterSet := toStringSet(opts.DerivationFilter)

	type bfsState struct {
		nodeID int64
		path   []int64
		edges  []store.EdgeRow
	}

	var foundPaths []Path
	queue := []bfsState{{nodeID: fromNode.NodeID, path: []int64{fromNode.NodeID}, edges: nil}}

	// BFS with full path tracking
	for len(queue) > 0 && len(foundPaths) < opts.TopK*10 { // explore up to 10x TopK candidates
		state := queue[0]
		queue = queue[1:]

		if len(state.path)-1 >= opts.MaxDepth {
			continue
		}

		// Get edges from current node
		var edgeSets [][]store.EdgeRow

		// Forward edges
		active := true
		fwdEdges, _ := g.store.ListEdges(store.EdgeFilter{FromNodeID: state.nodeID, Active: &active})
		edgeSets = append(edgeSets, fwdEdges)

		// In connected mode, also follow reverse edges
		if opts.Mode == "connected" {
			revEdges, _ := g.store.ListEdges(store.EdgeFilter{ToNodeID: state.nodeID, Active: &active})
			edgeSets = append(edgeSets, revEdges)
		}

		for setIdx, edges := range edgeSets {
			for _, edge := range edges {
				// Apply traversal policy
				if !opts.IncludeStructural && policy.IsStructural(edge.EdgeType) {
					continue
				}
				// Apply derivation filter
				if len(filterSet) > 0 && !filterSet[edge.DerivationKind] {
					continue
				}

				var nextID int64
				if setIdx == 0 { // forward
					nextID = edge.ToNodeID
				} else { // reverse (connected mode)
					nextID = edge.FromNodeID
				}

				// Check for cycles
				if inPath(state.path, nextID) {
					continue
				}

				newPath := append(append([]int64{}, state.path...), nextID)
				newEdges := append(append([]store.EdgeRow{}, state.edges...), edge)

				if nextID == toNode.NodeID {
					// Found a path
					p, err := g.buildPath(newPath, newEdges)
					if err == nil {
						foundPaths = append(foundPaths, p)
					}
					continue
				}

				queue = append(queue, bfsState{nodeID: nextID, path: newPath, edges: newEdges})
			}
		}
	}

	// Sort by cost (ascending), then depth, then lexicographic
	sort.Slice(foundPaths, func(i, j int) bool {
		if foundPaths[i].PathCost != foundPaths[j].PathCost {
			return foundPaths[i].PathCost < foundPaths[j].PathCost
		}
		if foundPaths[i].Depth != foundPaths[j].Depth {
			return foundPaths[i].Depth < foundPaths[j].Depth
		}
		// Lexicographic tie-break on first differing node
		for k := 0; k < len(foundPaths[i].Nodes) && k < len(foundPaths[j].Nodes); k++ {
			if foundPaths[i].Nodes[k] != foundPaths[j].Nodes[k] {
				return foundPaths[i].Nodes[k] < foundPaths[j].Nodes[k]
			}
		}
		return len(foundPaths[i].Nodes) < len(foundPaths[j].Nodes)
	})

	// Take top-k
	topK := foundPaths
	if len(topK) > opts.TopK {
		topK = topK[:opts.TopK]
	}

	return &PathResult{
		From:            fromKey,
		To:              toKey,
		Mode:            opts.Mode,
		Paths:           topK,
		TotalPathsFound: len(foundPaths),
	}, nil
}

func (g *Graph) buildPath(nodeIDs []int64, edges []store.EdgeRow) (Path, error) {
	nodes := make([]string, len(nodeIDs))
	for i, id := range nodeIDs {
		n, err := g.store.GetNodeByID(id)
		if err != nil {
			return Path{}, err
		}
		nodes[i] = n.NodeKey
	}

	pathEdges := make([]PathEdge, len(edges))
	var costSum float64
	scoreProduct := 1.0
	for i, e := range edges {
		fromNode, _ := g.store.GetNodeByID(e.FromNodeID)
		toNode, _ := g.store.GetNodeByID(e.ToNodeID)
		pathEdges[i] = PathEdge{
			From:       fromNode.NodeKey,
			To:         toNode.NodeKey,
			EdgeType:   e.EdgeType,
			Derivation: e.DerivationKind,
		}
		scoreProduct *= e.Confidence
		if e.Confidence > 0 {
			costSum += -math.Log(e.Confidence)
		}
	}

	depth := len(edges)
	depthDecay := math.Pow(0.95, float64(depth-1))
	pathScore := scoreProduct * depthDecay
	pathCost := costSum + 0.05*float64(depth)

	return Path{
		Nodes:     nodes,
		Edges:     pathEdges,
		Depth:     depth,
		PathScore: math.Round(pathScore*10000) / 10000,
		PathCost:  math.Round(pathCost*10000) / 10000,
	}, nil
}

func inPath(path []int64, id int64) bool {
	for _, p := range path {
		if p == id {
			return true
		}
	}
	return false
}

func toStringSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/graph/ -v -run TestQueryPath
```

Expected: all 6 path tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/graph/path.go internal/graph/path_test.go
git commit -m "feat: add path query with directed/connected modes and traversal policy"
```

---

### Task 3: Impact Query

**Files:**
- Create: `internal/graph/impact.go`
- Create: `internal/graph/impact_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/graph/impact_test.go`:

```go
package graph

import (
	"testing"
)

func TestImpactBasic(t *testing.T) {
	g := seedPathGraph(t)

	// C changes → B impacted (depth 1), A impacted (depth 2)
	result, err := g.QueryImpact("code:provider:orders:c", ImpactOptions{
		MaxDepth: 4,
		MinScore: 0.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if result.TotalImpacted < 2 {
		t.Errorf("impacted = %d, want >= 2", result.TotalImpacted)
	}

	// First result should be B (closer = higher score)
	if len(result.Impacts) < 2 {
		t.Fatalf("impacts = %d, want >= 2", len(result.Impacts))
	}
	if result.Impacts[0].NodeKey != "code:provider:orders:b" {
		t.Errorf("first impacted = %q, want code:provider:orders:b", result.Impacts[0].NodeKey)
	}
	if result.Impacts[0].Depth != 1 {
		t.Errorf("first depth = %d, want 1", result.Impacts[0].Depth)
	}
	if result.Impacts[1].NodeKey != "code:controller:orders:a" {
		t.Errorf("second impacted = %q, want code:controller:orders:a", result.Impacts[1].NodeKey)
	}
	// Closer node has higher score
	if result.Impacts[0].ImpactScore <= result.Impacts[1].ImpactScore {
		t.Errorf("closer node should have higher score: %f <= %f", result.Impacts[0].ImpactScore, result.Impacts[1].ImpactScore)
	}
}

func TestImpactExcludesContains(t *testing.T) {
	g := seedPathGraph(t)

	// A changes. M -CONTAINS-> A exists, but CONTAINS should NOT cause M to be impacted.
	result, err := g.QueryImpact("code:controller:orders:a", ImpactOptions{
		MaxDepth: 4,
		MinScore: 0.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}

	for _, imp := range result.Impacts {
		if imp.NodeKey == "code:module:orders:m" {
			t.Error("M should NOT be impacted via CONTAINS (structural edge)")
		}
	}
}

func TestImpactExcludesExposesEndpoint(t *testing.T) {
	g := seedPathGraph(t)

	// POST /x endpoint changes. A -EXPOSES_ENDPOINT-> POST /x, but no reverse impact.
	result, err := g.QueryImpact("contract:endpoint:orders:post:/x", ImpactOptions{
		MaxDepth: 4,
		MinScore: 0.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}

	for _, imp := range result.Impacts {
		if imp.NodeKey == "code:controller:orders:a" {
			t.Error("A should NOT be reverse-impacted via EXPOSES_ENDPOINT")
		}
	}
}

func TestImpactMinScore(t *testing.T) {
	g := seedPathGraph(t)

	// With min_score very high, fewer results
	result, err := g.QueryImpact("code:provider:orders:c", ImpactOptions{
		MaxDepth: 4,
		MinScore: 99.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	// Only depth-1 with full confidence should pass
	if result.TotalImpacted > 1 {
		t.Errorf("impacted = %d, want <= 1 with high min_score", result.TotalImpacted)
	}
}

func TestImpactDerivationFilter(t *testing.T) {
	g := seedPathGraph(t)

	// D changes. Reverse traversal needs CALLS_SERVICE (linked) to reach C.
	// With hard-only filter, D should have no impact (CALLS_SERVICE is linked).
	result, err := g.QueryImpact("service:service:orders:d", ImpactOptions{
		MaxDepth:         4,
		MinScore:         0.0,
		DerivationFilter: []string{"hard"},
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if result.TotalImpacted != 0 {
		t.Errorf("impacted = %d, want 0 (linked edges filtered out)", result.TotalImpacted)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/graph/ -v -run TestImpact
```

Expected: FAIL — QueryImpact doesn't exist.

- [ ] **Step 3: Implement impact query**

Create `internal/graph/impact.go`:

```go
package graph

import (
	"math"
	"sort"

	"github.com/anthropics/depbot/internal/store"
)

type ImpactOptions struct {
	MaxDepth         int
	MinScore         float64
	TopK             int
	DerivationFilter []string
	IncludeStructural bool
}

type ImpactEntry struct {
	NodeKey     string   `json:"node_key"`
	Name        string   `json:"name"`
	Layer       string   `json:"layer"`
	NodeType    string   `json:"node_type"`
	Depth       int      `json:"depth"`
	ImpactScore float64  `json:"impact_score"`
	Path        []string `json:"path"`
	EdgeTypes   []string `json:"edge_types"`
}

type ImpactResult struct {
	ChangedNode     string        `json:"changed_node"`
	Impacts         []ImpactEntry `json:"impacts"`
	TotalImpacted   int           `json:"total_impacted"`
	MaxDepthReached int           `json:"max_depth_reached"`
}

func (g *Graph) QueryImpact(nodeKey string, opts ImpactOptions) (*ImpactResult, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 4
	}
	if opts.TopK <= 0 {
		opts.TopK = 50
	}

	startNode, err := g.store.GetNodeByKey(nodeKey)
	if err != nil {
		return nil, err
	}

	policy := g.reg.TraversalPolicy()
	filterSet := toStringSet(opts.DerivationFilter)

	type bfsState struct {
		nodeID    int64
		depth     int
		path      []int64
		edgeTypes []string
		scoreProduct float64
	}

	visited := map[int64]bool{startNode.NodeID: true}
	queue := []bfsState{{
		nodeID:       startNode.NodeID,
		depth:        0,
		path:         []int64{startNode.NodeID},
		edgeTypes:    nil,
		scoreProduct: 1.0,
	}}

	var impacts []ImpactEntry
	maxDepthReached := 0

	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]

		if state.depth >= opts.MaxDepth {
			continue
		}

		// Reverse traversal: find edges where this node is the TO side
		active := true
		edges, err := g.store.ListEdges(store.EdgeFilter{ToNodeID: state.nodeID, Active: &active})
		if err != nil {
			continue
		}

		for _, edge := range edges {
			// Apply traversal policy — only edges that allow reverse impact
			if !opts.IncludeStructural && !policy.AllowsReverseImpact(edge.EdgeType) {
				continue
			}
			// Apply derivation filter
			if len(filterSet) > 0 && !filterSet[edge.DerivationKind] {
				continue
			}

			nextID := edge.FromNodeID
			if visited[nextID] {
				continue
			}
			visited[nextID] = true

			nextDepth := state.depth + 1
			nextScoreProduct := state.scoreProduct * edge.Confidence
			nextPath := append(append([]int64{}, state.path...), nextID)
			nextEdgeTypes := append(append([]string{}, state.edgeTypes...), edge.EdgeType)

			if nextDepth > maxDepthReached {
				maxDepthReached = nextDepth
			}

			// Compute impact score
			depthDecay := math.Pow(0.95, float64(nextDepth-1))
			impactScore := 100.0 * nextScoreProduct * depthDecay
			impactScore = math.Round(impactScore*100) / 100

			if impactScore < opts.MinScore {
				continue
			}

			node, err := g.store.GetNodeByID(nextID)
			if err != nil {
				continue
			}

			// Build path node keys
			pathKeys := make([]string, len(nextPath))
			for i, id := range nextPath {
				n, _ := g.store.GetNodeByID(id)
				pathKeys[i] = n.NodeKey
			}

			impacts = append(impacts, ImpactEntry{
				NodeKey:     node.NodeKey,
				Name:        node.Name,
				Layer:       node.Layer,
				NodeType:    node.NodeType,
				Depth:       nextDepth,
				ImpactScore: impactScore,
				Path:        pathKeys,
				EdgeTypes:   nextEdgeTypes,
			})

			queue = append(queue, bfsState{
				nodeID:       nextID,
				depth:        nextDepth,
				path:         nextPath,
				edgeTypes:    nextEdgeTypes,
				scoreProduct: nextScoreProduct,
			})
		}
	}

	// Sort by score descending
	sort.Slice(impacts, func(i, j int) bool {
		if impacts[i].ImpactScore != impacts[j].ImpactScore {
			return impacts[i].ImpactScore > impacts[j].ImpactScore
		}
		return impacts[i].Depth < impacts[j].Depth
	})

	// Apply top-k
	if len(impacts) > opts.TopK {
		impacts = impacts[:opts.TopK]
	}

	return &ImpactResult{
		ChangedNode:     nodeKey,
		Impacts:         impacts,
		TotalImpacted:   len(impacts),
		MaxDepthReached: maxDepthReached,
	}, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/graph/ -v -run TestImpact
```

Expected: all 5 impact tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/graph/impact.go internal/graph/impact_test.go
git commit -m "feat: add impact query with traversal policy and scoring"
```

---

### Task 4: Stats Enhancement

**Files:**
- Modify: `internal/graph/query.go`
- Modify: `internal/graph/query_test.go`

- [ ] **Step 1: Add EdgesByDerivation to Stats**

In `internal/graph/query.go`, update the `Stats` struct:

```go
type Stats struct {
	NodeCount        int            `json:"node_count"`
	EdgeCount        int            `json:"edge_count"`
	NodesByLayer     map[string]int `json:"nodes_by_layer"`
	EdgesByType      map[string]int `json:"edges_by_type"`
	EdgesByDerivation map[string]int `json:"edges_by_derivation"`
	ActiveNodes      int            `json:"active_nodes"`
	StaleNodes       int            `json:"stale_nodes"`
}
```

In `QueryStats`, initialize the new map and populate it:

```go
stats.EdgesByDerivation = make(map[string]int)
```

And in the edge loop:

```go
stats.EdgesByDerivation[e.DerivationKind]++
```

- [ ] **Step 2: Add test**

Add to `internal/graph/query_test.go`:

```go
func TestQueryStatsDerivation(t *testing.T) {
	g := seedGraphForQuery(t)
	stats, err := g.QueryStats("orders")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.EdgesByDerivation == nil {
		t.Fatal("EdgesByDerivation is nil")
	}
	if stats.EdgesByDerivation["hard"] != 2 {
		t.Errorf("hard edges = %d, want 2", stats.EdgesByDerivation["hard"])
	}
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/graph/ -v -run TestQueryStats
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/graph/query.go internal/graph/query_test.go
git commit -m "feat: add derivation breakdown to graph stats"
```

---

### Task 5: CLI + MCP — Path and Impact Commands

**Files:**
- Modify: `internal/cli/query.go`
- Create: `internal/cli/impact.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/mcp/server.go`

- [ ] **Step 1: Add path subcommand to query.go**

Add to `internal/cli/query.go` in `newQueryCmd()`:

```go
pathCmd := &cobra.Command{
	Use:   "path [from_node_key] [to_node_key]",
	Short: "Find paths between two nodes",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		g := openGraph()
		defer g.Store().Close()
		maxDepth, _ := cmd.Flags().GetInt("max-depth")
		topK, _ := cmd.Flags().GetInt("top-k")
		mode, _ := cmd.Flags().GetString("mode")
		derivation, _ := cmd.Flags().GetString("derivation")
		includeStructural, _ := cmd.Flags().GetBool("include-structural")
		var filter []string
		if derivation != "" {
			filter = strings.Split(derivation, ",")
		}
		result, err := g.QueryPath(args[0], args[1], graph.PathOptions{
			MaxDepth:          maxDepth,
			TopK:              topK,
			Mode:              mode,
			DerivationFilter:  filter,
			IncludeStructural: includeStructural,
		})
		if err != nil {
			outputError(err)
		}
		outputJSON(result)
	},
}
pathCmd.Flags().Int("max-depth", 6, "Max traversal depth")
pathCmd.Flags().Int("top-k", 3, "Max paths to return")
pathCmd.Flags().String("mode", "directed", "Traversal mode: directed or connected")
pathCmd.Flags().String("derivation", "", "Comma-separated derivation filter")
pathCmd.Flags().Bool("include-structural", false, "Include structural edges (CONTAINS, etc.)")
```

Add `pathCmd` to the `cmd.AddCommand(...)` call.

Add `"github.com/anthropics/depbot/internal/graph"` to imports if not already present.

- [ ] **Step 2: Create impact CLI command**

Create `internal/cli/impact.go`:

```go
package cli

import (
	"strings"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/spf13/cobra"
)

func newImpactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact [node_key]",
		Short: "Analyze impact of a node change",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			depth, _ := cmd.Flags().GetInt("depth")
			derivation, _ := cmd.Flags().GetString("derivation")
			minScore, _ := cmd.Flags().GetFloat64("min-score")
			topK, _ := cmd.Flags().GetInt("top-k")
			includeStructural, _ := cmd.Flags().GetBool("include-structural")

			var filter []string
			if derivation != "" {
				filter = strings.Split(derivation, ",")
			}

			result, err := g.QueryImpact(args[0], graph.ImpactOptions{
				MaxDepth:          depth,
				MinScore:          minScore,
				TopK:              topK,
				DerivationFilter:  filter,
				IncludeStructural: includeStructural,
			})
			if err != nil {
				outputError(err)
			}
			outputJSON(result)
		},
	}

	cmd.Flags().Int("depth", 4, "Max traversal depth")
	cmd.Flags().String("derivation", "", "Comma-separated derivation filter")
	cmd.Flags().Float64("min-score", 0.1, "Minimum impact score")
	cmd.Flags().Int("top-k", 50, "Max results")
	cmd.Flags().Bool("include-structural", false, "Include structural edges")

	return cmd
}
```

- [ ] **Step 3: Register impact command in root.go**

In `internal/cli/root.go`, add `newImpactCmd()` to the `root.AddCommand(...)` list.

- [ ] **Step 4: Add MCP tools for path and impact**

Add to `internal/mcp/server.go`:

```go
// In NewServer(), add:
s.AddTool(queryPathTool(), queryPathHandler(g))
s.AddTool(impactTool(), impactHandler(g))
```

```go
func queryPathTool() mcp.Tool {
	return mcp.NewTool("oracle_query_path",
		mcp.WithDescription("Find paths between two nodes in the graph"),
		mcp.WithString("from_node_key", mcp.Required(), mcp.Description("Source node key")),
		mcp.WithString("to_node_key", mcp.Required(), mcp.Description("Target node key")),
		mcp.WithNumber("max_depth", mcp.Description("Max depth (default 6)")),
		mcp.WithNumber("top_k", mcp.Description("Max paths (default 3)")),
		mcp.WithString("mode", mcp.Description("directed or connected (default directed)")),
		mcp.WithString("derivation", mcp.Description("Comma-separated derivation filter")),
		mcp.WithBoolean("include_structural", mcp.Description("Include structural edges")),
	)
}

func queryPathHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		maxDepth := intParam(args, "max_depth")
		if maxDepth == 0 { maxDepth = 6 }
		topK := intParam(args, "top_k")
		if topK == 0 { topK = 3 }
		mode := strParam(args, "mode")
		if mode == "" { mode = "directed" }
		var filter []string
		if d := strParam(args, "derivation"); d != "" {
			for _, s := range strings.Split(d, ",") {
				filter = append(filter, strings.TrimSpace(s))
			}
		}
		inclStructural, _ := args["include_structural"].(bool)
		result, err := g.QueryPath(strParam(args, "from_node_key"), strParam(args, "to_node_key"), graph.PathOptions{
			MaxDepth: maxDepth, TopK: topK, Mode: mode,
			DerivationFilter: filter, IncludeStructural: inclStructural,
		})
		if err != nil { return errorResult(err), nil }
		return jsonResult(result), nil
	}
}

func impactTool() mcp.Tool {
	return mcp.NewTool("oracle_impact",
		mcp.WithDescription("Analyze impact of a node change via reverse dependency traversal"),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Changed node key")),
		mcp.WithNumber("depth", mcp.Description("Max depth (default 4)")),
		mcp.WithString("derivation", mcp.Description("Comma-separated derivation filter")),
		mcp.WithNumber("min_score", mcp.Description("Minimum impact score (default 0.1)")),
		mcp.WithNumber("top_k", mcp.Description("Max results (default 50)")),
		mcp.WithBoolean("include_structural", mcp.Description("Include structural edges")),
	)
}

func impactHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		depth := intParam(args, "depth")
		if depth == 0 { depth = 4 }
		topK := intParam(args, "top_k")
		if topK == 0 { topK = 50 }
		minScore := float64Param(args, "min_score")
		var filter []string
		if d := strParam(args, "derivation"); d != "" {
			for _, s := range strings.Split(d, ",") {
				filter = append(filter, strings.TrimSpace(s))
			}
		}
		inclStructural, _ := args["include_structural"].(bool)
		result, err := g.QueryImpact(strParam(args, "node_key"), graph.ImpactOptions{
			MaxDepth: depth, MinScore: minScore, TopK: topK,
			DerivationFilter: filter, IncludeStructural: inclStructural,
		})
		if err != nil { return errorResult(err), nil }
		return jsonResult(result), nil
	}
}
```

Add `"strings"` to imports in server.go if not already there.

- [ ] **Step 5: Build and verify**

```bash
go build -o oracle ./cmd/oracle
./oracle query path --help
./oracle impact --help
```

Expected: both commands show help with correct flags.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/ internal/mcp/server.go
git commit -m "feat: add path and impact CLI commands and MCP tools"
```

---

### Task 6: Fixture Project — NestJS Source Files

**Files:**
- Create: `fixtures/orders-domain/oracle.domain.yaml`
- Create: `fixtures/orders-domain/orders-api/package.json`
- Create: `fixtures/orders-domain/orders-api/tsconfig.json`
- Create: `fixtures/orders-domain/orders-api/openapi.yaml`
- Create: `fixtures/orders-domain/orders-api/src/orders/orders.module.ts`
- Create: `fixtures/orders-domain/orders-api/src/orders/orders.controller.ts`
- Create: `fixtures/orders-domain/orders-api/src/orders/orders.service.ts`
- Create: `fixtures/orders-domain/orders-api/src/payments/payments.service.ts`
- Create: `fixtures/orders-domain/orders-api/src/events/order-created.producer.ts`
- Create: `fixtures/orders-domain/payments-api/package.json`
- Create: `fixtures/orders-domain/payments-api/tsconfig.json`
- Create: `fixtures/orders-domain/payments-api/openapi.yaml`
- Create: `fixtures/orders-domain/payments-api/src/payments/payments.controller.ts`
- Create: `fixtures/orders-domain/payments-api/src/payments/payments.service.ts`

This is a large set of files. The content should be realistic NestJS TypeScript code. Each file must have actual decorators, imports, and class structure that matches what the golden graph will represent.

- [ ] **Step 1: Create domain manifest**

Create `fixtures/orders-domain/oracle.domain.yaml`:

```yaml
domain: orders
description: Order processing and payment domain
repositories:
  - name: orders-api
    path: ./orders-api
    tags: [nestjs, rest, kafka-producer]
  - name: payments-api
    path: ./payments-api
    tags: [nestjs, rest]
owner: checkout-team
```

- [ ] **Step 2: Create orders-api source files**

Create `fixtures/orders-domain/orders-api/package.json`:
```json
{
  "name": "orders-api",
  "version": "1.0.0",
  "dependencies": {
    "@nestjs/common": "^10.0.0",
    "@nestjs/core": "^10.0.0",
    "kafkajs": "^2.0.0"
  }
}
```

Create `fixtures/orders-domain/orders-api/tsconfig.json`:
```json
{
  "compilerOptions": {
    "module": "commonjs",
    "target": "ES2021",
    "strict": true,
    "esModuleInterop": true,
    "experimentalDecorators": true,
    "emitDecoratorMetadata": true,
    "outDir": "./dist",
    "rootDir": "./src"
  }
}
```

Create `fixtures/orders-domain/orders-api/src/orders/orders.module.ts`:
```typescript
import { Module } from '@nestjs/common';
import { OrdersController } from './orders.controller';
import { OrdersService } from './orders.service';
import { PaymentsService } from '../payments/payments.service';
import { OrderCreatedProducer } from '../events/order-created.producer';

@Module({
  controllers: [OrdersController],
  providers: [OrdersService, PaymentsService, OrderCreatedProducer],
})
export class OrdersModule {}
```

Create `fixtures/orders-domain/orders-api/src/orders/orders.controller.ts`:
```typescript
import { Controller, Get, Post, Param, Body } from '@nestjs/common';
import { OrdersService } from './orders.service';

@Controller('orders')
export class OrdersController {
  constructor(private readonly ordersService: OrdersService) {}

  @Get()
  findAll() {
    return this.ordersService.findAll();
  }

  @Get(':id')
  findOne(@Param('id') id: string) {
    return this.ordersService.findOne(id);
  }

  @Post()
  create(@Body() body: any) {
    return this.ordersService.create(body);
  }

  @Post(':id/capture')
  capture(@Param('id') id: string) {
    return this.ordersService.capture(id);
  }
}
```

Create `fixtures/orders-domain/orders-api/src/orders/orders.service.ts`:
```typescript
import { Injectable } from '@nestjs/common';
import { PaymentsService } from '../payments/payments.service';
import { OrderCreatedProducer } from '../events/order-created.producer';

@Injectable()
export class OrdersService {
  constructor(
    private readonly paymentsService: PaymentsService,
    private readonly orderCreatedProducer: OrderCreatedProducer,
  ) {}

  findAll() {
    return [];
  }

  findOne(id: string) {
    return { id };
  }

  async create(data: any) {
    const order = { id: '1', ...data, status: 'created' };
    await this.orderCreatedProducer.publish(order);
    return order;
  }

  async capture(id: string) {
    const charge = await this.paymentsService.charge(id, 100);
    return { id, status: 'captured', charge };
  }
}
```

Create `fixtures/orders-domain/orders-api/src/payments/payments.service.ts`:
```typescript
import { Injectable, HttpException } from '@nestjs/common';

const PAYMENTS_API_URL = process.env.PAYMENTS_API_URL || 'http://payments-api:3001';

@Injectable()
export class PaymentsService {
  async charge(orderId: string, amount: number) {
    const response = await fetch(`${PAYMENTS_API_URL}/payments/charge`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ orderId, amount }),
    });
    if (!response.ok) {
      throw new HttpException('Payment failed', response.status);
    }
    return response.json();
  }
}
```

Create `fixtures/orders-domain/orders-api/src/events/order-created.producer.ts`:
```typescript
import { Injectable } from '@nestjs/common';

const TOPIC = 'order-created';

@Injectable()
export class OrderCreatedProducer {
  async publish(order: any) {
    // In real code: this.kafka.producer.send({ topic: TOPIC, messages: [...] })
    console.log(`Publishing to ${TOPIC}:`, order);
  }
}
```

Create `fixtures/orders-domain/orders-api/openapi.yaml`:
```yaml
openapi: "3.0.3"
info:
  title: Orders API
  version: "1.0.0"
paths:
  /orders:
    get:
      summary: List all orders
      responses:
        "200":
          description: OK
    post:
      summary: Create an order
      responses:
        "201":
          description: Created
  /orders/{id}:
    get:
      summary: Get order by ID
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
  /orders/{id}/capture:
    post:
      summary: Capture payment for order
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
```

- [ ] **Step 3: Create payments-api source files**

Create `fixtures/orders-domain/payments-api/package.json`:
```json
{
  "name": "payments-api",
  "version": "1.0.0",
  "dependencies": {
    "@nestjs/common": "^10.0.0",
    "@nestjs/core": "^10.0.0"
  }
}
```

Create `fixtures/orders-domain/payments-api/tsconfig.json`:
```json
{
  "compilerOptions": {
    "module": "commonjs",
    "target": "ES2021",
    "strict": true,
    "esModuleInterop": true,
    "experimentalDecorators": true,
    "emitDecoratorMetadata": true,
    "outDir": "./dist",
    "rootDir": "./src"
  }
}
```

Create `fixtures/orders-domain/payments-api/src/payments/payments.controller.ts`:
```typescript
import { Controller, Post, Body } from '@nestjs/common';
import { PaymentsService } from './payments.service';

@Controller('payments')
export class PaymentsController {
  constructor(private readonly paymentsService: PaymentsService) {}

  @Post('charge')
  charge(@Body() body: { orderId: string; amount: number }) {
    return this.paymentsService.processCharge(body.orderId, body.amount);
  }

  @Post('refund')
  refund(@Body() body: { orderId: string; amount: number }) {
    return this.paymentsService.processRefund(body.orderId, body.amount);
  }
}
```

Create `fixtures/orders-domain/payments-api/src/payments/payments.service.ts`:
```typescript
import { Injectable } from '@nestjs/common';

@Injectable()
export class PaymentsService {
  processCharge(orderId: string, amount: number) {
    return { orderId, amount, status: 'charged', transactionId: 'txn_123' };
  }

  processRefund(orderId: string, amount: number) {
    return { orderId, amount, status: 'refunded', transactionId: 'txn_456' };
  }
}
```

Create `fixtures/orders-domain/payments-api/openapi.yaml`:
```yaml
openapi: "3.0.3"
info:
  title: Payments API
  version: "1.0.0"
paths:
  /payments/charge:
    post:
      summary: Charge a payment
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                orderId:
                  type: string
                amount:
                  type: number
      responses:
        "200":
          description: OK
  /payments/refund:
    post:
      summary: Refund a payment
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                orderId:
                  type: string
                amount:
                  type: number
      responses:
        "200":
          description: OK
```

- [ ] **Step 4: Commit fixtures**

```bash
git add fixtures/
git commit -m "feat: add fixture NestJS project for orders domain"
```

---

### Task 7: Golden Graph (expected-graph.json)

**Files:**
- Create: `fixtures/orders-domain/expected-graph.json`

- [ ] **Step 1: Create the golden payload**

Create `fixtures/orders-domain/expected-graph.json` — a full `ImportPayload` JSON. This file contains all nodes, edges, and evidence representing a perfect extraction of the fixture project.

The payload must contain:

**18 nodes** — repos, modules, controllers, providers, service nodes, endpoints, topic.

**16 edges** — CONTAINS (structural), INJECTS, EXPOSES_ENDPOINT, CALLS_ENDPOINT, CALLS_SERVICE, PUBLISHES_TOPIC.

**Evidence entries** — at least one per non-structural edge, with file_path + line_start pointing to actual fixture files.

Node keys follow the format `layer:type:domain:qualified_name` (all lowercase).

The full JSON is large. The implementer should create it matching exactly the node/edge tables from the spec. All from/to node keys in edges must reference existing node keys. All edge types must be valid in the default registry.

Key details:
- `CALLS_ENDPOINT` edge: `code:provider:orders:paymentsservice` → `contract:endpoint:orders:post:/payments/charge` (linked)
- `CALLS_SERVICE` edge: `code:provider:orders:paymentsservice` → `service:service:orders:payments-api` (linked)
- Evidence for INJECTS edges should point to constructor lines in the .ts files
- Evidence for EXPOSES_ENDPOINT should point to route decorator lines
- Evidence extractor_id: "claude-code", extractor_version: "1.0"

- [ ] **Step 2: Commit**

```bash
git add fixtures/orders-domain/expected-graph.json
git commit -m "feat: add golden graph payload for orders domain fixture"
```

---

### Task 8: E2E Test

**Files:**
- Create: `e2e/graph_e2e_test.go`

- [ ] **Step 1: Create E2E test**

Create `e2e/graph_e2e_test.go`:

```go
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

func loadGoldenPayload(t *testing.T) graph.ImportPayload {
	t.Helper()
	data, err := os.ReadFile("../fixtures/orders-domain/expected-graph.json")
	if err != nil {
		t.Fatalf("reading golden payload: %v", err)
	}
	var payload graph.ImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parsing golden payload: %v", err)
	}
	return payload
}

func setupE2E(t *testing.T) (*graph.Graph, graph.ImportPayload) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "e2e.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	return graph.New(s, reg), loadGoldenPayload(t)
}

// Phase 1: Validate golden payload itself
func TestGoldenPayloadValid(t *testing.T) {
	payload := loadGoldenPayload(t)

	// All node keys unique
	nodeKeys := make(map[string]bool)
	for _, n := range payload.Nodes {
		key := n.NodeKey
		if nodeKeys[key] {
			t.Errorf("duplicate node_key: %s", key)
		}
		nodeKeys[key] = true
	}

	// All edge from/to reference existing nodes
	for i, e := range payload.Edges {
		if !nodeKeys[e.FromNodeKey] {
			t.Errorf("edge[%d] from_node_key %q not in nodes", i, e.FromNodeKey)
		}
		if !nodeKeys[e.ToNodeKey] {
			t.Errorf("edge[%d] to_node_key %q not in nodes", i, e.ToNodeKey)
		}
	}

	// All evidence targets exist
	for i, ev := range payload.Evidence {
		if ev.TargetKind == "node" && !nodeKeys[ev.NodeKey] {
			t.Errorf("evidence[%d] node_key %q not in nodes", i, ev.NodeKey)
		}
	}

	// Minimum counts
	if len(payload.Nodes) < 15 {
		t.Errorf("nodes = %d, want >= 15", len(payload.Nodes))
	}
	if len(payload.Edges) < 12 {
		t.Errorf("edges = %d, want >= 12", len(payload.Edges))
	}
}

// Phase 2: Import and basic queries
func TestE2EImport(t *testing.T) {
	g, payload := setupE2E(t)

	revID, err := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	if result.NodesCreated < 15 {
		t.Errorf("nodes = %d, want >= 15", result.NodesCreated)
	}
	if result.EdgesCreated < 12 {
		t.Errorf("edges = %d, want >= 12", result.EdgesCreated)
	}

	// Query deps of OrdersController (non-structural only)
	deps, err := g.QueryDeps("code:controller:orders:orderscontroller", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	// Should include OrdersService (INJECTS) and endpoints (EXPOSES_ENDPOINT)
	if len(deps) < 1 {
		t.Errorf("deps = %d, want >= 1", len(deps))
	}
}

// Phase 3: Path queries
func TestE2EPath(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	// Directed path: OrdersController -> payments-api (service)
	result, err := g.QueryPath(
		"code:controller:orders:orderscontroller",
		"service:service:orders:payments-api",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"},
	)
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) == 0 {
		t.Fatal("expected at least 1 directed path from controller to payments-api")
	}
	// Path should go through OrdersService and PaymentsService
	p := result.Paths[0]
	if p.Depth < 2 {
		t.Errorf("path depth = %d, want >= 2", p.Depth)
	}

	// No directed dependency path: OrderCreatedProducer -> PaymentsController
	result2, err := g.QueryPath(
		"code:provider:orders:ordercreatedproducer",
		"code:controller:orders:paymentscontroller",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"},
	)
	if err != nil {
		t.Fatalf("QueryPath no-path: %v", err)
	}
	if len(result2.Paths) != 0 {
		t.Errorf("expected 0 directed paths, got %d", len(result2.Paths))
	}

	// Derivation filter: hard only should NOT find path (CALLS_SERVICE/CALLS_ENDPOINT are linked)
	result3, err := g.QueryPath(
		"code:controller:orders:orderscontroller",
		"service:service:orders:payments-api",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed", DerivationFilter: []string{"hard"}},
	)
	if err != nil {
		t.Fatalf("QueryPath hard-only: %v", err)
	}
	if len(result3.Paths) != 0 {
		t.Errorf("expected 0 paths with hard-only filter, got %d", len(result3.Paths))
	}
}

// Phase 4: Impact
func TestE2EImpact(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	// PaymentsService changes -> OrdersService impacted (INJECTS), then OrdersController
	result, err := g.QueryImpact("code:provider:orders:paymentsservice", graph.ImpactOptions{
		MaxDepth: 4, MinScore: 0.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if result.TotalImpacted < 2 {
		t.Errorf("impacted = %d, want >= 2", result.TotalImpacted)
	}

	// Verify CONTAINS doesn't pollute: OrdersModule should NOT appear
	for _, imp := range result.Impacts {
		if imp.NodeKey == "code:module:orders:ordersmodule" {
			t.Error("OrdersModule should NOT be impacted (CONTAINS is structural)")
		}
	}

	// Closer nodes have higher scores
	if len(result.Impacts) >= 2 {
		if result.Impacts[0].ImpactScore <= result.Impacts[1].ImpactScore {
			t.Errorf("closer should score higher: %f <= %f", result.Impacts[0].ImpactScore, result.Impacts[1].ImpactScore)
		}
	}
}

// Phase 5: Lifecycle
func TestE2EIdempotent(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	// Re-import same payload
	g.ImportAll(payload, revID)

	nodes, _ := g.Store().ListNodes(store.NodeFilter{})
	nodeCount := len(payload.Nodes)
	if len(nodes) != nodeCount {
		t.Errorf("after re-import: %d nodes, want %d (no duplicates)", len(nodes), nodeCount)
	}
}

func TestE2EStaleMarking(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	// New revision, only re-import 1 node
	revID2, _ := g.Store().CreateRevision("orders", "abc123", "def456", "manual", "full", "{}")
	g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController",
	}, revID2)

	staleCount, _ := g.Store().MarkStaleNodes("orders", revID2)
	expectedStale := len(payload.Nodes) - 1
	if int(staleCount) != expectedStale {
		t.Errorf("stale = %d, want %d", staleCount, expectedStale)
	}
}

func TestE2EStats(t *testing.T) {
	g, payload := setupE2E(t)
	revID, _ := g.Store().CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	g.ImportAll(payload, revID)

	stats, err := g.QueryStats("orders")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.NodeCount != len(payload.Nodes) {
		t.Errorf("node count = %d, want %d", stats.NodeCount, len(payload.Nodes))
	}
	if stats.NodesByLayer["code"] < 5 {
		t.Errorf("code nodes = %d, want >= 5", stats.NodesByLayer["code"])
	}
	if stats.EdgesByDerivation == nil {
		t.Error("EdgesByDerivation is nil")
	}
}
```

- [ ] **Step 2: Run E2E tests**

```bash
go test ./e2e/ -v
```

Expected: all E2E tests PASS.

- [ ] **Step 3: Run full test suite**

```bash
go test ./... -count=1
```

Expected: all tests PASS.

- [ ] **Step 4: Build final binary**

```bash
go build -o oracle ./cmd/oracle
./oracle version
```

- [ ] **Step 5: Commit**

```bash
git add e2e/
git commit -m "test: add E2E tests with fixture project golden graph"
```

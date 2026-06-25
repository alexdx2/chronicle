package graph

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// callSymbolEdgeType is the edge Chronicle uses for direct function/method calls
// (the substrate for Tier-B complexity propagation).
const callSymbolEdgeType = "CALLS_SYMBOL"

// ComputeGraphComplexity runs the Tier-B pass over the live call graph: it reads
// each node's Tier-A loop_depth from Metadata, derives recursive +
// transitive_loop_depth by propagating along CALLS_SYMBOL edges, folds the result
// back into node Metadata, and emits one graph-derived evidence row per node.
// Non-TS coverage guard: nodes are processed only when they participate in
// CALLS_SYMBOL edges, so a metric is never emitted where no call graph exists.
func (g *Graph) ComputeGraphComplexity(revisionID int64) error {
	active := true
	edges, err := g.store.ListEdges(store.EdgeFilter{EdgeType: callSymbolEdgeType, Active: &active})
	if err != nil {
		return fmt.Errorf("ComputeGraphComplexity edges: %w", err)
	}
	if len(edges) == 0 {
		return nil // no call graph -> no derived metrics
	}

	nodes, err := g.store.ListNodes(store.NodeFilter{})
	if err != nil {
		return fmt.Errorf("ComputeGraphComplexity nodes: %w", err)
	}
	metaByKey := map[string]string{}
	loopDepth := map[string]int{}
	for _, n := range nodes {
		metaByKey[n.NodeKey] = n.Metadata
		if m, ok := complexityFromMetadata(n.Metadata); ok {
			loopDepth[n.NodeKey] = m.LoopDepth
		}
	}

	calls := make([]callEdge, 0, len(edges))
	inGraph := map[string]bool{}
	for _, e := range edges {
		calls = append(calls, callEdge{From: e.FromNodeKey, To: e.ToNodeKey, Confidence: e.Confidence})
		inGraph[e.FromNodeKey] = true
		inGraph[e.ToNodeKey] = true
	}
	result := computeGraphComplexity(loopDepth, calls)

	keys := make([]string, 0, len(result))
	for k := range result {
		if inGraph[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		gc := result[k]
		nodeID, err := g.store.GetNodeIDByKey(k)
		if err != nil {
			continue // edge endpoint without a resolved node (e.g. external) — skip
		}
		merged, err := mergeGraphComplexity(metaByKey[k], gc)
		if err != nil {
			return fmt.Errorf("ComputeGraphComplexity merge %s: %w", k, err)
		}
		if err := g.store.UpdateNodeMetadata(nodeID, merged); err != nil {
			return fmt.Errorf("ComputeGraphComplexity metadata %s: %w", k, err)
		}
		assertion := fmt.Sprintf(`{"recursive":%t,"transitive_loop_depth":%d}`, gc.Recursive, gc.TransitiveLoopDepth)
		if _, err := g.AddNodeEvidence(k, validate.EvidenceInput{
			SourceKind:       "graph",
			ExtractorID:      "complexity-graph",
			ExtractorVersion: "1",
			ASTRule:          "complexity/v1",
			Confidence:       gc.BindingConfidence,
			RevisionID:       revisionID,
			Assertion:        assertion,
			AssertionKind:    "complexity",
			Metadata:         `{"metric_type":"derived"}`,
		}); err != nil {
			return fmt.Errorf("ComputeGraphComplexity evidence %s: %w", k, err)
		}
	}
	return nil
}

// complexityFromMetadata extracts the v1 complexity block from a node's Metadata
// JSON. Returns ok=false when the node has no `complexity` key (no fabricated
// coverage). Malformed JSON is treated as absent.
func complexityFromMetadata(metadata string) (ComplexityMetrics, bool) {
	if metadata == "" {
		return ComplexityMetrics{}, false
	}
	var wrap struct {
		Complexity *ComplexityMetrics `json:"complexity"`
	}
	if err := json.Unmarshal([]byte(metadata), &wrap); err != nil || wrap.Complexity == nil {
		return ComplexityMetrics{}, false
	}
	return *wrap.Complexity, true
}

// callEdge is one resolved CALLS edge feeding the Tier-B complexity pass.
type callEdge struct {
	From, To   string
	Confidence float64
}

// graphComplexity holds the derived (Tier-B) metrics for one node.
type graphComplexity struct {
	Recursive           bool
	TransitiveLoopDepth int
	BindingConfidence   float64
}

// computeGraphComplexity derives recursive + transitive_loop_depth for every
// node in loopDepth by propagating local loop depth along CALLS edges. Cycles
// (self- or mutual recursion) are condensed into strongly-connected components
// so the upper bound stays finite: an SCC contributes the max member loop_depth
// and every member is flagged recursive. BindingConfidence is the minimum CALLS
// confidence along the path that produced the node's transitive depth.
//
// Deterministic: node iteration and adjacency are sorted, ties break by comp id.
func computeGraphComplexity(loopDepth map[string]int, calls []callEdge) map[string]graphComplexity {
	// Universe of nodes: loopDepth keys plus any endpoint seen in calls.
	loop := func(k string) int { return loopDepth[k] }
	nodeSet := map[string]bool{}
	for k := range loopDepth {
		nodeSet[k] = true
	}
	adj := map[string][]callEdge{}
	selfLoop := map[string]bool{}
	for _, e := range calls {
		nodeSet[e.From] = true
		nodeSet[e.To] = true
		adj[e.From] = append(adj[e.From], e)
		if e.From == e.To {
			selfLoop[e.From] = true
		}
	}
	nodes := make([]string, 0, len(nodeSet))
	for k := range nodeSet {
		nodes = append(nodes, k)
	}
	sort.Strings(nodes)
	for k := range adj {
		sort.Slice(adj[k], func(i, j int) bool { return adj[k][i].To < adj[k][j].To })
	}

	comp := tarjanSCC(nodes, adj)

	// Per-component aggregates.
	members := map[int][]string{}
	internalDepth := map[int]int{}
	minIntraConf := map[int]float64{}
	recursive := map[int]bool{}
	for _, n := range nodes {
		c := comp[n]
		members[c] = append(members[c], n)
		if loop(n) > internalDepth[c] {
			internalDepth[c] = loop(n)
		}
	}
	for c, ms := range members {
		if len(ms) > 1 {
			recursive[c] = true
		}
	}
	// Successor edges (condensed) + intra-SCC min confidence.
	succConf := map[int]map[int]float64{} // compFrom -> compTo -> min cross conf
	for _, e := range calls {
		cf, ct := comp[e.From], comp[e.To]
		if cf == ct {
			if selfLoop[e.From] {
				recursive[cf] = true
			}
			if _, ok := minIntraConf[cf]; !ok || e.Confidence < minIntraConf[cf] {
				minIntraConf[cf] = e.Confidence
			}
			continue
		}
		if succConf[cf] == nil {
			succConf[cf] = map[int]float64{}
		}
		if cur, ok := succConf[cf][ct]; !ok || e.Confidence < cur {
			succConf[cf][ct] = e.Confidence
		}
	}

	depthMemo := map[int]int{}
	confMemo := map[int]float64{}
	var depthOf func(c int) int
	var confOf func(c int) float64

	// chosenSucc returns the successor comp with max depth (tie: smallest comp id).
	chosenSucc := func(c int) (int, bool) {
		succs := make([]int, 0, len(succConf[c]))
		for t := range succConf[c] {
			succs = append(succs, t)
		}
		sort.Ints(succs)
		best, found := -1, false
		bestDepth := -1
		for _, t := range succs {
			if d := depthOf(t); d > bestDepth {
				bestDepth, best, found = d, t, true
			}
		}
		return best, found
	}

	depthOf = func(c int) int {
		if d, ok := depthMemo[c]; ok {
			return d
		}
		depthMemo[c] = internalDepth[c] // guard against revisits during recursion
		maxSucc := 0
		for t := range succConf[c] {
			if d := depthOf(t); d > maxSucc {
				maxSucc = d
			}
		}
		d := internalDepth[c] + maxSucc
		depthMemo[c] = d
		return d
	}

	confOf = func(c int) float64 {
		if v, ok := confMemo[c]; ok {
			return v
		}
		confMemo[c] = 1.0 // guard against revisits
		parts := []float64{}
		if recursive[c] {
			if v, ok := minIntraConf[c]; ok {
				parts = append(parts, v)
			}
		}
		if t, ok := chosenSucc(c); ok {
			parts = append(parts, succConf[c][t], confOf(t))
		}
		v := 1.0
		for i, p := range parts {
			if i == 0 || p < v {
				v = p
			}
		}
		confMemo[c] = v
		return v
	}

	out := map[string]graphComplexity{}
	for _, n := range nodes {
		c := comp[n]
		out[n] = graphComplexity{
			Recursive:           recursive[c],
			TransitiveLoopDepth: depthOf(c),
			BindingConfidence:   confOf(c),
		}
	}
	return out
}

// tarjanSCC assigns each node a component id. Recursive Tarjan; nodes/adjacency
// are pre-sorted by the caller for deterministic component numbering.
func tarjanSCC(nodes []string, adj map[string][]callEdge) map[string]int {
	const unvisited = -1
	index := map[string]int{}
	lowlink := map[string]int{}
	onStack := map[string]bool{}
	comp := map[string]int{}
	var stack []string
	counter := 0
	compID := 0

	var strongConnect func(v string)
	strongConnect = func(v string) {
		index[v] = counter
		lowlink[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, e := range adj[v] {
			w := e.To
			if _, ok := index[w]; !ok {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if index[w] < lowlink[v] {
					lowlink[v] = index[w]
				}
			}
		}
		if lowlink[v] == index[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = compID
				if w == v {
					break
				}
			}
			compID++
		}
	}

	for _, n := range nodes {
		if _, ok := index[n]; !ok {
			strongConnect(n)
		}
	}
	_ = unvisited
	return comp
}

// mergeGraphComplexity folds the Tier-B derived metrics (recursive,
// transitive_loop_depth) into a node's existing Metadata JSON, preserving any
// Tier-A metrics and unrelated keys, and tagging metric_sources as "graph" for
// the derived metrics. Deterministic output (Go marshals map keys sorted).
func mergeGraphComplexity(metadata string, gc graphComplexity) (string, error) {
	root := map[string]any{}
	if metadata != "" && metadata != "{}" {
		if err := json.Unmarshal([]byte(metadata), &root); err != nil {
			return "", err
		}
	}
	cx, _ := root["complexity"].(map[string]any)
	if cx == nil {
		cx = map[string]any{}
	}
	cx["recursive"] = gc.Recursive
	cx["transitive_loop_depth"] = gc.TransitiveLoopDepth
	ms, _ := cx["metric_sources"].(map[string]any)
	if ms == nil {
		ms = map[string]any{}
	}
	ms["recursive"] = "graph"
	ms["transitive_loop_depth"] = "graph"
	cx["metric_sources"] = ms
	root["complexity"] = cx
	b, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ComplexityMetrics is the v1 evidence-backed complexity signal stamped on
// Function/Method nodes. Exact metrics (cyclomatic, loop_count, loop_depth)
// come from the AST extractor; derived metrics (recursive, transitive_loop_depth)
// come from the Tier-B graph pass over CALLS edges. Missing metrics are left at
// their zero value and treated as "absent" by consumers.
type ComplexityMetrics struct {
	Cyclomatic          int  `json:"cyclomatic"`
	LoopCount           int  `json:"loop_count"`
	LoopDepth           int  `json:"loop_depth"`
	Recursive           bool `json:"recursive"`
	TransitiveLoopDepth int  `json:"transitive_loop_depth"`
}

// normComplexity maps a node's complexity to [0,1] for insights ranking. The
// graph-derived term (transitive_loop_depth) is weighted higher because it is
// present for most nodes; an absent metric contributes 0 to its term.
//
//	normComplexity = min(1, 0.6·min(1, tld/5) + 0.4·min(1, cyclomatic/20))
func normComplexity(m ComplexityMetrics) float64 {
	tldTerm := float64(m.TransitiveLoopDepth) / 5.0
	if tldTerm > 1 {
		tldTerm = 1
	}
	cycTerm := float64(m.Cyclomatic) / 20.0
	if cycTerm > 1 {
		cycTerm = 1
	}
	v := 0.6*tldTerm + 0.4*cycTerm
	if v > 1 {
		v = 1
	}
	return v
}

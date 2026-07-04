package graph

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/alexdx2/chronicle-core/extract/ast"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// callEdgeTypes are the edges Tier-B complexity propagates along. CALLS_SYMBOL
// is the direct call edge; INJECTS is included because live scans express
// call dependencies as DI edges (call facts attach call_expression evidence to
// INJECTS rather than creating CALLS_SYMBOL) — without it Tier-B never fires
// on a real scan. At Chronicle's class-level granularity "A injects B" is a
// call dependency.
var callEdgeTypes = []string{"CALLS_SYMBOL", "INJECTS"}

// ComputeASTComplexity runs the Tier-A pass: for every code-layer node backed by
// a readable TypeScript source file, it extracts exact per-function metrics,
// aggregates them to the node's (class-level) granularity, folds them into the
// node Metadata, and emits one exact (confidence 1.0) complexity-ast evidence row
// carrying the file path and line span. This pass must run BEFORE
// ComputeGraphComplexity so the AST-derived loop_depth is available to seed the
// Tier-B transitive_loop_depth propagation. Nodes whose file cannot be read are
// skipped — never fabricated.
func (g *Graph) ComputeASTComplexity(revisionID int64) error {
	return g.computeASTComplexity(revisionID, nil)
}

// computeASTComplexity is the scoped worker: a non-nil scope (incremental
// finalize) limits file reads to the changed set so a 1-file scan never
// re-parses the whole repo.
func (g *Graph) computeASTComplexity(revisionID int64, scope fileScope) error {
	nodes, err := g.store.ListNodes(store.NodeFilter{Layer: "code"})
	if err != nil {
		return fmt.Errorf("ComputeASTComplexity nodes: %w", err)
	}
	for _, n := range nodes {
		if n.FilePath == "" || !isTypeScriptFile(n.FilePath) || !scope.matches(n.FilePath) {
			continue
		}
		content := readFileContent(n.FilePath)
		if content == nil {
			continue // unreadable from any base — skip, do not fabricate coverage
		}
		agg := ast.AggregateComplexity(ast.ExtractComplexity(content))
		if !agg.Present {
			continue // no measurable functions/methods in this file
		}
		nodeID, err := g.store.GetNodeIDByKey(n.NodeKey)
		if err != nil {
			continue
		}
		merged, err := mergeASTComplexity(n.Metadata, agg)
		if err != nil {
			return fmt.Errorf("ComputeASTComplexity merge %s: %w", n.NodeKey, err)
		}
		if err := g.store.UpdateNodeMetadata(nodeID, merged); err != nil {
			return fmt.Errorf("ComputeASTComplexity metadata %s: %w", n.NodeKey, err)
		}
		assertion := fmt.Sprintf(`{"cyclomatic":%d,"loop_count":%d,"loop_depth":%d}`,
			agg.Cyclomatic, agg.LoopCount, agg.LoopDepth)
		if _, err := g.AddNodeEvidence(n.NodeKey, validate.EvidenceInput{
			SourceKind:       "ast",
			FilePath:         n.FilePath,
			LineStart:        agg.StartLine,
			LineEnd:          agg.EndLine,
			ExtractorID:      "complexity-ast",
			ExtractorVersion: "1",
			ASTRule:          "complexity/v1",
			Confidence:       1.0,
			RevisionID:       revisionID,
			Assertion:        assertion,
			AssertionKind:    "complexity",
			Metadata:         `{"metric_type":"exact"}`,
		}); err != nil {
			return fmt.Errorf("ComputeASTComplexity evidence %s: %w", n.NodeKey, err)
		}

		// Heuristic signals (cognitive + smells) ride a SEPARATE evidence row at
		// heuristic confidence — never conflated with the exact counts above.
		smellsJSON, err := json.Marshal(agg.Smells)
		if err != nil || agg.Smells == nil {
			smellsJSON = []byte("[]")
		}
		smellAssertion := fmt.Sprintf(`{"cognitive":%d,"smells":%s}`, agg.Cognitive, smellsJSON)
		if _, err := g.AddNodeEvidence(n.NodeKey, validate.EvidenceInput{
			SourceKind:       "ast",
			FilePath:         n.FilePath,
			LineStart:        agg.StartLine,
			LineEnd:          agg.EndLine,
			ExtractorID:      "complexity-smells",
			ExtractorVersion: "1",
			ASTRule:          "complexity/v1",
			Confidence:       0.6,
			RevisionID:       revisionID,
			Assertion:        smellAssertion,
			AssertionKind:    "complexity_smells",
			Metadata:         `{"metric_type":"heuristic"}`,
		}); err != nil {
			return fmt.Errorf("ComputeASTComplexity smells evidence %s: %w", n.NodeKey, err)
		}
	}
	return nil
}

// mergeASTComplexity folds the exact Tier-A metrics into a node's existing
// Metadata JSON, preserving any other keys, and tags metric_sources as "ast" for
// each exact metric. Deterministic output (Go marshals map keys sorted).
func mergeASTComplexity(metadata string, a ast.AggregatedComplexity) (string, error) {
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
	cx["cyclomatic"] = a.Cyclomatic
	cx["loop_count"] = a.LoopCount
	cx["loop_depth"] = a.LoopDepth
	cx["cognitive"] = a.Cognitive
	smells := a.Smells
	if smells == nil {
		smells = []string{}
	}
	cx["smells"] = smells
	ms, _ := cx["metric_sources"].(map[string]any)
	if ms == nil {
		ms = map[string]any{}
	}
	ms["cyclomatic"] = "ast"
	ms["loop_count"] = "ast"
	ms["loop_depth"] = "ast"
	ms["cognitive"] = "heuristic"
	ms["smells"] = "heuristic"
	cx["metric_sources"] = ms
	root["complexity"] = cx
	b, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ComputeGraphComplexity runs the Tier-B pass over the live call graph: it reads
// each node's Tier-A loop_depth from Metadata, derives recursive +
// transitive_loop_depth by propagating along CALLS_SYMBOL edges, folds the result
// back into node Metadata, and emits one graph-derived evidence row per node.
// Non-TS coverage guard: nodes are processed only when they participate in
// CALLS_SYMBOL edges, so a metric is never emitted where no call graph exists.
func (g *Graph) ComputeGraphComplexity(revisionID int64) error {
	active := true
	var edges []store.EdgeRow
	for _, et := range callEdgeTypes {
		batch, err := g.store.ListEdges(store.EdgeFilter{EdgeType: et, Active: &active})
		if err != nil {
			return fmt.Errorf("ComputeGraphComplexity edges %s: %w", et, err)
		}
		edges = append(edges, batch...)
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
	Cyclomatic          int      `json:"cyclomatic"`
	LoopCount           int      `json:"loop_count"`
	LoopDepth           int      `json:"loop_depth"`
	Recursive           bool     `json:"recursive"`
	TransitiveLoopDepth int      `json:"transitive_loop_depth"`
	Cognitive           int      `json:"cognitive"`
	Smells              []string `json:"smells,omitempty"`
}

// normComplexity maps a node's complexity to [0,1] for insights ranking, folding
// every available signal: graph-derived transitive depth, cyclomatic and
// cognitive counts, and a bump per heuristic smell. An absent metric contributes
// 0 to its term, so nodes without a given signal are never penalized for it.
//
//	normComplexity = min(1, 0.4·tld/5 + 0.25·cyc/20 + 0.2·cog/15 + 0.15·smells/3)
func normComplexity(m ComplexityMetrics) float64 {
	clamp := func(v float64) float64 {
		if v > 1 {
			return 1
		}
		return v
	}
	tldTerm := clamp(float64(m.TransitiveLoopDepth) / 5.0)
	cycTerm := clamp(float64(m.Cyclomatic) / 20.0)
	cogTerm := clamp(float64(m.Cognitive) / 15.0)
	smellTerm := clamp(float64(len(m.Smells)) / 3.0)
	return clamp(0.4*tldTerm + 0.25*cycTerm + 0.2*cogTerm + 0.15*smellTerm)
}

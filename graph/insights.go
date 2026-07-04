package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// InsightsResult is a deterministic, trust-aware summary of the graph: what's
// central, what's least supported, what to verify, and what's missing. All of
// it is graph math — no LLM. Unlike a generic "god node" report, the suspicious
// and verification sections are typed and trust-ranked, and the verification
// targets feed directly into evidence_verify work.
type InsightsResult struct {
	Domain              string        `json:"domain,omitempty"`
	NodeCount           int           `json:"node_count"`
	EdgeCount           int           `json:"edge_count"`
	Hubs                []HubInsight  `json:"hubs"`
	SuspiciousEdges     []EdgeInsight `json:"suspicious_cross_domain_edges"`
	VerificationTargets []EdgeInsight `json:"verification_targets"`
	HotPathTargets      []NodeInsight `json:"hot_path_targets"`
	Gaps                []GapInsight  `json:"gaps"`
	SuggestedQueries    []string      `json:"suggested_queries"`
}

// NodeInsight is a complexity hot-spot: a node ranked by complexity × impact ×
// staleness, independent of edge trust (so complex-but-trusted functions surface).
type NodeInsight struct {
	NodeKey    string  `json:"node_key"`
	Name       string  `json:"name"`
	Layer      string  `json:"layer"`
	Degree     int     `json:"degree"`
	Trust      float64 `json:"trust_score"`
	Complexity float64 `json:"complexity"`
	Churn      int     `json:"churn,omitempty"` // commits touching the node's file in the git window
	HotScore   float64 `json:"hot_score"`
	Reason     string  `json:"reason"`
}

type HubInsight struct {
	NodeKey string  `json:"node_key"`
	Name    string  `json:"name"`
	Layer   string  `json:"layer"`
	Degree  int     `json:"degree"`
	Trust   float64 `json:"trust_score"`
}

type EdgeInsight struct {
	EdgeKey  string  `json:"edge_key"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	EdgeType string  `json:"type"`
	Trust    float64 `json:"trust_score"`
	Reason   string  `json:"reason"`
	// Score is the verification priority (higher = check first), stamped at
	// construction so a federated aggregator can rank by the same
	// complexity-aware value rather than re-deriving or discarding it. Zero for
	// insights that aren't priority-ranked (e.g. suspicious/cross-repo edges).
	Score float64 `json:"score,omitempty"`
}

type GapInsight struct {
	Kind    string `json:"kind"`
	NodeKey string `json:"node_key"`
	Name    string `json:"name"`
	Detail  string `json:"detail"`
}

const (
	hubLimit                = 10
	suspiciousLimit         = 15
	verificationLimit       = 15
	hotPathLimit            = 15
	lowTrustThreshold       = 0.7
	staleFreshnessThreshold = 0.5
	highTransitiveDepth     = 3
	highChurnThreshold      = 10 // commits in the churn window that flag "high churn"
)

// Insights computes the report for one domain (empty = all domains).
func (g *Graph) Insights(domainKey string) (*InsightsResult, error) {
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	if err != nil {
		return nil, fmt.Errorf("Insights nodes: %w", err)
	}
	active := true
	edges, err := g.store.ListEdges(store.EdgeFilter{Active: &active})
	if err != nil {
		return nil, fmt.Errorf("Insights edges: %w", err)
	}

	nodeByKey := map[string]store.NodeRow{}
	inDomain := map[string]bool{}
	for _, n := range nodes {
		nodeByKey[n.NodeKey] = n
		inDomain[n.NodeKey] = true
	}

	// Degree over edges touching in-domain nodes.
	degree := map[string]int{}
	hasOutEdge := map[string]map[string]bool{} // nodeKey → set of outgoing edge types
	hasInEdge := map[string]map[string]bool{}  // nodeKey → set of incoming edge types
	res := &InsightsResult{Domain: domainKey, NodeCount: len(nodes)}

	for _, e := range edges {
		from, to := e.FromNodeKey, e.ToNodeKey
		touches := inDomain[from] || inDomain[to]
		if domainKey != "" && !touches {
			continue
		}
		res.EdgeCount++
		degree[from]++
		degree[to]++
		if hasOutEdge[from] == nil {
			hasOutEdge[from] = map[string]bool{}
		}
		hasOutEdge[from][e.EdgeType] = true
		if hasInEdge[to] == nil {
			hasInEdge[to] = map[string]bool{}
		}
		hasInEdge[to][e.EdgeType] = true

		// Cross-domain edge: from-domain != to-domain.
		fd, td := store.DomainFromNodeKey(from), store.DomainFromNodeKey(to)
		if fd != "" && td != "" && fd != td {
			res.SuspiciousEdges = append(res.SuspiciousEdges, EdgeInsight{
				EdgeKey: e.EdgeKey, From: from, To: to, EdgeType: e.EdgeType,
				Trust:  e.TrustScore,
				Reason: fmt.Sprintf("crosses domain boundary %s→%s", fd, td),
			})
		}

		// Verification target: low-trust edge, ranked by impact (degree of source)
		// weighted by the source function's complexity.
		if e.TrustScore < lowTrustThreshold {
			cx := nodeComplexityNorm(nodeByKey, from)
			score := float64(degree[from]) * (1 - e.TrustScore) * (1 + cx)
			res.VerificationTargets = append(res.VerificationTargets, EdgeInsight{
				EdgeKey: e.EdgeKey, From: from, To: to, EdgeType: e.EdgeType,
				Trust:  e.TrustScore,
				Score:  score,
				Reason: fmt.Sprintf("trust %.2f, source degree %d, src cx=%.2f — verify with evidence_verify", e.TrustScore, degree[from], cx),
			})
		}
	}

	// Hubs: top-degree non-noise nodes.
	for key, deg := range degree {
		n, ok := nodeByKey[key]
		if !ok || n.Status == "deleted" {
			continue
		}
		res.Hubs = append(res.Hubs, HubInsight{
			NodeKey: key, Name: n.Name, Layer: n.Layer, Degree: deg, Trust: n.TrustScore,
		})
	}
	sort.Slice(res.Hubs, func(i, j int) bool {
		if res.Hubs[i].Degree != res.Hubs[j].Degree {
			return res.Hubs[i].Degree > res.Hubs[j].Degree
		}
		return res.Hubs[i].NodeKey < res.Hubs[j].NodeKey
	})
	if len(res.Hubs) > hubLimit {
		res.Hubs = res.Hubs[:hubLimit]
	}

	// Suspicious cross-domain edges: lowest trust first (least-supported boundary crossings).
	sort.Slice(res.SuspiciousEdges, func(i, j int) bool {
		if res.SuspiciousEdges[i].Trust != res.SuspiciousEdges[j].Trust {
			return res.SuspiciousEdges[i].Trust < res.SuspiciousEdges[j].Trust
		}
		return res.SuspiciousEdges[i].EdgeKey < res.SuspiciousEdges[j].EdgeKey
	})
	if len(res.SuspiciousEdges) > suspiciousLimit {
		res.SuspiciousEdges = res.SuspiciousEdges[:suspiciousLimit]
	}

	// Verification targets: highest priority score (impact × lowest trust ×
	// source complexity) first — the score is stamped at construction.
	sort.Slice(res.VerificationTargets, func(i, j int) bool {
		if res.VerificationTargets[i].Score != res.VerificationTargets[j].Score {
			return res.VerificationTargets[i].Score > res.VerificationTargets[j].Score
		}
		return res.VerificationTargets[i].EdgeKey < res.VerificationTargets[j].EdgeKey
	})
	if len(res.VerificationTargets) > verificationLimit {
		res.VerificationTargets = res.VerificationTargets[:verificationLimit]
	}

	res.HotPathTargets = computeHotPathTargets(nodes, degree)

	res.Gaps = computeGaps(nodes, hasOutEdge, hasInEdge)
	res.SuggestedQueries = suggestedQueries(res.Hubs)
	return res, nil
}

// computeHotPathTargets ranks complex nodes by complexity × impact × staleness,
// independent of edge trust. Unlike verification targets (edges, low-trust only),
// this surfaces complex-but-trusted functions that would otherwise stay hidden.
func computeHotPathTargets(nodes []store.NodeRow, degree map[string]int) []NodeInsight {
	var out []NodeInsight
	for _, n := range nodes {
		if n.Status == "deleted" {
			continue
		}
		m, ok := complexityFromMetadata(n.Metadata)
		if !ok {
			continue
		}
		cx := normComplexity(m)
		if cx <= 0 {
			continue
		}
		deg := degree[n.NodeKey]
		churn := churnFromMetadata(n.Metadata)
		// churn × complexity is the classic hotspot formula: change frequency
		// multiplies the risk a complex unit carries. Capped so churn scales
		// the score by at most 2×.
		churnFactor := 1 + minF(1, float64(churn)/float64(highChurnThreshold))
		// A complex unit the test pass examined and found uncovered is riskier
		// than an identical covered one. Only applies when we actually looked.
		looked, tested := testSignalFromMetadata(n.Metadata)
		untestedFactor := 1.0
		if looked && !tested {
			untestedFactor = 1.5
		}
		hot := cx * float64(deg) * (1 + (1 - n.Freshness)) * churnFactor * untestedFactor
		out = append(out, NodeInsight{
			NodeKey: n.NodeKey, Name: n.Name, Layer: n.Layer, Degree: deg,
			Trust: n.TrustScore, Complexity: cx, Churn: churn, HotScore: hot,
			Reason: hotPathReason(m, n.Freshness, deg, churn, looked, tested),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HotScore != out[j].HotScore {
			return out[i].HotScore > out[j].HotScore
		}
		return out[i].NodeKey < out[j].NodeKey
	})
	if len(out) > hotPathLimit {
		out = out[:hotPathLimit]
	}
	return out
}

// hotPathReason tags the dominant factor by fixed precedence. Low-evidence-coverage
// (precedence 1) is added once per-node evidence counts are plumbed; until then the
// remaining factors apply in order: stale, recursive / high transitive depth, connected.
func hotPathReason(m ComplexityMetrics, freshness float64, degree int, churn int, testLooked, tested bool) string {
	if testLooked && !tested {
		return "complex + untested (no test file found)"
	}
	if len(m.Smells) > 0 {
		return fmt.Sprintf("complex + smell: %s", strings.Join(m.Smells, ", "))
	}
	if churn >= highChurnThreshold {
		return fmt.Sprintf("complex + high churn (%d commits in %dd)", churn, churnWindowDays)
	}
	if freshness < staleFreshnessThreshold {
		return fmt.Sprintf("complex + stale (freshness %.2f)", freshness)
	}
	if m.Recursive || m.TransitiveLoopDepth >= highTransitiveDepth {
		return fmt.Sprintf("complex: recursive=%v, transitive_loop_depth=%d", m.Recursive, m.TransitiveLoopDepth)
	}
	return fmt.Sprintf("complex + highly connected (degree %d)", degree)
}

// minF is a float64 min helper for score clamps.
func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// nodeComplexityNorm returns the [0,1] complexity weight for a node, or 0 when
// the node is unknown or carries no complexity metadata.
func nodeComplexityNorm(nodeByKey map[string]store.NodeRow, key string) float64 {
	n, ok := nodeByKey[key]
	if !ok {
		return 0
	}
	m, ok := complexityFromMetadata(n.Metadata)
	if !ok {
		return 0
	}
	return normComplexity(m)
}

// computeGaps finds structural holes: services without endpoints, endpoints
// that trigger no flow, and unresolved external nodes.
func computeGaps(nodes []store.NodeRow, hasOut, hasIn map[string]map[string]bool) []GapInsight {
	var gaps []GapInsight
	for _, n := range nodes {
		switch {
		case n.Status == "external":
			gaps = append(gaps, GapInsight{
				Kind: "unresolved_external", NodeKey: n.NodeKey, Name: n.Name,
				Detail: "external node not resolved to a definition — federation can connect it",
			})
		case n.Layer == "service" && n.NodeType == "service" && n.Status != "external":
			if !hasOut[n.NodeKey]["EXPOSES_ENDPOINT"] {
				gaps = append(gaps, GapInsight{
					Kind: "service_without_endpoints", NodeKey: n.NodeKey, Name: n.Name,
					Detail: "no EXPOSES_ENDPOINT edges — API surface may be unscanned",
				})
			}
		case n.Layer == "contract" && n.NodeType == "endpoint":
			if !hasOut[n.NodeKey]["TRIGGERS_FLOW"] && !hasIn[n.NodeKey]["TRIGGERS_FLOW"] {
				gaps = append(gaps, GapInsight{
					Kind: "endpoint_without_flow", NodeKey: n.NodeKey, Name: n.Name,
					Detail: "no TRIGGERS_FLOW edge — business flow not traced",
				})
			}
		}
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Kind != gaps[j].Kind {
			return gaps[i].Kind < gaps[j].Kind
		}
		return gaps[i].NodeKey < gaps[j].NodeKey
	})
	return gaps
}

func suggestedQueries(hubs []HubInsight) []string {
	q := []string{
		"chronicle_node_search(q=\"<a name you care about>\") — resolve a name to a node_key",
	}
	if len(hubs) > 0 {
		q = append(q,
			fmt.Sprintf("chronicle_impact(node_key=\"%s\") — what breaks if the top hub %q changes", hubs[0].NodeKey, hubs[0].Name),
			fmt.Sprintf("chronicle_subgraph(node_key=\"%s\", depth=2) — explore the busiest part of the graph", hubs[0].NodeKey),
		)
	}
	q = append(q,
		"chronicle_insights — re-run after a scan to watch gaps close",
		"chronicle_query_stats — node/edge counts by layer and derivation",
	)
	return q
}

// Markdown renders the insights as a human/demo-facing report. Deterministic:
// same InsightsResult → byte-identical output.
func (r *InsightsResult) Markdown() string {
	var b strings.Builder
	b.WriteString("# Chronicle Insights\n\n")
	scope := r.Domain
	if scope == "" {
		scope = "all domains"
	}
	fmt.Fprintf(&b, "Scope: **%s** — %d nodes, %d edges\n\n", scope, r.NodeCount, r.EdgeCount)

	b.WriteString("## Hubs (most-connected)\n\n")
	if len(r.Hubs) == 0 {
		b.WriteString("_none_\n\n")
	} else {
		for _, h := range r.Hubs {
			fmt.Fprintf(&b, "- `%s` (%s) — degree %d, trust %.2f\n", h.NodeKey, h.Layer, h.Degree, h.Trust)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Suspicious cross-domain edges (least supported first)\n\n")
	if len(r.SuspiciousEdges) == 0 {
		b.WriteString("_none_\n\n")
	} else {
		for _, e := range r.SuspiciousEdges {
			fmt.Fprintf(&b, "- `%s` →`%s` [%s] trust %.2f — %s\n", e.From, e.To, e.EdgeType, e.Trust, e.Reason)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Verification targets (high impact × low trust)\n\n")
	if len(r.VerificationTargets) == 0 {
		b.WriteString("_none — no low-trust edges_\n\n")
	} else {
		for _, e := range r.VerificationTargets {
			fmt.Fprintf(&b, "- `%s` →`%s` [%s] trust %.2f — %s\n", e.From, e.To, e.EdgeType, e.Trust, e.Reason)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Hot path targets (complexity × impact × staleness)\n\n")
	if len(r.HotPathTargets) == 0 {
		b.WriteString("_none — no complexity metrics yet_\n\n")
	} else {
		for _, n := range r.HotPathTargets {
			fmt.Fprintf(&b, "- `%s` (%s) — cx %.2f, degree %d, trust %.2f — %s\n", n.NodeKey, n.Layer, n.Complexity, n.Degree, n.Trust, n.Reason)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Gaps\n\n")
	if len(r.Gaps) == 0 {
		b.WriteString("_none_\n\n")
	} else {
		for _, g := range r.Gaps {
			fmt.Fprintf(&b, "- **%s**: `%s` — %s\n", g.Kind, g.NodeKey, g.Detail)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Suggested queries\n\n")
	for _, q := range r.SuggestedQueries {
		fmt.Fprintf(&b, "- %s\n", q)
	}
	return b.String()
}

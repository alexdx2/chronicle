package graph

import (
	"fmt"
	"sort"

	"github.com/alexdx2/chronicle-core/store"
)

// SubgraphOptions controls Subgraph traversal.
type SubgraphOptions struct {
	Depth     int    // hops from root; default 2
	MaxNodes  int    // hard cap on returned nodes; default 50
	Direction string // out|in|both; default both
}

// SubgraphNode is a compact node view for agent consumption.
type SubgraphNode struct {
	NodeKey  string  `json:"node_key"`
	Name     string  `json:"name"`
	Layer    string  `json:"layer"`
	NodeType string  `json:"node_type"`
	Trust    float64 `json:"trust_score"`
	Depth    int     `json:"depth"`
}

// SubgraphEdge is a compact edge view.
type SubgraphEdge struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	EdgeType   string  `json:"type"`
	Derivation string  `json:"derivation"`
	Trust      float64 `json:"trust_score"`
}

// SubgraphResult is the neighborhood around a root node. Truncation is always
// reported explicitly — silent cuts read as "covered everything" when it didn't.
type SubgraphResult struct {
	Root          string         `json:"root"`
	Nodes         []SubgraphNode `json:"nodes"`
	Edges         []SubgraphEdge `json:"edges"`
	Truncated     bool           `json:"truncated"`
	TruncatedNote string         `json:"truncated_note,omitempty"`
}

// Subgraph returns the BFS neighborhood around rootKey over active edges.
// When MaxNodes is hit, the lowest-trust periphery is dropped first and the
// result says exactly what was cut. One call replaces N query_deps round-trips.
func (g *Graph) Subgraph(rootKey string, opts SubgraphOptions) (*SubgraphResult, error) {
	if opts.Depth <= 0 {
		opts.Depth = 2
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 50
	}
	switch opts.Direction {
	case "":
		opts.Direction = "both"
	case "out", "in", "both":
	default:
		return nil, fmt.Errorf("Subgraph: direction must be out, in, or both (got %q)", opts.Direction)
	}

	root, err := g.store.GetNodeByKey(rootKey)
	if err != nil || root == nil {
		return nil, fmt.Errorf("node %q not found — use chronicle_node_search to find the key", rootKey)
	}

	res := &SubgraphResult{Root: rootKey, Nodes: []SubgraphNode{}, Edges: []SubgraphEdge{}}
	visited := map[int64]bool{root.NodeID: true}
	res.Nodes = append(res.Nodes, toSubgraphNode(*root, 0))
	frontier := []store.NodeRow{*root}
	seenEdges := map[string]bool{}
	droppedCount := 0
	droppedMaxTrust := 0.0

	for depth := 1; depth <= opts.Depth && len(frontier) > 0; depth++ {
		// Collect all candidate edges leaving the current frontier.
		type cand struct {
			edge store.EdgeRow
			next int64 // node on the far side
		}
		var cands []cand
		active := true
		for _, n := range frontier {
			if opts.Direction == "out" || opts.Direction == "both" {
				edges, err := g.store.ListEdges(store.EdgeFilter{FromNodeID: n.NodeID, Active: &active})
				if err != nil {
					return nil, fmt.Errorf("Subgraph edges: %w", err)
				}
				for _, e := range edges {
					cands = append(cands, cand{edge: e, next: e.ToNodeID})
				}
			}
			if opts.Direction == "in" || opts.Direction == "both" {
				edges, err := g.store.ListEdges(store.EdgeFilter{ToNodeID: n.NodeID, Active: &active})
				if err != nil {
					return nil, fmt.Errorf("Subgraph edges: %w", err)
				}
				for _, e := range edges {
					cands = append(cands, cand{edge: e, next: e.FromNodeID})
				}
			}
		}

		// Highest-trust edges admit their nodes first; ties break on edge key.
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].edge.TrustScore != cands[j].edge.TrustScore {
				return cands[i].edge.TrustScore > cands[j].edge.TrustScore
			}
			return cands[i].edge.EdgeKey < cands[j].edge.EdgeKey
		})

		var nextFrontier []store.NodeRow
		for _, c := range cands {
			admitted := visited[c.next]
			if !admitted {
				if len(res.Nodes) >= opts.MaxNodes {
					droppedCount++
					if c.edge.TrustScore > droppedMaxTrust {
						droppedMaxTrust = c.edge.TrustScore
					}
					continue
				}
				node, err := g.store.GetNodeByID(c.next)
				if err != nil || node == nil {
					continue
				}
				visited[c.next] = true
				res.Nodes = append(res.Nodes, toSubgraphNode(*node, depth))
				nextFrontier = append(nextFrontier, *node)
				admitted = true
			}
			// Record the edge whenever both endpoints are in the subgraph.
			if admitted && visited[c.edge.FromNodeID] && visited[c.edge.ToNodeID] && !seenEdges[c.edge.EdgeKey] {
				seenEdges[c.edge.EdgeKey] = true
				res.Edges = append(res.Edges, SubgraphEdge{
					From: c.edge.FromNodeKey, To: c.edge.ToNodeKey,
					EdgeType: c.edge.EdgeType, Derivation: c.edge.DerivationKind,
					Trust: c.edge.TrustScore,
				})
			}
		}
		frontier = nextFrontier
	}

	if droppedCount > 0 {
		res.Truncated = true
		res.TruncatedNote = fmt.Sprintf("truncated: %d nodes dropped at max_nodes=%d (highest dropped edge trust %.2f) — narrow with direction/depth or raise max_nodes", droppedCount, opts.MaxNodes, droppedMaxTrust)
	}
	return res, nil
}

func toSubgraphNode(n store.NodeRow, depth int) SubgraphNode {
	return SubgraphNode{
		NodeKey: n.NodeKey, Name: n.Name, Layer: n.Layer,
		NodeType: n.NodeType, Trust: n.TrustScore, Depth: depth,
	}
}

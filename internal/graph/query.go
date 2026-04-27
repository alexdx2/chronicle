package graph

import (
	"fmt"

	"github.com/anthropics/depbot/internal/store"
)

// DepNode is a node in the dependency traversal result.
type DepNode struct {
	NodeKey    string  `json:"node_key"`
	Name       string  `json:"name"`
	Layer      string  `json:"layer"`
	NodeType   string  `json:"node_type"`
	Depth      int     `json:"depth"`
	TrustScore float64 `json:"trust_score"`
	Freshness  float64 `json:"freshness"`
	Status     string  `json:"status"`
}

// Stats holds aggregate counts for a domain.
type Stats struct {
	NodeCount          int            `json:"node_count"`
	EdgeCount          int            `json:"edge_count"`
	NodesByLayer       map[string]int `json:"nodes_by_layer"`
	EdgesByType        map[string]int `json:"edges_by_type"`
	EdgesByDerivation  map[string]int `json:"edges_by_derivation"`
	ActiveNodes        int            `json:"active_nodes"`
	StaleNodes         int            `json:"stale_nodes"`
}

// QueryDeps performs a BFS from nodeKey following outgoing edges, up to maxDepth.
// derivationFilter, if non-empty, restricts which derivation_kinds are followed.
func (g *Graph) QueryDeps(nodeKey string, maxDepth int, derivationFilter []string) ([]DepNode, error) {
	return g.traverseDeps(nodeKey, maxDepth, derivationFilter, false)
}

// QueryReverseDeps performs a BFS from nodeKey following incoming edges, up to maxDepth.
func (g *Graph) QueryReverseDeps(nodeKey string, maxDepth int, derivationFilter []string) ([]DepNode, error) {
	return g.traverseDeps(nodeKey, maxDepth, derivationFilter, true)
}

func (g *Graph) traverseDeps(nodeKey string, maxDepth int, derivationFilter []string, reverse bool) ([]DepNode, error) {
	// Build a set for fast filter lookup.
	filterSet := make(map[string]bool, len(derivationFilter))
	for _, d := range derivationFilter {
		filterSet[d] = true
	}

	// Get the starting node.
	startNode, err := g.store.GetNodeByKey(nodeKey)
	if err != nil {
		return nil, fmt.Errorf("traverseDeps: %w", err)
	}

	// BFS
	type queueItem struct {
		nodeID int64
		depth  int
	}

	visited := map[int64]bool{startNode.NodeID: true}
	queue := []queueItem{{nodeID: startNode.NodeID, depth: 0}}
	var result []DepNode

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if item.depth >= maxDepth {
			continue
		}

		// List edges from this node.
		var filter store.EdgeFilter
		active := true
		filter.Active = &active
		if reverse {
			filter.ToNodeID = item.nodeID
		} else {
			filter.FromNodeID = item.nodeID
		}

		edges, err := g.store.ListEdges(filter)
		if err != nil {
			return nil, fmt.Errorf("traverseDeps ListEdges: %w", err)
		}

		for _, edge := range edges {
			// Apply derivation filter.
			if len(filterSet) > 0 && !filterSet[edge.DerivationKind] {
				continue
			}

			var nextID int64
			if reverse {
				nextID = edge.FromNodeID
			} else {
				nextID = edge.ToNodeID
			}

			if visited[nextID] {
				continue
			}
			visited[nextID] = true

			// Get node info.
			node, err := g.store.GetNodeByID(nextID)
			if err != nil {
				return nil, fmt.Errorf("traverseDeps GetNodeByID %d: %w", nextID, err)
			}

			nextDepth := item.depth + 1
			result = append(result, DepNode{
				NodeKey:    node.NodeKey,
				Name:       node.Name,
				Layer:      node.Layer,
				NodeType:   node.NodeType,
				Depth:      nextDepth,
				TrustScore: node.TrustScore,
				Freshness:  node.Freshness,
				Status:     node.Status,
			})

			queue = append(queue, queueItem{nodeID: nextID, depth: nextDepth})
		}
	}

	return result, nil
}

// QueryStats returns aggregate statistics for the given domain.
func (g *Graph) QueryStats(domainKey string) (*Stats, error) {
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	if err != nil {
		return nil, fmt.Errorf("QueryStats ListNodes: %w", err)
	}

	stats := &Stats{
		NodesByLayer:      make(map[string]int),
		EdgesByType:       make(map[string]int),
		EdgesByDerivation: make(map[string]int),
	}

	nodeIDSet := make(map[int64]bool, len(nodes))
	for _, n := range nodes {
		stats.NodeCount++
		stats.NodesByLayer[n.Layer]++
		nodeIDSet[n.NodeID] = true
		switch n.Status {
		case "active":
			stats.ActiveNodes++
		case "stale":
			stats.StaleNodes++
		}
	}

	// List all edges and count those whose from_node_id belongs to this domain.
	edges, err := g.store.ListEdges(store.EdgeFilter{})
	if err != nil {
		return nil, fmt.Errorf("QueryStats ListEdges: %w", err)
	}

	for _, e := range edges {
		if nodeIDSet[e.FromNodeID] {
			stats.EdgeCount++
			stats.EdgesByType[e.EdgeType]++
			stats.EdgesByDerivation[e.DerivationKind]++
		}
	}

	return stats, nil
}

package graph

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// PathOptions configures path queries.
type PathOptions struct {
	MaxDepth          int
	TopK              int
	Mode              string   // "directed" or "connected"
	DerivationFilter  []string
	IncludeStructural bool
}

// PathEdge represents a single edge in a path.
type PathEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	EdgeType   string `json:"type"`
	Derivation string `json:"derivation"`
}

// Path represents a single path from source to destination.
type Path struct {
	Nodes     []string   `json:"nodes"`
	Edges     []PathEdge `json:"edges"`
	Depth     int        `json:"depth"`
	PathScore float64    `json:"path_score"`
	PathCost  float64    `json:"path_cost"`
}

// PathResult contains all paths found between two nodes.
type PathResult struct {
	From            string `json:"from"`
	To              string `json:"to"`
	Mode            string `json:"mode"`
	Paths           []Path `json:"paths"`
	TotalPathsFound int    `json:"total_paths_found"`
}

// bfsState holds a BFS queue entry.
type bfsState struct {
	nodeID      int64
	nodeKey     string
	pathNodes   []string // node keys in order
	pathEdges   []PathEdge
	pathConfs   []float64 // per-edge confidence values
	visited     map[int64]bool
}

// QueryPath finds paths between two nodes using BFS.
func (g *Graph) QueryPath(fromKey, toKey string, opts PathOptions) (*PathResult, error) {
	policy := g.reg.TraversalPolicy()

	// Resolve start/end node IDs.
	fromID, err := g.store.GetNodeIDByKey(fromKey)
	if err != nil {
		return nil, fmt.Errorf("QueryPath: from node %q: %w", fromKey, err)
	}
	toID, err := g.store.GetNodeIDByKey(toKey)
	if err != nil {
		return nil, fmt.Errorf("QueryPath: to node %q: %w", toKey, err)
	}

	// Build derivation filter set (empty = allow all).
	derivSet := make(map[string]bool, len(opts.DerivationFilter))
	for _, d := range opts.DerivationFilter {
		derivSet[d] = true
	}

	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 10
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = 5
	}
	maxCandidates := topK * 10

	// BFS queue.
	queue := []bfsState{
		{
			nodeID:    fromID,
			nodeKey:   fromKey,
			pathNodes: []string{fromKey},
			pathEdges: nil,
			pathConfs: nil,
			visited:   map[int64]bool{fromID: true},
		},
	}

	var found []Path

	for len(queue) > 0 && len(found) < maxCandidates {
		cur := queue[0]
		queue = queue[1:]

		depth := len(cur.pathEdges)
		if depth >= maxDepth {
			continue
		}

		// Collect edges to explore: forward edges from cur.nodeID.
		edges, err := g.store.ListEdges(store.EdgeFilter{FromNodeID: cur.nodeID})
		if err != nil {
			return nil, fmt.Errorf("QueryPath ListEdges from %d: %w", cur.nodeID, err)
		}

		// In connected mode, also get reverse edges.
		if opts.Mode == "connected" {
			revEdges, err := g.store.ListEdges(store.EdgeFilter{ToNodeID: cur.nodeID})
			if err != nil {
				return nil, fmt.Errorf("QueryPath ListEdges to %d: %w", cur.nodeID, err)
			}
			edges = append(edges, revEdges...)
		}

		for _, e := range edges {
			// Skip inactive edges.
			if !e.Active {
				continue
			}

			// Determine the neighbor node ID and direction.
			var neighborID int64
			var edgeFrom, edgeTo string

			if e.FromNodeID == cur.nodeID {
				// Forward edge.
				neighborID = e.ToNodeID
				edgeFrom = cur.nodeKey
			} else {
				// Reverse edge (connected mode).
				neighborID = e.FromNodeID
				edgeTo = cur.nodeKey
			}

			// Skip structural edges unless opted in.
			if policy.IsStructural(e.EdgeType) && !opts.IncludeStructural {
				continue
			}

			// Apply derivation filter.
			if len(derivSet) > 0 && !derivSet[e.DerivationKind] {
				continue
			}

			// Skip cycles.
			if cur.visited[neighborID] {
				continue
			}

			// Resolve neighbor key.
			neighborNode, err := g.store.GetNodeByID(neighborID)
			if err != nil {
				return nil, fmt.Errorf("QueryPath GetNodeByID %d: %w", neighborID, err)
			}
			neighborKey := neighborNode.NodeKey

			// Set the missing side of the edge direction.
			if edgeFrom == "" {
				edgeFrom = neighborKey
			}
			if edgeTo == "" {
				edgeTo = neighborKey
			}

			// Confidence (default 1.0 if zero).
			conf := e.Confidence
			if conf <= 0 {
				conf = 1.0
			}

			pe := PathEdge{
				From:       edgeFrom,
				To:         edgeTo,
				EdgeType:   e.EdgeType,
				Derivation: e.DerivationKind,
			}

			newVisited := make(map[int64]bool, len(cur.visited)+1)
			for k, v := range cur.visited {
				newVisited[k] = v
			}
			newVisited[neighborID] = true

			newNodes := append(append([]string{}, cur.pathNodes...), neighborKey)
			newEdges := append(append([]PathEdge{}, cur.pathEdges...), pe)
			newConfs := append(append([]float64{}, cur.pathConfs...), conf)

			if neighborID == toID {
				// Found a path — compute score and record.
				p := buildPath(newNodes, newEdges, newConfs)
				found = append(found, p)
				if len(found) >= maxCandidates {
					break
				}
				continue
			}

			// Enqueue next state.
			queue = append(queue, bfsState{
				nodeID:    neighborID,
				nodeKey:   neighborKey,
				pathNodes: newNodes,
				pathEdges: newEdges,
				pathConfs: newConfs,
				visited:   newVisited,
			})
		}
	}

	// Sort: cost asc, then depth asc, then lexicographic node keys.
	sort.Slice(found, func(i, j int) bool {
		if found[i].PathCost != found[j].PathCost {
			return found[i].PathCost < found[j].PathCost
		}
		if found[i].Depth != found[j].Depth {
			return found[i].Depth < found[j].Depth
		}
		return strings.Join(found[i].Nodes, ",") < strings.Join(found[j].Nodes, ",")
	})

	total := len(found)
	if len(found) > topK {
		found = found[:topK]
	}

	return &PathResult{
		From:            fromKey,
		To:              toKey,
		Mode:            opts.Mode,
		Paths:           found,
		TotalPathsFound: total,
	}, nil
}

// buildPath constructs a Path from node keys, edges, and per-edge confidences.
func buildPath(nodes []string, edges []PathEdge, confs []float64) Path {
	depth := len(edges)
	cost := computePathCost(confs, depth)
	score := computePathScore(confs, depth)

	return Path{
		Nodes:     nodes,
		Edges:     edges,
		Depth:     depth,
		PathScore: score,
		PathCost:  cost,
	}
}

// computePathCost computes path_cost = Σ(-ln(edge_confidence)) + 0.05 * depth.
func computePathCost(confs []float64, depth int) float64 {
	sum := 0.0
	for _, c := range confs {
		if c <= 0 {
			c = 1.0
		}
		sum += -math.Log(c)
	}
	cost := sum + 0.05*float64(depth)
	return math.Round(cost*10000) / 10000
}

// computePathScore computes path_score = Π(edge_confidence) * 0.95^(depth-1).
func computePathScore(confs []float64, depth int) float64 {
	product := 1.0
	for _, c := range confs {
		if c <= 0 {
			c = 1.0
		}
		product *= c
	}
	if depth > 1 {
		product *= math.Pow(0.95, float64(depth-1))
	}
	return math.Round(product*10000) / 10000
}

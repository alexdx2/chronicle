package graph

import (
	"fmt"
	"math"
	"sort"

	"github.com/alexdx2/chronicle-core/store"
)

// ImpactOptions controls the impact query behavior.
type ImpactOptions struct {
	MaxDepth          int
	MinScore          float64
	TopK              int
	DerivationFilter  []string
	IncludeStructural bool
}

// ImpactEntry represents a single impacted node in the result.
type ImpactEntry struct {
	NodeKey     string   `json:"node_key"`
	Name        string   `json:"name"`
	Layer       string   `json:"layer"`
	NodeType    string   `json:"node_type"`
	Depth       int      `json:"depth"`
	ImpactScore float64  `json:"impact_score"`
	TrustChain  float64  `json:"trust_chain"`
	Path        []string `json:"path"`
	EdgeTypes   []string `json:"edge_types"`
}

// SurfaceEntry represents an endpoint or topic exposed by an impacted node.
type SurfaceEntry struct {
	NodeKey   string `json:"node_key"`
	Name      string `json:"name"`
	ExposedBy string `json:"exposed_by"`
}

// AffectedSurface contains endpoints and topics reachable from impacted nodes.
type AffectedSurface struct {
	Endpoints []SurfaceEntry `json:"endpoints"`
	Topics    []SurfaceEntry `json:"topics"`
}

// ImpactResult holds the full result of an impact query.
type ImpactResult struct {
	ChangedNode     string          `json:"changed_node"`
	Impacts         []ImpactEntry   `json:"impacts"`
	AffectedSurface AffectedSurface `json:"affected_surface"`
	TotalImpacted   int             `json:"total_impacted"`
	MaxDepthReached int             `json:"max_depth_reached"`
}

// QueryImpact performs a reverse BFS from changedNodeKey, finding all nodes
// that would be impacted if the changed node changes.
func (g *Graph) QueryImpact(changedNodeKey string, opts ImpactOptions) (*ImpactResult, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 10
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = 50
	}

	policy := g.reg.TraversalPolicy()

	// Build derivation filter set.
	filterSet := make(map[string]bool, len(opts.DerivationFilter))
	for _, d := range opts.DerivationFilter {
		filterSet[d] = true
	}

	// Get the starting node.
	startNode, err := g.store.GetNodeByKey(changedNodeKey)
	if err != nil {
		return nil, fmt.Errorf("QueryImpact: %w", err)
	}

	type queueItem struct {
		nodeID      int64
		depth       int
		pathKeys    []string  // node keys from start to this node
		edgeTypes   []string  // edge types traversed
		scoreProduct float64  // product of confidences so far
	}

	visited := map[int64]bool{startNode.NodeID: true}
	queue := []queueItem{{
		nodeID:       startNode.NodeID,
		depth:        0,
		pathKeys:     []string{changedNodeKey},
		edgeTypes:    []string{},
		scoreProduct: 1.0,
	}}

	var impacts []ImpactEntry
	maxDepthReached := 0

	active := true

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if item.depth >= opts.MaxDepth {
			continue
		}

		// Find incoming edges: edges where ToNodeID = current node.
		edges, err := g.store.ListEdges(store.EdgeFilter{
			ToNodeID: item.nodeID,
			Active:   &active,
		})
		if err != nil {
			return nil, fmt.Errorf("QueryImpact ListEdges: %w", err)
		}

		for _, edge := range edges {
			// Check traversal policy.
			if !opts.IncludeStructural && policy.IsStructural(edge.EdgeType) {
				continue
			}
			if !policy.AllowsReverseImpact(edge.EdgeType) {
				continue
			}

			// Apply derivation filter.
			if len(filterSet) > 0 && !filterSet[edge.DerivationKind] {
				continue
			}

			nextID := edge.FromNodeID
			if visited[nextID] {
				continue
			}
			visited[nextID] = true

			// Get node info.
			node, err := g.store.GetNodeByID(nextID)
			if err != nil {
				return nil, fmt.Errorf("QueryImpact GetNodeByID %d: %w", nextID, err)
			}

			nextDepth := item.depth + 1
			if nextDepth > maxDepthReached {
				maxDepthReached = nextDepth
			}

			// Compute impact score: 100 * Π(trust_score) * 0.95^(depth-1)
			newScoreProduct := item.scoreProduct * edge.TrustScore
			score := 100.0 * newScoreProduct * math.Pow(0.95, float64(nextDepth-1))
			score = math.Round(score*100) / 100 // round to 2 decimal places

			// Build path and edge types.
			newPath := make([]string, len(item.pathKeys)+1)
			copy(newPath, item.pathKeys)
			newPath[len(item.pathKeys)] = node.NodeKey

			newEdgeTypes := make([]string, len(item.edgeTypes)+1)
			copy(newEdgeTypes, item.edgeTypes)
			newEdgeTypes[len(item.edgeTypes)] = edge.EdgeType

			impacts = append(impacts, ImpactEntry{
				NodeKey:     node.NodeKey,
				Name:        node.Name,
				Layer:       node.Layer,
				NodeType:    node.NodeType,
				Depth:       nextDepth,
				ImpactScore: score,
				TrustChain:  math.Round(newScoreProduct*1000) / 1000,
				Path:        newPath,
				EdgeTypes:   newEdgeTypes,
			})

			queue = append(queue, queueItem{
				nodeID:       nextID,
				depth:        nextDepth,
				pathKeys:     newPath,
				edgeTypes:    newEdgeTypes,
				scoreProduct: newScoreProduct,
			})
		}
	}

	// Filter by MinScore.
	filtered := impacts[:0]
	for _, imp := range impacts {
		if imp.ImpactScore >= opts.MinScore {
			filtered = append(filtered, imp)
		}
	}
	impacts = filtered

	// Sort by score descending, then depth ascending.
	sort.Slice(impacts, func(i, j int) bool {
		if impacts[i].ImpactScore != impacts[j].ImpactScore {
			return impacts[i].ImpactScore > impacts[j].ImpactScore
		}
		return impacts[i].Depth < impacts[j].Depth
	})

	// Apply TopK.
	if len(impacts) > topK {
		impacts = impacts[:topK]
	}

	// Forward expansion: find affected endpoints and topics from impacted nodes.
	surface := g.collectAffectedSurface(startNode.NodeID, impacts)

	return &ImpactResult{
		ChangedNode:     changedNodeKey,
		Impacts:         impacts,
		AffectedSurface: surface,
		TotalImpacted:   len(impacts),
		MaxDepthReached: maxDepthReached,
	}, nil
}

// surfaceEdgeTypes defines which forward edge types reveal affected surface.
var surfaceEdgeTypes = map[string]string{
	"EXPOSES_ENDPOINT": "endpoints",
	"PUBLISHES_TOPIC":  "topics",
}

// collectAffectedSurface follows forward EXPOSES_ENDPOINT and PUBLISHES_TOPIC
// edges from all impacted nodes (and the changed node itself) to find the
// externally visible surface affected by the change.
func (g *Graph) collectAffectedSurface(changedNodeID int64, impacts []ImpactEntry) AffectedSurface {
	// Collect all impacted node IDs.
	impactedIDs := map[int64]string{changedNodeID: "(changed)"}
	for _, imp := range impacts {
		node, err := g.store.GetNodeByKey(imp.NodeKey)
		if err == nil {
			impactedIDs[node.NodeID] = imp.NodeKey
		}
	}

	seen := map[string]bool{}
	var endpoints []SurfaceEntry
	var topics []SurfaceEntry

	active := true
	for nodeID, nodeKey := range impactedIDs {
		edges, err := g.store.ListEdges(store.EdgeFilter{
			FromNodeID: nodeID,
			Active:     &active,
		})
		if err != nil {
			continue
		}
		for _, e := range edges {
			category, ok := surfaceEdgeTypes[e.EdgeType]
			if !ok {
				continue
			}
			targetNode, err := g.store.GetNodeByID(e.ToNodeID)
			if err != nil {
				continue
			}
			if seen[targetNode.NodeKey] {
				continue
			}
			seen[targetNode.NodeKey] = true

			entry := SurfaceEntry{
				NodeKey:   targetNode.NodeKey,
				Name:      targetNode.Name,
				ExposedBy: nodeKey,
			}
			if category == "endpoints" {
				endpoints = append(endpoints, entry)
			} else {
				topics = append(topics, entry)
			}
		}
	}

	return AffectedSurface{
		Endpoints: endpoints,
		Topics:    topics,
	}
}

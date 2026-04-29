package graph

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// AliasKey scopes alias lookups to prevent cross-kind false matches.
type AliasKey struct {
	Kind  string
	Value string
}

// NodeRef identifies a node across repos.
type NodeRef struct {
	RepoName   string
	NodeID     int64
	NodeKey    string
	Status     string
	TrustScore float64
}

// FederatedGraph implements GraphQuerier across multiple repos.
type FederatedGraph struct {
	graphs  map[string]*Graph     // repo_name -> Graph
	order   []string              // sorted repo names for deterministic iteration
	index   map[string][]NodeRef  // node_key -> candidates (non-external nodes)
	aliases map[AliasKey][]NodeRef // (kind, normalized_alias) -> candidates
}

// NewFederatedGraph creates a federated graph from multiple repo graphs.
// repos maps repo_name -> *Graph. Builds indexes for cross-repo resolution.
func NewFederatedGraph(repos map[string]*Graph) (*FederatedGraph, error) {
	fg := &FederatedGraph{
		graphs:  repos,
		index:   make(map[string][]NodeRef),
		aliases: make(map[AliasKey][]NodeRef),
	}

	// Deterministic order.
	for name := range repos {
		fg.order = append(fg.order, name)
	}
	sort.Strings(fg.order)

	// Build indexes.
	for _, repoName := range fg.order {
		g := repos[repoName]
		nodes, err := g.store.ListNodes(store.NodeFilter{})
		if err != nil {
			return nil, fmt.Errorf("indexing repo %s: %w", repoName, err)
		}
		for _, n := range nodes {
			if n.Status == "external" {
				continue // external nodes are NOT indexed as resolution candidates
			}
			ref := NodeRef{
				RepoName:   repoName,
				NodeID:     n.NodeID,
				NodeKey:    n.NodeKey,
				Status:     n.Status,
				TrustScore: n.TrustScore,
			}
			fg.index[n.NodeKey] = append(fg.index[n.NodeKey], ref)

			// Index aliases for this node.
			nodeAliases, err := g.store.ListAliasesByNode(n.NodeID)
			if err != nil {
				continue
			}
			for _, a := range nodeAliases {
				key := AliasKey{Kind: a.AliasKind, Value: a.NormalizedAlias}
				fg.aliases[key] = append(fg.aliases[key], ref)
			}
		}
	}

	return fg, nil
}

// resolve attempts to resolve an external node to a non-external node in another repo.
// Returns the resolved NodeRef, resolution method, alias details, or ambiguous candidates.
func (fg *FederatedGraph) resolve(externalNode *store.NodeRow, sourceRepo string) (ref *NodeRef, method string, aliasKind string, aliasValue string, ambiguous []AmbiguousRef) {
	// Tier 1: exact node_key match.
	if candidates, ok := fg.index[externalNode.NodeKey]; ok {
		active := filterActive(candidates, sourceRepo)
		if len(active) == 1 {
			return &active[0], "exact", "", "", nil
		}
		if len(active) > 1 {
			return nil, "", "", "", toAmbiguous(active)
		}
	}

	// Tier 2: alias match scoped by kind.
	// Collect all aliases for this external node.
	sourceGraph := fg.graphs[sourceRepo]
	if sourceGraph == nil {
		return nil, "none", "", "", nil
	}
	extAliases, err := sourceGraph.store.ListAliasesByNode(externalNode.NodeID)
	if err != nil {
		return nil, "none", "", "", nil
	}

	// Also try the node's name as a potential alias match.
	nameNorm := strings.ToLower(strings.TrimSpace(externalNode.Name))

	// Try each alias against the index.
	for _, a := range extAliases {
		key := AliasKey{Kind: a.AliasKind, Value: a.NormalizedAlias}
		if candidates, ok := fg.aliases[key]; ok {
			active := filterActive(candidates, sourceRepo)
			if len(active) == 1 {
				return &active[0], "alias", a.AliasKind, a.NormalizedAlias, nil
			}
			if len(active) > 1 {
				return nil, "", "", "", toAmbiguous(active)
			}
		}
	}

	// Try node name against aliases of all kinds.
	for key, candidates := range fg.aliases {
		if key.Value == nameNorm {
			active := filterActive(candidates, sourceRepo)
			if len(active) == 1 {
				return &active[0], "alias", key.Kind, nameNorm, nil
			}
		}
	}

	return nil, "none", "", "", nil
}

func filterActive(candidates []NodeRef, excludeRepo string) []NodeRef {
	var active []NodeRef
	for _, c := range candidates {
		if c.RepoName != excludeRepo && c.Status == "active" {
			active = append(active, c)
		}
	}
	return active
}

func toAmbiguous(refs []NodeRef) []AmbiguousRef {
	out := make([]AmbiguousRef, len(refs))
	for i, r := range refs {
		out[i] = AmbiguousRef{
			RepoName:   r.RepoName,
			NodeKey:    r.NodeKey,
			TrustScore: r.TrustScore,
			Status:     r.Status,
		}
	}
	return out
}

// QueryDeps performs BFS across federated graphs, resolving external nodes.
func (fg *FederatedGraph) QueryDeps(nodeKey string, maxDepth int, derivationFilter []string) ([]DepNode, error) {
	return fg.traverseDeps(nodeKey, maxDepth, derivationFilter, false)
}

// QueryReverseDeps performs reverse BFS across federated graphs.
func (fg *FederatedGraph) QueryReverseDeps(nodeKey string, maxDepth int, derivationFilter []string) ([]DepNode, error) {
	return fg.traverseDeps(nodeKey, maxDepth, derivationFilter, true)
}

func (fg *FederatedGraph) traverseDeps(nodeKey string, maxDepth int, derivationFilter []string, reverse bool) ([]DepNode, error) {
	filterSet := make(map[string]bool, len(derivationFilter))
	for _, d := range derivationFilter {
		filterSet[d] = true
	}

	// Find the starting node in any repo.
	startRepo, startNode, err := fg.findNode(nodeKey)
	if err != nil {
		return nil, fmt.Errorf("traverseDeps: %w", err)
	}

	type queueItem struct {
		repoName string
		nodeID   int64
		nodeKey  string
		depth    int
	}

	visited := make(map[string]bool) // "repo:nodeID"
	visitKey := func(repo string, id int64) string { return fmt.Sprintf("%s:%d", repo, id) }
	visited[visitKey(startRepo, startNode.NodeID)] = true

	queue := []queueItem{{repoName: startRepo, nodeID: startNode.NodeID, nodeKey: nodeKey, depth: 0}}
	var result []DepNode

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if item.depth >= maxDepth {
			continue
		}

		g := fg.graphs[item.repoName]
		if g == nil {
			continue
		}

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
			if len(filterSet) > 0 && !filterSet[edge.DerivationKind] {
				continue
			}

			var nextID int64
			if reverse {
				nextID = edge.FromNodeID
			} else {
				nextID = edge.ToNodeID
			}

			vk := visitKey(item.repoName, nextID)
			if visited[vk] {
				continue
			}
			visited[vk] = true

			node, err := g.store.GetNodeByID(nextID)
			if err != nil {
				return nil, fmt.Errorf("traverseDeps GetNodeByID %d: %w", nextID, err)
			}

			nextDepth := item.depth + 1

			// If node is external, try to resolve it.
			if node.Status == "external" {
				ref, method, aliasKind, aliasValue, ambiguous := fg.resolve(node, item.repoName)
				if ref != nil {
					// Resolved — add the resolved node and continue traversal in the target repo.
					resolvedGraph := fg.graphs[ref.RepoName]
					resolvedNode, err := resolvedGraph.store.GetNodeByID(ref.NodeID)
					if err != nil {
						continue
					}

					rvk := visitKey(ref.RepoName, ref.NodeID)
					if visited[rvk] {
						continue
					}
					visited[rvk] = true

					result = append(result, DepNode{
						NodeKey:             resolvedNode.NodeKey,
						Name:                resolvedNode.Name,
						Layer:               resolvedNode.Layer,
						NodeType:            resolvedNode.NodeType,
						Depth:               nextDepth,
						TrustScore:          resolvedNode.TrustScore,
						Freshness:           resolvedNode.Freshness,
						Status:              resolvedNode.Status,
						SourceRepo:          item.repoName,
						ResolvedRepo:        ref.RepoName,
						ResolutionStatus:    "external_resolved",
						ResolutionMethod:    method,
						ResolutionAliasKind: aliasKind,
						ResolutionAlias:     aliasValue,
					})

					queue = append(queue, queueItem{
						repoName: ref.RepoName,
						nodeID:   ref.NodeID,
						nodeKey:  resolvedNode.NodeKey,
						depth:    nextDepth,
					})
				} else if len(ambiguous) > 0 {
					result = append(result, DepNode{
						NodeKey:             node.NodeKey,
						Name:                node.Name,
						Layer:               node.Layer,
						NodeType:            node.NodeType,
						Depth:               nextDepth,
						TrustScore:          node.TrustScore,
						Freshness:           node.Freshness,
						Status:              node.Status,
						SourceRepo:          item.repoName,
						ResolutionStatus:    "ambiguous",
						AmbiguousCandidates: ambiguous,
					})
					// Stop traversal at ambiguous nodes.
				} else {
					result = append(result, DepNode{
						NodeKey:          node.NodeKey,
						Name:             node.Name,
						Layer:            node.Layer,
						NodeType:         node.NodeType,
						Depth:            nextDepth,
						TrustScore:       node.TrustScore,
						Freshness:        node.Freshness,
						Status:           node.Status,
						SourceRepo:       item.repoName,
						ResolutionStatus: "external_unresolved",
					})
					// Stop traversal at unresolved nodes.
				}
				continue
			}

			// Regular local node.
			result = append(result, DepNode{
				NodeKey:          node.NodeKey,
				Name:             node.Name,
				Layer:            node.Layer,
				NodeType:         node.NodeType,
				Depth:            nextDepth,
				TrustScore:       node.TrustScore,
				Freshness:        node.Freshness,
				Status:           node.Status,
				SourceRepo:       item.repoName,
				ResolutionStatus: "local",
			})

			queue = append(queue, queueItem{
				repoName: item.repoName,
				nodeID:   nextID,
				nodeKey:  node.NodeKey,
				depth:    nextDepth,
			})
		}
	}

	return result, nil
}

// findNode searches all repos for a node by key (prefers non-external).
func (fg *FederatedGraph) findNode(nodeKey string) (repoName string, node *store.NodeRow, err error) {
	// Check index first (non-external nodes).
	if refs, ok := fg.index[nodeKey]; ok && len(refs) > 0 {
		ref := refs[0]
		g := fg.graphs[ref.RepoName]
		n, err := g.store.GetNodeByKey(nodeKey)
		if err == nil {
			return ref.RepoName, n, nil
		}
	}

	// Fall back to searching all repos.
	for _, name := range fg.order {
		g := fg.graphs[name]
		n, err := g.store.GetNodeByKey(nodeKey)
		if err == nil {
			return name, n, nil
		}
	}
	return "", nil, fmt.Errorf("node %q not found in any federated repo", nodeKey)
}

// QueryPath finds paths across federated graphs.
func (fg *FederatedGraph) QueryPath(fromKey, toKey string, opts PathOptions) (*PathResult, error) {
	// For now, delegate to the repo that owns the from node.
	// Full cross-repo pathfinding is complex — start with single-repo path.
	repoName, _, err := fg.findNode(fromKey)
	if err != nil {
		return nil, err
	}
	return fg.graphs[repoName].QueryPath(fromKey, toKey, opts)
}

// QueryImpact performs impact analysis across federated graphs.
func (fg *FederatedGraph) QueryImpact(changedNodeKey string, opts ImpactOptions) (*ImpactResult, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 10
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = 50
	}

	// Find the changed node.
	startRepo, startNode, err := fg.findNode(changedNodeKey)
	if err != nil {
		return nil, fmt.Errorf("QueryImpact: %w", err)
	}

	policy := fg.graphs[startRepo].reg.TraversalPolicy()

	filterSet := make(map[string]bool, len(opts.DerivationFilter))
	for _, d := range opts.DerivationFilter {
		filterSet[d] = true
	}

	type queueItem struct {
		repoName     string
		nodeID       int64
		depth        int
		pathKeys     []string
		edgeTypes    []string
		scoreProduct float64
	}

	visited := make(map[string]bool)
	visitKey := func(repo string, id int64) string { return fmt.Sprintf("%s:%d", repo, id) }
	visited[visitKey(startRepo, startNode.NodeID)] = true

	queue := []queueItem{{
		repoName:     startRepo,
		nodeID:       startNode.NodeID,
		depth:        0,
		pathKeys:     []string{changedNodeKey},
		edgeTypes:    nil,
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

		g := fg.graphs[item.repoName]
		if g == nil {
			continue
		}

		edges, err := g.store.ListEdges(store.EdgeFilter{
			ToNodeID: item.nodeID,
			Active:   &active,
		})
		if err != nil {
			return nil, fmt.Errorf("QueryImpact ListEdges: %w", err)
		}

		for _, edge := range edges {
			if !opts.IncludeStructural && policy.IsStructural(edge.EdgeType) {
				continue
			}
			if !policy.AllowsReverseImpact(edge.EdgeType) {
				continue
			}
			if len(filterSet) > 0 && !filterSet[edge.DerivationKind] {
				continue
			}

			nextID := edge.FromNodeID
			vk := visitKey(item.repoName, nextID)
			if visited[vk] {
				continue
			}
			visited[vk] = true

			node, err := g.store.GetNodeByID(nextID)
			if err != nil {
				continue
			}

			nextDepth := item.depth + 1
			if nextDepth > maxDepthReached {
				maxDepthReached = nextDepth
			}

			newScoreProduct := item.scoreProduct * edge.TrustScore
			score := 100.0 * newScoreProduct * math.Pow(0.95, float64(nextDepth-1))
			score = math.Round(score*100) / 100

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
				repoName:     item.repoName,
				nodeID:       nextID,
				depth:        nextDepth,
				pathKeys:     newPath,
				edgeTypes:    newEdgeTypes,
				scoreProduct: newScoreProduct,
			})
		}

		// Also check: are there external nodes in OTHER repos that resolve to this node?
		// This enables cross-repo reverse impact.
		for _, otherRepo := range fg.order {
			if otherRepo == item.repoName {
				continue
			}
			otherGraph := fg.graphs[otherRepo]

			// Find external nodes that reference this node's key.
			extNodes, err := otherGraph.store.ListNodes(store.NodeFilter{Status: "external"})
			if err != nil {
				continue
			}

			for _, ext := range extNodes {
				evk := visitKey(otherRepo, ext.NodeID)
				if visited[evk] {
					continue
				}

				// Check if this external node resolves to our current node.
				ref, _, _, _, _ := fg.resolve(&ext, otherRepo)
				if ref == nil || ref.RepoName != item.repoName || ref.NodeID != item.nodeID {
					continue
				}

				visited[evk] = true

				// Find edges TO this external node in the other repo (reverse impact).
				otherEdges, err := otherGraph.store.ListEdges(store.EdgeFilter{
					ToNodeID: ext.NodeID,
					Active:   &active,
				})
				if err != nil {
					continue
				}

				for _, oe := range otherEdges {
					if !opts.IncludeStructural && policy.IsStructural(oe.EdgeType) {
						continue
					}
					if len(filterSet) > 0 && !filterSet[oe.DerivationKind] {
						continue
					}

					fromID := oe.FromNodeID
					fvk := visitKey(otherRepo, fromID)
					if visited[fvk] {
						continue
					}
					visited[fvk] = true

					fromNode, err := otherGraph.store.GetNodeByID(fromID)
					if err != nil {
						continue
					}

					nextDepth := item.depth + 1
					if nextDepth > maxDepthReached {
						maxDepthReached = nextDepth
					}

					newScoreProduct := item.scoreProduct * oe.TrustScore
					impactScore := 100.0 * newScoreProduct * math.Pow(0.95, float64(nextDepth-1))
					impactScore = math.Round(impactScore*100) / 100

					newPath := make([]string, len(item.pathKeys)+1)
					copy(newPath, item.pathKeys)
					newPath[len(item.pathKeys)] = fromNode.NodeKey

					newEdgeTypes := make([]string, len(item.edgeTypes)+1)
					copy(newEdgeTypes, item.edgeTypes)
					newEdgeTypes[len(item.edgeTypes)] = oe.EdgeType

					impacts = append(impacts, ImpactEntry{
						NodeKey:     fromNode.NodeKey,
						Name:        fromNode.Name,
						Layer:       fromNode.Layer,
						NodeType:    fromNode.NodeType,
						Depth:       nextDepth,
						ImpactScore: impactScore,
						TrustChain:  math.Round(newScoreProduct*1000) / 1000,
						Path:        newPath,
						EdgeTypes:   newEdgeTypes,
					})

					queue = append(queue, queueItem{
						repoName:     otherRepo,
						nodeID:       fromID,
						depth:        nextDepth,
						pathKeys:     newPath,
						edgeTypes:    newEdgeTypes,
						scoreProduct: newScoreProduct,
					})
				}
			}
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

	sort.Slice(impacts, func(i, j int) bool {
		if impacts[i].ImpactScore != impacts[j].ImpactScore {
			return impacts[i].ImpactScore > impacts[j].ImpactScore
		}
		return impacts[i].Depth < impacts[j].Depth
	})

	if len(impacts) > topK {
		impacts = impacts[:topK]
	}

	return &ImpactResult{
		ChangedNode:     changedNodeKey,
		Impacts:         impacts,
		TotalImpacted:   len(impacts),
		MaxDepthReached: maxDepthReached,
	}, nil
}

// QueryStats returns aggregated stats across all federated repos.
func (fg *FederatedGraph) QueryStats(domainKey string) (*Stats, error) {
	agg := &Stats{
		NodesByLayer:      make(map[string]int),
		EdgesByType:       make(map[string]int),
		EdgesByDerivation: make(map[string]int),
	}

	for _, name := range fg.order {
		g := fg.graphs[name]
		stats, err := g.QueryStats(domainKey)
		if err != nil {
			continue
		}
		agg.NodeCount += stats.NodeCount
		agg.EdgeCount += stats.EdgeCount
		agg.ActiveNodes += stats.ActiveNodes
		agg.StaleNodes += stats.StaleNodes
		for k, v := range stats.NodesByLayer {
			agg.NodesByLayer[k] += v
		}
		for k, v := range stats.EdgesByType {
			agg.EdgesByType[k] += v
		}
		for k, v := range stats.EdgesByDerivation {
			agg.EdgesByDerivation[k] += v
		}
	}

	return agg, nil
}

// Compile-time check: FederatedGraph implements GraphQuerier.
var _ GraphQuerier = (*FederatedGraph)(nil)

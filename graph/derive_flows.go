package graph

import (
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// maxInjectDepth caps the BFS transitive-closure over INJECTS edges to prevent
// runaway traversal in pathological graphs.
const maxInjectDepth = 6

// DeriveFlows derives flow nodes deterministically from endpoint nodes in the domain.
//
// For each active contract:endpoint node:
//  1. Find its exposing component (controller/gateway) via EXPOSES_ENDPOINT.
//  2. Compute the REQUIRES set = transitive closure of active INJECTS edges from
//     that component (code-layer only, max depth 6, cycle-safe). The controller
//     itself is excluded; every reached provider is included.
//  3. Upsert a flow:use_case node with a deterministic key, and write:
//     - endpoint TRIGGERS_FLOW flow
//     - flow REQUIRES each provider in the closure
//
// The derivation kind is "inferred" (the registry's derivation_kinds list does
// not include "derived"). Confidence is 0.9.
//
// Idempotent: re-running upserts rather than duplicates (deterministic keys).
func (g *Graph) DeriveFlows(domainKey string, revisionID int64) error {
	active := true

	// Collect all active EXPOSES_ENDPOINT edges.
	exposeEdges, err := g.store.ListEdges(store.EdgeFilter{
		EdgeType: "EXPOSES_ENDPOINT",
		Active:   &active,
	})
	if err != nil {
		return err
	}

	// Collect all active INJECTS edges.
	injectEdges, err := g.store.ListEdges(store.EdgeFilter{
		EdgeType: "INJECTS",
		Active:   &active,
	})
	if err != nil {
		return err
	}

	// Build a node-ID → node_key + layer lookup (code-layer nodes for injection closure).
	// We need this because graph.UpsertEdge stores IDs but not keys in edge rows.
	allCodeNodes, err := g.store.ListNodes(store.NodeFilter{Layer: "code"})
	if err != nil {
		return err
	}
	codeNodeKey := make(map[int64]string, len(allCodeNodes))
	codeNodeLayer := make(map[int64]string, len(allCodeNodes))
	for _, n := range allCodeNodes {
		codeNodeKey[n.NodeID] = n.NodeKey
		codeNodeLayer[n.NodeID] = n.Layer
	}

	// Also need endpoint and controller node IDs → keys.
	// Build a general ID→key map for all nodes we may reference.
	idToKey := make(map[int64]string)
	for _, n := range allCodeNodes {
		idToKey[n.NodeID] = n.NodeKey
	}
	// Add contract-layer nodes (endpoints)
	contractNodes, err := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "endpoint"})
	if err != nil {
		return err
	}
	for _, n := range contractNodes {
		idToKey[n.NodeID] = n.NodeKey
	}

	// Build adjacency (node_id → []neighbor node_id) for INJECTS edges (code-layer only).
	// We must verify both sides are code-layer nodes.
	injectAdj := make(map[int64][]int64)
	for _, e := range injectEdges {
		_, fromIsCode := codeNodeKey[e.FromNodeID]
		_, toIsCode := codeNodeKey[e.ToNodeID]
		if !fromIsCode || !toIsCode {
			continue
		}
		injectAdj[e.FromNodeID] = append(injectAdj[e.FromNodeID], e.ToNodeID)
	}

	// Build: endpoint node_id → controller node_id (from EXPOSES_ENDPOINT edges).
	endpointToController := make(map[int64]int64)
	for _, e := range exposeEdges {
		// to_node should be a contract:endpoint; from_node is the controller.
		// Check that the to_node is an endpoint in this domain.
		toKey := resolveNodeKey(g, e.ToNodeID, idToKey)
		if !strings.HasPrefix(toKey, "contract:endpoint:"+domainKey+":") {
			continue
		}
		endpointToController[e.ToNodeID] = e.FromNodeID
	}

	// Process each endpoint in the domain.
	for _, ep := range contractNodes {
		if ep.Status == "deleted" || ep.Status == "stale" {
			continue
		}
		ctrlID, ok := endpointToController[ep.NodeID]
		if !ok {
			// Endpoint has no known exposing controller — skip.
			continue
		}

		// Compute REQUIRES closure from the controller via INJECTS, max depth 6.
		closureIDs := injectiveClosureIDs(ctrlID, injectAdj, maxInjectDepth)

		// Derive flow name: lastSegment (METHOD path)
		flowName := deriveFlowName(ep.Name)

		// Deterministic flow key: strip "contract:endpoint:<domain>:" prefix to get the suffix.
		epSuffix := strings.TrimPrefix(ep.NodeKey, "contract:endpoint:"+domainKey+":")
		flowKey := "flow:use_case:" + domainKey + ":" + epSuffix

		// Upsert the flow node.
		g.ensureFlowNode(domainKey, revisionID, flowKey, flowName)

		// Get the flow node ID.
		flowID, err := g.store.GetNodeIDByKey(flowKey)
		if err != nil {
			continue
		}

		// Upsert TRIGGERS_FLOW: endpoint → flow
		triggerEdgeKey := ep.NodeKey + "->" + flowKey + ":TRIGGERS_FLOW"
		g.store.UpsertEdge(store.EdgeRow{
			EdgeKey:            triggerEdgeKey,
			FromNodeID:         ep.NodeID,
			ToNodeID:           flowID,
			FromNodeKey:        ep.NodeKey,
			ToNodeKey:          flowKey,
			EdgeType:           "TRIGGERS_FLOW",
			DerivationKind:     "inferred",
			Active:             true,
			LastSeenRevisionID: revisionID,
			Confidence:         0.9,
			Freshness:          1.0,
			TrustScore:         0.9,
			Metadata:           "{}",
		})

		// Upsert REQUIRES: flow → each provider in closure.
		for _, provID := range closureIDs {
			provKey, found := codeNodeKey[provID]
			if !found {
				continue
			}
			reqEdgeKey := flowKey + "->" + provKey + ":REQUIRES"
			g.store.UpsertEdge(store.EdgeRow{
				EdgeKey:            reqEdgeKey,
				FromNodeID:         flowID,
				ToNodeID:           provID,
				FromNodeKey:        flowKey,
				ToNodeKey:          provKey,
				EdgeType:           "REQUIRES",
				DerivationKind:     "inferred",
				Active:             true,
				LastSeenRevisionID: revisionID,
				Confidence:         0.9,
				Freshness:          1.0,
				TrustScore:         0.9,
				Metadata:           "{}",
			})
		}
	}

	return nil
}

// resolveNodeKey returns the node key for a given node ID, consulting the
// pre-built idToKey map first, then falling back to a store lookup.
func resolveNodeKey(g *Graph, nodeID int64, idToKey map[int64]string) string {
	if key, ok := idToKey[nodeID]; ok {
		return key
	}
	n, err := g.store.GetNodeByID(nodeID)
	if err != nil || n == nil {
		return ""
	}
	return n.NodeKey
}

// injectiveClosureIDs performs a BFS over INJECTS edges starting from originID,
// collecting all reachable provider node IDs (excluding origin itself).
// Terminates at maxDepth hops or when cycles are detected (cycle-safe via visited set).
func injectiveClosureIDs(originID int64, adj map[int64][]int64, maxDepth int) []int64 {
	visited := make(map[int64]bool)
	visited[originID] = true // exclude controller itself

	type entry struct {
		id    int64
		depth int
	}
	queue := []entry{{id: originID, depth: 0}}
	var result []int64

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur.depth >= maxDepth {
			continue
		}
		for _, neighborID := range adj[cur.id] {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true
			result = append(result, neighborID)
			queue = append(queue, entry{id: neighborID, depth: cur.depth + 1})
		}
	}
	return result
}

// deriveFlowName converts an endpoint display name to a human-readable flow name.
// Format: lastSegment (METHOD /full/path)
// Examples:
//
//	"POST /arena/attack"      → "attack (POST /arena/attack)"
//	"GET /stats/leaderboard"  → "leaderboard (GET /stats/leaderboard)"
//	"WS /watch-battle"        → "watch-battle (WS /watch-battle)"
//	"GET /"                   → "/ (GET /)"
func deriveFlowName(endpointName string) string {
	// endpointName is "METHOD /path", e.g. "POST /arena/attack"
	parts := strings.SplitN(endpointName, " ", 2)
	if len(parts) != 2 {
		return endpointName
	}
	path := parts[1] // e.g. "/arena/attack"

	// Find the last path segment
	segments := strings.Split(strings.Trim(path, "/"), "/")
	lastSeg := segments[len(segments)-1]
	if lastSeg == "" {
		lastSeg = "/"
	}

	return lastSeg + " (" + endpointName + ")"
}

// ensureFlowNode creates a flow:use_case node if it doesn't already exist.
func (g *Graph) ensureFlowNode(domainKey string, revisionID int64, nodeKey, name string) {
	if _, err := g.store.GetNodeIDByKey(nodeKey); err == nil {
		return // already exists
	}
	g.store.UpsertNode(store.NodeRow{
		NodeKey:            nodeKey,
		Layer:              "flow",
		NodeType:           "use_case",
		DomainKey:          domainKey,
		Name:               name,
		Status:             "active",
		LastSeenRevisionID: revisionID,
		Confidence:         0.9,
		Freshness:          1.0,
		TrustScore:         0.9,
		Metadata:           "{}",
	})
}

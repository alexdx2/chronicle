package viewmodel

import (
	"fmt"

	"github.com/alexdx2/chronicle-core/store"
)

// OwnershipMap computes, once, the owning-service NodeRow for every code/data
// node in st's current graph — the same list-and-build step ownershipMap
// (this package's private diagram-framing logic, unchanged by this file)
// already performs for C2/C3/deps boundary framing. Keyed by the OWNED
// node's own node_key (not its numeric id, and not the owner's key): a
// caller collapsing many results in one query (Task 11's
// CoreImpactQuerier.QueryImpact/TopicSubscribers, in package review) already
// has each result's NodeKey in hand and wants O(1) lookups against ONE
// list-and-build pass, not one full rebuild per result — see OwningService's
// own doc comment for why that distinction matters.
//
// A missing entry means unowned: either a top-level node with no service
// claiming it (a domain concept, an infra node, ...), or a node that IS
// itself a service/external_system, which this map never assigns an "owner"
// to (ownershipMap's own loops only ever attribute layer=="code" and
// layer=="data" nodes). Callers that need to tell those two unowned cases
// apart (Task 11's Collapse does) fall back to the node's own Layer/NodeType.
func OwnershipMap(st *store.Store) (map[string]*store.NodeRow, error) {
	nodes, err := st.ListNodes(store.NodeFilter{})
	if err != nil {
		return nil, fmt.Errorf("viewmodel: OwnershipMap: list nodes: %w", err)
	}
	edges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		return nil, fmt.Errorf("viewmodel: OwnershipMap: list edges: %w", err)
	}

	byID := ownershipMap(activeOnly(nodes), activeEdges(edges))

	byKey := make(map[string]*store.NodeRow, len(byID))
	for i := range nodes {
		if svc, ok := byID[nodes[i].NodeID]; ok {
			byKey[nodes[i].NodeKey] = svc
		}
	}
	return byKey, nil
}

// OwningService returns the node_key of the service that owns nodeKey — the
// same logic that builds C2/C3/deps boundary frames today (package-private
// ownershipMap, via OwnershipMap above). ok=false means nodeKey is unowned: a
// top-level node the graph doesn't attribute to any service (a bare domain
// concept, an infra node, ...), or nodeKey is itself a service/external_system.
//
// This rebuilds the WHOLE graph's ownership map on every call (via
// OwnershipMap: a full ListNodes/ListEdges plus the ownershipMap pass) — fine
// for a single lookup (e.g. Task 11's CoreImpactQuerier.Collapse, which
// resolves exactly one bare node_key with no traversal), but wrong to call in
// a loop: a caller collapsing many results from one query (QueryImpact's
// dozens of ImpactEntry rows) must call OwnershipMap ONCE itself and do its
// own per-entry map lookups against that single result, or it silently
// redoes the full-graph list-and-build work once per result instead of once
// per query.
func OwningService(st *store.Store, nodeKey string) (serviceKey string, ok bool) {
	m, err := OwnershipMap(st)
	if err != nil {
		return "", false
	}
	svc, found := m[nodeKey]
	if !found {
		return "", false
	}
	return svc.NodeKey, true
}

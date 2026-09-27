package review

import (
	"fmt"
	"sort"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/store"
)

// Kind values a SystemRef can carry — a closed, three-member vocabulary
// (SystemRef.Kind is a plain string, per the brief's own type, not a named
// type: these are unexported convenience constants for this file's own use,
// not a public enum).
const (
	kindService        = "service"
	kindExternalSystem = "external_system"
	kindDomain         = "domain"
)

// PathStep is one hop of a SystemImpact's path: the node reached, by name,
// and the edge type that reached it.
type PathStep struct{ Node, Edge string }

// SystemRef names one collapsed system — a service, an external boundary, or
// (the catch-all) a top-level graph node that is neither: a bare domain
// concept, an infra node, or anything else ownershipMap never attributes to
// a service.
type SystemRef struct{ Kind, Key, Name, Repo, Domain string }

// SystemImpact is one system reachable from a seed, collapsed from
// (possibly several) raw graph nodes down to the system that owns them.
type SystemImpact struct {
	System     SystemRef
	Path       []PathStep
	Confidence float64
}

// ImpactQuerier abstracts the impact BFS + collapsing step Analyze needs,
// over one graph version (base or head). CoreImpactQuerier is the single-repo
// implementation; a federated implementation (chronicle-pro, Task 15) can
// satisfy the same interface over a FederatedGraph.
type ImpactQuerier interface {
	// QueryImpact walks depth hops of reverse dependency edges from
	// seedNodeKey and collapses every reached node to the system that owns
	// it, deduplicated by SystemRef.Key (keeping the shortest path).
	QueryImpact(seedNodeKey string, depth int) ([]SystemImpact, error)
	// TopicSubscribers collapses every active CONSUMES_TOPIC edge onto
	// topicNodeKey to the system that owns the subscribing node.
	TopicSubscribers(topicNodeKey string) ([]SystemImpact, error)
	// Collapse resolves a bare node_key (no traversal) to the SystemRef that
	// owns it — the same collapsing step QueryImpact/TopicSubscribers apply
	// to each result, exposed standalone for Analyze's direct-counterpart
	// findings (finding.go).
	Collapse(nodeKey string) (SystemRef, error)
}

// CoreImpactQuerier is the single-repo ImpactQuerier, backed directly by a
// *graph.Graph (and, through it, its *store.Store).
type CoreImpactQuerier struct{ Graph *graph.Graph }

var _ ImpactQuerier = CoreImpactQuerier{}

// QueryImpact runs graph.Graph.QueryImpact (a reverse BFS: who depends on
// seed) and collapses each ImpactEntry to the system that owns it.
//
// Performance note (see task-11-report.md): this calls viewmodel.OwnershipMap
// ONCE per query, building one map for every entry in the result to look
// itself up against — never viewmodel.OwningService per entry, which would
// rebuild the whole graph's ownership map once per impacted node
// (O(results × graph size) instead of O(graph size)).
func (q CoreImpactQuerier) QueryImpact(seed string, depth int) ([]SystemImpact, error) {
	res, err := q.Graph.QueryImpact(seed, graph.ImpactOptions{MaxDepth: depth})
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.QueryImpact: %w", err)
	}
	if len(res.Impacts) == 0 {
		return nil, nil
	}

	st := q.Graph.Store()
	own, err := viewmodel.OwnershipMap(st)
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.QueryImpact: ownership: %w", err)
	}
	idx, err := nodeIndex(st)
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.QueryImpact: node index: %w", err)
	}

	flowTrigger, err := flowTriggerIndex(st)
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.QueryImpact: flow triggers: %w", err)
	}

	bestDepth := make(map[string]int, len(res.Impacts))
	best := make(map[string]SystemImpact, len(res.Impacts))
	for _, entry := range res.Impacts {
		ref, ok := collapseKey(own, idx, flowTrigger, entry.NodeKey)
		if !ok {
			continue
		}
		if prev, seen := bestDepth[ref.Key]; seen && prev <= entry.Depth {
			continue
		}
		bestDepth[ref.Key] = entry.Depth
		best[ref.Key] = SystemImpact{
			System:     ref,
			Path:       pathStepsFromEntry(entry, idx),
			Confidence: entry.TrustChain,
		}
	}
	return sortedImpacts(best), nil
}

// TopicSubscribers finds every active CONSUMES_TOPIC edge onto topic
// directly (ListEdges, not a BFS — the brief's more precise reuse of
// expandTopicConsumers's IDEA, "a topic's consumers are nodes with a
// CONSUMES_TOPIC edge onto it", rather than that function's own
// QueryImpact-based traversal, which is shaped for a different, older
// reporting path) and collapses each subscriber to its owning system.
func (q CoreImpactQuerier) TopicSubscribers(topic string) ([]SystemImpact, error) {
	st := q.Graph.Store()
	topicNode, err := st.GetNodeByKey(topic)
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.TopicSubscribers: %w", err)
	}

	active := true
	edges, err := st.ListEdges(store.EdgeFilter{ToNodeID: topicNode.NodeID, EdgeType: "CONSUMES_TOPIC", Active: &active})
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.TopicSubscribers: list edges: %w", err)
	}
	if len(edges) == 0 {
		return nil, nil
	}

	own, err := viewmodel.OwnershipMap(st)
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.TopicSubscribers: ownership: %w", err)
	}
	idx, err := nodeIndex(st)
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.TopicSubscribers: node index: %w", err)
	}
	flowTrigger, err := flowTriggerIndex(st)
	if err != nil {
		return nil, fmt.Errorf("review: CoreImpactQuerier.TopicSubscribers: flow triggers: %w", err)
	}

	best := make(map[string]SystemImpact, len(edges))
	for _, e := range edges {
		ref, ok := collapseKey(own, idx, flowTrigger, e.FromNodeKey)
		if !ok {
			continue
		}
		if _, dup := best[ref.Key]; dup {
			// Two different subscriber nodes owned by the same system: one
			// entry per SYSTEM, like QueryImpact's own dedup — first one
			// found wins (every candidate has an identical one-step Path
			// shape, so there is no "shorter path" to prefer between them).
			continue
		}
		best[ref.Key] = SystemImpact{
			System:     ref,
			Path:       []PathStep{{Node: topicNode.Name, Edge: "CONSUMES_TOPIC"}},
			Confidence: e.TrustScore,
		}
	}
	return sortedImpacts(best), nil
}

// Collapse resolves nodeKey with no traversal: OwningService's single-lookup
// case (see its own doc comment) — fine to call directly here since Collapse
// itself is a single lookup, never a per-entry step inside a larger loop.
func (q CoreImpactQuerier) Collapse(nodeKey string) (SystemRef, error) {
	return collapseOne(q.Graph.Store(), nodeKey)
}

func collapseOne(st *store.Store, nodeKey string) (SystemRef, error) {
	if svcKey, ok := viewmodel.OwningService(st, nodeKey); ok {
		svc, err := st.GetNodeByKey(svcKey)
		if err != nil {
			return SystemRef{}, fmt.Errorf("review: collapse %s: owning service %s: %w", nodeKey, svcKey, err)
		}
		return systemRefFromNode(svc, kindService), nil
	}
	n, err := st.GetNodeByKey(nodeKey)
	if err != nil {
		return SystemRef{}, fmt.Errorf("review: collapse %s: %w", nodeKey, err)
	}
	// A flow:use_case node is derived scaffolding — DeriveFlows synthesizes
	// it from an endpoint plus that endpoint's own INJECTS closure, so
	// viewmodel.OwnershipMap never attributes one to a service directly (its
	// per-node loops only ever walk layer=="code"/"data" nodes — see
	// OwnershipMap's own doc comment). Falling straight to kindForUnowned
	// below would label it its own separate "domain" system, keyed by this
	// one flow's own synthetic node key — Bug 3 (task-18-report.md): a real
	// reproduction against tom-and-jerry inflated one real change into "6
	// systems affected" this way, five of them derived flow nodes with no
	// owner a human would recognize. Attribute it instead to whatever owns
	// the endpoint that triggers it (TRIGGERS_FLOW, the same edge DeriveFlows
	// itself writes to connect the two) — a real, resolvable node, and
	// usually a service once collapsed again through this same function.
	if n.Layer == "flow" {
		if trigger, ok, terr := flowTriggerFor(st, n.NodeID); terr == nil && ok {
			return collapseOne(st, trigger)
		}
	}
	return systemRefFromNode(n, kindForUnowned(n)), nil
}

// collapseKey is collapseOne's batch-friendly twin: same rule (owned ->
// owner; a flow node -> whatever collapses its triggering endpoint; anything
// else unowned -> self, typed by NodeType), but against caller-supplied
// own/idx/flowTrigger maps built once per query instead of a fresh store
// round trip per call. flowTrigger maps a flow node's own key to the node_key
// of the endpoint whose TRIGGERS_FLOW edge created it (flowTriggerIndex).
func collapseKey(own, idx map[string]*store.NodeRow, flowTrigger map[string]string, nodeKey string) (SystemRef, bool) {
	if svc, ok := own[nodeKey]; ok {
		return systemRefFromNode(svc, kindService), true
	}
	n, ok := idx[nodeKey]
	if !ok {
		return SystemRef{}, false
	}
	if n.Layer == "flow" {
		if trigger, ok := flowTrigger[nodeKey]; ok {
			if ref, ok := collapseKey(own, idx, flowTrigger, trigger); ok {
				return ref, true
			}
		}
	}
	return systemRefFromNode(n, kindForUnowned(n)), true
}

// flowTriggerIndex maps every flow node's own key to the node_key of the
// endpoint whose active TRIGGERS_FLOW edge points at it — DeriveFlows
// (graph/derive_flows.go) writes exactly one such edge per flow node, from
// the endpoint it was derived from, and a traced flow (not derived at all,
// see DeriveFlows' own doc comment on "a traced flow claimed this entry
// point") gets the same edge shape from whichever code traced it. Built once
// per query, the same way OwnershipMap/nodeIndex already are, so collapsing
// N impact-BFS results costs one extra ListEdges, not N.
func flowTriggerIndex(st *store.Store) (map[string]string, error) {
	active := true
	edges, err := st.ListEdges(store.EdgeFilter{EdgeType: "TRIGGERS_FLOW", Active: &active})
	if err != nil {
		return nil, fmt.Errorf("review: flowTriggerIndex: %w", err)
	}
	idx := make(map[string]string, len(edges))
	for _, e := range edges {
		idx[e.ToNodeKey] = e.FromNodeKey
	}
	return idx, nil
}

// flowTriggerFor is flowTriggerIndex's single-lookup twin, for collapseOne's
// one-off resolution (Collapse's own contract: a single lookup, no
// traversal-scale cost to justify building the whole map).
func flowTriggerFor(st *store.Store, flowNodeID int64) (triggerKey string, ok bool, err error) {
	active := true
	edges, err := st.ListEdges(store.EdgeFilter{EdgeType: "TRIGGERS_FLOW", ToNodeID: flowNodeID, Active: &active})
	if err != nil {
		return "", false, fmt.Errorf("review: flowTriggerFor: %w", err)
	}
	if len(edges) == 0 {
		return "", false, nil
	}
	return edges[0].FromNodeKey, true, nil
}

// kindForUnowned decides the SystemRef.Kind for a node ownershipMap never
// attributed to a service: a node explicitly marked as living outside this
// graph (NodeType "external_system", the same check graph/review_report.go's
// isExternalImpact uses) is Kind "external_system"; a node that IS itself a
// service (ownershipMap never assigns a service its own self as "owner") is
// Kind "service"; everything else — a bare domain concept, an infra node,
// anything ownershipMap's containment/path/name rules never reached — falls
// to the catch-all Kind "domain", matching OwningService's own doc comment
// ("ok=false — узел ничей (домен верхнего уровня)").
func kindForUnowned(n *store.NodeRow) string {
	switch {
	case n.NodeType == "external_system":
		return kindExternalSystem
	case n.Layer == "service" && n.NodeType == "service":
		return kindService
	default:
		return kindDomain
	}
}

func systemRefFromNode(n *store.NodeRow, kind string) SystemRef {
	return SystemRef{Kind: kind, Key: n.NodeKey, Name: n.Name, Repo: n.RepoName, Domain: n.DomainKey}
}

// nodeIndex lists every current node in st, keyed by its own node_key — the
// one full-graph pass QueryImpact/TopicSubscribers each make once per query
// (alongside viewmodel.OwnershipMap's own pass) to resolve Repo/Domain/Name
// for an unowned entry and human-readable names for every PathStep hop,
// without a per-entry store round trip.
func nodeIndex(st *store.Store) (map[string]*store.NodeRow, error) {
	nodes, err := st.ListNodes(store.NodeFilter{})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]*store.NodeRow, len(nodes))
	for i := range nodes {
		idx[nodes[i].NodeKey] = &nodes[i]
	}
	return idx, nil
}

// pathStepsFromEntry zips entry.Path against entry.EdgeTypes: Path is node
// keys from the seed to this entry INCLUDING the seed itself as element 0
// (confirmed empirically against graph/impact.go's own construction — see
// task-11-report.md), so the seed is sliced off first, leaving exactly
// len(EdgeTypes) keys to zip 1:1 against the edge types that reached them.
func pathStepsFromEntry(entry graph.ImpactEntry, idx map[string]*store.NodeRow) []PathStep {
	keys := entry.Path
	if len(keys) > 0 {
		keys = keys[1:]
	}
	n := len(keys)
	if len(entry.EdgeTypes) < n {
		n = len(entry.EdgeTypes)
	}
	steps := make([]PathStep, 0, n)
	for i := 0; i < n; i++ {
		steps = append(steps, PathStep{Node: nodeName(idx, keys[i]), Edge: entry.EdgeTypes[i]})
	}
	return steps
}

func nodeName(idx map[string]*store.NodeRow, key string) string {
	if n, ok := idx[key]; ok {
		return n.Name
	}
	return key
}

// sortedImpacts flattens a System.Key -> SystemImpact map into a
// deterministically ordered slice (sorted by System.Key) — QueryImpact and
// TopicSubscribers both dedup into a map first, and map iteration order is
// not stable, so callers (and tests) need a fixed order from here on.
func sortedImpacts(m map[string]SystemImpact) []SystemImpact {
	out := make([]SystemImpact, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].System.Key < out[j].System.Key })
	return out
}

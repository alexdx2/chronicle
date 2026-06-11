package graph

import (
	"errors"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// GraphHygieneStats counts resolver-side cleanups after merge.
type GraphHygieneStats struct {
	TopicEndpointsSuppressed int `json:"topic_endpoints_suppressed"`
	ControllerContainsRemoved int `json:"controller_contains_removed"`
	ServiceDuplicatesMerged  int `json:"service_duplicates_merged"`
}

// applyGraphHygiene enforces graph model invariants after fact resolution.
// This is the "graph compiler" pass — it does not depend on LLM behavior.
func (g *Graph) applyGraphHygiene(domainKey string) GraphHygieneStats {
	var stats GraphHygieneStats
	stats.TopicEndpointsSuppressed = g.suppressTopicDuplicateEndpoints(domainKey)
	stats.ControllerContainsRemoved = g.scrubControllerEndpointContains(domainKey)
	stats.ServiceDuplicatesMerged = g.deduplicateInternalExternalSystems(domainKey)
	return stats
}

// normalizeContractIdentity lowercases a topic or endpoint identity for comparison.
// Examples: "GET /battle-results" → "battle-results", "tom.weapon.equipped" → "tom.weapon.equipped"
func normalizeContractIdentity(name string) string {
	s := strings.TrimSpace(strings.ToLower(name))
	if s == "" {
		return ""
	}
	// Strip HTTP method prefix from display names like "GET /foo".
	for _, method := range []string{"get ", "post ", "put ", "patch ", "delete ", "head ", "options ", "ws "} {
		if strings.HasPrefix(s, method) {
			s = strings.TrimSpace(s[len(method):])
			break
		}
	}
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	return s
}

func endpointIdentity(ep store.NodeRow) string {
	if id := normalizeContractIdentity(ep.Name); id != "" {
		return id
	}
	// contract:endpoint:{domain}:{method}:{path}
	parts := strings.SplitN(ep.NodeKey, ":", 5)
	if len(parts) >= 5 {
		return normalizeContractIdentity(parts[4])
	}
	return ""
}

func topicIdentity(topic store.NodeRow) string {
	if id := normalizeContractIdentity(topic.Name); id != "" {
		return id
	}
	parts := strings.SplitN(topic.NodeKey, ":", 4)
	if len(parts) >= 4 {
		return normalizeContractIdentity(parts[3])
	}
	return ""
}

func (g *Graph) buildTopicIdentitySet(domainKey string) map[string]bool {
	topics, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "topic", Status: "active"})
	set := make(map[string]bool, len(topics))
	for _, t := range topics {
		if id := topicIdentity(t); id != "" {
			set[id] = true
		}
	}
	return set
}

// suppressTopicDuplicateEndpoints removes HTTP endpoint nodes that duplicate Kafka/event topics.
func (g *Graph) suppressTopicDuplicateEndpoints(domainKey string) int {
	topicIDs := g.buildTopicIdentitySet(domainKey)
	if len(topicIDs) == 0 {
		return 0
	}

	endpoints, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "endpoint", Status: "active"})
	suppressed := 0
	for _, ep := range endpoints {
		id := endpointIdentity(ep)
		if id == "" || !topicIDs[id] {
			continue
		}
		// Topic names masquerading as endpoints always drop; orphan ones are especially noisy.
		if g.suppressEndpointNode(ep) {
			suppressed++
		}
	}
	return suppressed
}

func (g *Graph) suppressEndpointNode(ep store.NodeRow) bool {
	edges, _ := g.store.ListEdges(store.EdgeFilter{})
	for _, e := range edges {
		if !e.Active {
			continue
		}
		if e.FromNodeID == ep.NodeID || e.ToNodeID == ep.NodeID {
			_ = g.store.DeleteEdge(e.EdgeKey)
		}
	}
	if err := g.store.DeleteNode(ep.NodeKey); err != nil {
		return false
	}
	return true
}

// scrubControllerEndpointContains removes CONTAINS edges that duplicate EXPOSES_ENDPOINT semantics.
func (g *Graph) scrubControllerEndpointContains(domainKey string) int {
	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	removed := 0
	for _, e := range edges {
		if !e.Active {
			continue
		}
		from, err1 := g.store.GetNodeByID(e.FromNodeID)
		to, err2 := g.store.GetNodeByID(e.ToNodeID)
		if err1 != nil || err2 != nil {
			continue
		}
		if from.DomainKey != domainKey || to.DomainKey != domainKey {
			continue
		}
		if from.NodeType == "controller" && to.NodeType == "endpoint" {
			if err := g.store.DeleteEdge(e.EdgeKey); err == nil {
				removed++
			}
		}
	}
	return removed
}

func serviceKeyTail(nodeKey string) string {
	if i := strings.LastIndex(nodeKey, ":"); i >= 0 && i < len(nodeKey)-1 {
		return strings.ToLower(nodeKey[i+1:])
	}
	return strings.ToLower(nodeKey)
}

// deduplicateInternalExternalSystems merges external_system nodes that represent scanned in-repo services.
func (g *Graph) deduplicateInternalExternalSystems(domainKey string) int {
	services, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "service", Status: "active"})
	externals, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "external_system", Status: "active"})

	byTail := make(map[string]store.NodeRow)
	for _, svc := range services {
		tail := serviceKeyTail(svc.NodeKey)
		if tail == "" {
			continue
		}
		existing, ok := byTail[tail]
		if !ok {
			byTail[tail] = svc
			continue
		}
		if strings.HasPrefix(svc.NodeKey, "service:service:") && !strings.HasPrefix(existing.NodeKey, "service:service:") {
			byTail[tail] = svc
		}
	}

	merged := 0
	for _, ext := range externals {
		tail := serviceKeyTail(ext.NodeKey)
		canonical, ok := byTail[tail]
		if !ok {
			// Also try normalized display name match.
			extNorm := normalizePackageName(ext.Name)
			for _, svc := range services {
				if normalizePackageName(svc.Name) == extNorm || serviceKeyTail(svc.NodeKey) == extNorm {
					canonical = svc
					ok = true
					break
				}
			}
		}
		if !ok || canonical.NodeID == ext.NodeID {
			continue
		}
		if g.mergeNodeIntoCanonical(canonical, ext) {
			merged++
		}
	}
	return merged
}

func (g *Graph) mergeNodeIntoCanonical(canonical, duplicate store.NodeRow) bool {
	if canonical.NodeID == 0 || duplicate.NodeID == 0 || canonical.NodeID == duplicate.NodeID {
		return false
	}

	// Repoint every active edge touching the duplicate onto the canonical node,
	// edge by edge through the instrumented store method (journaled). An edge
	// whose repointed key collides with an existing edge is a duplicate
	// relationship — tombstone it instead (edge_key is UNIQUE).
	active := true
	edges, _ := g.store.ListEdges(store.EdgeFilter{Active: &active})
	for _, e := range edges {
		var r store.EdgeRepoint
		if e.FromNodeID == duplicate.NodeID {
			r.NewFromNodeID, r.NewFromNodeKey = canonical.NodeID, canonical.NodeKey
		}
		if e.ToNodeID == duplicate.NodeID {
			r.NewToNodeID, r.NewToNodeKey = canonical.NodeID, canonical.NodeKey
		}
		if r.NewFromNodeKey == "" && r.NewToNodeKey == "" {
			continue
		}
		err := g.store.RepointEdge(e.EdgeID, r)
		if errors.Is(err, store.ErrEdgeKeyConflict) {
			err = g.store.DeactivateEdge(e.EdgeID, "merge_duplicate")
		}
		g.noteEvidenceErr(err)
	}

	if err := g.store.DeleteNode(duplicate.NodeKey); err != nil {
		return false
	}
	return true
}

package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

func TestNormalizeContractIdentity(t *testing.T) {
	cases := map[string]string{
		"GET /battle-results":     "battle-results",
		"get /tom.weapon.equipped": "tom.weapon.equipped",
		"battle.result":           "battle.result",
		"/arena/attack":           "arena/attack",
	}
	for in, want := range cases {
		if got := normalizeContractIdentity(in); got != want {
			t.Errorf("normalizeContractIdentity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHygiene_SuppressTopicDuplicateEndpoint(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "myapp"

	topicKey := "contract:topic:myapp:battle-results"
	epKey := "contract:endpoint:myapp:get:/battle-results"
	s.UpsertNode(store.NodeRow{
		NodeKey: topicKey, Layer: "contract", NodeType: "topic",
		DomainKey: domain, Name: "battle-results", Status: "active",
		LastSeenRevisionID: revID,
	})
	s.UpsertNode(store.NodeRow{
		NodeKey: epKey, Layer: "contract", NodeType: "endpoint",
		DomainKey: domain, Name: "GET /battle-results", Status: "active",
		LastSeenRevisionID: revID,
	})

	stats := g.applyGraphHygiene(domain)
	if stats.TopicEndpointsSuppressed != 1 {
		t.Fatalf("expected 1 suppressed endpoint, got %d", stats.TopicEndpointsSuppressed)
	}
	remaining, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "endpoint", Status: "active"})
	for _, ep := range remaining {
		if ep.NodeKey == epKey {
			t.Fatal("phantom endpoint node should be deleted")
		}
	}
	if _, err := s.GetNodeByKey(topicKey); err != nil {
		t.Fatal("topic node should remain")
	}
}

func TestHygiene_ScrubControllerEndpointContains(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "myapp"

	ctrlID, _ := s.UpsertNode(store.NodeRow{
		NodeKey: "code:controller:myapp:arena.controller", Layer: "code", NodeType: "controller",
		DomainKey: domain, Name: "ArenaController", Status: "active", LastSeenRevisionID: revID,
	})
	epID, _ := s.UpsertNode(store.NodeRow{
		NodeKey: "contract:endpoint:myapp:post:/arena/attack", Layer: "contract", NodeType: "endpoint",
		DomainKey: domain, Name: "POST /arena/attack", Status: "active", LastSeenRevisionID: revID,
	})
	s.UpsertEdge(store.EdgeRow{
		EdgeKey: "code:controller:myapp:arena.controller->contract:endpoint:myapp:post:/arena/attack:CONTAINS",
		FromNodeKey: "code:controller:myapp:arena.controller", ToNodeKey: "contract:endpoint:myapp:post:/arena/attack",
		FromNodeID: ctrlID, ToNodeID: epID, EdgeType: "CONTAINS", DerivationKind: "hard", Active: true,
		LastSeenRevisionID: revID,
	})
	s.UpsertEdge(store.EdgeRow{
		EdgeKey: "code:controller:myapp:arena.controller->contract:endpoint:myapp:post:/arena/attack:EXPOSES_ENDPOINT",
		FromNodeKey: "code:controller:myapp:arena.controller", ToNodeKey: "contract:endpoint:myapp:post:/arena/attack",
		FromNodeID: ctrlID, ToNodeID: epID, EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", Active: true,
		LastSeenRevisionID: revID,
	})

	stats := g.applyGraphHygiene(domain)
	if stats.ControllerContainsRemoved != 1 {
		t.Fatalf("expected 1 CONTAINS removed, got %d", stats.ControllerContainsRemoved)
	}

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	for _, e := range edges {
		if e.Active {
			t.Errorf("unexpected active CONTAINS edge: %s", e.EdgeKey)
		}
	}
	exposes, _ := s.ListEdges(store.EdgeFilter{EdgeType: "EXPOSES_ENDPOINT"})
	if len(exposes) == 0 {
		t.Fatal("EXPOSES_ENDPOINT should remain")
	}
}

func TestHygiene_DeduplicateServiceExternalSystem(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "myapp"

	svcID, _ := s.UpsertNode(store.NodeRow{
		NodeKey: "service:service:myapp:tom-api", Layer: "service", NodeType: "service",
		DomainKey: domain, Name: "tom-api", Status: "active", LastSeenRevisionID: revID,
	})
	extID, _ := s.UpsertNode(store.NodeRow{
		NodeKey: "service:external_system:myapp:tom-api", Layer: "service", NodeType: "external_system",
		DomainKey: domain, Name: "tom-api", Status: "active", LastSeenRevisionID: revID,
	})
	clientID, _ := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:myapp:arena.service", Layer: "code", NodeType: "provider",
		DomainKey: domain, Name: "ArenaService", Status: "active", LastSeenRevisionID: revID,
	})
	s.UpsertEdge(store.EdgeRow{
		EdgeKey: "code:provider:myapp:arena.service->service:external_system:myapp:tom-api:CALLS_SERVICE",
		FromNodeKey: "code:provider:myapp:arena.service", ToNodeKey: "service:external_system:myapp:tom-api",
		FromNodeID: clientID, ToNodeID: extID, EdgeType: "CALLS_SERVICE", DerivationKind: "linked", Active: true,
		LastSeenRevisionID: revID,
	})

	stats := g.applyGraphHygiene(domain)
	if stats.ServiceDuplicatesMerged != 1 {
		t.Fatalf("expected 1 merge, got %d", stats.ServiceDuplicatesMerged)
	}
	externals, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "external_system", Status: "active"})
	for _, ext := range externals {
		if ext.NodeKey == "service:external_system:myapp:tom-api" {
			t.Fatal("external_system duplicate should be deleted")
		}
	}

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	found := false
	for _, e := range edges {
		if e.Active && e.ToNodeID == svcID {
			found = true
		}
	}
	if !found {
		t.Fatal("CALLS_SERVICE should redirect to canonical service node")
	}
}

func TestHygiene_ResolveSuppressesPhantomEndpointFact(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "myapp"

	facts := `[{"kind":"produces","to":"battle-results","from_type":"provider"},{"kind":"endpoint","method":"GET","target":"/battle-results","from_type":"controller"}]`
	g.SaveFileExtraction(revID, domain, "arena/battle-result.producer.cs", "extracted", "provider", facts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	endpoints, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "endpoint", Status: "active"})
	for _, ep := range endpoints {
		if endpointIdentity(ep) == "battle-results" {
			t.Fatalf("phantom endpoint should be suppressed, still have %s", ep.NodeKey)
		}
	}
	topics, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "topic", Status: "active"})
	if len(topics) == 0 {
		t.Fatal("topic node should exist")
	}
}

func TestHygiene_InvariantsAfterResolve(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "myapp"

	facts := `[
		{"kind":"declares_service","to":"tom-api"},
		{"kind":"produces","to":"battle-results","from_type":"provider"},
		{"kind":"endpoint","method":"GET","target":"/battle-results","from_type":"controller"},
		{"kind":"http_call","from":"ArenaService","from_type":"provider","target":"http://tom-api:3001/status","method":"GET"}
	]`
	g.SaveFileExtraction(revID, domain, "arena/arena.service.ts", "extracted", "provider", facts, "")
	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	assertNoTopicEndpointNameCollision(t, s, domain)
	assertNoControllerEndpointContains(t, s, domain)
	assertNoInternalServiceExternalDuplicate(t, s, domain)
}

func assertNoTopicEndpointNameCollision(t *testing.T, s *store.Store, domain string) {
	t.Helper()
	topicIDs := make(map[string]bool)
	topics, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "topic", Status: "active"})
	for _, topic := range topics {
		topicIDs[topicIdentity(topic)] = true
	}
	endpoints, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "endpoint", Status: "active"})
	for _, ep := range endpoints {
		if topicIDs[endpointIdentity(ep)] {
			t.Errorf("endpoint %q collides with topic name", ep.NodeKey)
		}
	}
}

func assertNoControllerEndpointContains(t *testing.T, s *store.Store, domain string) {
	t.Helper()
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	for _, e := range edges {
		if !e.Active {
			continue
		}
		from, _ := s.GetNodeByID(e.FromNodeID)
		to, _ := s.GetNodeByID(e.ToNodeID)
		if from.DomainKey == domain && from.NodeType == "controller" && to.NodeType == "endpoint" {
			t.Errorf("CONTAINS controller→endpoint forbidden: %s", e.EdgeKey)
		}
	}
}

func assertNoInternalServiceExternalDuplicate(t *testing.T, s *store.Store, domain string) {
	t.Helper()
	services, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "service", Status: "active"})
	tails := make(map[string]bool)
	for _, svc := range services {
		tails[serviceKeyTail(svc.NodeKey)] = true
	}
	externals, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "external_system", Status: "active"})
	for _, ext := range externals {
		if tails[serviceKeyTail(ext.NodeKey)] {
			t.Errorf("external_system %q duplicates scanned service tail", ext.NodeKey)
		}
	}
}

package store

import (
	"testing"
)

func TestGetDomains(t *testing.T) {
	s := openTestStore(t)
	s.UpsertNode(NodeRow{NodeKey: "code:module:core:OrdersModule", Layer: "code", NodeType: "module", DomainKey: "core-api", Name: "OrdersModule", Status: "active"})
	s.UpsertNode(NodeRow{NodeKey: "code:module:core:PaymentsModule", Layer: "code", NodeType: "module", DomainKey: "core-api", Name: "PaymentsModule", Status: "active"})
	s.UpsertNode(NodeRow{NodeKey: "code:module:crm:CrmModule", Layer: "code", NodeType: "module", DomainKey: "crm", Name: "CrmModule", Status: "active"})
	s.UpsertNode(NodeRow{NodeKey: "code:module:stale:Old", Layer: "code", NodeType: "module", DomainKey: "old", Name: "Old", Status: "stale"})

	domains, err := s.GetDomains()
	if err != nil {
		t.Fatalf("GetDomains: %v", err)
	}
	if len(domains) != 2 {
		t.Fatalf("got %d domains, want 2: %v", len(domains), domains)
	}
	if domains[0] != "core-api" || domains[1] != "crm" {
		t.Errorf("domains = %v, want [core-api crm]", domains)
	}
}

func TestGetCrossDomainEdges(t *testing.T) {
	s := openTestStore(t)
	idA, _ := s.UpsertNode(NodeRow{NodeKey: "service:service:core:OrdersAPI", Layer: "service", NodeType: "service", DomainKey: "core-api", Name: "OrdersAPI", Status: "active"})
	idB, _ := s.UpsertNode(NodeRow{NodeKey: "service:service:crm:CrmAPI", Layer: "service", NodeType: "service", DomainKey: "crm", Name: "CrmAPI", Status: "active"})
	idC, _ := s.UpsertNode(NodeRow{NodeKey: "service:service:core:PaymentsAPI", Layer: "service", NodeType: "service", DomainKey: "core-api", Name: "PaymentsAPI", Status: "active"})

	s.UpsertEdge(EdgeRow{EdgeKey: "service:service:core:OrdersAPI->service:service:crm:CrmAPI:CALLS_SERVICE", FromNodeID: idA, ToNodeID: idB, EdgeType: "CALLS_SERVICE", DerivationKind: "hard"})
	s.UpsertEdge(EdgeRow{EdgeKey: "service:service:core:OrdersAPI->service:service:core:PaymentsAPI:CALLS_SERVICE", FromNodeID: idA, ToNodeID: idC, EdgeType: "CALLS_SERVICE", DerivationKind: "hard"})

	edges, err := s.GetCrossDomainEdges("core-api", "crm")
	if err != nil {
		t.Fatalf("GetCrossDomainEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("got %d edges, want 1", len(edges))
	}
	if edges[0].FromNodeID != idA || edges[0].ToNodeID != idB {
		t.Errorf("edge from=%d to=%d, want from=%d to=%d", edges[0].FromNodeID, edges[0].ToNodeID, idA, idB)
	}
}

package store

import "testing"

func TestVersionedEdgePopulatesNodeKeys(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev, _ := s.CreateRevisionWithContext("dom", "", "e-aaa", "manual", "full", "{}", ctxID)

	s.UpsertNode(NodeRow{
		NodeKey: "code:svc:dom:ea", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "A", Status: "active",
		FirstSeenRevisionID: rev, LastSeenRevisionID: rev,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})
	s.UpsertNode(NodeRow{
		NodeKey: "code:svc:dom:eb", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "B", Status: "active",
		FirstSeenRevisionID: rev, LastSeenRevisionID: rev,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	fromID, _ := s.GetNodeIDByKey("code:svc:dom:ea")
	toID, _ := s.GetNodeIDByKey("code:svc:dom:eb")

	_, err := s.UpsertEdge(EdgeRow{
		EdgeKey: "code:svc:dom:ea->code:svc:dom:eb::INJECTS",
		FromNodeID: fromID, ToNodeID: toID,
		FromNodeKey: "code:svc:dom:ea", ToNodeKey: "code:svc:dom:eb",
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: rev, LastSeenRevisionID: rev,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
		ValidFromRevisionID: rev, ContextID: ctxID,
	})
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	edge, _ := s.GetEdgeByKey("code:svc:dom:ea->code:svc:dom:eb::INJECTS")
	if edge.FromNodeKey != "code:svc:dom:ea" {
		t.Errorf("from_node_key = %q, want code:svc:dom:ea", edge.FromNodeKey)
	}
	if edge.ToNodeKey != "code:svc:dom:eb" {
		t.Errorf("to_node_key = %q, want code:svc:dom:eb", edge.ToNodeKey)
	}
}

func TestLegacyEdgeUpsertStillWorks(t *testing.T) {
	s := openTestDB(t)

	s.UpsertNode(NodeRow{
		NodeKey: "code:svc:dom:la", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "A", Status: "active",
		FirstSeenRevisionID: 1, LastSeenRevisionID: 1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})
	s.UpsertNode(NodeRow{
		NodeKey: "code:svc:dom:lb", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "B", Status: "active",
		FirstSeenRevisionID: 1, LastSeenRevisionID: 1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	fromID, _ := s.GetNodeIDByKey("code:svc:dom:la")
	toID, _ := s.GetNodeIDByKey("code:svc:dom:lb")

	id1, _ := s.UpsertEdge(EdgeRow{
		EdgeKey: "code:svc:dom:la->code:svc:dom:lb::CALLS",
		FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "CALLS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: 1, LastSeenRevisionID: 1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	id2, _ := s.UpsertEdge(EdgeRow{
		EdgeKey: "code:svc:dom:la->code:svc:dom:lb::CALLS",
		FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "CALLS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: 1, LastSeenRevisionID: 2,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	if id1 != id2 {
		t.Errorf("legacy mode should return same edge_id, got %d vs %d", id1, id2)
	}
}

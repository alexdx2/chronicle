package store

import "testing"

func TestVersionedNodeUpsertCreatesNewVersion(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev1, _ := s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", ctxID)
	rev2, _ := s.CreateRevisionWithContext("dom", "aaa", "bbb", "manual", "incremental", "{}", ctxID)

	n := NodeRow{
		NodeKey: "code:svc:dom:foo", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "Foo", Status: "active",
		FirstSeenRevisionID: rev1, LastSeenRevisionID: rev1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
		ValidFromRevisionID: rev1, ContextID: ctxID,
	}
	id1, err := s.UpsertNode(n)
	if err != nil {
		t.Fatalf("UpsertNode v1: %v", err)
	}

	n.Name = "FooUpdated"
	n.LastSeenRevisionID = rev2
	n.ValidFromRevisionID = rev2
	id2, err := s.UpsertNode(n)
	if err != nil {
		t.Fatalf("UpsertNode v2: %v", err)
	}

	if id1 == id2 {
		t.Error("expected different node_id for new version")
	}

	// Old version should have valid_to set
	var validTo int64
	s.db.QueryRow(`SELECT COALESCE(valid_to_revision_id,0) FROM graph_nodes WHERE node_id = ?`, id1).Scan(&validTo)
	if validTo == 0 {
		t.Error("old version should have valid_to_revision_id set")
	}

	// New version should have valid_to NULL
	s.db.QueryRow(`SELECT COALESCE(valid_to_revision_id,0) FROM graph_nodes WHERE node_id = ?`, id2).Scan(&validTo)
	if validTo != 0 {
		t.Error("new version should have valid_to_revision_id = NULL")
	}
}

func TestVersionedNodeGetByKeyReturnsCurrent(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev1, _ := s.CreateRevisionWithContext("dom", "", "aaa2", "manual", "full", "{}", ctxID)
	rev2, _ := s.CreateRevisionWithContext("dom", "aaa2", "bbb2", "manual", "incremental", "{}", ctxID)

	n := NodeRow{
		NodeKey: "code:svc:dom:bar", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "Bar", Status: "active",
		FirstSeenRevisionID: rev1, LastSeenRevisionID: rev1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
		ValidFromRevisionID: rev1, ContextID: ctxID,
	}
	s.UpsertNode(n)

	n.Name = "BarV2"
	n.LastSeenRevisionID = rev2
	n.ValidFromRevisionID = rev2
	s.UpsertNode(n)

	current, err := s.GetNodeByKey("code:svc:dom:bar")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if current.Name != "BarV2" {
		t.Errorf("name = %q, want BarV2", current.Name)
	}
}

func TestLegacyUpsertStillWorks(t *testing.T) {
	s := openTestDB(t)
	// Legacy mode: ValidFromRevisionID = 0 (default)
	n := NodeRow{
		NodeKey: "code:svc:dom:legacy", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "Legacy", Status: "active",
		FirstSeenRevisionID: 1, LastSeenRevisionID: 1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	}
	id1, _ := s.UpsertNode(n)

	n.Name = "LegacyUpdated"
	n.LastSeenRevisionID = 2
	id2, _ := s.UpsertNode(n)

	// Legacy mode should update in place (same ID)
	if id1 != id2 {
		t.Errorf("legacy mode should return same node_id, got %d vs %d", id1, id2)
	}

	node, _ := s.GetNodeByKey("code:svc:dom:legacy")
	if node.Name != "LegacyUpdated" {
		t.Errorf("name = %q, want LegacyUpdated", node.Name)
	}
}

package store

import (
	"path/filepath"
	"testing"
)

func TestAliasCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create a revision and node to attach aliases to.
	revID := createTestRevision(t, s)
	nodeID := createTestNode(t, s, revID)

	// Add alias.
	aliasID, err := s.AddAlias(AliasRow{
		NodeID:     nodeID,
		Alias:      "Orders API",
		AliasKind:  "openapi_title",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("AddAlias: %v", err)
	}
	if aliasID == 0 {
		t.Fatal("expected non-zero alias_id")
	}

	// List by node.
	aliases, err := s.ListAliasesByNode(nodeID)
	if err != nil {
		t.Fatalf("ListAliasesByNode: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}
	if aliases[0].Alias != "Orders API" {
		t.Errorf("expected alias 'Orders API', got %q", aliases[0].Alias)
	}
	if aliases[0].NormalizedAlias != "orders api" {
		t.Errorf("expected normalized 'orders api', got %q", aliases[0].NormalizedAlias)
	}

	// Lookup by normalized alias.
	found, err := s.ListAliasesByNormalized("orders api", "openapi_title")
	if err != nil {
		t.Fatalf("ListAliasesByNormalized: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 match, got %d", len(found))
	}

	// Duplicate alias (same node, same normalized, same kind) should fail.
	_, err = s.AddAlias(AliasRow{
		NodeID:     nodeID,
		Alias:      "orders api",
		AliasKind:  "openapi_title",
		Confidence: 0.8,
	})
	if err == nil {
		t.Fatal("expected duplicate alias to fail")
	}

	// Different kind should succeed.
	_, err = s.AddAlias(AliasRow{
		NodeID:     nodeID,
		Alias:      "orders api",
		AliasKind:  "dns",
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("AddAlias different kind: %v", err)
	}

	// Remove alias.
	err = s.RemoveAlias(aliasID)
	if err != nil {
		t.Fatalf("RemoveAlias: %v", err)
	}

	aliases, err = s.ListAliasesByNode(nodeID)
	if err != nil {
		t.Fatalf("ListAliasesByNode after remove: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias after remove (dns one remains), got %d", len(aliases))
	}
}

func createTestRevision(t *testing.T, s *Store) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO graph_revisions (domain_key, git_after_sha, trigger_kind, mode) VALUES ('test', 'abc123', 'manual', 'full')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func createTestNode(t *testing.T, s *Store, revID int64) int64 {
	t.Helper()
	id, err := s.UpsertNode(NodeRow{
		NodeKey:             "service:service:orders:orders-service",
		Layer:               "service",
		NodeType:            "service",
		DomainKey:           "orders",
		Name:                "orders-service",
		Status:              "active",
		FirstSeenRevisionID: revID,
		LastSeenRevisionID:  revID,
		Confidence:          1.0,
		Metadata:            "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

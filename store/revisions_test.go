package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// openTestStore opens a fresh in-memory-like SQLite store in a temp dir.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedNodes creates a revision and two nodes for edge/evidence tests.
func seedNodes(t *testing.T, s *Store) (revID, nodeID1, nodeID2 int64) {
	t.Helper()
	var err error
	revID, err = s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("seedNodes CreateRevision: %v", err)
	}
	nodeID1, err = s.UpsertNode(NodeRow{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("seedNodes UpsertNode 1: %v", err)
	}
	nodeID2, err = s.UpsertNode(NodeRow{
		NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider",
		DomainKey: "orders", Name: "OrdersService", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("seedNodes UpsertNode 2: %v", err)
	}
	return
}

func TestCreateRevision(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateRevision("orders", "abc", "def", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}
}

func TestCreateRevisionDuplicate(t *testing.T) {
	s := openTestStore(t)
	_, err := s.CreateRevision("orders", "abc", "def", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("first CreateRevision: %v", err)
	}
	_, err = s.CreateRevision("orders", "xyz", "def", "manual", "full", "{}")
	if err == nil {
		t.Fatal("expected error on duplicate domain+after_sha, got nil")
	}
}

func TestGetRevision(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateRevision("orders", "before", "after", "manual", "full", `{"k":"v"}`)
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	r, err := s.GetRevision(id)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if r.DomainKey != "orders" {
		t.Errorf("DomainKey = %q, want orders", r.DomainKey)
	}
	if r.GitAfterSHA != "after" {
		t.Errorf("GitAfterSHA = %q, want after", r.GitAfterSHA)
	}
	if r.GitBeforeSHA != "before" {
		t.Errorf("GitBeforeSHA = %q, want before", r.GitBeforeSHA)
	}
}

func TestGetLatestRevision(t *testing.T) {
	s := openTestStore(t)

	_, err := s.GetLatestRevision("orders")
	if err == nil {
		t.Fatal("expected error for no revisions")
	}

	s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	s.CreateRevision("orders", "sha1", "sha2", "manual", "incremental", "{}")
	s.CreateRevision("other", "", "sha3", "manual", "full", "{}")

	rev, err := s.GetLatestRevision("orders")
	if err != nil {
		t.Fatalf("GetLatestRevision: %v", err)
	}
	if rev.GitAfterSHA != "sha2" {
		t.Errorf("after_sha = %q, want sha2", rev.GitAfterSHA)
	}
	if rev.Mode != "incremental" {
		t.Errorf("mode = %q, want incremental", rev.Mode)
	}
}

func TestGetRevisionNotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetRevision(9999)
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

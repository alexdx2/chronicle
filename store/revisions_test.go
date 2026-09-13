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

func TestGetRevisionBySHA(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateRevision("d", "", "sha-one", "manual", "incremental", `{"layer":"ui"}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRevisionBySHA("d", "sha-one")
	if err != nil {
		t.Fatalf("GetRevisionBySHA: %v", err)
	}
	if got.RevisionID != id || got.Metadata != `{"layer":"ui"}` {
		t.Fatalf("got %+v", got)
	}
	if _, err := s.GetRevisionBySHA("d", "sha-two"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent sha: %v", err)
	}
	if _, err := s.GetRevisionBySHA("other", "sha-one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other domain: %v", err)
	}
}

// The structural phase writes its own git_hook revisions. Verification must not
// mistake one of them for the last re-verification and diff from its SHA — the
// two pointers move independently.
func TestLatestRefreshRevisionSkipsStructural(t *testing.T) {
	s := openTestStore(t)
	refreshID, err := s.CreateRevision("d", "", "sha-refresh", "git_hook", "incremental", `{"refresh":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("d", "sha-refresh", "sha-structural", "git_hook", "incremental",
		`{"kind":"structural","complete":true}`); err != nil {
		t.Fatal(err)
	}

	got, err := s.LatestRefreshRevision("d")
	if err != nil {
		t.Fatalf("LatestRefreshRevision: %v", err)
	}
	if got.RevisionID != refreshID {
		t.Fatalf("LatestRefreshRevision = %d (%s), want %d (%s) — structural rows are not refresh pointers",
			got.RevisionID, got.GitAfterSHA, refreshID, "sha-refresh")
	}

	// Any-domain form skips them too, and malformed metadata still counts as a
	// refresh rather than failing the query.
	if _, err := s.CreateRevision("e", "", "sha-bad-json", "git_hook", "incremental", "not json"); err != nil {
		t.Fatal(err)
	}
	any, err := s.LatestRefreshRevision("")
	if err != nil {
		t.Fatalf("LatestRefreshRevision(\"\"): %v", err)
	}
	if any.GitAfterSHA != "sha-bad-json" {
		t.Fatalf("any-domain LatestRefreshRevision = %s, want sha-bad-json", any.GitAfterSHA)
	}

	// A store whose only git_hook rows are structural has no refresh pointer.
	s2 := openTestStore(t)
	if _, err := s2.CreateRevision("d", "", "sha-s", "git_hook", "incremental",
		`{"kind":"structural","complete":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.LatestRefreshRevision("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("only-structural store: %v, want ErrNotFound", err)
	}
}

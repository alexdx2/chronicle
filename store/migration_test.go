package store

import (
	"path/filepath"
	"testing"
)

func TestMigrationBackfillsMainContext(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open DB, create some data without contexts (simulating old schema)
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.CreateRevision("mydom", "", "abc123", "manual", "full", "{}")
	s.Close()

	// Re-open — migration should backfill
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Re-open: %v", err)
	}
	defer s2.Close()

	ctx, err := s2.GetContextByRef("mydom", "main")
	if err != nil {
		t.Fatalf("expected main context to be backfilled: %v", err)
	}
	if ctx.Status != "active" {
		t.Errorf("status = %q, want active", ctx.Status)
	}
	if ctx.HeadCommitSHA != "abc123" {
		t.Errorf("head_commit_sha = %q, want abc123", ctx.HeadCommitSHA)
	}

	rev, _ := s2.GetLatestRevision("mydom")
	if rev == nil {
		t.Fatal("expected revision to exist")
	}
}

func TestMigrationBackfillIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.CreateRevision("mydom", "", "abc123", "manual", "full", "{}")
	s.Close()

	// Open twice to trigger backfill twice
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Re-open 1: %v", err)
	}
	s2.Close()

	s3, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Re-open 2: %v", err)
	}
	defer s3.Close()

	ctxs, err := s3.ListContexts("mydom")
	if err != nil {
		t.Fatalf("ListContexts: %v", err)
	}
	if len(ctxs) != 1 {
		t.Errorf("expected 1 context, got %d", len(ctxs))
	}
}

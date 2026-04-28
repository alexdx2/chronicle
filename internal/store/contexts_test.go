package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSchemaContextTablesExist(t *testing.T) {
	s := openTestDB(t)
	_, err := s.db.Exec(`INSERT INTO knowledge_contexts (domain_key, name, status) VALUES ('test', 'main', 'active')`)
	if err != nil {
		t.Fatalf("knowledge_contexts table missing: %v", err)
	}
	_, err = s.db.Exec(`INSERT INTO graph_changelog (revision_id, context_id, entity_type, entity_key, change_type) VALUES (0, 1, 'node', 'test', 'created')`)
	if err != nil {
		t.Fatalf("graph_changelog table missing: %v", err)
	}
}

func TestContextCreate(t *testing.T) {
	s := openTestDB(t)
	id, err := s.CreateContext("mydom", "main", "main", 0, 0)
	if err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero context_id")
	}
}

func TestContextGet(t *testing.T) {
	s := openTestDB(t)
	id, _ := s.CreateContext("mydom", "main", "main", 0, 0)
	ctx, err := s.GetContext(id)
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if ctx.Name != "main" || ctx.DomainKey != "mydom" || ctx.Status != "active" {
		t.Errorf("unexpected context: %+v", ctx)
	}
}

func TestContextGetNotFound(t *testing.T) {
	s := openTestDB(t)
	_, err := s.GetContext(9999)
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestContextGetByRef(t *testing.T) {
	s := openTestDB(t)
	s.CreateContext("mydom", "main", "main", 0, 0)
	ctx, err := s.GetContextByRef("mydom", "main")
	if err != nil {
		t.Fatalf("GetContextByRef: %v", err)
	}
	if ctx.Name != "main" {
		t.Errorf("name = %q, want main", ctx.Name)
	}
}

func TestContextList(t *testing.T) {
	s := openTestDB(t)
	s.CreateContext("mydom", "main", "main", 0, 0)
	s.CreateContext("mydom", "feature/x", "feature/x", 1, 0)
	list, err := s.ListContexts("mydom")
	if err != nil {
		t.Fatalf("ListContexts: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}
}

func TestContextArchive(t *testing.T) {
	s := openTestDB(t)
	id, _ := s.CreateContext("mydom", "feat", "feat", 0, 0)
	if err := s.ArchiveContext(id); err != nil {
		t.Fatalf("ArchiveContext: %v", err)
	}
	ctx, _ := s.GetContext(id)
	if ctx.Status != "archived" {
		t.Errorf("status = %q, want archived", ctx.Status)
	}
}

func TestContextUpdateHead(t *testing.T) {
	s := openTestDB(t)
	id, _ := s.CreateContext("mydom", "main", "main", 0, 0)
	revID, _ := s.CreateRevision("mydom", "", "abc123", "manual", "full", "{}")
	if err := s.UpdateContextHead(id, revID, "abc123"); err != nil {
		t.Fatalf("UpdateContextHead: %v", err)
	}
	ctx, _ := s.GetContext(id)
	if ctx.HeadRevisionID != revID || ctx.HeadCommitSHA != "abc123" {
		t.Errorf("head not updated: rev=%d sha=%s", ctx.HeadRevisionID, ctx.HeadCommitSHA)
	}
}

func TestContextDuplicateNameFails(t *testing.T) {
	s := openTestDB(t)
	s.CreateContext("mydom", "main", "main", 0, 0)
	_, err := s.CreateContext("mydom", "main", "main", 0, 0)
	if err == nil {
		t.Fatal("expected error on duplicate context name")
	}
}

func TestSchemaNewColumns(t *testing.T) {
	s := openTestDB(t)
	_, err := s.db.Exec(`SELECT context_id FROM graph_revisions LIMIT 0`)
	if err != nil {
		t.Fatalf("graph_revisions.context_id missing: %v", err)
	}
	_, err = s.db.Exec(`SELECT valid_from_revision_id, valid_to_revision_id, context_id FROM graph_nodes LIMIT 0`)
	if err != nil {
		t.Fatalf("graph_nodes temporal columns missing: %v", err)
	}
	_, err = s.db.Exec(`SELECT valid_from_revision_id, valid_to_revision_id, context_id, from_node_key, to_node_key FROM graph_edges LIMIT 0`)
	if err != nil {
		t.Fatalf("graph_edges temporal/key columns missing: %v", err)
	}
	_, err = s.db.Exec(`SELECT evidence_uid, context_id FROM graph_evidence LIMIT 0`)
	if err != nil {
		t.Fatalf("graph_evidence new columns missing: %v", err)
	}
}

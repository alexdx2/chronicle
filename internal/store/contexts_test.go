package store

import (
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

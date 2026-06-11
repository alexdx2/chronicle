package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	tables := []string{"graph_revisions", "graph_nodes", "graph_edges", "graph_evidence", "graph_snapshots"}
	for _, table := range tables {
		var count int
		err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil {
			t.Errorf("table %s does not exist: %v", table, err)
		}
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "dir", "test.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("db file was not created")
	}
}

func TestOpenIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
}

func TestInTxAndDirPropagation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.InTx() {
		t.Fatal("fresh store must not report InTx")
	}
	err = s.WithTx(func(tx *Store) error {
		if !tx.InTx() {
			t.Error("tx store must report InTx")
		}
		if tx.Dir() != s.Dir() {
			t.Errorf("tx store Dir = %q, want %q", tx.Dir(), s.Dir())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

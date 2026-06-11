package store

import (
	"errors"
	"path/filepath"
	"testing"
)

var errInjected = errors.New("injected")

func openJournalTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.SetJournalActor("tester@host")
	return s
}

func TestAppendEventWritesOutboxAndChangelog(t *testing.T) {
	s := openJournalTestStore(t)
	err := s.WithTx(func(tx *Store) error {
		return tx.appendEvent(journalEvent{
			DomainKey: "dom", RevisionID: 1, Kind: "node_upsert",
			Key: "code:module:dom:a", Fields: map[string]any{"name": "a", "status": "active"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	// WithTx auto-flushes after commit, so the row is in the outbox but
	// already marked flushed (no longer flushed_at IS NULL).
	var n int
	if err := s.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox`, &n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("outbox rows = %d, want 1", n)
	}
	if err := s.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox WHERE flushed_at IS NOT NULL`, &n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("flushed outbox rows = %d, want 1 (WithTx auto-flush)", n)
	}
	if err := s.QueryRowScan(`SELECT COUNT(*) FROM graph_changelog`, &n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("changelog rows = %d, want 1", n)
	}
}

func TestAppendEventRollsBackWithTx(t *testing.T) {
	s := openJournalTestStore(t)
	wantErr := s.WithTx(func(tx *Store) error {
		if err := tx.appendEvent(journalEvent{DomainKey: "dom", Kind: "node_upsert", Key: "k"}); err != nil {
			return err
		}
		return errInjected
	})
	if wantErr == nil {
		t.Fatal("expected injected error")
	}
	var n int
	s.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox`, &n)
	if n != 0 {
		t.Fatalf("outbox rows after rollback = %d, want 0", n)
	}
}

func TestLamportMonotonicPerActor(t *testing.T) {
	s := openJournalTestStore(t)
	for i := 0; i < 3; i++ {
		s.WithTx(func(tx *Store) error {
			return tx.appendEvent(journalEvent{DomainKey: "dom", Kind: "node_upsert", Key: "k"})
		})
	}
	var max int64
	s.QueryRowScan(`SELECT MAX(lamport) FROM journal_outbox`, &max)
	if max != 3 {
		t.Fatalf("max lamport = %d, want 3", max)
	}
}

package store

import (
	"fmt"
	"testing"
)

// TestCopyLocalState seeds a source store with one row in each kind of
// non-journaled local state, copies into a fresh store, and asserts presence —
// plus that journal_applied_events is deliberately NOT carried and the
// lamport floor MAX-merges instead of overwriting.
func TestCopyLocalState(t *testing.T) {
	src := openJournalTestStore(t)

	revID, err := src.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.CreateContext("dom", "main", "main", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := src.CreateScanRun(revID, "dom"); err != nil {
		t.Fatal(err)
	}
	// UpsertNode outside WithTx leaves its journal event unflushed in the outbox.
	if _, err := src.UpsertNode(NodeRow{
		NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
		Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	var srcPending int
	src.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox WHERE flushed_at IS NULL`, &srcPending)
	if srcPending == 0 {
		t.Fatal("setup: expected at least one unflushed outbox row in source")
	}
	// A previously-applied event in the source must NOT travel: the rebuilt db
	// gets its applied-events bookkeeping from its own replay.
	if err := src.Exec(`
		INSERT INTO journal_applied_events (event_id, domain_key, lamport, actor, kind, applied_at)
		VALUES ('ev-1', 'dom', 1, 'tester@host', 'node_upsert', '2026-06-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var srcLamport int64
	if err := src.QueryRowScan(`SELECT last_lamport FROM journal_state WHERE actor = 'tester@host'`, &srcLamport); err != nil {
		t.Fatal(err)
	}

	dst := openJournalTestStore(t)
	// Pre-seed a HIGHER lamport floor on the destination (as replay would):
	// the merge must keep the max, not overwrite with the source value.
	if err := dst.Exec(`INSERT INTO journal_state (actor, last_lamport) VALUES ('tester@host', ?)`, srcLamport+10); err != nil {
		t.Fatal(err)
	}

	counts, err := dst.CopyLocalState(src)
	if err != nil {
		t.Fatal(err)
	}

	for table, want := range map[string]int{
		"graph_revisions":    1,
		"knowledge_contexts": 1,
		"scan_runs":          1,
		"journal_outbox":     srcPending,
	} {
		if counts[table] != want {
			t.Errorf("counts[%s] = %d, want %d", table, counts[table], want)
		}
	}

	// QueryRowScan takes no bind args — inline the (test-controlled) id.
	var n int
	dst.QueryRowScan(fmt.Sprintf(`SELECT COUNT(*) FROM graph_revisions WHERE revision_id = %d AND git_after_sha = 'abc123'`, revID), &n)
	if n != 1 {
		t.Errorf("revision not carried with original id: count=%d", n)
	}
	dst.QueryRowScan(`SELECT COUNT(*) FROM knowledge_contexts WHERE domain_key='dom' AND name='main'`, &n)
	if n != 1 {
		t.Errorf("knowledge context not carried: count=%d", n)
	}
	dst.QueryRowScan(fmt.Sprintf(`SELECT COUNT(*) FROM scan_runs WHERE revision_id = %d`, revID), &n)
	if n != 1 {
		t.Errorf("scan run not carried: count=%d", n)
	}
	dst.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox WHERE flushed_at IS NULL`, &n)
	if n != srcPending {
		t.Errorf("unflushed outbox rows carried = %d, want %d (must stay pending)", n, srcPending)
	}
	dst.QueryRowScan(`SELECT COUNT(*) FROM journal_applied_events`, &n)
	if n != 0 {
		t.Errorf("journal_applied_events copied (%d rows) — must come from replay only", n)
	}
	var dstLamport int64
	dst.QueryRowScan(`SELECT last_lamport FROM journal_state WHERE actor = 'tester@host'`, &dstLamport)
	if dstLamport != srcLamport+10 {
		t.Errorf("lamport floor = %d, want %d (MAX merge must not move the floor backwards)", dstLamport, srcLamport+10)
	}
}

// TestCopyLocalStateSkipsFlushedOutbox verifies flushed outbox rows stay
// behind — they are already durable in events/*.jsonl.
func TestCopyLocalStateSkipsFlushedOutbox(t *testing.T) {
	src := openJournalTestStore(t)
	revID, err := src.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.UpsertNode(NodeRow{
		NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
		Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.FlushJournal(); err != nil {
		t.Fatal(err)
	}

	dst := openJournalTestStore(t)
	counts, err := dst.CopyLocalState(src)
	if err != nil {
		t.Fatal(err)
	}
	if counts["journal_outbox"] != 0 {
		t.Errorf("copied %d flushed outbox rows, want 0 (they live in events/*.jsonl)", counts["journal_outbox"])
	}
}

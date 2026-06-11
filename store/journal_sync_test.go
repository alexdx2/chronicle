package store

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// --- Task A: self-produced events are applied-by-definition ---

func TestFlushMarksSelfProducedEventsApplied(t *testing.T) {
	eventsDir, src := buildSourceJournal(t)

	var flushed, applied int
	src.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox WHERE flushed_at IS NOT NULL`, &flushed)
	src.QueryRowScan(`SELECT COUNT(*) FROM journal_applied_events`, &applied)
	if flushed == 0 {
		t.Fatal("setup: no flushed outbox rows")
	}
	if applied != flushed {
		t.Fatalf("applied events = %d, want %d (every flushed row is applied-by-definition)", applied, flushed)
	}

	// Replaying our own events over ourselves applies nothing.
	res, err := src.ReplayJournal(eventsDir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 {
		t.Errorf("replay over own events applied %d, want 0", res.Applied)
	}
	if res.Skipped != flushed {
		t.Errorf("replay skipped %d, want %d", res.Skipped, flushed)
	}
}

func TestFlushCatchUpCoversPreExistingFlushedRows(t *testing.T) {
	eventsDir, src := buildSourceJournal(t)

	// Simulate a db flushed BEFORE applied-bookkeeping existed.
	if err := src.Exec(`DELETE FROM journal_applied_events`); err != nil {
		t.Fatal(err)
	}

	// Any flush (e.g. the one on Open) backfills the applied entries.
	if _, err := src.FlushJournal(); err != nil {
		t.Fatal(err)
	}
	var flushed, applied int
	src.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox WHERE flushed_at IS NOT NULL`, &flushed)
	src.QueryRowScan(`SELECT COUNT(*) FROM journal_applied_events`, &applied)
	if applied != flushed {
		t.Fatalf("catch-up: applied = %d, want %d", applied, flushed)
	}

	res, err := src.ReplayJournal(eventsDir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 {
		t.Errorf("replay after catch-up applied %d, want 0", res.Applied)
	}
}

// --- Task B: auto-sync on Open ---

func copyEventsDir(t *testing.T, srcDir, dstDepbot string) {
	t.Helper()
	dst := filepath.Join(dstDepbot, "events")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		in, err := os.Open(filepath.Join(srcDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(filepath.Join(dst, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatal(err)
		}
		in.Close()
		out.Close()
	}
}

func TestOpenAutoSyncMaterializesSharedJournal(t *testing.T) {
	eventsDir, _ := buildSourceJournal(t)

	// Store B: a fresh "clone" that has ONLY the events dir.
	dirB := t.TempDir()
	copyEventsDir(t, eventsDir, dirB)
	b, err := Open(filepath.Join(dirB, "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

	var nodes, edges int
	b.QueryRowScan(`SELECT COUNT(*) FROM graph_nodes WHERE status='active'`, &nodes)
	b.QueryRowScan(`SELECT COUNT(*) FROM graph_edges WHERE active=1`, &edges)
	if nodes != 2 || edges != 1 {
		t.Fatalf("auto-sync on open: nodes=%d edges=%d, want 2/1", nodes, edges)
	}

	// Synced events must NOT re-journal (replay is journal-suppressed).
	var outbox int
	b.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox`, &outbox)
	if outbox != 0 {
		t.Errorf("synced events re-journaled: %d outbox rows, want 0", outbox)
	}

	// A second sync applies nothing.
	res, err := b.SyncJournal()
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 {
		t.Errorf("second sync applied %d, want 0", res.Applied)
	}
}

func TestOpenAutoSyncReopenAppliesZero(t *testing.T) {
	_, src := buildSourceJournal(t)
	dbPath := src.dbPath

	var nodesBefore, appliedBefore int
	src.QueryRowScan(`SELECT COUNT(*) FROM graph_nodes`, &nodesBefore)
	src.QueryRowScan(`SELECT COUNT(*) FROM journal_applied_events`, &appliedBefore)
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })

	var nodesAfter, appliedAfter int
	reopened.QueryRowScan(`SELECT COUNT(*) FROM graph_nodes`, &nodesAfter)
	reopened.QueryRowScan(`SELECT COUNT(*) FROM journal_applied_events`, &appliedAfter)
	if nodesAfter != nodesBefore {
		t.Errorf("reopen changed node rows: %d -> %d", nodesBefore, nodesAfter)
	}
	if appliedAfter != appliedBefore {
		t.Errorf("reopen applied new events: applied %d -> %d, want unchanged", appliedBefore, appliedAfter)
	}
}

func TestOpenWithoutEventsDirSkipsSync(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	res, err := s.SyncJournal()
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Skipped != 0 {
		t.Errorf("sync without events dir: %+v, want zero result", res)
	}
}

// TestLamportFloorSurvivesActorSwitch: sync-on-open records the merge floor
// before the CLI sets the git identity — the NEXT mutation, under whatever
// actor identity, must still sort after everything merged (global-max floor).
func TestLamportFloorSurvivesActorSwitch(t *testing.T) {
	eventsDir, src := buildSourceJournal(t)
	var maxLamport int64
	src.QueryRowScan(`SELECT MAX(lamport) FROM journal_outbox`, &maxLamport)

	dirB := t.TempDir()
	copyEventsDir(t, eventsDir, dirB)
	b, err := Open(filepath.Join(dirB, "chronicle.db")) // sync under default actor
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

	b.SetJournalActor("someone-new@host") // actor never seen by the merge floor
	err = b.WithTx(func(tx *Store) error {
		return tx.appendEvent(journalEvent{
			DomainKey: "dom", Kind: EvNodeStatus,
			Key: "code:module:dom:a", Fields: map[string]any{"status": "stale"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var newLamport int64
	b.QueryRowScan(`SELECT MAX(lamport) FROM journal_outbox`, &newLamport)
	if newLamport <= maxLamport {
		t.Errorf("post-merge event lamport = %d, want > %d (global floor must cover new actors)", newLamport, maxLamport)
	}
}

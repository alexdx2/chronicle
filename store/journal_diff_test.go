package store

import (
	"testing"
)

func TestJournalDiffCleanOnReplay(t *testing.T) {
	eventsDir, src := buildSourceJournal(t)

	dst := openJournalTestStore(t)
	if _, err := dst.ReplayJournal(eventsDir); err != nil {
		t.Fatal(err)
	}

	diff, err := DiffGraphStores(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Clean() {
		t.Fatalf("replayed db diverges from live db:\n%s", diff.String())
	}
}

func TestJournalDiffDetectsDivergence(t *testing.T) {
	eventsDir, src := buildSourceJournal(t)
	dst := openJournalTestStore(t)
	if _, err := dst.ReplayJournal(eventsDir); err != nil {
		t.Fatal(err)
	}

	// Mutate live db only.
	err := src.WithTx(func(tx *Store) error {
		_, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:extra", Layer: "code", NodeType: "module",
			DomainKey: "dom", Name: "extra", Status: "active", Metadata: "{}",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	diff, err := DiffGraphStores(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Clean() {
		t.Fatal("expected divergence, got clean diff")
	}
}

package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildPreJournalStore creates a store with a small graph (nodes, edge,
// evidence, alias, glossary term) and then strips ALL journal state —
// simulating a database that predates the event journal.
func buildPreJournalStore(t *testing.T) *Store {
	t.Helper()
	s := openJournalTestStore(t)
	revID, err := s.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	err = s.WithTx(func(tx *Store) error {
		aID, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		if err != nil {
			return err
		}
		bID, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:b", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "b", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		if err != nil {
			return err
		}
		if _, err := tx.UpsertEdge(EdgeRow{
			EdgeKey:    "code:module:dom:a->code:module:dom:b:IMPORTS",
			FromNodeID: aID, ToNodeID: bID, EdgeType: "IMPORTS", DerivationKind: "hard",
			Active: true, LastSeenRevisionID: revID, Metadata: "{}",
			FromNodeKey: "code:module:dom:a", ToNodeKey: "code:module:dom:b",
		}); err != nil {
			return err
		}
		if _, err := tx.AddEvidence(EvidenceRow{
			TargetKind: "node", NodeID: aID, SourceKind: "file", FilePath: "a.ts",
			LineStart: 1, ExtractorID: "x", ExtractorVersion: "1.0",
			Confidence: 0.8, AssertionKind: "import", Metadata: "{}",
			ValidFromRevisionID: revID,
		}); err != nil {
			return err
		}
		if _, err := tx.AddAlias(AliasRow{NodeID: aID, Alias: "ModuleA", AliasKind: "name", Confidence: 0.9}); err != nil {
			return err
		}
		if _, err := tx.UpsertTerm(DomainTerm{
			DomainKey: "dom", Term: "widget", Description: "a thing",
			Aliases: []string{"gadget"},
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Strip journal state: pretend the graph was built before the journal existed.
	for _, q := range []string{
		`DELETE FROM journal_outbox`, `DELETE FROM journal_applied_events`, `DELETE FROM journal_state`,
	} {
		if err := s.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(s.Dir(), "events")); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBootstrapJournalReplayMatchesLive(t *testing.T) {
	s := buildPreJournalStore(t)

	total, err := s.BootstrapJournal()
	if err != nil {
		t.Fatal(err)
	}
	// 2 nodes + 1 edge + 1 evidence + 1 alias + 1 glossary term.
	if total != 6 {
		t.Errorf("bootstrap emitted %d events, want 6", total)
	}

	// WithTx auto-flush must have written the events and marked them applied.
	var pending, applied int
	s.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox WHERE flushed_at IS NULL`, &pending)
	s.QueryRowScan(`SELECT COUNT(*) FROM journal_applied_events`, &applied)
	if pending != 0 {
		t.Errorf("outbox pending after bootstrap = %d, want 0 (auto-flush)", pending)
	}
	if applied != total {
		t.Errorf("applied events = %d, want %d (flush marks self-produced applied)", applied, total)
	}

	// Replay into a fresh store must reproduce the live graph exactly.
	fresh := openJournalTestStore(t)
	res, err := fresh.ReplayJournal(filepath.Join(s.Dir(), "events"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != total {
		t.Errorf("replay applied %d, want %d", res.Applied, total)
	}
	diff, err := DiffGraphStores(s, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Clean() {
		t.Fatalf("replay of bootstrapped journal diverges from live:\n%s", diff.String())
	}

	// Alias + glossary travel too (not covered by DiffGraphStores).
	var aliases, terms int
	fresh.QueryRowScan(`SELECT COUNT(*) FROM node_aliases`, &aliases)
	fresh.QueryRowScan(`SELECT COUNT(*) FROM domain_language`, &terms)
	if aliases != 1 || terms != 1 {
		t.Errorf("replayed aliases=%d terms=%d, want 1/1", aliases, terms)
	}

	// A sync over the same events on the SOURCE store applies nothing.
	syncRes, err := s.SyncJournal()
	if err != nil {
		t.Fatal(err)
	}
	if syncRes.Applied != 0 {
		t.Errorf("sync after bootstrap applied %d, want 0", syncRes.Applied)
	}
}

func TestBootstrapJournalBackfillsEvidenceUID(t *testing.T) {
	s := buildPreJournalStore(t)
	// Simulate a legacy row without a uid.
	if err := s.Exec(`UPDATE graph_evidence SET evidence_uid = NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BootstrapJournal(); err != nil {
		t.Fatal(err)
	}
	var uid string
	s.QueryRowScan(`SELECT COALESCE(evidence_uid,'') FROM graph_evidence LIMIT 1`, &uid)
	if uid == "" {
		t.Error("evidence_uid not backfilled by bootstrap")
	}
}

func TestBootstrapJournalRefusesExistingJournal(t *testing.T) {
	// Outbox guard: a normally-journaled store must refuse init.
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	err := s.WithTx(func(tx *Store) error {
		_, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BootstrapJournal(); err == nil || !strings.Contains(err.Error(), "already initialized") {
		t.Fatalf("BootstrapJournal on journaled store: err = %v, want 'already initialized'", err)
	}

	// Second-init guard: a bootstrapped store must refuse another init.
	pre := buildPreJournalStore(t)
	if _, err := pre.BootstrapJournal(); err != nil {
		t.Fatal(err)
	}
	if _, err := pre.BootstrapJournal(); err == nil || !strings.Contains(err.Error(), "already initialized") {
		t.Fatalf("second BootstrapJournal: err = %v, want 'already initialized'", err)
	}

	// Events-file guard: empty outbox but non-empty events file (e.g. db reset
	// by hand while events survived) must also refuse.
	pre2 := buildPreJournalStore(t)
	if _, err := pre2.BootstrapJournal(); err != nil {
		t.Fatal(err)
	}
	if err := pre2.Exec(`DELETE FROM journal_outbox`); err != nil {
		t.Fatal(err)
	}
	if _, err := pre2.BootstrapJournal(); err == nil || !strings.Contains(err.Error(), "already initialized") {
		t.Fatalf("BootstrapJournal with existing events files: err = %v, want 'already initialized'", err)
	}
}

package store

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// buildSourceJournal creates a store, performs mutations, flushes, and returns its events dir.
func buildSourceJournal(t *testing.T) (string, *Store) {
	t.Helper()
	src := openJournalTestStore(t)
	revID, _ := src.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	err := src.WithTx(func(tx *Store) error {
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
		_, err = tx.UpsertEdge(EdgeRow{
			EdgeKey:    "code:module:dom:a->code:module:dom:b:IMPORTS",
			FromNodeID: aID, ToNodeID: bID, EdgeType: "IMPORTS", DerivationKind: "hard",
			Active: true, LastSeenRevisionID: revID, Metadata: "{}",
			FromNodeKey: "code:module:dom:a", ToNodeKey: "code:module:dom:b",
		})
		if err != nil {
			return err
		}
		_, err = tx.AddEvidence(EvidenceRow{
			TargetKind: "node", NodeID: aID, SourceKind: "file", FilePath: "a.ts",
			LineStart: 1, ExtractorID: "x", ExtractorVersion: "1.0",
			Confidence: 0.8, AssertionKind: "import", Metadata: "{}",
			ValidFromRevisionID: revID,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.FlushJournal(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(src.Dir(), "events"), src
}

func TestReplayRebuildsGraph(t *testing.T) {
	eventsDir, _ := buildSourceJournal(t)

	dst := openJournalTestStore(t)
	res, err := dst.ReplayJournal(eventsDir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied == 0 {
		t.Fatal("applied 0 events")
	}

	var nodes, edges, evid int
	dst.QueryRowScan(`SELECT COUNT(*) FROM graph_nodes WHERE status='active'`, &nodes)
	dst.QueryRowScan(`SELECT COUNT(*) FROM graph_edges WHERE active=1`, &edges)
	dst.QueryRowScan(`SELECT COUNT(*) FROM graph_evidence`, &evid)
	if nodes != 2 || edges != 1 || evid < 1 {
		t.Fatalf("replayed graph: nodes=%d edges=%d evidence=%d, want 2/1/>=1", nodes, edges, evid)
	}
	// evidence_uid must survive replay
	var uid string
	dst.QueryRowScan(`SELECT COALESCE(evidence_uid,'') FROM graph_evidence LIMIT 1`, &uid)
	if uid == "" {
		t.Error("replayed evidence lost evidence_uid")
	}
}

func TestReplayIgnoresDuplicateEventIDs(t *testing.T) {
	eventsDir, _ := buildSourceJournal(t)

	// Duplicate the whole dom.jsonl (simulates merge=union duplication / crash re-append).
	domFile := filepath.Join(eventsDir, "dom.jsonl")
	data, _ := os.ReadFile(domFile)
	os.WriteFile(domFile, append(data, data...), 0o644)

	dst := openJournalTestStore(t)
	res1, err := dst.ReplayJournal(eventsDir)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Skipped == 0 {
		t.Fatal("expected duplicate events to be skipped")
	}
	res2, err := dst.ReplayJournal(eventsDir)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Applied != 0 {
		t.Fatalf("second replay applied %d, want 0", res2.Applied)
	}
	var nodes int
	dst.QueryRowScan(`SELECT COUNT(*) FROM graph_nodes WHERE status='active'`, &nodes)
	if nodes != 2 {
		t.Fatalf("nodes after duplicated replay = %d, want 2", nodes)
	}
}

func TestReplayIsDeterministic(t *testing.T) {
	eventsDir, _ := buildSourceJournal(t)

	fingerprint := func() string {
		dst := openJournalTestStore(t)
		if _, err := dst.ReplayJournal(eventsDir); err != nil {
			t.Fatal(err)
		}
		var out string
		for _, q := range []string{diffNodesQ, diffEdgesQ, diffEvidenceQ} {
			rows, err := semanticRows(dst, q)
			if err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(rows))
			for k := range rows {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				out += k + "=" + rows[k] + "\n"
			}
		}
		return out
	}

	a, b := fingerprint(), fingerprint()
	if a != b {
		t.Fatal("two replays of the same journal produced different graphs")
	}
}

// TestReplayAdvancesLamportFloor: after merging someone else's journal, the
// local actor's next events must sort AFTER everything replayed —
// max(seen)+1, not a counter that restarts at 1.
func TestReplayAdvancesLamportFloor(t *testing.T) {
	eventsDir, src := buildSourceJournal(t)

	var maxLamport int64
	src.QueryRowScan(`SELECT MAX(lamport) FROM journal_outbox`, &maxLamport)
	if maxLamport == 0 {
		t.Fatal("source journal has no lamports")
	}

	dst := openJournalTestStore(t)
	dst.SetJournalActor("other@host") // different actor than the journal's author
	if _, err := dst.ReplayJournal(eventsDir); err != nil {
		t.Fatal(err)
	}

	// New local event after the merge.
	err := dst.WithTx(func(tx *Store) error {
		return tx.appendEvent(journalEvent{
			DomainKey: "dom", Kind: EvNodeStatus,
			Key: "code:module:dom:a", Fields: map[string]any{"status": "stale"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var newLamport int64
	dst.QueryRowScan(`SELECT MAX(lamport) FROM journal_outbox`, &newLamport)
	if newLamport <= maxLamport {
		t.Errorf("post-replay event lamport = %d, want > %d (replayed max)", newLamport, maxLamport)
	}
}

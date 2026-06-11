package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSONLLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad JSONL line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestFlushJournalWritesPerDomainFiles(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	s.WithTx(func(tx *Store) error {
		_, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		return err
	})

	// WithTx auto-flushes after commit, so this explicit flush may legitimately
	// write 0 events. The file contents below carry the real assertions.
	if _, err := s.FlushJournal(); err != nil {
		t.Fatal(err)
	}

	domFile := filepath.Join(s.Dir(), "events", "dom.jsonl")
	lines := readJSONLLines(t, domFile)
	if len(lines) == 0 {
		t.Fatal("dom.jsonl is empty — nothing was flushed")
	}
	var sawUpsert bool
	for _, m := range lines {
		if m["kind"] == "node_upsert" {
			sawUpsert = true
			if m["event_id"] == "" || m["event_id"] == nil {
				t.Error("event missing event_id")
			}
			if m["revision"] != "abc123" {
				t.Errorf("revision = %v, want abc123 (git sha, not numeric id)", m["revision"])
			}
			if m["key"] != "code:module:dom:a" {
				t.Errorf("key = %v", m["key"])
			}
		}
	}
	if !sawUpsert {
		t.Fatal("node_upsert not found in dom.jsonl")
	}

	n2, err := s.FlushJournal()
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("second flush wrote %d events, want 0", n2)
	}
}

func TestFlushCrashAfterAppendIsDeduplicatedByEventID(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	s.WithTx(func(tx *Store) error {
		_, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		return err
	})
	if _, err := s.FlushJournal(); err != nil {
		t.Fatal(err)
	}
	// Simulate "crash after append, before flushed-mark": un-mark and re-flush.
	if err := s.Exec(`UPDATE journal_outbox SET flushed_at = NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlushJournal(); err != nil {
		t.Fatal(err)
	}
	lines := readJSONLLines(t, filepath.Join(s.Dir(), "events", "dom.jsonl"))
	seen := map[string]int{}
	for _, m := range lines {
		seen[m["event_id"].(string)]++
	}
	for _, n := range seen {
		if n > 1 {
			return // good — same id appended twice; replay dedups
		}
	}
	t.Fatal("expected a duplicated event_id after simulated crash re-flush")
}

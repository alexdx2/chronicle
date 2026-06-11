package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// flushedEvent is the JSONL wire format.
type flushedEvent struct {
	V        int             `json:"v"`
	EventID  string          `json:"event_id"`
	Lamport  int64           `json:"lamport"`
	Actor    string          `json:"actor"`
	TS       string          `json:"ts"`
	Revision string          `json:"revision,omitempty"`
	Kind     string          `json:"kind"`
	Key      string          `json:"key"`
	OwnerKey string          `json:"entity_key,omitempty"`
	Fields   json.RawMessage `json:"fields"`
}

// sanitizeDomainFile makes a domain_key safe to use as a filename: path
// separators and ".." sequences are replaced with "_" so a hostile or
// malformed domain key cannot escape the events directory.
func sanitizeDomainFile(d string) string {
	d = strings.ReplaceAll(d, "/", "_")
	d = strings.ReplaceAll(d, "\\", "_")
	d = strings.ReplaceAll(d, "..", "_")
	if d == "" {
		d = "_unknown"
	}
	return d
}

// FlushJournal drains unflushed outbox rows into .depbot/events/<domain>.jsonl.
// Crash-safe: rows are marked flushed only after the file append fsyncs; a
// crash in between re-appends the same event_id, which replay ignores.
// Returns the number of events written.
func (s *Store) FlushJournal() (int, error) {
	if s.InTx() {
		return 0, nil // flushing happens outside transactions
	}
	rows, err := s.db.Query(`
		SELECT o.outbox_id, o.event_id, o.lamport, o.actor, o.ts,
		       COALESCE(r.git_after_sha, ''), o.domain_key, o.kind,
		       o.subject_key, COALESCE(o.owner_key, ''), o.fields
		FROM journal_outbox o
		LEFT JOIN graph_revisions r ON r.revision_id = o.revision_id
		WHERE o.flushed_at IS NULL
		ORDER BY o.outbox_id`)
	if err != nil {
		return 0, fmt.Errorf("FlushJournal select: %w", err)
	}
	type pending struct {
		outboxID int64
		domain   string
		line     []byte
	}
	var batch []pending
	for rows.Next() {
		var p pending
		var ev flushedEvent
		ev.V = 1
		var fields string
		if err := rows.Scan(&p.outboxID, &ev.EventID, &ev.Lamport, &ev.Actor, &ev.TS,
			&ev.Revision, &p.domain, &ev.Kind, &ev.Key, &ev.OwnerKey, &fields); err != nil {
			rows.Close()
			return 0, err
		}
		ev.Fields = json.RawMessage(fields)
		line, err := json.Marshal(ev)
		if err != nil {
			rows.Close()
			return 0, err
		}
		p.line = line
		batch = append(batch, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		return 0, nil
	}

	eventsDir := filepath.Join(s.Dir(), "events")
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		return 0, err
	}

	byDomain := map[string][]pending{}
	for _, p := range batch {
		byDomain[p.domain] = append(byDomain[p.domain], p)
	}
	domains := make([]string, 0, len(byDomain))
	for d := range byDomain {
		domains = append(domains, d)
	}
	sort.Strings(domains)

	written := 0
	for _, d := range domains {
		f, err := os.OpenFile(filepath.Join(eventsDir, sanitizeDomainFile(d)+".jsonl"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return written, err
		}
		var ids []int64
		for _, p := range byDomain[d] {
			if _, err := f.Write(append(p.line, '\n')); err != nil {
				f.Close()
				return written, err
			}
			ids = append(ids, p.outboxID)
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return written, err
		}
		f.Close()
		for _, id := range ids {
			if _, err := s.db.Exec(
				`UPDATE journal_outbox SET flushed_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE outbox_id = ?`,
				id); err != nil {
				return written, err
			}
			written++
		}
	}
	return written, nil
}

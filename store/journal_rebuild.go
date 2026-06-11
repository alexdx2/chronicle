package store

import (
	"fmt"
	"strings"
)

// localStateTables are the NON-journaled tables carried over verbatim during
// `chronicle journal rebuild`. The journal covers graph-semantic state
// (nodes/edges/evidence/aliases/glossary); everything operational or local
// lives only in the db and must be copied from the old db into the rebuilt one.
//
// Deliberately NOT in this list:
//   - graph_nodes / graph_edges / graph_evidence / node_aliases /
//     domain_language — rebuilt by ReplayJournal.
//   - journal_applied_events — produced by the replay itself; copying the old
//     bookkeeping would mark events as applied that the new db never saw.
//   - journal_outbox flushed rows — already durable in events/*.jsonl;
//     only unflushed rows are carried (see the where clause).
//   - journal_state — merged separately with MAX (lamport floor must never
//     move backwards; replay may already have raised it on the new db).
var localStateTables = []struct {
	name  string
	where string
}{
	{"graph_revisions", ""},
	{"knowledge_contexts", ""},
	{"graph_snapshots", ""},
	{"graph_changelog", ""}, // audit log; replay suppresses appendEvent so it is not rebuilt
	{"graph_discoveries", ""},
	{"project_settings", ""},
	{"diagram_sessions", ""},
	{"mcp_request_log", ""},
	{"scan_runs", ""},
	{"scan_obligations", ""},
	{"scan_extractions", ""},
	{"evidence_verification_runs", ""},
	{"journal_outbox", "WHERE flushed_at IS NULL"},
}

// CopyLocalState copies all non-journaled local state from another store into
// this one (the freshly replayed db). Runs in a single transaction on the
// destination; the source is only read. Returns rows copied per table.
//
// Copied rows keep their original ids (revision ids, run ids, ...). Replayed
// graph rows reference revisions only loosely (revision-id columns are 0/NULL
// by design — DiffGraphStores ignores them), so id preservation is safe.
//
// Deliberately does NOT auto-flush the journal afterwards: carried unflushed
// outbox rows must stay pending in the new db exactly as in the source.
func (s *Store) CopyLocalState(from *Store) (map[string]int, error) {
	if s.conn == nil {
		return nil, fmt.Errorf("CopyLocalState: destination store is transaction-scoped")
	}
	counts := map[string]int{}
	sqlTx, err := s.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("CopyLocalState begin: %w", err)
	}
	tx := &Store{conn: nil, db: sqlTx, dbPath: s.dbPath, journalActor: s.journalActor, suppressJournal: true}

	for _, t := range localStateTables {
		n, err := tx.copyTableRows(from, t.name, t.where)
		if err != nil {
			sqlTx.Rollback()
			return nil, fmt.Errorf("CopyLocalState %s: %w", t.name, err)
		}
		counts[t.name] = n
	}

	// journal_state: MAX-merge per actor. Replay already raised this db's
	// lamport floor to max(seen in files); the source may hold a higher
	// counter (e.g. unflushed events) — the floor must never move backwards.
	rows, err := from.db.Query(`SELECT actor, last_lamport FROM journal_state`)
	if err != nil {
		sqlTx.Rollback()
		return nil, fmt.Errorf("CopyLocalState journal_state: %w", err)
	}
	stateRows := 0
	for rows.Next() {
		var actor string
		var lamport int64
		if err := rows.Scan(&actor, &lamport); err != nil {
			rows.Close()
			sqlTx.Rollback()
			return nil, err
		}
		if _, err := tx.db.Exec(`
			INSERT INTO journal_state (actor, last_lamport) VALUES (?, ?)
			ON CONFLICT(actor) DO UPDATE SET last_lamport = MAX(last_lamport, excluded.last_lamport)`,
			actor, lamport); err != nil {
			rows.Close()
			sqlTx.Rollback()
			return nil, fmt.Errorf("CopyLocalState journal_state merge: %w", err)
		}
		stateRows++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		sqlTx.Rollback()
		return nil, err
	}
	counts["journal_state"] = stateRows

	if err := sqlTx.Commit(); err != nil {
		return nil, fmt.Errorf("CopyLocalState commit: %w", err)
	}
	return counts, nil
}

// copyTableRows copies rows of one table from another store into this one,
// matching on the intersection of column names (old dbs grew columns via
// ALTER TABLE, so column ORDER differs from a fresh schema — a positional
// `INSERT ... SELECT *` would scramble values).
func (s *Store) copyTableRows(from *Store, table, where string) (int, error) {
	srcCols, err := tableColumns(from, table)
	if err != nil {
		return 0, err
	}
	dstCols, err := tableColumns(s, table)
	if err != nil {
		return 0, err
	}
	dstSet := map[string]bool{}
	for _, c := range dstCols {
		dstSet[c] = true
	}
	var cols []string
	for _, c := range srcCols {
		if dstSet[c] {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return 0, nil // table missing on one side — nothing to carry
	}

	quoted := make([]string, len(cols))
	marks := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = `"` + c + `"`
		marks[i] = "?"
	}
	colList := strings.Join(quoted, ", ")

	rows, err := from.db.Query("SELECT " + colList + " FROM " + table + " " + where)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	insert := "INSERT INTO " + table + " (" + colList + ") VALUES (" + strings.Join(marks, ", ") + ")"
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	n := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		if _, err := s.db.Exec(insert, vals...); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// tableColumns returns the column names of a table (empty if the table does
// not exist).
func tableColumns(s *Store, table string) ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

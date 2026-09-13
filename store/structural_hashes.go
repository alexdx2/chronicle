package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Structural content hashes: "did the structural phase already look at this
// exact file content, with this rules pack?"
//
// They live in project_settings, NOT on the evidence rows they justify. A file
// contributes many evidence rows, and AddEvidence's dedup path updates an
// existing row without rewriting its metadata — so a hash written into
// evidence metadata is frozen at the moment the row was first inserted and goes
// quietly stale forever after. One record per (domain, file) is also the right
// shape for the question: the phase asks it once per file, before deciding
// whether to parse at all, and a file with zero facts (which has no evidence
// rows to hang a hash on) still needs an answer.
//
// Key:   structural:hash:<domain>:<file_path>
// Value: <sha256>:<pack_version>:<revision_id>
//
// The revision is what makes a rebase recoverable: structure extracted on a
// branch nobody merged was recorded against a revision this checkout can no
// longer reach, and DeleteStructuralHashesAfter forgets exactly those files so
// the next run looks at them again under the base it CAN reach. A two-field
// value (written before the revision was recorded) reads as revision 0, i.e.
// "older than anything" — it is never mistaken for branch-only work.

const structuralHashPrefix = "structural:hash:"

// structuralHashKeyPrefix is every key of one domain. The file path is whatever
// follows, verbatim — it is never split on, so a path containing ':' round
// trips.
func structuralHashKeyPrefix(domain string) string {
	return structuralHashPrefix + domain + ":"
}

func structuralHashKey(domain, filePath string) string {
	return structuralHashKeyPrefix(domain) + filePath
}

// GetStructuralHash returns the content hash and rules-pack version of the last
// structural look at filePath, and whether there was one.
func (s *Store) GetStructuralHash(domain, filePath string) (hash, pack string, ok bool, err error) {
	var value string
	err = s.db.QueryRow(`SELECT value FROM project_settings WHERE key = ?`,
		structuralHashKey(domain, filePath)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("GetStructuralHash %q: %w", filePath, err)
	}
	// A record we cannot read is no record: the caller re-extracts, which is
	// always the safe answer.
	h, rest, found := strings.Cut(value, ":")
	if !found {
		return "", "", false, nil
	}
	// sha256 hex has no colon, so the first one separates the hash; the second
	// (when present) separates the pack from the revision that recorded it.
	p, _, _ := strings.Cut(rest, ":")
	return h, p, true, nil
}

// SetStructuralHash records what the structural phase saw, replacing any
// previous record for the same file. This is a record, not a history.
func (s *Store) SetStructuralHash(domain, filePath, hash, pack string, revisionID int64) error {
	value := fmt.Sprintf("%s:%s:%d", hash, pack, revisionID)
	if _, err := s.db.Exec(`
		INSERT INTO project_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		structuralHashKey(domain, filePath), value); err != nil {
		return fmt.Errorf("SetStructuralHash %q: %w", filePath, err)
	}
	return nil
}

// DeleteStructuralHash forgets a file — a deletion in the diff. Forgetting what
// was never recorded is not an error: a deleted file may never have been
// structural in the first place.
func (s *Store) DeleteStructuralHash(domain, filePath string) error {
	if _, err := s.db.Exec(`DELETE FROM project_settings WHERE key = ?`,
		structuralHashKey(domain, filePath)); err != nil {
		return fmt.Errorf("DeleteStructuralHash %q: %w", filePath, err)
	}
	return nil
}

// structuralTailExpr is everything after the hash: "<pack>" or "<pack>:<rev>".
// sha256 hex contains no ':', so the first one is the separator; instr returns
// 0 for a value with no separator at all, and substr(value, 1) then yields the
// whole malformed value, which compares unequal to any real pack — a record in
// a shape we do not recognise counts as backlog, and re-extracting is the safe
// answer.
const structuralTailExpr = `substr(value, instr(value, ':') + 1)`

// structuralPackExpr is the pack alone. The ELSE branch reads a record written
// before the revision id was part of the value.
const structuralPackExpr = `CASE WHEN instr(` + structuralTailExpr + `, ':') > 0
	THEN substr(` + structuralTailExpr + `, 1, instr(` + structuralTailExpr + `, ':') - 1)
	ELSE ` + structuralTailExpr + ` END`

// structuralRevExpr is the revision that recorded the look, 0 when the value
// predates the field — "older than anything", never branch-only work.
const structuralRevExpr = `CASE WHEN instr(` + structuralTailExpr + `, ':') > 0
	THEN CAST(substr(` + structuralTailExpr + `, instr(` + structuralTailExpr + `, ':') + 1) AS INTEGER)
	ELSE 0 END`

// structuralBacklogPredicate matches one domain's records that were not written
// by pack. The key prefix is compared with substr rather than LIKE so a domain
// containing '%' or '_' cannot turn into a wildcard.
func structuralBacklogPredicate(domain string) (string, []any) {
	prefix := structuralHashKeyPrefix(domain)
	return `substr(key, 1, ?) = ? AND ` + structuralPackExpr + ` != ?`,
		[]any{len(prefix), prefix}
}

// CountStructuralHashesNotOnPack counts this domain's files whose last
// structural look used a different rules pack — the "340 files on old rules"
// the freshness line owes the reader. Never capped by a batch size.
func (s *Store) CountStructuralHashesNotOnPack(domain, pack string) (int, error) {
	where, args := structuralBacklogPredicate(domain)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM project_settings WHERE `+where,
		append(args, pack)...).Scan(&n); err != nil {
		return 0, fmt.Errorf("CountStructuralHashesNotOnPack: %w", err)
	}
	return n, nil
}

// FilesWithStructuralHashNotOnPack lists that backlog, oldest pack first, at
// most limit files (limit <= 0 means no bound). A pack bump makes every file it
// touched re-extractable even though the source did not change; the phase works
// through them in bounded batches and `structured` does not advance past HEAD
// until the backlog is empty.
//
// Packs are integers written as strings, so the numeric CAST orders them; the
// textual tiebreak keeps the order total when a value is not a number.
func (s *Store) FilesWithStructuralHashNotOnPack(domain, pack string, limit int) ([]string, error) {
	where, args := structuralBacklogPredicate(domain)
	q := `SELECT key FROM project_settings WHERE ` + where + `
	      ORDER BY CAST(` + structuralPackExpr + ` AS INTEGER) ASC, ` + structuralPackExpr + ` ASC, key ASC`
	args = append(args, pack)
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("FilesWithStructuralHashNotOnPack: %w", err)
	}
	defer rows.Close()
	prefix := structuralHashKeyPrefix(domain)
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("FilesWithStructuralHashNotOnPack scan: %w", err)
		}
		out = append(out, strings.TrimPrefix(key, prefix))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("FilesWithStructuralHashNotOnPack rows: %w", err)
	}
	return out, nil
}

// DeleteStructuralHashesAfter forgets every record this domain wrote after
// revisionID, so those files are looked at again.
//
// It is the answer to a rebase. When the structural pointer names a commit this
// checkout cannot reach, the runs that produced it happened on a branch nobody
// merged: their content records say "already done under this pack" about files
// whose structure came from code that is not in this history. Keeping them
// would leave the new base's version of those files permanently unread — the
// one way a content hash can hide a change instead of skipping a no-op.
//
// The cut is by REVISION ID, not by reachability: revision ids are a local
// insertion order, not a commit graph, so "written after revision N" is a
// proxy for "written on work this checkout can no longer reach", not a proof
// of it. It errs on the side of forgetting — a record dropped for a file that
// was in fact still reachable costs one re-extraction and nothing else, while
// a record kept for an unreachable one costs a file that is never read again.
// Asking git per record would be exact and would also mean one subprocess per
// file; if that trade ever changes, this is the function to change.
func (s *Store) DeleteStructuralHashesAfter(domain string, revisionID int64) (int64, error) {
	prefix := structuralHashKeyPrefix(domain)
	res, err := s.db.Exec(`DELETE FROM project_settings
		WHERE substr(key, 1, ?) = ? AND `+structuralRevExpr+` > ?`,
		len(prefix), prefix, revisionID)
	if err != nil {
		return 0, fmt.Errorf("DeleteStructuralHashesAfter: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Revision represents a row in graph_revisions.
type Revision struct {
	RevisionID   int64  `json:"revision_id"`
	DomainKey    string `json:"domain_key"`
	GitBeforeSHA string `json:"git_before_sha"`
	GitAfterSHA  string `json:"git_after_sha"`
	TriggerKind  string `json:"trigger_kind"`
	Mode         string `json:"mode"`
	CreatedAt    string `json:"created_at"`
	Metadata     string `json:"metadata"`
}

// CreateRevision inserts a new revision and returns the revision_id.
func (s *Store) CreateRevision(domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata string) (int64, error) {
	const q = `
		INSERT INTO graph_revisions (domain_key, git_before_sha, git_after_sha, trigger_kind, mode, metadata)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	res, err := s.db.Exec(q, domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata)
	if err != nil {
		return 0, fmt.Errorf("CreateRevision: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("CreateRevision last insert id: %w", err)
	}
	if err := s.emitRevisionOpen(domainKey, id, afterSHA, mode, triggerKind); err != nil {
		return 0, err
	}
	return id, nil
}

// emitRevisionOpen journals the start of a revision (exactly one event per Create*Revision call).
func (s *Store) emitRevisionOpen(domainKey string, id int64, afterSHA, mode, triggerKind string) error {
	return s.appendEvent(journalEvent{
		DomainKey: domainKey, RevisionID: id, Kind: EvRevisionOpen,
		Key:    fmt.Sprintf("revision:%s:%d", domainKey, id),
		Fields: map[string]any{"after_sha": afterSHA, "mode": mode, "trigger": triggerKind},
	})
}

// CreateRevisionWithContext inserts a new revision linked to a knowledge context and returns the revision_id.
func (s *Store) CreateRevisionWithContext(domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata string, contextID int64) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO graph_revisions (domain_key, git_before_sha, git_after_sha, trigger_kind, mode, metadata, context_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata, contextID)
	if err != nil {
		return 0, fmt.Errorf("CreateRevisionWithContext: %w", err)
	}
	id, _ := res.LastInsertId()
	if err := s.emitRevisionOpen(domainKey, id, afterSHA, mode, triggerKind); err != nil {
		return 0, err
	}
	return id, nil
}

// GetLatestRevision returns the most recent revision for a domain.
func (s *Store) GetLatestRevision(domainKey string) (*Revision, error) {
	const q = `
		SELECT revision_id, domain_key, COALESCE(git_before_sha,''), git_after_sha,
		       trigger_kind, mode, created_at, metadata
		FROM graph_revisions
		WHERE domain_key = ?
		ORDER BY revision_id DESC
		LIMIT 1
	`
	r := &Revision{}
	err := s.db.QueryRow(q, domainKey).Scan(
		&r.RevisionID, &r.DomainKey, &r.GitBeforeSHA, &r.GitAfterSHA,
		&r.TriggerKind, &r.Mode, &r.CreatedAt, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetLatestRevision %q: %w", domainKey, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetLatestRevision %q: %w", domainKey, err)
	}
	return r, nil
}

// LatestRevisionAnyDomain returns the most recent revision across all domains
// (GetLatestRevision is domain-scoped via WHERE domain_key = ?, which does not
// match rows with a real domain key when called with "").
func (s *Store) LatestRevisionAnyDomain() (*Revision, error) {
	const q = `
		SELECT revision_id, domain_key, COALESCE(git_before_sha,''), git_after_sha,
		       trigger_kind, mode, created_at, metadata
		FROM graph_revisions
		ORDER BY revision_id DESC
		LIMIT 1
	`
	r := &Revision{}
	err := s.db.QueryRow(q).Scan(
		&r.RevisionID, &r.DomainKey, &r.GitBeforeSHA, &r.GitAfterSHA,
		&r.TriggerKind, &r.Mode, &r.CreatedAt, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("LatestRevisionAnyDomain: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("LatestRevisionAnyDomain: %w", err)
	}
	return r, nil
}

// GetRevision returns the revision with the given id, or ErrNotFound if absent.
func (s *Store) GetRevision(id int64) (*Revision, error) {
	const q = `
		SELECT revision_id, domain_key, COALESCE(git_before_sha,''), git_after_sha,
		       trigger_kind, mode, created_at, metadata
		FROM graph_revisions
		WHERE revision_id = ?
	`
	row := s.db.QueryRow(q, id)
	r := &Revision{}
	err := row.Scan(
		&r.RevisionID, &r.DomainKey, &r.GitBeforeSHA, &r.GitAfterSHA,
		&r.TriggerKind, &r.Mode, &r.CreatedAt, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetRevision %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetRevision %d: %w", id, err)
	}
	return r, nil
}

// revisionCols is the column list every revision query selects, in Revision
// field order.
const revisionCols = `revision_id, domain_key, COALESCE(git_before_sha,''), git_after_sha,
		       trigger_kind, mode, created_at, metadata`

// revisionLayerExpr reads metadata.layer defensively. json_extract raises on
// malformed JSON, which would fail the whole query because of one bad row;
// CASE is documented to short-circuit, so json_extract never sees invalid
// input and a non-JSON metadata simply reads as "no layer".
const revisionLayerExpr = `CASE WHEN json_valid(metadata) THEN json_extract(metadata,'$.layer') ELSE NULL END`

// oneRevision runs a single-row revision query and maps no rows to ErrNotFound.
func (s *Store) oneRevision(what, q string, args ...any) (*Revision, error) {
	r := &Revision{}
	err := s.db.QueryRow(q, args...).Scan(
		&r.RevisionID, &r.DomainKey, &r.GitBeforeSHA, &r.GitAfterSHA,
		&r.TriggerKind, &r.Mode, &r.CreatedAt, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return r, nil
}

// LatestScanRevision is the newest non-refresh, non-layer revision of
// domainKey (empty domainKey = any domain) — the revision that says how old
// the code knowledge is. Refresh revisions (trigger_kind='git_hook') only
// re-verify existing knowledge and layer imports (metadata.layer) only carry
// one layer, so neither may move the scanned SHA. ErrNotFound when none.
func (s *Store) LatestScanRevision(domainKey string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE trigger_kind != 'git_hook' AND ` + revisionLayerExpr + ` IS NULL`
	var args []any
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY revision_id DESC LIMIT 1`
	return s.oneRevision("LatestScanRevision", q, args...)
}

// LatestRefreshRevision is the newest trigger_kind='git_hook' revision — the
// last time knowledge was re-verified against a commit without a rescan.
// ErrNotFound when none.
func (s *Store) LatestRefreshRevision(domainKey string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE trigger_kind = 'git_hook'`
	var args []any
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY revision_id DESC LIMIT 1`
	return s.oneRevision("LatestRefreshRevision", q, args...)
}

// LatestLayerRevision is the newest revision whose metadata.layer == layer
// (e.g. a "ui" surface import). ErrNotFound when none.
func (s *Store) LatestLayerRevision(domainKey, layer string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE ` + revisionLayerExpr + ` = ?`
	args := []any{layer}
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY revision_id DESC LIMIT 1`
	return s.oneRevision("LatestLayerRevision", q, args...)
}

// NewestRevisionDomain returns the domain of the newest revision in the
// store — the caller's answer to "which domain is this project" when no
// domain was passed. ErrNotFound when the store holds no revisions.
func (s *Store) NewestRevisionDomain() (string, error) {
	var d string
	err := s.db.QueryRow(`SELECT domain_key FROM graph_revisions ORDER BY revision_id DESC LIMIT 1`).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("NewestRevisionDomain: %w", ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("NewestRevisionDomain: %w", err)
	}
	return d, nil
}

// GetRevisionBySHA returns the revision a domain recorded for one git SHA, or
// ErrNotFound. There is at most one (UNIQUE(domain_key, git_after_sha)), which
// is what lets an importer ask "have I already been told about this commit?"
// before it writes anything.
func (s *Store) GetRevisionBySHA(domainKey, sha string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions WHERE domain_key = ? AND git_after_sha = ?`
	return s.oneRevision(fmt.Sprintf("GetRevisionBySHA %q %q", domainKey, sha), q, domainKey, sha)
}

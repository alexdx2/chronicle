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
	return id, nil
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

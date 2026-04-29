package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ContextRow represents a row in knowledge_contexts.
type ContextRow struct {
	ContextID      int64  `json:"context_id"`
	DomainKey      string `json:"domain_key"`
	Name           string `json:"name"`
	GitRef         string `json:"git_ref"`
	BaseContextID  int64  `json:"base_context_id"`
	BaseRevisionID int64  `json:"base_revision_id"`
	HeadRevisionID int64  `json:"head_revision_id"`
	HeadCommitSHA  string `json:"head_commit_sha"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
}

// CreateContext inserts a new knowledge context and returns the context_id.
func (s *Store) CreateContext(domainKey, name, gitRef string, baseContextID, baseRevisionID int64) (int64, error) {
	const q = `
		INSERT INTO knowledge_contexts (domain_key, name, git_ref, base_context_id, base_revision_id, status)
		VALUES (?, ?, ?, ?, ?, 'active')
	`
	res, err := s.db.Exec(q, domainKey, name, nullableStr(gitRef), nullableInt64(baseContextID), nullableInt64(baseRevisionID))
	if err != nil {
		return 0, fmt.Errorf("CreateContext: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("CreateContext last insert id: %w", err)
	}
	return id, nil
}

// GetContext returns the context with the given context_id, or ErrNotFound if absent.
func (s *Store) GetContext(id int64) (*ContextRow, error) {
	const q = `
		SELECT context_id, domain_key, name, COALESCE(git_ref,''),
		       COALESCE(base_context_id,0), COALESCE(base_revision_id,0),
		       COALESCE(head_revision_id,0), COALESCE(head_commit_sha,''),
		       status, created_at
		FROM knowledge_contexts WHERE context_id = ?
	`
	r := &ContextRow{}
	err := s.db.QueryRow(q, id).Scan(
		&r.ContextID, &r.DomainKey, &r.Name, &r.GitRef,
		&r.BaseContextID, &r.BaseRevisionID,
		&r.HeadRevisionID, &r.HeadCommitSHA,
		&r.Status, &r.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetContext %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetContext %d: %w", id, err)
	}
	return r, nil
}

// GetContextByRef returns the active context matching domain_key and git_ref,
// or ErrNotFound if absent.
func (s *Store) GetContextByRef(domainKey, gitRef string) (*ContextRow, error) {
	const q = `
		SELECT context_id, domain_key, name, COALESCE(git_ref,''),
		       COALESCE(base_context_id,0), COALESCE(base_revision_id,0),
		       COALESCE(head_revision_id,0), COALESCE(head_commit_sha,''),
		       status, created_at
		FROM knowledge_contexts
		WHERE domain_key = ? AND git_ref = ? AND status = 'active'
	`
	r := &ContextRow{}
	err := s.db.QueryRow(q, domainKey, gitRef).Scan(
		&r.ContextID, &r.DomainKey, &r.Name, &r.GitRef,
		&r.BaseContextID, &r.BaseRevisionID,
		&r.HeadRevisionID, &r.HeadCommitSHA,
		&r.Status, &r.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetContextByRef %q/%q: %w", domainKey, gitRef, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetContextByRef %q/%q: %w", domainKey, gitRef, err)
	}
	return r, nil
}

// ListContexts returns all contexts for a domain, ordered by context_id.
func (s *Store) ListContexts(domainKey string) ([]ContextRow, error) {
	const q = `
		SELECT context_id, domain_key, name, COALESCE(git_ref,''),
		       COALESCE(base_context_id,0), COALESCE(base_revision_id,0),
		       COALESCE(head_revision_id,0), COALESCE(head_commit_sha,''),
		       status, created_at
		FROM knowledge_contexts
		WHERE domain_key = ?
		ORDER BY context_id
	`
	rows, err := s.db.Query(q, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ListContexts: %w", err)
	}
	defer rows.Close()

	var out []ContextRow
	for rows.Next() {
		var r ContextRow
		if err := rows.Scan(
			&r.ContextID, &r.DomainKey, &r.Name, &r.GitRef,
			&r.BaseContextID, &r.BaseRevisionID,
			&r.HeadRevisionID, &r.HeadCommitSHA,
			&r.Status, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("ListContexts scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ArchiveContext sets the status of the context to 'archived'.
func (s *Store) ArchiveContext(id int64) error {
	res, err := s.db.Exec(`UPDATE knowledge_contexts SET status='archived' WHERE context_id=?`, id)
	if err != nil {
		return fmt.Errorf("ArchiveContext: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("ArchiveContext %d: %w", id, ErrNotFound)
	}
	return nil
}

// UpdateContextHead updates the head revision and commit SHA for a context.
func (s *Store) UpdateContextHead(contextID, revisionID int64, commitSHA string) error {
	res, err := s.db.Exec(
		`UPDATE knowledge_contexts SET head_revision_id=?, head_commit_sha=? WHERE context_id=?`,
		revisionID, commitSHA, contextID,
	)
	if err != nil {
		return fmt.Errorf("UpdateContextHead: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("UpdateContextHead %d: %w", contextID, ErrNotFound)
	}
	return nil
}

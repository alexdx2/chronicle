package store

import "fmt"

// ChangelogRow represents a row in graph_changelog.
type ChangelogRow struct {
	ChangelogID  int64  `json:"changelog_id"`
	RevisionID   int64  `json:"revision_id"`
	ContextID    int64  `json:"context_id"`
	EntityType   string `json:"entity_type"`
	EntityKey    string `json:"entity_key"`
	EntityID     int64  `json:"entity_id,omitempty"`
	ChangeType   string `json:"change_type"`
	FieldChanges string `json:"field_changes,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// AppendChangelog inserts a new changelog entry and returns the changelog_id.
func (s *Store) AppendChangelog(r ChangelogRow) (int64, error) {
	const q = `
		INSERT INTO graph_changelog (revision_id, context_id, entity_type, entity_key, entity_id, change_type, field_changes)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	res, err := s.db.Exec(q,
		r.RevisionID, r.ContextID, r.EntityType, r.EntityKey,
		nullableInt64(r.EntityID), r.ChangeType, nullableStr(r.FieldChanges),
	)
	if err != nil {
		return 0, fmt.Errorf("AppendChangelog: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("AppendChangelog last insert id: %w", err)
	}
	return id, nil
}

// QueryChangelog returns changelog entries for a context, with optional filters
// on entity_key and revision range.
func (s *Store) QueryChangelog(contextID int64, entityKey string, fromRevision, toRevision int64) ([]ChangelogRow, error) {
	base := `
		SELECT changelog_id, revision_id, context_id, entity_type, entity_key,
		       COALESCE(entity_id, 0), change_type, COALESCE(field_changes, ''),
		       created_at
		FROM graph_changelog WHERE context_id = ?`
	args := []any{contextID}

	if entityKey != "" {
		base += " AND entity_key = ?"
		args = append(args, entityKey)
	}
	if fromRevision > 0 {
		base += " AND revision_id >= ?"
		args = append(args, fromRevision)
	}
	if toRevision > 0 {
		base += " AND revision_id <= ?"
		args = append(args, toRevision)
	}
	base += " ORDER BY changelog_id"

	rows, err := s.db.Query(base, args...)
	if err != nil {
		return nil, fmt.Errorf("QueryChangelog: %w", err)
	}
	defer rows.Close()

	var out []ChangelogRow
	for rows.Next() {
		var r ChangelogRow
		if err := rows.Scan(
			&r.ChangelogID, &r.RevisionID, &r.ContextID, &r.EntityType, &r.EntityKey,
			&r.EntityID, &r.ChangeType, &r.FieldChanges, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("QueryChangelog scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

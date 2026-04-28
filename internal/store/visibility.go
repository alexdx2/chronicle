package store

import "fmt"

// BuildVisibleRevisionsCTE returns a SQL CTE string and args slice that defines
// which revisions are visible for a given context's lineage.
//
// For a root context (no parent): all revisions belonging to that context,
// optionally capped by asOfRevision.
//
// For a branch context (has parent): parent revisions up to BaseRevisionID
// plus the branch's own revisions, optionally capped by asOfRevision.
//
// The returned CTE is of the form: "WITH visible_revisions AS (...) "
// Callers append their own SELECT after it.
func (s *Store) BuildVisibleRevisionsCTE(contextID int64, asOfRevision int64) (string, []any) {
	ctx, err := s.GetContext(contextID)
	if err != nil {
		// Fallback: just filter by context_id directly.
		cte := `WITH visible_revisions AS (SELECT revision_id FROM graph_revisions WHERE context_id = ?) `
		return cte, []any{contextID}
	}

	if ctx.BaseContextID == 0 {
		// Root context: all its revisions, optionally capped.
		if asOfRevision > 0 {
			cte := `WITH visible_revisions AS (SELECT revision_id FROM graph_revisions WHERE context_id = ? AND revision_id <= ?) `
			return cte, []any{contextID, asOfRevision}
		}
		cte := `WITH visible_revisions AS (SELECT revision_id FROM graph_revisions WHERE context_id = ?) `
		return cte, []any{contextID}
	}

	// Branch context: parent revisions up to base_revision_id + own revisions.
	if asOfRevision > 0 {
		cte := `WITH visible_revisions AS (` +
			`SELECT revision_id FROM graph_revisions WHERE context_id = ? AND revision_id <= ? ` +
			`UNION ALL ` +
			`SELECT revision_id FROM graph_revisions WHERE context_id = ? AND revision_id <= ?` +
			`) `
		return cte, []any{ctx.BaseContextID, ctx.BaseRevisionID, contextID, asOfRevision}
	}
	cte := `WITH visible_revisions AS (` +
		`SELECT revision_id FROM graph_revisions WHERE context_id = ? AND revision_id <= ? ` +
		`UNION ALL ` +
		`SELECT revision_id FROM graph_revisions WHERE context_id = ?` +
		`) `
	return cte, []any{ctx.BaseContextID, ctx.BaseRevisionID, contextID}
}

// VisibleRevisionIDs returns the list of revision IDs visible for the given
// context and optional asOfRevision ceiling.
func (s *Store) VisibleRevisionIDs(contextID int64, asOfRevision int64) ([]int64, error) {
	cte, args := s.BuildVisibleRevisionsCTE(contextID, asOfRevision)
	q := cte + `SELECT revision_id FROM visible_revisions ORDER BY revision_id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("VisibleRevisionIDs: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("VisibleRevisionIDs scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

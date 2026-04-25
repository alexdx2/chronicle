package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// EdgeRow represents a row in graph_edges.
type EdgeRow struct {
	EdgeID              int64   `json:"edge_id"`
	EdgeKey             string  `json:"edge_key"`
	FromNodeID          int64   `json:"from_node_id"`
	ToNodeID            int64   `json:"to_node_id"`
	EdgeType            string  `json:"edge_type"`
	DerivationKind      string  `json:"derivation_kind"`
	ContextKey          string  `json:"context_key,omitempty"`
	Active              bool    `json:"active"`
	FirstSeenRevisionID int64   `json:"first_seen_revision_id"`
	LastSeenRevisionID  int64   `json:"last_seen_revision_id"`
	Confidence          float64 `json:"confidence"`
	Metadata            string  `json:"metadata"`
}

// EdgeFilter holds optional filters for ListEdges.
type EdgeFilter struct {
	FromNodeID     int64
	ToNodeID       int64
	EdgeType       string
	DerivationKind string
	Active         *bool
}

// UpsertEdge inserts or updates an edge by edge_key.
func (s *Store) UpsertEdge(e EdgeRow) (int64, error) {
	const selQ = `SELECT edge_id FROM graph_edges WHERE edge_key = ?`
	var existingID int64
	err := s.db.QueryRow(selQ, e.EdgeKey).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("UpsertEdge lookup: %w", err)
	}

	activeInt := 0
	if e.Active {
		activeInt = 1
	}

	if errors.Is(err, sql.ErrNoRows) {
		const insQ = `
			INSERT INTO graph_edges
			  (edge_key, from_node_id, to_node_id, edge_type, derivation_kind, context_key,
			   active, first_seen_revision_id, last_seen_revision_id, confidence, metadata)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)
		`
		res, err := s.db.Exec(insQ,
			e.EdgeKey, e.FromNodeID, e.ToNodeID, e.EdgeType, e.DerivationKind,
			nullableStr(e.ContextKey), activeInt,
			e.FirstSeenRevisionID, e.LastSeenRevisionID, e.Confidence, e.Metadata,
		)
		if err != nil {
			return 0, fmt.Errorf("UpsertEdge insert: %w", err)
		}
		id, _ := res.LastInsertId()
		return id, nil
	}

	// Update mutable fields.
	const updQ = `
		UPDATE graph_edges
		SET derivation_kind=?, context_key=?, active=?, last_seen_revision_id=?,
		    confidence=?, metadata=?
		WHERE edge_key=?
	`
	_, err = s.db.Exec(updQ,
		e.DerivationKind, nullableStr(e.ContextKey), activeInt,
		e.LastSeenRevisionID, e.Confidence, e.Metadata,
		e.EdgeKey,
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertEdge update: %w", err)
	}
	return existingID, nil
}

// GetEdgeByKey returns the edge with the given edge_key.
func (s *Store) GetEdgeByKey(key string) (*EdgeRow, error) {
	const q = `
		SELECT edge_id, edge_key, from_node_id, to_node_id, edge_type, derivation_kind,
		       COALESCE(context_key,''), active,
		       first_seen_revision_id, last_seen_revision_id, confidence, metadata
		FROM graph_edges WHERE edge_key = ?
	`
	r := &EdgeRow{}
	var activeInt int
	err := s.db.QueryRow(q, key).Scan(
		&r.EdgeID, &r.EdgeKey, &r.FromNodeID, &r.ToNodeID, &r.EdgeType, &r.DerivationKind,
		&r.ContextKey, &activeInt,
		&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetEdgeByKey %q: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetEdgeByKey %q: %w", key, err)
	}
	r.Active = activeInt != 0
	return r, nil
}

// ListEdges returns edges matching the filter.
func (s *Store) ListEdges(f EdgeFilter) ([]EdgeRow, error) {
	base := `
		SELECT edge_id, edge_key, from_node_id, to_node_id, edge_type, derivation_kind,
		       COALESCE(context_key,''), active,
		       first_seen_revision_id, last_seen_revision_id, confidence, metadata
		FROM graph_edges
	`
	var conds []string
	var args []any
	if f.FromNodeID != 0 {
		conds = append(conds, "from_node_id = ?")
		args = append(args, f.FromNodeID)
	}
	if f.ToNodeID != 0 {
		conds = append(conds, "to_node_id = ?")
		args = append(args, f.ToNodeID)
	}
	if f.EdgeType != "" {
		conds = append(conds, "edge_type = ?")
		args = append(args, f.EdgeType)
	}
	if f.DerivationKind != "" {
		conds = append(conds, "derivation_kind = ?")
		args = append(args, f.DerivationKind)
	}
	if f.Active != nil {
		v := 0
		if *f.Active {
			v = 1
		}
		conds = append(conds, "active = ?")
		args = append(args, v)
	}
	if len(conds) > 0 {
		base += " WHERE " + strings.Join(conds, " AND ")
	}
	base += " ORDER BY edge_key"

	rows, err := s.db.Query(base, args...)
	if err != nil {
		return nil, fmt.Errorf("ListEdges: %w", err)
	}
	defer rows.Close()

	var out []EdgeRow
	for rows.Next() {
		var r EdgeRow
		var activeInt int
		if err := rows.Scan(
			&r.EdgeID, &r.EdgeKey, &r.FromNodeID, &r.ToNodeID, &r.EdgeType, &r.DerivationKind,
			&r.ContextKey, &activeInt,
			&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("ListEdges scan: %w", err)
		}
		r.Active = activeInt != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteEdge sets the edge active=0 (soft delete).
func (s *Store) DeleteEdge(key string) error {
	res, err := s.db.Exec(`UPDATE graph_edges SET active=0 WHERE edge_key=?`, key)
	if err != nil {
		return fmt.Errorf("DeleteEdge: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("DeleteEdge %q: %w", key, ErrNotFound)
	}
	return nil
}

// MarkStaleEdges marks active edges (from nodes in domainKey) with last_seen < revisionID as inactive.
func (s *Store) MarkStaleEdges(domainKey string, revisionID int64) (int64, error) {
	res, err := s.db.Exec(`
		UPDATE graph_edges
		SET active=0
		WHERE active=1
		  AND last_seen_revision_id < ?
		  AND from_node_id IN (
		    SELECT node_id FROM graph_nodes WHERE domain_key=?
		  )
	`, revisionID, domainKey)
	if err != nil {
		return 0, fmt.Errorf("MarkStaleEdges: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

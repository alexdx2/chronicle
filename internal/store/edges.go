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
	Freshness           float64 `json:"freshness"`
	TrustScore          float64 `json:"trust_score"`
	Metadata            string  `json:"metadata"`
	FromNodeKey         string  `json:"from_node_key,omitempty"`
	ToNodeKey           string  `json:"to_node_key,omitempty"`
	ValidFromRevisionID int64   `json:"valid_from_revision_id,omitempty"`
	ValidToRevisionID   int64   `json:"valid_to_revision_id,omitempty"`
	ContextID           int64   `json:"context_id,omitempty"`
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
// If ValidFromRevisionID > 0, versioned mode: close old row, insert new.
// If ValidFromRevisionID == 0, legacy mode: update in place.
func (s *Store) UpsertEdge(e EdgeRow) (int64, error) {
	const selQ = `SELECT edge_id FROM graph_edges WHERE edge_key = ? AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0) ORDER BY edge_id DESC LIMIT 1`
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
		// No current row — insert new.
		const insQ = `
			INSERT INTO graph_edges
			  (edge_key, from_node_id, to_node_id, edge_type, derivation_kind, context_key,
			   active, first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata,
			   from_node_key, to_node_key, valid_from_revision_id, context_id)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`
		res, err := s.db.Exec(insQ,
			e.EdgeKey, e.FromNodeID, e.ToNodeID, e.EdgeType, e.DerivationKind,
			nullableStr(e.ContextKey), activeInt,
			e.FirstSeenRevisionID, e.LastSeenRevisionID, e.Confidence, e.Freshness, e.TrustScore, e.Metadata,
			nullableStr(e.FromNodeKey), nullableStr(e.ToNodeKey), nullableInt64(e.ValidFromRevisionID), nullableInt64(e.ContextID),
		)
		if err != nil {
			return 0, fmt.Errorf("UpsertEdge insert: %w", err)
		}
		id, _ := res.LastInsertId()
		return id, nil
	}

	// Existing row found.
	if e.ValidFromRevisionID > 0 {
		// Versioned mode: close old row, insert new.
		_, err = s.db.Exec(`UPDATE graph_edges SET valid_to_revision_id = ? WHERE edge_id = ?`,
			e.ValidFromRevisionID, existingID)
		if err != nil {
			return 0, fmt.Errorf("UpsertEdge close old: %w", err)
		}

		const insQ = `
			INSERT INTO graph_edges
			  (edge_key, from_node_id, to_node_id, edge_type, derivation_kind, context_key,
			   active, first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata,
			   from_node_key, to_node_key, valid_from_revision_id, context_id)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`
		res, err := s.db.Exec(insQ,
			e.EdgeKey, e.FromNodeID, e.ToNodeID, e.EdgeType, e.DerivationKind,
			nullableStr(e.ContextKey), activeInt,
			e.FirstSeenRevisionID, e.LastSeenRevisionID, e.Confidence, e.Freshness, e.TrustScore, e.Metadata,
			nullableStr(e.FromNodeKey), nullableStr(e.ToNodeKey), nullableInt64(e.ValidFromRevisionID), nullableInt64(e.ContextID),
		)
		if err != nil {
			return 0, fmt.Errorf("UpsertEdge versioned insert: %w", err)
		}
		id, _ := res.LastInsertId()
		return id, nil
	}

	// Legacy mode: update in place.
	const updQ = `
		UPDATE graph_edges
		SET derivation_kind=?, context_key=?, active=?, last_seen_revision_id=?,
		    confidence=?, freshness=?, trust_score=?, metadata=?,
		    from_node_key=COALESCE(?,from_node_key), to_node_key=COALESCE(?,to_node_key)
		WHERE edge_id=?
	`
	_, err = s.db.Exec(updQ,
		e.DerivationKind, nullableStr(e.ContextKey), activeInt,
		e.LastSeenRevisionID, e.Confidence, e.Freshness, e.TrustScore, e.Metadata,
		nullableStr(e.FromNodeKey), nullableStr(e.ToNodeKey),
		existingID,
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertEdge update: %w", err)
	}
	return existingID, nil
}

// GetEdgeByKey returns the current (non-closed) edge with the given edge_key.
func (s *Store) GetEdgeByKey(key string) (*EdgeRow, error) {
	const q = `
		SELECT edge_id, edge_key, from_node_id, to_node_id, edge_type, derivation_kind,
		       COALESCE(context_key,''), active,
		       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata,
		       COALESCE(from_node_key,''), COALESCE(to_node_key,''),
		       COALESCE(valid_from_revision_id,0), COALESCE(valid_to_revision_id,0), COALESCE(context_id,0)
		FROM graph_edges WHERE edge_key = ? AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		ORDER BY edge_id DESC LIMIT 1
	`
	r := &EdgeRow{}
	var activeInt int
	err := s.db.QueryRow(q, key).Scan(
		&r.EdgeID, &r.EdgeKey, &r.FromNodeID, &r.ToNodeID, &r.EdgeType, &r.DerivationKind,
		&r.ContextKey, &activeInt,
		&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Freshness, &r.TrustScore, &r.Metadata,
		&r.FromNodeKey, &r.ToNodeKey,
		&r.ValidFromRevisionID, &r.ValidToRevisionID, &r.ContextID,
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

// ListEdges returns current (non-closed) edges matching the filter.
func (s *Store) ListEdges(f EdgeFilter) ([]EdgeRow, error) {
	base := `
		SELECT edge_id, edge_key, from_node_id, to_node_id, edge_type, derivation_kind,
		       COALESCE(context_key,''), active,
		       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata,
		       COALESCE(from_node_key,''), COALESCE(to_node_key,''),
		       COALESCE(valid_from_revision_id,0), COALESCE(valid_to_revision_id,0), COALESCE(context_id,0)
		FROM graph_edges
	`
	// Always filter to current (non-closed) rows.
	conds := []string{"(valid_to_revision_id IS NULL OR valid_to_revision_id = 0)"}
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
	base += " WHERE " + strings.Join(conds, " AND ")
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
			&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Freshness, &r.TrustScore, &r.Metadata,
			&r.FromNodeKey, &r.ToNodeKey,
			&r.ValidFromRevisionID, &r.ValidToRevisionID, &r.ContextID,
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

// UpdateEdgeTrust updates computed trust fields on an edge.
func (s *Store) UpdateEdgeTrust(edgeID int64, confidence, freshness, trustScore float64, status string) error {
	activeInt := 1
	if status == "removed" || status == "contradicted" {
		activeInt = 0
	}
	_, err := s.db.Exec(`
		UPDATE graph_edges SET confidence=?, freshness=?, trust_score=?, active=? WHERE edge_id=?
	`, confidence, freshness, trustScore, activeInt, edgeID)
	if err != nil {
		return fmt.Errorf("UpdateEdgeTrust: %w", err)
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

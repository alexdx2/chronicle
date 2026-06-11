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
	// The journal (and cross-db diffing) identifies edge endpoints by node key,
	// not row id — ids differ across replays. Callers that resolved nodes by id
	// may leave the keys empty; backfill them so both the persisted row and the
	// emitted journal event are replayable.
	if e.FromNodeKey == "" && e.FromNodeID > 0 {
		n, err := s.GetNodeByID(e.FromNodeID)
		if err != nil {
			return 0, fmt.Errorf("UpsertEdge resolve from_node_key: %w", err)
		}
		e.FromNodeKey = n.NodeKey
	}
	if e.ToNodeKey == "" && e.ToNodeID > 0 {
		n, err := s.GetNodeByID(e.ToNodeID)
		if err != nil {
			return 0, fmt.Errorf("UpsertEdge resolve to_node_key: %w", err)
		}
		e.ToNodeKey = n.NodeKey
	}

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
		if err := s.emitEdgeUpsert(e); err != nil {
			return 0, err
		}
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
		if err := s.emitEdgeUpsert(e); err != nil {
			return 0, err
		}
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
	if err := s.emitEdgeUpsert(e); err != nil {
		return 0, err
	}
	return existingID, nil
}

// emitEdgeUpsert records an edge_upsert journal event for e.
func (s *Store) emitEdgeUpsert(e EdgeRow) error {
	return s.appendEvent(journalEvent{
		DomainKey:  DomainFromNodeKey(e.FromNodeKey),
		RevisionID: e.LastSeenRevisionID,
		Kind:       EvEdgeUpsert,
		Key:        e.EdgeKey,
		Fields:     edgeEventFields(e),
	})
}

// edgeEventFields builds the journal event payload for an edge upsert.
// Derived values (confidence/freshness/trust_score) are excluded by design.
func edgeEventFields(e EdgeRow) map[string]any {
	f := map[string]any{
		"from": e.FromNodeKey, "to": e.ToNodeKey, "edge_type": e.EdgeType,
		"derivation_kind": e.DerivationKind, "active": e.Active,
	}
	if e.ContextKey != "" {
		f["context_key"] = e.ContextKey
	}
	if e.Metadata != "" && e.Metadata != "{}" {
		f["metadata"] = e.Metadata
	}
	return f
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
	if err := s.appendEvent(journalEvent{
		DomainKey: domainFromEdgeKey(key),
		Kind:      EvEdgeStatus,
		Key:       key,
		Fields:    map[string]any{"active": false},
	}); err != nil {
		return err
	}
	return nil
}

// ErrEdgeKeyConflict is returned by RepointEdge when the recomputed edge_key
// already belongs to another current edge (edge_key is UNIQUE). Callers decide:
// hygiene merges deactivate the now-duplicate edge; other passes skip.
var ErrEdgeKeyConflict = errors.New("edge key already exists")

// EdgeRepoint describes which identity fields of an edge change. Zero fields
// keep their current value; the new edge_key is derived from the result.
type EdgeRepoint struct {
	NewFromNodeID  int64
	NewFromNodeKey string
	NewToNodeID    int64
	NewToNodeKey   string
	NewEdgeType    string
}

// RepointEdge rewrites an edge's endpoints and/or type (post-resolve merge
// passes: external-system → service repoint, module-edge re-type, duplicate
// node merge) and recomputes its edge_key. Emits one edge_rekey event keyed by
// the OLD edge_key; replay re-applies by old key, resolving node ids by key.
// Returns ErrEdgeKeyConflict (without mutating) if the new key is taken.
func (s *Store) RepointEdge(edgeID int64, r EdgeRepoint) error {
	cur, err := s.getEdgeByID(edgeID)
	if err != nil {
		return fmt.Errorf("RepointEdge: %w", err)
	}

	fromKey, toKey, edgeType := cur.FromNodeKey, cur.ToNodeKey, cur.EdgeType
	fields := map[string]any{}
	sets := []string{"edge_key = ?"}
	var args []any
	if r.NewFromNodeKey != "" {
		fromKey = r.NewFromNodeKey
		sets = append(sets, "from_node_key = ?", "from_node_id = ?")
		args = append(args, r.NewFromNodeKey, r.NewFromNodeID)
		fields["from"] = r.NewFromNodeKey
	}
	if r.NewToNodeKey != "" {
		toKey = r.NewToNodeKey
		sets = append(sets, "to_node_key = ?", "to_node_id = ?")
		args = append(args, r.NewToNodeKey, r.NewToNodeID)
		fields["to"] = r.NewToNodeKey
	}
	if r.NewEdgeType != "" {
		edgeType = r.NewEdgeType
		sets = append(sets, "edge_type = ?")
		args = append(args, r.NewEdgeType)
		fields["edge_type"] = r.NewEdgeType
	}
	newKey := fromKey + "->" + toKey + ":" + edgeType
	if newKey == cur.EdgeKey {
		return nil // nothing changes
	}
	var taken int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE edge_key = ?`, newKey).Scan(&taken); err != nil {
		return fmt.Errorf("RepointEdge key check: %w", err)
	}
	if taken > 0 {
		return fmt.Errorf("RepointEdge %q -> %q: %w", cur.EdgeKey, newKey, ErrEdgeKeyConflict)
	}

	args = append([]any{newKey}, args...)
	args = append(args, edgeID)
	if _, err := s.db.Exec(
		`UPDATE graph_edges SET `+strings.Join(sets, ", ")+` WHERE edge_id = ?`, args...); err != nil {
		return fmt.Errorf("RepointEdge update: %w", err)
	}
	fields["new_edge_key"] = newKey
	return s.appendEvent(journalEvent{
		DomainKey: domainFromEdgeKey(cur.EdgeKey),
		Kind:      EvEdgeRekey,
		Key:       cur.EdgeKey,
		Fields:    fields,
	})
}

// DeactivateEdge sets active=0 on an edge by id and journals an edge_status
// event keyed by edge_key, with a reason for forensics. Transition-only:
// an already-inactive edge emits nothing.
//
// Status/evidence coherence rule (journal P2 spec §2.1): edge active is
// recomputed from evidence after replay, so a forced deactivation must
// invalidate the edge's valid evidence in the same write — otherwise the
// replayed db recomputes the edge back to active and diverges from live.
func (s *Store) DeactivateEdge(edgeID int64, reason string) error {
	cur, err := s.getEdgeByID(edgeID)
	if err != nil {
		return fmt.Errorf("DeactivateEdge: %w", err)
	}
	res, err := s.db.Exec(`UPDATE graph_edges SET active = 0 WHERE edge_id = ? AND active = 1`, edgeID)
	if err != nil {
		return fmt.Errorf("DeactivateEdge update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already inactive — no transition, no event
	}
	if err := s.appendEvent(journalEvent{
		DomainKey: domainFromEdgeKey(cur.EdgeKey),
		Kind:      EvEdgeStatus,
		Key:       cur.EdgeKey,
		Fields:    map[string]any{"active": false, "reason": reason},
	}); err != nil {
		return err
	}
	// The structural decision (self-loop, duplicate, conflict, cycle, merge)
	// rejects the edge's supporting claims — invalidate them so replay-side
	// trust recompute converges on active=0.
	return s.markEvidenceStatusForOwner("edge", edgeID, cur.EdgeKey, "invalidated")
}

// UpdateEdgeMetadata replaces an edge's metadata by id and journals an
// edge_meta event keyed by edge_key.
func (s *Store) UpdateEdgeMetadata(edgeID int64, metadata string) error {
	cur, err := s.getEdgeByID(edgeID)
	if err != nil {
		return fmt.Errorf("UpdateEdgeMetadata: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE graph_edges SET metadata = ? WHERE edge_id = ?`, metadata, edgeID); err != nil {
		return fmt.Errorf("UpdateEdgeMetadata update: %w", err)
	}
	return s.appendEvent(journalEvent{
		DomainKey: domainFromEdgeKey(cur.EdgeKey),
		Kind:      EvEdgeMeta,
		Key:       cur.EdgeKey,
		Fields:    map[string]any{"metadata": metadata},
	})
}

// getEdgeByID returns the identity columns of an edge by row id.
func (s *Store) getEdgeByID(edgeID int64) (*EdgeRow, error) {
	r := &EdgeRow{EdgeID: edgeID}
	err := s.db.QueryRow(`
		SELECT edge_key, COALESCE(from_node_key,''), COALESCE(to_node_key,''), edge_type, from_node_id, to_node_id
		FROM graph_edges WHERE edge_id = ?`, edgeID,
	).Scan(&r.EdgeKey, &r.FromNodeKey, &r.ToNodeKey, &r.EdgeType, &r.FromNodeID, &r.ToNodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("edge %d: %w", edgeID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("edge %d: %w", edgeID, err)
	}
	return r, nil
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

// UpdateEdgeVerificationStatus updates the verification_status field on an edge by key.
func (s *Store) UpdateEdgeVerificationStatus(edgeKey, verificationStatus string) error {
	_, err := s.db.Exec(`UPDATE graph_edges SET verification_status=? WHERE edge_key=?`, verificationStatus, edgeKey)
	return err
}

// GetEdgesWithNoValidEvidence returns active edges that have no valid positive evidence.
// These are candidates for needs_review status. Checks edges that have at least one
// evidence record (to exclude edges that were never evidenced) but no currently valid positive evidence.
func (s *Store) GetEdgesWithNoValidEvidence(domainKey string) ([]EdgeRow, error) {
	q := `
		SELECT e.edge_id, e.edge_key, e.from_node_id, e.to_node_id, e.edge_type,
		       e.derivation_kind, COALESCE(e.context_key,''), e.active,
		       COALESCE(e.first_seen_revision_id,0), COALESCE(e.last_seen_revision_id,0),
		       e.confidence, e.freshness, e.trust_score, e.metadata
		FROM graph_edges e
		LEFT JOIN graph_nodes n ON e.from_node_id = n.node_id
		WHERE (n.domain_key = ? OR ? = '')
		  AND e.active = 1
		  AND NOT EXISTS (
			SELECT 1 FROM graph_evidence ev
			WHERE ev.edge_id = e.edge_id
			  AND ev.evidence_polarity = 'positive'
			  AND ev.evidence_status IN ('valid','revalidated')
		  )
		  AND EXISTS (
			SELECT 1 FROM graph_evidence ev2
			WHERE ev2.edge_id = e.edge_id
		  )
	`
	rows, err := s.db.Query(q, domainKey, domainKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EdgeRow
	for rows.Next() {
		var r EdgeRow
		if err := rows.Scan(&r.EdgeID, &r.EdgeKey, &r.FromNodeID, &r.ToNodeID, &r.EdgeType,
			&r.DerivationKind, &r.ContextKey, &r.Active,
			&r.FirstSeenRevisionID, &r.LastSeenRevisionID,
			&r.Confidence, &r.Freshness, &r.TrustScore, &r.Metadata); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetEdgesBetweenNodes returns current (non-closed) edges where both from_node_id and to_node_id
// are within the provided set of node IDs.
func (s *Store) GetEdgesBetweenNodes(nodeIDs []int64) ([]EdgeRow, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(nodeIDs))
	for i := range nodeIDs {
		placeholders[i] = "?"
	}
	inClause := strings.Join(placeholders, ",")

	q := fmt.Sprintf(`
		SELECT edge_id, edge_key, from_node_id, to_node_id, edge_type, derivation_kind,
		       COALESCE(context_key,''), active,
		       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata,
		       COALESCE(from_node_key,''), COALESCE(to_node_key,''),
		       COALESCE(valid_from_revision_id,0), COALESCE(valid_to_revision_id,0), COALESCE(context_id,0)
		FROM graph_edges
		WHERE (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		  AND from_node_id IN (%s)
		  AND to_node_id IN (%s)
		ORDER BY edge_key
	`, inClause, inClause)

	args := make([]any, 0, len(nodeIDs)*2)
	for _, id := range nodeIDs {
		args = append(args, id)
	}
	for _, id := range nodeIDs {
		args = append(args, id)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("GetEdgesBetweenNodes: %w", err)
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
			return nil, fmt.Errorf("GetEdgesBetweenNodes scan: %w", err)
		}
		r.Active = activeInt != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkStaleEdges marks active edges (from nodes in domainKey) with last_seen < revisionID
// as inactive and journals one edge_status event per actual transition. Repeated calls
// are no-ops: only rows still active=1 transition, so no duplicate events.
// Only CURRENT versions (valid_to unset) are considered: a closed historical
// row keeps active=1 but the re-seen edge's current row has last_seen = this
// revision — touching the closed row would emit a bogus stale event for an
// edge that is still alive.
func (s *Store) MarkStaleEdges(domainKey string, revisionID int64) (int64, error) {
	rows, err := s.db.Query(`
		SELECT edge_id, edge_key FROM graph_edges
		WHERE active=1
		  AND last_seen_revision_id < ?
		  AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		  AND from_node_id IN (
		    SELECT node_id FROM graph_nodes WHERE domain_key=?
		  )
		ORDER BY edge_key`, revisionID, domainKey)
	if err != nil {
		return 0, fmt.Errorf("MarkStaleEdges select: %w", err)
	}
	type staleRow struct {
		id  int64
		key string
	}
	var subjects []staleRow
	for rows.Next() {
		var r staleRow
		if err := rows.Scan(&r.id, &r.key); err != nil {
			rows.Close()
			return 0, fmt.Errorf("MarkStaleEdges scan: %w", err)
		}
		subjects = append(subjects, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("MarkStaleEdges rows: %w", err)
	}

	for _, sub := range subjects {
		if _, err := s.db.Exec(`
			UPDATE graph_edges
			SET active=0
			WHERE edge_id=? AND active=1`, sub.id); err != nil {
			return 0, fmt.Errorf("MarkStaleEdges update: %w", err)
		}
		if err := s.appendEvent(journalEvent{
			DomainKey: domainKey, RevisionID: revisionID,
			Kind: EvEdgeStatus, Key: sub.key,
			Fields: map[string]any{"active": false, "status": "stale"},
		}); err != nil {
			return 0, err
		}
		// Stale the edge's evidence too (journaled) — same convergence rule as
		// MarkStaleNodes: post-replay trust recompute derives status from
		// evidence, and valid evidence would flip the edge back to active.
		if err := s.markEvidenceStatusForOwner("edge", sub.id, sub.key, "stale"); err != nil {
			return 0, err
		}
	}
	return int64(len(subjects)), nil
}

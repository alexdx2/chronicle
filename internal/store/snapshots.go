package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// SnapshotRow represents a row in graph_snapshots.
type SnapshotRow struct {
	SnapshotID        int64  `json:"snapshot_id"`
	RevisionID        int64  `json:"revision_id"`
	DomainKey         string `json:"domain_key"`
	Kind              string `json:"snapshot_kind"`
	CreatedAt         string `json:"created_at"`
	NodeCount         int    `json:"node_count"`
	EdgeCount         int    `json:"edge_count"`
	ChangedFileCount  int    `json:"changed_file_count"`
	ChangedNodeCount  int    `json:"changed_node_count"`
	ChangedEdgeCount  int    `json:"changed_edge_count"`
	ImpactedNodeCount int    `json:"impacted_node_count"`
	Summary           string `json:"summary"`
}

// CreateSnapshot inserts a new snapshot row and returns the snapshot_id.
// If Summary is empty it defaults to "{}".
func (s *Store) CreateSnapshot(snap SnapshotRow) (int64, error) {
	if snap.Summary == "" {
		snap.Summary = "{}"
	}
	const q = `
		INSERT INTO graph_snapshots
		  (revision_id, domain_key, snapshot_kind,
		   node_count, edge_count,
		   changed_file_count, changed_node_count, changed_edge_count, impacted_node_count,
		   summary)
		VALUES (?,?,?,?,?,?,?,?,?,?)
	`
	res, err := s.db.Exec(q,
		snap.RevisionID, snap.DomainKey, snap.Kind,
		snap.NodeCount, snap.EdgeCount,
		snap.ChangedFileCount, snap.ChangedNodeCount, snap.ChangedEdgeCount, snap.ImpactedNodeCount,
		snap.Summary,
	)
	if err != nil {
		return 0, fmt.Errorf("CreateSnapshot: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// ListSnapshots returns snapshots for a domain, ordered newest first.
func (s *Store) ListSnapshots(domainKey string) ([]SnapshotRow, error) {
	const q = `
		SELECT snapshot_id, revision_id, domain_key, snapshot_kind, created_at,
		       node_count, edge_count,
		       changed_file_count, changed_node_count, changed_edge_count, impacted_node_count,
		       summary
		FROM graph_snapshots
		WHERE domain_key = ?
		ORDER BY created_at DESC
	`
	rows, err := s.db.Query(q, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ListSnapshots: %w", err)
	}
	defer rows.Close()

	var out []SnapshotRow
	for rows.Next() {
		var r SnapshotRow
		if err := rows.Scan(
			&r.SnapshotID, &r.RevisionID, &r.DomainKey, &r.Kind, &r.CreatedAt,
			&r.NodeCount, &r.EdgeCount,
			&r.ChangedFileCount, &r.ChangedNodeCount, &r.ChangedEdgeCount, &r.ImpactedNodeCount,
			&r.Summary,
		); err != nil {
			return nil, fmt.Errorf("ListSnapshots scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetLatestSnapshot returns the most recent snapshot for a domain.
func (s *Store) GetLatestSnapshot(domainKey string) (*SnapshotRow, error) {
	const q = `
		SELECT snapshot_id, revision_id, domain_key, snapshot_kind, created_at,
		       node_count, edge_count,
		       changed_file_count, changed_node_count, changed_edge_count, impacted_node_count,
		       summary
		FROM graph_snapshots
		WHERE domain_key = ?
		ORDER BY created_at DESC, snapshot_id DESC
		LIMIT 1
	`
	r := &SnapshotRow{}
	err := s.db.QueryRow(q, domainKey).Scan(
		&r.SnapshotID, &r.RevisionID, &r.DomainKey, &r.Kind, &r.CreatedAt,
		&r.NodeCount, &r.EdgeCount,
		&r.ChangedFileCount, &r.ChangedNodeCount, &r.ChangedEdgeCount, &r.ImpactedNodeCount,
		&r.Summary,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetLatestSnapshot %q: %w", domainKey, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetLatestSnapshot %q: %w", domainKey, err)
	}
	return r, nil
}

// GetSnapshot returns a single snapshot by id (used internally / for tests).
func (s *Store) GetSnapshot(id int64) (*SnapshotRow, error) {
	const q = `
		SELECT snapshot_id, revision_id, domain_key, snapshot_kind, created_at,
		       node_count, edge_count,
		       changed_file_count, changed_node_count, changed_edge_count, impacted_node_count,
		       summary
		FROM graph_snapshots WHERE snapshot_id = ?
	`
	r := &SnapshotRow{}
	err := s.db.QueryRow(q, id).Scan(
		&r.SnapshotID, &r.RevisionID, &r.DomainKey, &r.Kind, &r.CreatedAt,
		&r.NodeCount, &r.EdgeCount,
		&r.ChangedFileCount, &r.ChangedNodeCount, &r.ChangedEdgeCount, &r.ImpactedNodeCount,
		&r.Summary,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetSnapshot %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetSnapshot %d: %w", id, err)
	}
	return r, nil
}

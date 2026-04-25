package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// NodeRow represents a row in graph_nodes.
type NodeRow struct {
	NodeID              int64   `json:"node_id"`
	NodeKey             string  `json:"node_key"`
	Layer               string  `json:"layer"`
	NodeType            string  `json:"node_type"`
	DomainKey           string  `json:"domain_key"`
	Name                string  `json:"name"`
	QualifiedName       string  `json:"qualified_name,omitempty"`
	RepoName            string  `json:"repo_name,omitempty"`
	FilePath            string  `json:"file_path,omitempty"`
	Lang                string  `json:"lang,omitempty"`
	OwnerKey            string  `json:"owner_key,omitempty"`
	Environment         string  `json:"environment,omitempty"`
	Visibility          string  `json:"visibility,omitempty"`
	Status              string  `json:"status"`
	FirstSeenRevisionID int64   `json:"first_seen_revision_id"`
	LastSeenRevisionID  int64   `json:"last_seen_revision_id"`
	Confidence          float64 `json:"confidence"`
	Metadata            string  `json:"metadata"`
}

// NodeFilter holds optional filters for ListNodes.
type NodeFilter struct {
	Layer    string
	NodeType string
	Domain   string
	RepoName string
	Status   string
}

// UpsertNode inserts or updates a node by node_key.
// If the key already exists, immutable fields (layer, node_type, domain_key) must match.
// Returns the node_id.
func (s *Store) UpsertNode(n NodeRow) (int64, error) {
	// Check if node already exists.
	const selQ = `SELECT node_id, layer, node_type, domain_key FROM graph_nodes WHERE node_key = ?`
	row := s.db.QueryRow(selQ, n.NodeKey)
	var existingID int64
	var existingLayer, existingType, existingDomain string
	err := row.Scan(&existingID, &existingLayer, &existingType, &existingDomain)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("UpsertNode lookup: %w", err)
	}

	if errors.Is(err, sql.ErrNoRows) {
		// Insert.
		const insQ = `
			INSERT INTO graph_nodes
			  (node_key, layer, node_type, domain_key, name, qualified_name, repo_name,
			   file_path, lang, owner_key, environment, visibility, status,
			   first_seen_revision_id, last_seen_revision_id, confidence, metadata)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`
		res, err := s.db.Exec(insQ,
			n.NodeKey, n.Layer, n.NodeType, n.DomainKey, n.Name,
			nullableStr(n.QualifiedName), nullableStr(n.RepoName), nullableStr(n.FilePath),
			nullableStr(n.Lang), nullableStr(n.OwnerKey), nullableStr(n.Environment),
			nullableStr(n.Visibility), n.Status,
			n.FirstSeenRevisionID, n.LastSeenRevisionID, n.Confidence, n.Metadata,
		)
		if err != nil {
			return 0, fmt.Errorf("UpsertNode insert: %w", err)
		}
		id, _ := res.LastInsertId()
		return id, nil
	}

	// Existing: check immutable fields.
	if existingLayer != n.Layer || existingType != n.NodeType || existingDomain != n.DomainKey {
		return 0, fmt.Errorf("UpsertNode conflict: node_key %q immutable fields mismatch (layer=%s/%s, type=%s/%s, domain=%s/%s)",
			n.NodeKey, existingLayer, n.Layer, existingType, n.NodeType, existingDomain, n.DomainKey)
	}

	// Update mutable fields.
	const updQ = `
		UPDATE graph_nodes
		SET name=?, qualified_name=?, repo_name=?, file_path=?, lang=?, owner_key=?,
		    environment=?, visibility=?, status=?, last_seen_revision_id=?,
		    confidence=?, metadata=?
		WHERE node_key=?
	`
	_, err = s.db.Exec(updQ,
		n.Name, nullableStr(n.QualifiedName), nullableStr(n.RepoName), nullableStr(n.FilePath),
		nullableStr(n.Lang), nullableStr(n.OwnerKey), nullableStr(n.Environment),
		nullableStr(n.Visibility), n.Status, n.LastSeenRevisionID, n.Confidence, n.Metadata,
		n.NodeKey,
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertNode update: %w", err)
	}
	return existingID, nil
}

// GetNodeByKey returns the node with the given node_key.
func (s *Store) GetNodeByKey(key string) (*NodeRow, error) {
	const q = `
		SELECT node_id, node_key, layer, node_type, domain_key, name,
		       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		       COALESCE(visibility,''), status,
		       first_seen_revision_id, last_seen_revision_id, confidence, metadata
		FROM graph_nodes WHERE node_key = ?
	`
	r := &NodeRow{}
	err := s.db.QueryRow(q, key).Scan(
		&r.NodeID, &r.NodeKey, &r.Layer, &r.NodeType, &r.DomainKey, &r.Name,
		&r.QualifiedName, &r.RepoName, &r.FilePath, &r.Lang, &r.OwnerKey,
		&r.Environment, &r.Visibility, &r.Status,
		&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetNodeByKey %q: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetNodeByKey %q: %w", key, err)
	}
	return r, nil
}

// GetNodeByID returns the node with the given node_id.
func (s *Store) GetNodeByID(id int64) (*NodeRow, error) {
	const q = `
		SELECT node_id, node_key, layer, node_type, domain_key, name,
		       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		       COALESCE(visibility,''), status,
		       first_seen_revision_id, last_seen_revision_id, confidence, metadata
		FROM graph_nodes WHERE node_id = ?
	`
	r := &NodeRow{}
	err := s.db.QueryRow(q, id).Scan(
		&r.NodeID, &r.NodeKey, &r.Layer, &r.NodeType, &r.DomainKey, &r.Name,
		&r.QualifiedName, &r.RepoName, &r.FilePath, &r.Lang, &r.OwnerKey,
		&r.Environment, &r.Visibility, &r.Status,
		&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetNodeByID %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetNodeByID %d: %w", id, err)
	}
	return r, nil
}

// GetNodeIDByKey returns the node_id for the given node_key.
func (s *Store) GetNodeIDByKey(key string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT node_id FROM graph_nodes WHERE node_key = ?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("GetNodeIDByKey %q: %w", key, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("GetNodeIDByKey %q: %w", key, err)
	}
	return id, nil
}

// ListNodes returns nodes matching the filter, ordered by node_key.
func (s *Store) ListNodes(f NodeFilter) ([]NodeRow, error) {
	base := `
		SELECT node_id, node_key, layer, node_type, domain_key, name,
		       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		       COALESCE(visibility,''), status,
		       first_seen_revision_id, last_seen_revision_id, confidence, metadata
		FROM graph_nodes
	`
	var conds []string
	var args []any
	if f.Layer != "" {
		conds = append(conds, "layer = ?")
		args = append(args, f.Layer)
	}
	if f.NodeType != "" {
		conds = append(conds, "node_type = ?")
		args = append(args, f.NodeType)
	}
	if f.Domain != "" {
		conds = append(conds, "domain_key = ?")
		args = append(args, f.Domain)
	}
	if f.RepoName != "" {
		conds = append(conds, "repo_name = ?")
		args = append(args, f.RepoName)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if len(conds) > 0 {
		base += " WHERE " + strings.Join(conds, " AND ")
	}
	base += " ORDER BY node_key"

	rows, err := s.db.Query(base, args...)
	if err != nil {
		return nil, fmt.Errorf("ListNodes: %w", err)
	}
	defer rows.Close()

	var out []NodeRow
	for rows.Next() {
		var r NodeRow
		if err := rows.Scan(
			&r.NodeID, &r.NodeKey, &r.Layer, &r.NodeType, &r.DomainKey, &r.Name,
			&r.QualifiedName, &r.RepoName, &r.FilePath, &r.Lang, &r.OwnerKey,
			&r.Environment, &r.Visibility, &r.Status,
			&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("ListNodes scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteNode sets the status of the node to 'deleted'.
func (s *Store) DeleteNode(key string) error {
	res, err := s.db.Exec(`UPDATE graph_nodes SET status='deleted' WHERE node_key=?`, key)
	if err != nil {
		return fmt.Errorf("DeleteNode: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("DeleteNode %q: %w", key, ErrNotFound)
	}
	return nil
}

// MarkStaleNodes marks active nodes with last_seen_revision_id < revisionID as stale.
func (s *Store) MarkStaleNodes(domainKey string, revisionID int64) (int64, error) {
	res, err := s.db.Exec(`
		UPDATE graph_nodes
		SET status='stale'
		WHERE domain_key=? AND status='active' AND last_seen_revision_id < ?
	`, domainKey, revisionID)
	if err != nil {
		return 0, fmt.Errorf("MarkStaleNodes: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// nullableStr returns nil for empty strings so they're stored as NULL.
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

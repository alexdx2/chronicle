package store

import (
	"database/sql"
	"errors"
	"fmt"
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
	Freshness           float64 `json:"freshness"`
	TrustScore          float64 `json:"trust_score"`
	Metadata            string  `json:"metadata"`
	SymbolName          string  `json:"symbol_name,omitempty"`
	SupportKind         string  `json:"support_kind,omitempty"`
	ValidFromRevisionID int64   `json:"valid_from_revision_id,omitempty"`
	ValidToRevisionID   int64   `json:"valid_to_revision_id,omitempty"`
	ContextID           int64   `json:"context_id,omitempty"`
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
// When ValidFromRevisionID > 0 (versioned mode): closes old version and inserts new.
// When ValidFromRevisionID == 0 (legacy mode): updates in place (backward compatible).
// Returns the node_id.
func (s *Store) UpsertNode(n NodeRow) (int64, error) {
	// Look up the current version of this node (one where valid_to is NULL or 0).
	const selQ = `SELECT node_id, layer, node_type, domain_key FROM graph_nodes
		WHERE node_key = ? AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		ORDER BY node_id DESC LIMIT 1`
	row := s.db.QueryRow(selQ, n.NodeKey)
	var existingID int64
	var existingLayer, existingType, existingDomain string
	err := row.Scan(&existingID, &existingLayer, &existingType, &existingDomain)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("UpsertNode lookup: %w", err)
	}

	if errors.Is(err, sql.ErrNoRows) {
		// No existing current version — insert new.
		id, err := s.insertNodeVersion(n)
		if err != nil {
			return 0, err
		}
		if err := s.emitNodeUpsert(n); err != nil {
			return 0, err
		}
		return id, nil
	}

	// Existing: check immutable fields.
	if existingLayer != n.Layer || existingType != n.NodeType || existingDomain != n.DomainKey {
		return 0, fmt.Errorf("UpsertNode conflict: node_key %q immutable fields mismatch (layer=%s/%s, type=%s/%s, domain=%s/%s)",
			n.NodeKey, existingLayer, n.Layer, existingType, n.NodeType, existingDomain, n.DomainKey)
	}

	if n.ValidFromRevisionID > 0 {
		// Versioned mode: close old version, insert new.
		_, err = s.db.Exec(`UPDATE graph_nodes SET valid_to_revision_id = ? WHERE node_id = ?`,
			n.ValidFromRevisionID, existingID)
		if err != nil {
			return 0, fmt.Errorf("UpsertNode close old version: %w", err)
		}
		id, err := s.insertNodeVersion(n)
		if err != nil {
			return 0, err
		}
		if err := s.emitNodeUpsert(n); err != nil {
			return 0, err
		}
		return id, nil
	}

	// Legacy mode: update in place.
	const updQ = `
		UPDATE graph_nodes
		SET name=?, qualified_name=?, repo_name=?, file_path=?, lang=?, owner_key=?,
		    environment=?, visibility=?, status=?, last_seen_revision_id=?,
		    confidence=?, freshness=?, trust_score=?, metadata=?,
		    symbol_name=?, support_kind=?
		WHERE node_id=?
	`
	_, err = s.db.Exec(updQ,
		n.Name, nullableStr(n.QualifiedName), nullableStr(n.RepoName), nullableStr(n.FilePath),
		nullableStr(n.Lang), nullableStr(n.OwnerKey), nullableStr(n.Environment),
		nullableStr(n.Visibility), n.Status, n.LastSeenRevisionID, n.Confidence, n.Freshness, n.TrustScore, n.Metadata,
		nullableStr(n.SymbolName), nullableStr(n.SupportKind),
		existingID,
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertNode update: %w", err)
	}
	if err := s.emitNodeUpsert(n); err != nil {
		return 0, err
	}
	return existingID, nil
}

// emitNodeUpsert records a node_upsert journal event for n.
func (s *Store) emitNodeUpsert(n NodeRow) error {
	return s.appendEvent(journalEvent{
		DomainKey:  n.DomainKey,
		RevisionID: n.LastSeenRevisionID,
		Kind:       EvNodeUpsert,
		Key:        n.NodeKey,
		Fields:     nodeEventFields(n),
	})
}

// nodeEventFields builds the journal event payload for a node upsert.
// Derived values (confidence/freshness/trust_score) are excluded by design.
func nodeEventFields(n NodeRow) map[string]any {
	f := map[string]any{
		"name": n.Name, "layer": n.Layer, "node_type": n.NodeType,
		"domain_key": n.DomainKey, "status": n.Status,
	}
	if n.QualifiedName != "" {
		f["qualified_name"] = n.QualifiedName
	}
	if n.RepoName != "" {
		f["repo_name"] = n.RepoName
	}
	if n.FilePath != "" {
		f["file_path"] = n.FilePath
	}
	if n.Lang != "" {
		f["lang"] = n.Lang
	}
	if n.OwnerKey != "" {
		f["owner_key"] = n.OwnerKey
	}
	if n.Environment != "" {
		f["environment"] = n.Environment
	}
	if n.Visibility != "" {
		f["visibility"] = n.Visibility
	}
	if n.SymbolName != "" {
		f["symbol_name"] = n.SymbolName
	}
	if n.SupportKind != "" {
		f["support_kind"] = n.SupportKind
	}
	if n.Metadata != "" && n.Metadata != "{}" {
		f["metadata"] = n.Metadata
	}
	return f
}

// insertNodeVersion inserts a new node row including versioning columns.
func (s *Store) insertNodeVersion(n NodeRow) (int64, error) {
	const insQ = `
		INSERT INTO graph_nodes
		  (node_key, layer, node_type, domain_key, name, qualified_name, repo_name,
		   file_path, lang, owner_key, environment, visibility, status,
		   first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata,
		   symbol_name, support_kind,
		   valid_from_revision_id, valid_to_revision_id, context_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`
	res, err := s.db.Exec(insQ,
		n.NodeKey, n.Layer, n.NodeType, n.DomainKey, n.Name,
		nullableStr(n.QualifiedName), nullableStr(n.RepoName), nullableStr(n.FilePath),
		nullableStr(n.Lang), nullableStr(n.OwnerKey), nullableStr(n.Environment),
		nullableStr(n.Visibility), n.Status,
		n.FirstSeenRevisionID, n.LastSeenRevisionID, n.Confidence, n.Freshness, n.TrustScore, n.Metadata,
		nullableStr(n.SymbolName), nullableStr(n.SupportKind),
		nullableInt64(n.ValidFromRevisionID), nullableInt64(n.ValidToRevisionID), nullableInt64(n.ContextID),
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertNode insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// GetNodeByKey returns the node with the given node_key.
func (s *Store) GetNodeByKey(key string) (*NodeRow, error) {
	const q = `
		SELECT node_id, node_key, layer, node_type, domain_key, name,
		       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		       COALESCE(visibility,''), status,
		       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata
		FROM graph_nodes WHERE node_key = ? AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		ORDER BY node_id DESC LIMIT 1
	`
	r := &NodeRow{}
	err := s.db.QueryRow(q, key).Scan(
		&r.NodeID, &r.NodeKey, &r.Layer, &r.NodeType, &r.DomainKey, &r.Name,
		&r.QualifiedName, &r.RepoName, &r.FilePath, &r.Lang, &r.OwnerKey,
		&r.Environment, &r.Visibility, &r.Status,
		&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Freshness, &r.TrustScore, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetNodeByKey %q: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetNodeByKey %q: %w", key, err)
	}
	return r, nil
}

// GetNodesByKeys returns nodes matching the given keys (batch lookup).
// Returns found nodes and a list of keys that were not found.
func (s *Store) GetNodesByKeys(keys []string) ([]NodeRow, []string, error) {
	if len(keys) == 0 {
		return nil, nil, nil
	}

	// Build placeholder string for IN clause.
	placeholders := ""
	args := make([]any, len(keys))
	for i, k := range keys {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = k
	}

	q := `
		SELECT node_id, node_key, layer, node_type, domain_key, name,
		       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		       COALESCE(visibility,''), status,
		       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata
		FROM graph_nodes
		WHERE node_key IN (` + placeholders + `) AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		  AND status = 'active'
		ORDER BY node_key
	`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("GetNodesByKeys: %w", err)
	}
	defer rows.Close()

	foundMap := make(map[string]bool, len(keys))
	var out []NodeRow
	for rows.Next() {
		var r NodeRow
		if err := rows.Scan(
			&r.NodeID, &r.NodeKey, &r.Layer, &r.NodeType, &r.DomainKey, &r.Name,
			&r.QualifiedName, &r.RepoName, &r.FilePath, &r.Lang, &r.OwnerKey,
			&r.Environment, &r.Visibility, &r.Status,
			&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Freshness, &r.TrustScore, &r.Metadata,
		); err != nil {
			return nil, nil, fmt.Errorf("GetNodesByKeys scan: %w", err)
		}
		foundMap[r.NodeKey] = true
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("GetNodesByKeys rows: %w", err)
	}

	var missing []string
	for _, k := range keys {
		if !foundMap[k] {
			missing = append(missing, k)
		}
	}

	return out, missing, nil
}

// GetNodeByID returns the node with the given node_id.
func (s *Store) GetNodeByID(id int64) (*NodeRow, error) {
	const q = `
		SELECT node_id, node_key, layer, node_type, domain_key, name,
		       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		       COALESCE(visibility,''), status,
		       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata
		FROM graph_nodes WHERE node_id = ?
	`
	r := &NodeRow{}
	err := s.db.QueryRow(q, id).Scan(
		&r.NodeID, &r.NodeKey, &r.Layer, &r.NodeType, &r.DomainKey, &r.Name,
		&r.QualifiedName, &r.RepoName, &r.FilePath, &r.Lang, &r.OwnerKey,
		&r.Environment, &r.Visibility, &r.Status,
		&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Freshness, &r.TrustScore, &r.Metadata,
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
		       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata
		FROM graph_nodes WHERE (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
	`
	var args []any
	if f.Layer != "" {
		base += " AND layer = ?"
		args = append(args, f.Layer)
	}
	if f.NodeType != "" {
		base += " AND node_type = ?"
		args = append(args, f.NodeType)
	}
	if f.Domain != "" {
		base += " AND domain_key = ?"
		args = append(args, f.Domain)
	}
	if f.RepoName != "" {
		base += " AND repo_name = ?"
		args = append(args, f.RepoName)
	}
	if f.Status != "" {
		base += " AND status = ?"
		args = append(args, f.Status)
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
			&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Freshness, &r.TrustScore, &r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("ListNodes scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchNodesByName finds active nodes whose name matches (case-insensitive).
// Returns up to 10 matches, preferring exact matches first.
func (s *Store) SearchNodesByName(name string) ([]NodeRow, error) {
	const q = `
		SELECT node_id, node_key, layer, node_type, domain_key, name,
		       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		       COALESCE(visibility,''), status,
		       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata
		FROM graph_nodes
		WHERE (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		  AND status = 'active'
		  AND name LIKE ?
		ORDER BY
		  CASE WHEN LOWER(name) = LOWER(?) THEN 0 ELSE 1 END,
		  node_key
		LIMIT 10
	`
	rows, err := s.db.Query(q, "%"+name+"%", name)
	if err != nil {
		return nil, fmt.Errorf("SearchNodesByName: %w", err)
	}
	defer rows.Close()

	var out []NodeRow
	for rows.Next() {
		var r NodeRow
		if err := rows.Scan(
			&r.NodeID, &r.NodeKey, &r.Layer, &r.NodeType, &r.DomainKey, &r.Name,
			&r.QualifiedName, &r.RepoName, &r.FilePath, &r.Lang, &r.OwnerKey,
			&r.Environment, &r.Visibility, &r.Status,
			&r.FirstSeenRevisionID, &r.LastSeenRevisionID, &r.Confidence, &r.Freshness, &r.TrustScore, &r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("SearchNodesByName scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteNode sets the status of the node to 'deleted' (tombstone) and
// supersedes its valid evidence — see SetNodeStatus for why deleted facts
// must not keep valid existence evidence.
func (s *Store) DeleteNode(key string) error {
	nodeID, err := s.GetNodeIDByKey(key)
	if err != nil {
		return fmt.Errorf("DeleteNode %q: %w", key, ErrNotFound)
	}
	res, err := s.db.Exec(`UPDATE graph_nodes SET status='deleted' WHERE node_key=?`, key)
	if err != nil {
		return fmt.Errorf("DeleteNode: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("DeleteNode %q: %w", key, ErrNotFound)
	}
	if err := s.appendEvent(journalEvent{
		DomainKey: DomainFromNodeKey(key),
		Kind:      EvNodeStatus,
		Key:       key,
		Fields:    map[string]any{"status": "deleted"},
	}); err != nil {
		return err
	}
	return s.markEvidenceStatusForOwner("node", nodeID, key, "superseded")
}

// RekeyNode re-keys a node to a new node_key (stem-merge: an import fact minted
// the node under a class-name key before the file scan produced the path key)
// and rewrites the denormalized keys on every edge touching it. One node_rekey
// event keyed by the OLD key carries the whole rewrite — replay re-applies the
// same UPDATEs after resolving the node by its old key.
func (s *Store) RekeyNode(nodeID int64, oldKey, newKey, filePath string, revisionID int64) error {
	if _, err := s.db.Exec(
		`UPDATE graph_nodes SET node_key = ?, file_path = ?, last_seen_revision_id = ? WHERE node_id = ?`,
		newKey, nullableStr(filePath), revisionID, nodeID); err != nil {
		return fmt.Errorf("RekeyNode node update: %w", err)
	}
	if err := s.rekeyNodeEdges(nodeID, newKey); err != nil {
		return fmt.Errorf("RekeyNode: %w", err)
	}
	return s.appendEvent(journalEvent{
		DomainKey:  DomainFromNodeKey(oldKey),
		RevisionID: revisionID,
		Kind:       EvNodeRekey,
		Key:        oldKey,
		Fields:     map[string]any{"new_key": newKey, "file_path": filePath},
	})
}

// rekeyNodeEdges rewrites the denormalized from/to node keys and the derived
// edge_key on every edge touching nodeID. Shared by RekeyNode and its replay.
func (s *Store) rekeyNodeEdges(nodeID int64, newKey string) error {
	if _, err := s.db.Exec(
		`UPDATE graph_edges SET from_node_key = ? WHERE from_node_id = ?`, newKey, nodeID); err != nil {
		return fmt.Errorf("edge from_node_key update: %w", err)
	}
	if _, err := s.db.Exec(
		`UPDATE graph_edges SET to_node_key = ? WHERE to_node_id = ?`, newKey, nodeID); err != nil {
		return fmt.Errorf("edge to_node_key update: %w", err)
	}
	if _, err := s.db.Exec(
		`UPDATE graph_edges SET edge_key = from_node_key || '->' || to_node_key || ':' || edge_type
		 WHERE from_node_id = ? OR to_node_id = ?`, nodeID, nodeID); err != nil {
		return fmt.Errorf("edge_key recompute: %w", err)
	}
	return nil
}

// SetNodeStatus sets a node's status by id and journals the transition as a
// node_status event keyed by node_key. No-op (and no event) if the status
// already matches — transition-only, like MarkStaleNodes.
func (s *Store) SetNodeStatus(nodeID int64, status string) error {
	var nodeKey, oldStatus string
	if err := s.db.QueryRow(
		`SELECT node_key, status FROM graph_nodes WHERE node_id = ?`, nodeID,
	).Scan(&nodeKey, &oldStatus); err != nil {
		return fmt.Errorf("SetNodeStatus lookup %d: %w", nodeID, err)
	}
	if oldStatus == status {
		return nil
	}
	if _, err := s.db.Exec(`UPDATE graph_nodes SET status = ? WHERE node_id = ?`, status, nodeID); err != nil {
		return fmt.Errorf("SetNodeStatus update: %w", err)
	}
	if err := s.appendEvent(journalEvent{
		DomainKey: DomainFromNodeKey(nodeKey),
		Kind:      EvNodeStatus,
		Key:       nodeKey,
		Fields:    map[string]any{"status": status},
	}); err != nil {
		return err
	}
	if status == "deleted" {
		// A deleted node must not keep valid existence evidence: trust recompute
		// derives status from evidence and would resurrect it (on the journal
		// replay side first — live and replay then diverge). The merge passes
		// delete placeholders whose representation moved to another node, so
		// their evidence is superseded, not refuted.
		return s.markEvidenceStatusForOwner("node", nodeID, nodeKey, "superseded")
	}
	return nil
}

// UpdateNodeMetadata replaces the metadata JSON on a node row. Used by the
// complexity pass to fold derived metrics into the node without a versioned
// re-upsert (mirrors UpdateNodeTrust's single-column update).
func (s *Store) UpdateNodeMetadata(nodeID int64, metadata string) error {
	if metadata == "" {
		metadata = "{}"
	}
	if _, err := s.db.Exec(`UPDATE graph_nodes SET metadata=? WHERE node_id=?`, metadata, nodeID); err != nil {
		return fmt.Errorf("UpdateNodeMetadata: %w", err)
	}
	return nil
}

// UpdateNodeTrust updates computed trust fields on a node.
func (s *Store) UpdateNodeTrust(nodeID int64, confidence, freshness, trustScore float64, status string) error {
	_, err := s.db.Exec(`
		UPDATE graph_nodes SET confidence=?, freshness=?, trust_score=?, status=? WHERE node_id=?
	`, confidence, freshness, trustScore, status, nodeID)
	if err != nil {
		return fmt.Errorf("UpdateNodeTrust: %w", err)
	}
	return nil
}

// MarkStaleNodes marks active nodes not seen in this revision as stale and
// journals one node_status event per actual transition. Repeated calls are
// no-ops: only rows still 'active' transition, so no duplicate events.
// Only CURRENT versions (valid_to unset) are considered: in versioned mode a
// closed historical row keeps status='active' but a re-seen node's current row
// has last_seen = this revision — marking the closed row would emit a bogus
// stale event for a node that is still very much alive.
func (s *Store) MarkStaleNodes(domainKey string, revisionID int64) (int64, error) {
	rows, err := s.db.Query(`
		SELECT node_id, node_key FROM graph_nodes
		WHERE domain_key=? AND status='active' AND last_seen_revision_id < ?
		  AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		ORDER BY node_key`, domainKey, revisionID)
	if err != nil {
		return 0, fmt.Errorf("MarkStaleNodes select: %w", err)
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
			return 0, fmt.Errorf("MarkStaleNodes scan: %w", err)
		}
		subjects = append(subjects, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("MarkStaleNodes rows: %w", err)
	}

	for _, sub := range subjects {
		if _, err := s.db.Exec(`
			UPDATE graph_nodes
			SET status='stale'
			WHERE node_id=? AND status='active'`, sub.id); err != nil {
			return 0, fmt.Errorf("MarkStaleNodes update: %w", err)
		}
		if err := s.appendEvent(journalEvent{
			DomainKey: domainKey, RevisionID: revisionID,
			Kind: EvNodeStatus, Key: sub.key,
			Fields: map[string]any{"status": "stale"},
		}); err != nil {
			return 0, err
		}
		// Stale the node's evidence too (journaled). Status is recomputed from
		// evidence after journal replay — a stale node with valid evidence
		// recomputes back to active and replay diverges from live.
		if err := s.markEvidenceStatusForOwner("node", sub.id, sub.key, "stale"); err != nil {
			return 0, err
		}
	}
	return int64(len(subjects)), nil
}

// nullableStr returns nil for empty strings so they're stored as NULL.
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

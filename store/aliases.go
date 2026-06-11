package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// AliasRow represents a row in node_aliases.
type AliasRow struct {
	AliasID         int64   `json:"alias_id"`
	NodeID          int64   `json:"node_id"`
	Alias           string  `json:"alias"`
	NormalizedAlias string  `json:"normalized_alias"`
	AliasKind       string  `json:"alias_kind"`
	Confidence      float64 `json:"confidence"`
}

// AddAlias inserts a new alias for a node. The normalized_alias is computed automatically.
func (s *Store) AddAlias(a AliasRow) (int64, error) {
	normalized := strings.ToLower(strings.TrimSpace(a.Alias))
	if normalized == "" {
		return 0, fmt.Errorf("AddAlias: alias is empty")
	}
	if a.AliasKind == "" {
		return 0, fmt.Errorf("AddAlias: alias_kind is empty")
	}
	confidence := a.Confidence
	if confidence <= 0 {
		confidence = 0.8
	}

	// Resolve the owner before inserting: an alias on a nonexistent node is an
	// integrity bug, and a journal event with an empty owner_key hard-fails
	// replay later — fail loud at emission time instead.
	var ownerKey string
	if err := s.db.QueryRow(`SELECT node_key FROM graph_nodes WHERE node_id = ?`, a.NodeID).Scan(&ownerKey); err != nil || ownerKey == "" {
		return 0, fmt.Errorf("AddAlias: owner node %d not found (err=%v)", a.NodeID, err)
	}

	res, err := s.db.Exec(`
		INSERT INTO node_aliases (node_id, alias, normalized_alias, alias_kind, confidence)
		VALUES (?, ?, ?, ?, ?)
	`, a.NodeID, a.Alias, normalized, a.AliasKind, confidence)
	if err != nil {
		return 0, fmt.Errorf("AddAlias: %w", err)
	}
	id, _ := res.LastInsertId()

	if err := s.appendEvent(journalEvent{
		DomainKey: DomainFromNodeKey(ownerKey),
		Kind:      EvAliasAdd,
		Key:       ownerKey + "#alias:" + normalized,
		OwnerKey:  ownerKey,
		Fields: map[string]any{
			"alias": a.Alias, "alias_kind": a.AliasKind,
			"confidence": confidence, "normalized_alias": normalized,
		},
	}); err != nil {
		return 0, err
	}
	return id, nil
}

// ListAliasesByNode returns all aliases for a given node.
func (s *Store) ListAliasesByNode(nodeID int64) ([]AliasRow, error) {
	rows, err := s.db.Query(`
		SELECT alias_id, node_id, alias, normalized_alias, alias_kind, confidence
		FROM node_aliases WHERE node_id = ? ORDER BY alias_kind, normalized_alias
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("ListAliasesByNode: %w", err)
	}
	defer rows.Close()
	return scanAliases(rows)
}

// ListAliasesByNormalized returns aliases matching a normalized value and kind.
func (s *Store) ListAliasesByNormalized(normalized, kind string) ([]AliasRow, error) {
	rows, err := s.db.Query(`
		SELECT alias_id, node_id, alias, normalized_alias, alias_kind, confidence
		FROM node_aliases WHERE normalized_alias = ? AND alias_kind = ?
	`, normalized, kind)
	if err != nil {
		return nil, fmt.Errorf("ListAliasesByNormalized: %w", err)
	}
	defer rows.Close()
	return scanAliases(rows)
}

// ListAliasesByNormalizedAnyKind returns aliases matching a normalized value across all kinds.
func (s *Store) ListAliasesByNormalizedAnyKind(normalized string) ([]AliasRow, error) {
	rows, err := s.db.Query(`
		SELECT alias_id, node_id, alias, normalized_alias, alias_kind, confidence
		FROM node_aliases WHERE normalized_alias = ?
	`, normalized)
	if err != nil {
		return nil, fmt.Errorf("ListAliasesByNormalizedAnyKind: %w", err)
	}
	defer rows.Close()
	return scanAliases(rows)
}

// FindCodeNodesByAlias searches code-layer nodes by normalized alias.
// pathPrefix optionally scopes to a directory (empty = all).
func (s *Store) FindCodeNodesByAlias(domain, alias, pathPrefix string) ([]NodeRow, error) {
	normalized := strings.ToLower(alias)
	// Also try without dots/dashes for fuzzy match
	normalizedClean := strings.NewReplacer(".", "", "-", "", "_", "").Replace(normalized)

	aliases, err := s.ListAliasesByNormalizedAnyKind(normalized)
	if err != nil {
		return nil, err
	}
	// Also search cleaned version
	if normalizedClean != normalized {
		aliases2, _ := s.ListAliasesByNormalizedAnyKind(normalizedClean)
		aliases = append(aliases, aliases2...)
	}

	seen := map[int64]bool{}
	var results []NodeRow
	for _, a := range aliases {
		if seen[a.NodeID] {
			continue
		}
		seen[a.NodeID] = true
		node, err := s.GetNodeByID(a.NodeID)
		if err != nil || node == nil {
			continue
		}
		if node.Layer != "code" {
			continue
		}
		if domain != "" && node.DomainKey != domain {
			continue
		}
		if pathPrefix != "" && !strings.HasPrefix(node.FilePath, pathPrefix) {
			continue
		}
		results = append(results, *node)
	}
	return results, nil
}

// RemoveAlias deletes an alias by ID.
func (s *Store) RemoveAlias(aliasID int64) error {
	var normalized, ownerKey string
	lookupErr := s.db.QueryRow(`
		SELECT a.normalized_alias, COALESCE(n.node_key,'')
		FROM node_aliases a
		LEFT JOIN graph_nodes n ON a.node_id = n.node_id
		WHERE a.alias_id = ?
	`, aliasID).Scan(&normalized, &ownerKey)
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return fmt.Errorf("RemoveAlias %d: %w", aliasID, ErrNotFound)
	}
	if lookupErr != nil {
		return fmt.Errorf("RemoveAlias lookup: %w", lookupErr)
	}
	if ownerKey == "" {
		// Alias row pointing at a missing node is an integrity bug; an event
		// with an empty owner_key hard-fails replay — fail loud here.
		return fmt.Errorf("RemoveAlias: alias %d has no owner node", aliasID)
	}

	res, err := s.db.Exec(`DELETE FROM node_aliases WHERE alias_id = ?`, aliasID)
	if err != nil {
		return fmt.Errorf("RemoveAlias: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("RemoveAlias %d: %w", aliasID, ErrNotFound)
	}
	return s.appendEvent(journalEvent{
		DomainKey: DomainFromNodeKey(ownerKey),
		Kind:      EvAliasDel,
		Key:       ownerKey + "#alias:" + normalized,
		OwnerKey:  ownerKey,
		Fields:    map[string]any{"normalized_alias": normalized},
	})
}

func scanAliases(rows interface {
	Next() bool
	Scan(...any) error
}) ([]AliasRow, error) {
	var out []AliasRow
	for rows.Next() {
		var r AliasRow
		if err := rows.Scan(&r.AliasID, &r.NodeID, &r.Alias, &r.NormalizedAlias, &r.AliasKind, &r.Confidence); err != nil {
			return nil, fmt.Errorf("scanAliases: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

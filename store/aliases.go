package store

import (
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

	res, err := s.db.Exec(`
		INSERT INTO node_aliases (node_id, alias, normalized_alias, alias_kind, confidence)
		VALUES (?, ?, ?, ?, ?)
	`, a.NodeID, a.Alias, normalized, a.AliasKind, confidence)
	if err != nil {
		return 0, fmt.Errorf("AddAlias: %w", err)
	}
	id, _ := res.LastInsertId()
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

// RemoveAlias deletes an alias by ID.
func (s *Store) RemoveAlias(aliasID int64) error {
	res, err := s.db.Exec(`DELETE FROM node_aliases WHERE alias_id = ?`, aliasID)
	if err != nil {
		return fmt.Errorf("RemoveAlias: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("RemoveAlias %d: %w", aliasID, ErrNotFound)
	}
	return nil
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

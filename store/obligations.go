package store

import "fmt"

// ObligationRow represents a scan obligation.
type ObligationRow struct {
	ObligationID   int64  `json:"obligation_id"`
	RevisionID     int64  `json:"revision_id"`
	DomainKey      string `json:"domain_key"`
	ObligationType string `json:"obligation_type"` // verify_file, review_edge, review_node
	TargetKey      string `json:"target_key"`      // file path or edge_key or node_key
	Reason         string `json:"reason,omitempty"`
	Status         string `json:"status"`       // open, satisfied, skipped, deferred
	DeferReason    string `json:"defer_reason,omitempty"`
	CreatedAt      string `json:"created_at"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
}

// CreateObligation inserts a new scan obligation.
func (s *Store) CreateObligation(revisionID int64, domainKey, obligationType, targetKey, reason string) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO scan_obligations (revision_id, domain_key, obligation_type, target_key, reason)
		VALUES (?, ?, ?, ?, ?)
	`, revisionID, domainKey, obligationType, targetKey, reason)
	if err != nil {
		return 0, fmt.Errorf("CreateObligation: %w", err)
	}
	return res.LastInsertId()
}

// SatisfyObligation marks an obligation as satisfied and clears claim fields.
func (s *Store) SatisfyObligation(revisionID int64, obligationType, targetKey string) error {
	_, err := s.db.Exec(`
		UPDATE scan_obligations
		SET status = 'satisfied',
		    claimed_at = NULL, claim_expires_at = NULL,
		    resolved_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE revision_id = ? AND obligation_type = ? AND target_key = ? AND status = 'open'
	`, revisionID, obligationType, targetKey)
	return err
}

// ClaimObligations atomically claims up to `limit` unclaimed (or expired) open obligations.
// Returns the target keys of claimed obligations. Concurrent callers get different rows
// because the UPDATE changes claimed_at, excluding those rows from subsequent subselects.
// Self-healing: expired claims are reclaimed in the same query.
func (s *Store) ClaimObligations(revisionID int64, obligationType string, limit int) ([]string, error) {
	rows, err := s.db.Query(`
		UPDATE scan_obligations
		SET claimed_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
		    claim_expires_at = strftime('%Y-%m-%dT%H:%M:%SZ','now','+15 minutes')
		WHERE obligation_id IN (
			SELECT obligation_id FROM scan_obligations
			WHERE revision_id = ? AND obligation_type = ? AND status = 'open'
			  AND (claimed_at IS NULL OR claim_expires_at < strftime('%Y-%m-%dT%H:%M:%SZ','now'))
			ORDER BY obligation_id
			LIMIT ?
		)
		RETURNING target_key
	`, revisionID, obligationType, limit)
	if err != nil {
		return nil, fmt.Errorf("ClaimObligations: %w", err)
	}
	defer rows.Close()

	var targets []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		targets = append(targets, key)
	}
	return targets, rows.Err()
}

// CountPendingObligations returns the count of open obligations (claimed or unclaimed).
func (s *Store) CountPendingObligations(revisionID int64, obligationType string) (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM scan_obligations
		WHERE revision_id = ? AND obligation_type = ? AND status = 'open'
	`, revisionID, obligationType).Scan(&count)
	return count, err
}

// DeferObligation marks an obligation as deferred with a reason.
func (s *Store) DeferObligation(revisionID int64, obligationType, targetKey, deferReason string) error {
	_, err := s.db.Exec(`
		UPDATE scan_obligations
		SET status = 'deferred', defer_reason = ?, resolved_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE revision_id = ? AND obligation_type = ? AND target_key = ? AND status = 'open'
	`, deferReason, revisionID, obligationType, targetKey)
	return err
}

// SkipObligation marks an obligation as skipped.
func (s *Store) SkipObligation(revisionID int64, obligationType, targetKey, reason string) error {
	_, err := s.db.Exec(`
		UPDATE scan_obligations
		SET status = 'skipped', defer_reason = ?, resolved_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE revision_id = ? AND obligation_type = ? AND target_key = ? AND status = 'open'
	`, reason, revisionID, obligationType, targetKey)
	return err
}

// ListOpenObligations returns all open obligations for a revision.
func (s *Store) ListOpenObligations(revisionID int64) ([]ObligationRow, error) {
	rows, err := s.db.Query(`
		SELECT obligation_id, revision_id, domain_key, obligation_type, target_key,
		       COALESCE(reason,''), status, COALESCE(defer_reason,''),
		       created_at, COALESCE(resolved_at,'')
		FROM scan_obligations
		WHERE revision_id = ? AND status = 'open'
		ORDER BY obligation_id
	`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("ListOpenObligations: %w", err)
	}
	defer rows.Close()

	var out []ObligationRow
	for rows.Next() {
		var r ObligationRow
		if err := rows.Scan(&r.ObligationID, &r.RevisionID, &r.DomainKey,
			&r.ObligationType, &r.TargetKey, &r.Reason,
			&r.Status, &r.DeferReason, &r.CreatedAt, &r.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAllObligations returns all obligations for a revision (any status).
func (s *Store) ListAllObligations(revisionID int64) ([]ObligationRow, error) {
	rows, err := s.db.Query(`
		SELECT obligation_id, revision_id, domain_key, obligation_type, target_key,
		       COALESCE(reason,''), status, COALESCE(defer_reason,''),
		       created_at, COALESCE(resolved_at,'')
		FROM scan_obligations
		WHERE revision_id = ?
		ORDER BY obligation_id
	`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("ListAllObligations: %w", err)
	}
	defer rows.Close()

	var out []ObligationRow
	for rows.Next() {
		var r ObligationRow
		if err := rows.Scan(&r.ObligationID, &r.RevisionID, &r.DomainKey,
			&r.ObligationType, &r.TargetKey, &r.Reason,
			&r.Status, &r.DeferReason, &r.CreatedAt, &r.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

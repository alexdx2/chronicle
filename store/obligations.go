package store

import (
	"fmt"
	"time"
)

// ClaimedObligation is returned by ClaimObligations with both target and domain.
type ClaimedObligation struct {
	ObligationID int64  `json:"obligation_id"`
	TargetKey    string `json:"target_key"`
	DomainKey    string `json:"domain_key"`
	VoteGroup    string `json:"vote_group"`
	VoteIndex    int    `json:"vote_index"`
}

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

// CreateObligationWithVote inserts a new scan obligation with vote group and index.
func (s *Store) CreateObligationWithVote(revisionID int64, domainKey, obligationType, targetKey, reason, voteGroup string, voteIndex int) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO scan_obligations (revision_id, domain_key, obligation_type, target_key, reason, vote_group, vote_index)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, revisionID, domainKey, obligationType, targetKey, reason, voteGroup, voteIndex)
	if err != nil {
		return 0, fmt.Errorf("CreateObligationWithVote: %w", err)
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

// SatisfyObligationByID marks a single obligation as satisfied by its ID.
// Idempotent: if already satisfied (or not found), returns nil.
func (s *Store) SatisfyObligationByID(obligationID int64) error {
	_, err := s.db.Exec(`
		UPDATE scan_obligations
		SET status = 'satisfied',
		    claimed_at = NULL, claim_expires_at = NULL,
		    resolved_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE obligation_id = ? AND status = 'open'
	`, obligationID)
	if err != nil {
		return fmt.Errorf("SatisfyObligationByID: %w", err)
	}
	// If 0 rows affected = already satisfied = idempotent, not an error
	return nil
}

// ClaimObligations atomically claims up to `limit` unclaimed (or expired) open obligations.
// Returns the target keys and domain keys of claimed obligations. Concurrent callers get
// different rows because the UPDATE changes claimed_at, excluding those rows from subsequent subselects.
// Self-healing: expired claims are reclaimed in the same query.
func (s *Store) ClaimObligations(revisionID int64, obligationType string, limit int) ([]ClaimedObligation, error) {
	// First: mark obligations with too many attempts (5+) as failed
	s.db.Exec(`
		UPDATE scan_obligations SET status = 'skipped', defer_reason = 'too many attempts'
		WHERE revision_id = ? AND obligation_type = ? AND status = 'open' AND attempt_count >= 5
	`, revisionID, obligationType)

	ttl := ClaimTTLMinutes()
	rows, err := s.db.Query(fmt.Sprintf(`
		UPDATE scan_obligations
		SET claimed_at = strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ','now'),
		    claim_expires_at = strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ','now','+%d minutes'),
		    attempt_count = attempt_count + 1
`, ttl)+`
		WHERE obligation_id IN (
			WITH candidates AS (
				SELECT obligation_id, vote_group,
				       ROW_NUMBER() OVER (
				           PARTITION BY CASE WHEN vote_group = '' THEN CAST(obligation_id AS TEXT) ELSE vote_group END
				           ORDER BY obligation_id
				       ) AS rn
				FROM scan_obligations
				WHERE revision_id = ? AND obligation_type = ? AND status = 'open'
				  AND (claimed_at IS NULL OR claim_expires_at < strftime('%Y-%m-%dT%H:%M:%SZ','now'))
			)
			SELECT obligation_id FROM candidates WHERE rn = 1
			ORDER BY obligation_id
			LIMIT ?
		)
		RETURNING obligation_id, target_key, domain_key, vote_group, vote_index
	`, revisionID, obligationType, limit)
	if err != nil {
		return nil, fmt.Errorf("ClaimObligations: %w", err)
	}
	defer rows.Close()

	var claimed []ClaimedObligation
	for rows.Next() {
		var c ClaimedObligation
		if err := rows.Scan(&c.ObligationID, &c.TargetKey, &c.DomainKey, &c.VoteGroup, &c.VoteIndex); err != nil {
			return nil, err
		}
		claimed = append(claimed, c)
	}
	return claimed, rows.Err()
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

// PoolStatus holds aggregated counts for scan obligation pool monitoring.
type PoolStatus struct {
	RemainingTotal       int `json:"remaining_total"`
	ClaimableNow         int `json:"claimable_now"`
	InProgress           int `json:"in_progress"`
	Completed            int `json:"completed"`
	Failed               int `json:"failed"`
	Expired              int `json:"expired"`
	OldestInProgressSec  int `json:"oldest_in_progress_sec"`
	ClaimTTLMinutes      int `json:"claim_ttl_minutes"`
}

// MarkObligationFailed marks an open obligation as skipped and clears its claim.
func (s *Store) MarkObligationFailed(obligationID int64, reason string) error {
	_, err := s.db.Exec(`
		UPDATE scan_obligations
		SET status = 'skipped',
		    defer_reason = ?,
		    claimed_at = NULL,
		    claim_expires_at = NULL,
		    resolved_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE obligation_id = ? AND status = 'open'
	`, reason, obligationID)
	if err != nil {
		return fmt.Errorf("MarkObligationFailed: %w", err)
	}
	return nil
}

// RequeueObligation clears an active claim so the obligation becomes claimable again.
func (s *Store) RequeueObligation(obligationID int64) error {
	_, err := s.db.Exec(`
		UPDATE scan_obligations
		SET claimed_at = NULL,
		    claim_expires_at = NULL
		WHERE obligation_id = ? AND status = 'open'
	`, obligationID)
	if err != nil {
		return fmt.Errorf("RequeueObligation: %w", err)
	}
	return nil
}

// ObligationPoolStatus returns aggregated counts of obligations by state.
// Read-only — does not claim or modify any obligations.
func (s *Store) ObligationPoolStatus(revisionID int64, obligationType string) (*PoolStatus, error) {
	row := s.db.QueryRow(`
		SELECT
			SUM(CASE WHEN status = 'open' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'open' AND (claimed_at IS NULL OR claim_expires_at < strftime('%Y-%m-%dT%H:%M:%SZ','now')) THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'open' AND claimed_at IS NOT NULL AND claim_expires_at >= strftime('%Y-%m-%dT%H:%M:%SZ','now') THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'satisfied' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'skipped' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'open' AND claimed_at IS NOT NULL AND claim_expires_at < strftime('%Y-%m-%dT%H:%M:%SZ','now') THEN 1 ELSE 0 END)
		FROM scan_obligations
		WHERE revision_id = ? AND obligation_type = ?
	`, revisionID, obligationType)

	var ps PoolStatus
	var rt, cn, ip, co, fa, ex *int
	err := row.Scan(&rt, &cn, &ip, &co, &fa, &ex)
	if err != nil {
		return nil, fmt.Errorf("ObligationPoolStatus: %w", err)
	}
	if rt != nil {
		ps.RemainingTotal = *rt
	}
	if cn != nil {
		ps.ClaimableNow = *cn
	}
	if ip != nil {
		ps.InProgress = *ip
	}
	if co != nil {
		ps.Completed = *co
	}
	if fa != nil {
		ps.Failed = *fa
	}
	if ex != nil {
		ps.Expired = *ex
	}
	ps.ClaimTTLMinutes = ClaimTTLMinutes()
	ps.OldestInProgressSec = s.oldestInProgressSeconds(revisionID, obligationType)
	return &ps, nil
}

func (s *Store) oldestInProgressSeconds(revisionID int64, obligationType string) int {
	var claimedAt string
	err := s.db.QueryRow(`
		SELECT MIN(claimed_at)
		FROM scan_obligations
		WHERE revision_id = ? AND obligation_type = ? AND status = 'open'
		  AND claimed_at IS NOT NULL
		  AND claim_expires_at >= strftime('%Y-%m-%dT%H:%M:%SZ','now')
	`, revisionID, obligationType).Scan(&claimedAt)
	if err != nil || claimedAt == "" {
		return 0
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", claimedAt)
	if err != nil {
		return 0
	}
	sec := int(time.Since(t).Seconds())
	if sec < 0 {
		return 0
	}
	return sec
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

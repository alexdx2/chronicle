package store

import (
	"database/sql"
	"fmt"
)

// ScanRunRow represents a stateful scan workflow run.
type ScanRunRow struct {
	RunID          int64  `json:"run_id"`
	RevisionID     int64  `json:"revision_id"`
	DomainKey      string `json:"domain_key"`
	Phase          string `json:"phase"`
	Status         string `json:"status"`
	TotalFiles     int    `json:"total_files"`
	ExtractedFiles int    `json:"extracted_files"`
	Resolved       int    `json:"resolved"`
	VotesNeeded    int    `json:"votes_needed"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

// CreateScanRun inserts a new scan run with phase=setup, status=running.
func (s *Store) CreateScanRun(revisionID int64, domainKey string, votesNeeded ...int) (int64, error) {
	votes := 1
	if len(votesNeeded) > 0 && votesNeeded[0] > 0 {
		votes = votesNeeded[0]
	}
	res, err := s.db.Exec(`
		INSERT INTO scan_runs (revision_id, domain_key, votes_needed)
		VALUES (?, ?, ?)
	`, revisionID, domainKey, votes)
	if err != nil {
		return 0, fmt.Errorf("CreateScanRun: %w", err)
	}
	return res.LastInsertId()
}

// GetScanRun returns a scan run by ID.
func (s *Store) GetScanRun(runID int64) (*ScanRunRow, error) {
	var r ScanRunRow
	var updatedAt sql.NullString
	err := s.db.QueryRow(`
		SELECT run_id, revision_id, domain_key, phase, status,
		       total_files, extracted_files, resolved, COALESCE(votes_needed,1), created_at, updated_at
		FROM scan_runs WHERE run_id = ?
	`, runID).Scan(&r.RunID, &r.RevisionID, &r.DomainKey, &r.Phase, &r.Status,
		&r.TotalFiles, &r.ExtractedFiles, &r.Resolved, &r.VotesNeeded, &r.CreatedAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("GetScanRun: %w", err)
	}
	if updatedAt.Valid {
		r.UpdatedAt = updatedAt.String
	}
	return &r, nil
}

// GetActiveScanRun returns the most recent non-finalized, non-failed run for a domain.
// Returns nil, nil if no active run exists.
func (s *Store) GetActiveScanRun(domainKey string) (*ScanRunRow, error) {
	var r ScanRunRow
	var updatedAt sql.NullString
	err := s.db.QueryRow(`
		SELECT run_id, revision_id, domain_key, phase, status,
		       total_files, extracted_files, resolved, COALESCE(votes_needed,1), created_at, updated_at
		FROM scan_runs
		WHERE domain_key = ? AND phase != 'finalized' AND status NOT IN ('completed','failed')
		ORDER BY run_id DESC LIMIT 1
	`, domainKey).Scan(&r.RunID, &r.RevisionID, &r.DomainKey, &r.Phase, &r.Status,
		&r.TotalFiles, &r.ExtractedFiles, &r.Resolved, &r.VotesNeeded, &r.CreatedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetActiveScanRun: %w", err)
	}
	if updatedAt.Valid {
		r.UpdatedAt = updatedAt.String
	}
	return &r, nil
}

// TransitionScanRun updates the phase and optionally sets total_files if > 0.
func (s *Store) TransitionScanRun(runID int64, newPhase string, totalFiles int) error {
	var err error
	if totalFiles > 0 {
		_, err = s.db.Exec(`
			UPDATE scan_runs
			SET phase = ?, total_files = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
			WHERE run_id = ?
		`, newPhase, totalFiles, runID)
	} else {
		_, err = s.db.Exec(`
			UPDATE scan_runs
			SET phase = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
			WHERE run_id = ?
		`, newPhase, runID)
	}
	if err != nil {
		return fmt.Errorf("TransitionScanRun: %w", err)
	}
	return nil
}

// IncrementScanRunExtracted increments extracted_files by 1.
func (s *Store) IncrementScanRunExtracted(runID int64) error {
	_, err := s.db.Exec(`
		UPDATE scan_runs
		SET extracted_files = extracted_files + 1, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE run_id = ?
	`, runID)
	if err != nil {
		return fmt.Errorf("IncrementScanRunExtracted: %w", err)
	}
	return nil
}

// SetScanRunResolved sets the resolved count.
func (s *Store) SetScanRunResolved(runID int64, count int) error {
	_, err := s.db.Exec(`
		UPDATE scan_runs
		SET resolved = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE run_id = ?
	`, count, runID)
	if err != nil {
		return fmt.Errorf("SetScanRunResolved: %w", err)
	}
	return nil
}

// CompleteScanRun sets phase=finalized and status=completed.
func (s *Store) CompleteScanRun(runID int64) error {
	_, err := s.db.Exec(`
		UPDATE scan_runs
		SET phase = 'finalized', status = 'completed', updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE run_id = ?
	`, runID)
	if err != nil {
		return fmt.Errorf("CompleteScanRun: %w", err)
	}
	return nil
}

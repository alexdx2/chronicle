package store

import "fmt"

// VerificationRunRow represents a verification run for a file.
type VerificationRunRow struct {
	RunID           int64  `json:"run_id"`
	RevisionID      int64  `json:"revision_id"`
	DomainKey       string `json:"domain_key"`
	FilePath        string `json:"file_path"`
	VerifierID      string `json:"verifier_id"`
	VerifierVersion string `json:"verifier_version"`
	Status          string `json:"status"` // succeeded, failed, skipped, unsupported
	EvidenceChecked int    `json:"evidence_checked"`
	ValidCount      int    `json:"valid_count"`
	MovedCount      int    `json:"moved_count"`
	MissingCount    int    `json:"missing_count"`
	ChangedCount    int    `json:"changed_count"`
	AmbiguousCount  int    `json:"ambiguous_count"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
}

// CreateVerificationRun records a verification run.
func (s *Store) CreateVerificationRun(r VerificationRunRow) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO evidence_verification_runs
		  (revision_id, domain_key, file_path, verifier_id, verifier_version,
		   status, evidence_checked, valid_count, moved_count, missing_count,
		   changed_count, ambiguous_count, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.RevisionID, r.DomainKey, r.FilePath, r.VerifierID, r.VerifierVersion,
		r.Status, r.EvidenceChecked, r.ValidCount, r.MovedCount, r.MissingCount,
		r.ChangedCount, r.AmbiguousCount, r.StartedAt, nullableStr(r.FinishedAt))
	if err != nil {
		return 0, fmt.Errorf("CreateVerificationRun: %w", err)
	}
	return res.LastInsertId()
}

// GetVerifiedFiles returns all files that had a verification run in the given revision.
func (s *Store) GetVerifiedFiles(revisionID int64) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT file_path FROM evidence_verification_runs
		WHERE revision_id = ?
	`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("GetVerifiedFiles: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, err
		}
		out = append(out, fp)
	}
	return out, rows.Err()
}

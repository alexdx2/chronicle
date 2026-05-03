package store

import "fmt"

// ExtractionRow represents facts extracted from a single file during scan.
type ExtractionRow struct {
	ExtractionID int64  `json:"extraction_id"`
	RevisionID   int64  `json:"revision_id"`
	DomainKey    string `json:"domain_key"`
	FilePath     string `json:"file_path"`
	Status       string `json:"status"` // extracted, no_architecture, skipped, error, resolved
	FactsJSON    string `json:"facts_json"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// SaveExtraction stores facts extracted from a file by an agent.
func (s *Store) SaveExtraction(revisionID int64, domainKey, filePath, status, factsJSON, errorMessage string) (int64, error) {
	if factsJSON == "" {
		factsJSON = "[]"
	}
	res, err := s.db.Exec(`
		INSERT INTO scan_extractions (revision_id, domain_key, file_path, status, facts_json, error_message)
		VALUES (?, ?, ?, ?, ?, ?)
	`, revisionID, domainKey, filePath, status, factsJSON, nullableStr(errorMessage))
	if err != nil {
		return 0, fmt.Errorf("SaveExtraction: %w", err)
	}
	return res.LastInsertId()
}

// ListExtractions returns all extractions for a revision.
func (s *Store) ListExtractions(revisionID int64, domainKey string) ([]ExtractionRow, error) {
	q := `SELECT extraction_id, revision_id, domain_key, file_path, status,
	             facts_json, COALESCE(error_message,''), created_at
	      FROM scan_extractions
	      WHERE revision_id = ? AND domain_key = ?
	      ORDER BY extraction_id`
	rows, err := s.db.Query(q, revisionID, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ListExtractions: %w", err)
	}
	defer rows.Close()

	var out []ExtractionRow
	for rows.Next() {
		var r ExtractionRow
		if err := rows.Scan(&r.ExtractionID, &r.RevisionID, &r.DomainKey,
			&r.FilePath, &r.Status, &r.FactsJSON, &r.ErrorMessage, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListUnresolvedExtractions returns extractions with status='extracted' (not yet resolved into graph).
func (s *Store) ListUnresolvedExtractions(revisionID int64, domainKey string) ([]ExtractionRow, error) {
	q := `SELECT extraction_id, revision_id, domain_key, file_path, status,
	             facts_json, COALESCE(error_message,''), created_at
	      FROM scan_extractions
	      WHERE revision_id = ? AND domain_key = ? AND status = 'extracted'
	      ORDER BY extraction_id`
	rows, err := s.db.Query(q, revisionID, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ListUnresolvedExtractions: %w", err)
	}
	defer rows.Close()

	var out []ExtractionRow
	for rows.Next() {
		var r ExtractionRow
		if err := rows.Scan(&r.ExtractionID, &r.RevisionID, &r.DomainKey,
			&r.FilePath, &r.Status, &r.FactsJSON, &r.ErrorMessage, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkExtractionsResolved marks extractions as resolved after graph build.
func (s *Store) MarkExtractionsResolved(revisionID int64, domainKey string) error {
	_, err := s.db.Exec(`
		UPDATE scan_extractions SET status = 'resolved'
		WHERE revision_id = ? AND domain_key = ? AND status = 'extracted'
	`, revisionID, domainKey)
	return err
}

// GetScanCoverage returns file processing stats for a revision.
func (s *Store) GetScanCoverage(revisionID int64, domainKey string) (total, extracted, noArch, skipped, errored int, err error) {
	q := `SELECT status, COUNT(*) FROM scan_extractions
	      WHERE revision_id = ? AND domain_key = ?
	      GROUP BY status`
	rows, err := s.db.Query(q, revisionID, domainKey)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		rows.Scan(&status, &count)
		total += count
		switch status {
		case "extracted", "resolved":
			extracted += count
		case "no_architecture":
			noArch += count
		case "skipped":
			skipped += count
		case "error":
			errored += count
		}
	}
	return total, extracted, noArch, skipped, errored, rows.Err()
}

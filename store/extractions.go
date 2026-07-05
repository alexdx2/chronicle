package store

import (
	"encoding/json"
	"fmt"
)

// extractPrimaryKind returns the "kind" from the first fact in a JSON array.
func extractPrimaryKind(factsJSON string) string {
	var facts []struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(factsJSON), &facts); err != nil || len(facts) == 0 {
		return ""
	}
	return facts[0].Kind
}

// ExtractionRow represents facts extracted from a single file during scan.
type ExtractionRow struct {
	ExtractionID   int64  `json:"extraction_id"`
	RevisionID     int64  `json:"revision_id"`
	DomainKey      string `json:"domain_key"`
	FilePath       string `json:"file_path"`
	Status         string `json:"status"` // extracted, no_runtime_architecture, config_only, type_only, generated, skipped, failed, resolved
	FromType       string `json:"from_type,omitempty"` // controller, module, provider — set by agent at file level
	ExtractionRole string `json:"extraction_role"`     // ast, llm_single, llm_vote, llm_merged
	VoteGroup      string `json:"vote_group,omitempty"`
	VoteIndex      int    `json:"vote_index"`
	FactsJSON      string `json:"facts_json"`
	ErrorMessage   string `json:"error_message,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// SaveExtraction stores facts extracted from a file by an agent.
// role: "ast", "llm_single", "llm_vote". voteGroup/voteIndex for voting mode.
func (s *Store) SaveExtraction(revisionID int64, domainKey, filePath, status, fromType, factsJSON, errorMessage string) (int64, error) {
	return s.SaveExtractionWithVote(revisionID, domainKey, filePath, status, fromType, factsJSON, errorMessage, "single", "", 0)
}

// SaveExtractionWithVote stores facts with voting metadata.
func (s *Store) SaveExtractionWithVote(revisionID int64, domainKey, filePath, status, fromType, factsJSON, errorMessage, role, voteGroup string, voteIndex int) (int64, error) {
	id, _, err := s.SaveExtractionWithOutcome(revisionID, domainKey, filePath, status, fromType, factsJSON, errorMessage, role, voteGroup, voteIndex)
	return id, err
}

// SaveExtractionWithOutcome saves an extraction and additionally reports
// whether the facts were actually stored (written=false means dedup returned
// an existing row and the new facts were discarded). Callers surfacing scan
// progress must use this — the 2026-07-05 otopoint scans lost every phase-2
// flow artifact silently because the plain save hid the dedup outcome.
//
// Roles:
//   - "llm_vote": always INSERT (each vote independent)
//   - "flow": phase-2 flow facts — dedup ONLY within role='flow' for the same
//     file (a phase-1 row must never swallow them); re-commit refreshes in place
//   - anything else: dedup by domain+file against non-flow rows
func (s *Store) SaveExtractionWithOutcome(revisionID int64, domainKey, filePath, status, fromType, factsJSON, errorMessage, role, voteGroup string, voteIndex int) (int64, bool, error) {
	if factsJSON == "" {
		factsJSON = "[]"
	}

	insert := func() (int64, bool, error) {
		res, err := s.db.Exec(`
			INSERT INTO scan_extractions (revision_id, domain_key, file_path, status, from_type, extraction_role, vote_group, vote_index, facts_json, error_message)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, revisionID, domainKey, filePath, status, fromType, role, voteGroup, voteIndex, factsJSON, nullableStr(errorMessage))
		if err != nil {
			return 0, false, fmt.Errorf("SaveExtraction: %w", err)
		}
		id, err := res.LastInsertId()
		return id, true, err
	}

	// llm_vote: always INSERT (no dedup — each vote is independent)
	if role == "llm_vote" {
		return insert()
	}

	// flow: dedup within role='flow' only; refresh in place on re-commit.
	if role == "flow" {
		var existingID int64
		err := s.db.QueryRow(`
			SELECT extraction_id FROM scan_extractions
			WHERE domain_key = ? AND file_path = ? AND extraction_role = 'flow'
			ORDER BY extraction_id DESC LIMIT 1
		`, domainKey, filePath).Scan(&existingID)
		if err == nil {
			_, uerr := s.db.Exec(`UPDATE scan_extractions SET facts_json = ?, from_type = ?, status = ?, error_message = ? WHERE extraction_id = ?`,
				factsJSON, fromType, status, nullableStr(errorMessage), existingID)
			if uerr != nil {
				return 0, false, fmt.Errorf("SaveExtraction: %w", uerr)
			}
			return existingID, true, nil
		}
		return insert()
	}

	// ast / llm_single / default: dedup by domain + file against non-flow rows.
	var existingID int64
	var existingFacts string
	err := s.db.QueryRow(`
		SELECT extraction_id, facts_json FROM scan_extractions
		WHERE domain_key = ? AND file_path = ? AND COALESCE(extraction_role,'single') != 'flow'
		ORDER BY extraction_id DESC LIMIT 1
	`, domainKey, filePath).Scan(&existingID, &existingFacts)
	if err == nil {
		if factsJSON == "[]" || factsJSON == "" {
			return existingID, false, nil
		}
		if existingFacts == "[]" || existingFacts == "" {
			s.db.Exec(`UPDATE scan_extractions SET facts_json = ?, from_type = ?, status = 'extracted', extraction_role = ? WHERE extraction_id = ?`, factsJSON, fromType, role, existingID)
			return existingID, true, nil
		}
		// Same primary fact kind already stored for this file — dedup.
		primaryKind := extractPrimaryKind(factsJSON)
		var existingWithKind int64
		if primaryKind != "" {
			s.db.QueryRow(`
				SELECT extraction_id FROM scan_extractions
				WHERE domain_key = ? AND file_path = ? AND facts_json LIKE ?
				LIMIT 1
			`, domainKey, filePath, "%\"kind\":\""+primaryKind+"\"%").Scan(&existingWithKind)
			if existingWithKind > 0 {
				return existingWithKind, false, nil
			}
		}
	}

	return insert()
}

// ListExtractions returns all extractions for a revision.
func (s *Store) ListExtractions(revisionID int64, domainKey string) ([]ExtractionRow, error) {
	q := `SELECT extraction_id, revision_id, domain_key, file_path, status,
	             COALESCE(from_type,''),
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
			&r.FilePath, &r.Status, &r.FromType, &r.FactsJSON, &r.ErrorMessage, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListUnresolvedExtractions returns extractions with status='extracted' (not yet resolved into graph).
func (s *Store) ListUnresolvedExtractions(revisionID int64, domainKey string) ([]ExtractionRow, error) {
	q := `SELECT extraction_id, revision_id, domain_key, file_path, status,
	             COALESCE(from_type,''), COALESCE(extraction_role,'single'),
	             COALESCE(vote_group,''), COALESCE(vote_index,0),
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
			&r.FilePath, &r.Status, &r.FromType, &r.ExtractionRole,
			&r.VoteGroup, &r.VoteIndex,
			&r.FactsJSON, &r.ErrorMessage, &r.CreatedAt); err != nil {
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
		case "no_runtime_architecture", "config_only", "type_only", "generated":
			noArch += count
		case "skipped":
			skipped += count
		case "failed":
			errored += count
		}
	}
	return total, extracted, noArch, skipped, errored, rows.Err()
}

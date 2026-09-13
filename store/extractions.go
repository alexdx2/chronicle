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
	Status         string `json:"status"`              // extracted, no_runtime_architecture, config_only, type_only, generated, skipped, failed, resolved
	FromType       string `json:"from_type,omitempty"` // controller, module, provider — set by agent at file level
	ExtractionRole string `json:"extraction_role"`     // ast, llm_single, llm_vote, llm_merged
	VoteGroup      string `json:"vote_group,omitempty"`
	VoteIndex      int    `json:"vote_index"`
	FactsJSON      string `json:"facts_json"`
	ErrorMessage   string `json:"error_message,omitempty"`
	// Metadata is a free-form JSON object attached to the extraction row.
	// The deterministic resolver writes {"unresolved":[{kind,name,candidates}]}
	// here — the names it saw but refused to turn into an edge.
	Metadata  string `json:"metadata,omitempty"`
	CreatedAt string `json:"created_at"`
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

	// ast / llm_single / default: dedup by domain + file against rows that
	// belong to the same conversation. A flow row is phase 2's, and a
	// structural row is the refresh phase's standing answer for the file
	// (StructuralExtractionRole) — deduping a scan's reading against either
	// would throw the scan's facts away.
	var existingID int64
	var existingFacts string
	err := s.db.QueryRow(`
		SELECT extraction_id, facts_json FROM scan_extractions
		WHERE domain_key = ? AND file_path = ?
		  AND COALESCE(extraction_role,'single') NOT IN ('flow', '`+StructuralExtractionRole+`')
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

// StructuralExtractionRole marks the row the deterministic structural phase
// keeps for a file. It is its own role because the phase REPLACES its previous
// answer on every run, while every other role's dedup exists to stop a second
// writer overwriting the first: SaveExtraction would have handed the phase back
// the row from the previous commit and silently discarded the new facts, so the
// file's structure could never change again.
const StructuralExtractionRole = "structural"

// SaveStructuralExtraction stores the structural phase's answer for one file,
// replacing its previous one and re-pointing it at the revision now being
// resolved. One row per (domain, file) — this is a standing answer, not a
// history; the history lives in the evidence rows it justifies.
func (s *Store) SaveStructuralExtraction(revisionID int64, domainKey, filePath, status, fromType, factsJSON, errorMessage string) (int64, error) {
	if factsJSON == "" {
		factsJSON = "[]"
	}
	var existingID int64
	err := s.db.QueryRow(`
		SELECT extraction_id FROM scan_extractions
		WHERE domain_key = ? AND file_path = ? AND extraction_role = ?
		ORDER BY extraction_id DESC LIMIT 1
	`, domainKey, filePath, StructuralExtractionRole).Scan(&existingID)
	if err == nil {
		// metadata is reset with the facts: the unresolved names recorded on
		// the row describe the answer being replaced, not the new one.
		if _, uerr := s.db.Exec(`
			UPDATE scan_extractions
			SET revision_id = ?, status = ?, from_type = ?, facts_json = ?,
			    error_message = ?, metadata = '{}'
			WHERE extraction_id = ?`,
			revisionID, status, fromType, factsJSON, nullableStr(errorMessage), existingID); uerr != nil {
			return 0, fmt.Errorf("SaveStructuralExtraction: %w", uerr)
		}
		return existingID, nil
	}
	res, err := s.db.Exec(`
		INSERT INTO scan_extractions (revision_id, domain_key, file_path, status, from_type, extraction_role, vote_index, facts_json, error_message)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)
	`, revisionID, domainKey, filePath, status, fromType, StructuralExtractionRole, factsJSON, nullableStr(errorMessage))
	if err != nil {
		return 0, fmt.Errorf("SaveStructuralExtraction: %w", err)
	}
	return res.LastInsertId()
}

// ListExtractions returns all extractions for a revision.
func (s *Store) ListExtractions(revisionID int64, domainKey string) ([]ExtractionRow, error) {
	q := `SELECT extraction_id, revision_id, domain_key, file_path, status,
	             COALESCE(from_type,''),
	             facts_json, COALESCE(error_message,''), COALESCE(metadata,'{}'), created_at
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
			&r.FilePath, &r.Status, &r.FromType, &r.FactsJSON, &r.ErrorMessage, &r.Metadata, &r.CreatedAt); err != nil {
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

// UpdateExtractionMetadata merges patch into scan_extractions.metadata for one
// extraction row. Top-level keys in patch replace keys already stored; keys the
// patch does not mention survive. A nil/empty patch is a no-op, and an
// extraction id that names no row is reported — the caller is asserting the row
// exists (it just resolved its facts).
func (s *Store) UpdateExtractionMetadata(extractionID int64, patch map[string]any) error {
	if extractionID <= 0 || len(patch) == 0 {
		return nil
	}
	var current string
	err := s.db.QueryRow(`SELECT COALESCE(metadata,'{}') FROM scan_extractions WHERE extraction_id = ?`, extractionID).Scan(&current)
	if err != nil {
		return fmt.Errorf("UpdateExtractionMetadata: %w", err)
	}
	merged := map[string]any{}
	if current != "" {
		// A row written before this column existed (or hand-edited) must not
		// fail the merge — an unreadable value is replaced, not preserved.
		_ = json.Unmarshal([]byte(current), &merged)
	}
	for k, v := range patch {
		merged[k] = v
	}
	buf, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("UpdateExtractionMetadata: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE scan_extractions SET metadata = ? WHERE extraction_id = ?`, string(buf), extractionID); err != nil {
		return fmt.Errorf("UpdateExtractionMetadata: %w", err)
	}
	return nil
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

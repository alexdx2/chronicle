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
		//
		// Scoped to the same conversation, exactly like the lookup above. The
		// structural phase records an import fact for nearly every file it
		// reads, so an unscoped match hit ITS standing row for any scan row
		// whose first fact was an import: the scan's facts were dropped
		// (written=false), chronicle_import_extractions reported the file as
		// deduped, and the id handed back belonged to another writer's row.
		primaryKind := extractPrimaryKind(factsJSON)
		var existingWithKind int64
		if primaryKind != "" {
			s.db.QueryRow(`
				SELECT extraction_id FROM scan_extractions
				WHERE domain_key = ? AND file_path = ? AND facts_json LIKE ?
				  AND COALESCE(extraction_role,'single') NOT IN ('flow', '`+StructuralExtractionRole+`')
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

// StructuralOutcomeUnreadable marks a standing structural row whose file could
// not be read at all, as against one the parser choked on.
//
// scan_extractions.status is a CHECK enum with no room for a new value, so both
// land as 'failed' and the difference rides in metadata. It is worth keeping:
// a parse failure is a file to hand a model, an unread file is a file to look
// at again, and reporting them as one number would ask for the wrong fix.
const StructuralOutcomeUnreadable = "unreadable"

// SaveStructuralExtraction stores the structural phase's answer for one file,
// replacing its previous one and re-pointing it at the revision now being
// resolved. One row per (domain, file) — this is a standing answer, not a
// history; the history lives in the evidence rows it justifies.
//
// outcome is written to metadata as structural_outcome; empty means the plain
// reading of status.
func (s *Store) SaveStructuralExtraction(revisionID int64, domainKey, filePath, status, fromType, factsJSON, errorMessage, outcome string) (int64, error) {
	if factsJSON == "" {
		factsJSON = "[]"
	}
	metadata := "{}"
	if outcome != "" {
		buf, err := json.Marshal(map[string]string{"structural_outcome": outcome})
		if err != nil {
			return 0, fmt.Errorf("SaveStructuralExtraction: %w", err)
		}
		metadata = string(buf)
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
			    error_message = ?, metadata = ?
			WHERE extraction_id = ?`,
			revisionID, status, fromType, factsJSON, nullableStr(errorMessage), metadata, existingID); uerr != nil {
			return 0, fmt.Errorf("SaveStructuralExtraction: %w", uerr)
		}
		return existingID, nil
	}
	res, err := s.db.Exec(`
		INSERT INTO scan_extractions (revision_id, domain_key, file_path, status, from_type, extraction_role, vote_index, facts_json, error_message, metadata)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
	`, revisionID, domainKey, filePath, status, fromType, StructuralExtractionRole, factsJSON, nullableStr(errorMessage), metadata)
	if err != nil {
		return 0, fmt.Errorf("SaveStructuralExtraction: %w", err)
	}
	return res.LastInsertId()
}

// DeleteStructuralExtraction forgets the phase's standing row for one file.
// Called when the file is gone: a deleted file's `failed` row would otherwise
// keep it in the retry set forever, spending a batch slot per run on something
// that cannot come back.
func (s *Store) DeleteStructuralExtraction(domainKey, filePath string) error {
	if _, err := s.db.Exec(`DELETE FROM scan_extractions
		WHERE domain_key = ? AND file_path = ? AND extraction_role = ?`,
		domainKey, filePath, StructuralExtractionRole); err != nil {
		return fmt.Errorf("DeleteStructuralExtraction %q: %w", filePath, err)
	}
	return nil
}

// StructuralFailures lists the files whose STANDING structural answer is "no
// answer", split by why. They are two different disclosures and one retry set:
// the phase re-targets both on every run, and the freshness line reports them
// as "failed to parse" and "not read" separately.
//
// Standing, not historical: there is one row per (domain, file), so a file
// that failed three runs ago is still here — which is the point. Counting the
// last run's stamp instead let a failure disappear the moment any other commit
// landed.
func (s *Store) StructuralFailures(domainKey string) (failed, unreadable []string, err error) {
	rows, qerr := s.db.Query(`
		SELECT file_path, COALESCE(metadata,'{}') FROM scan_extractions
		WHERE domain_key = ? AND extraction_role = ? AND status = 'failed'
		ORDER BY file_path`, domainKey, StructuralExtractionRole)
	if qerr != nil {
		return nil, nil, fmt.Errorf("StructuralFailures: %w", qerr)
	}
	defer rows.Close()
	for rows.Next() {
		var path, metadata string
		if err := rows.Scan(&path, &metadata); err != nil {
			return nil, nil, fmt.Errorf("StructuralFailures scan: %w", err)
		}
		var m struct {
			Outcome string `json:"structural_outcome"`
		}
		_ = json.Unmarshal([]byte(metadata), &m)
		if m.Outcome == StructuralOutcomeUnreadable {
			unreadable = append(unreadable, path)
		} else {
			failed = append(failed, path)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("StructuralFailures rows: %w", err)
	}
	return failed, unreadable, nil
}

// ListScanExtractions is ListExtractions without the structural phase's
// standing rows — what a SCAN's coverage, counts and file index are about. The
// phase writes one row per file that outlives the revision it was last
// resolved on, so counting it as part of a scan's work would report files the
// scan never touched.
func (s *Store) ListScanExtractions(revisionID int64, domainKey string) ([]ExtractionRow, error) {
	all, err := s.ListExtractions(revisionID, domainKey)
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for _, e := range all {
		if e.ExtractionRole == StructuralExtractionRole {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ListExtractions returns all extractions for a revision.
//
// extraction_role is selected, and that is load-bearing rather than tidy: it
// was missing, so every row came back with an empty role and every caller that
// filters on one filtered nothing — ListScanExtractions kept the structural
// phase's rows in a scan's coverage, and the resolver's role-scoped file index
// matched zero rows and left the index EMPTY, which made an import guess the
// type of the file it points at and mint a second, mistyped node for the same
// path.
func (s *Store) ListExtractions(revisionID int64, domainKey string) ([]ExtractionRow, error) {
	q := `SELECT extraction_id, revision_id, domain_key, file_path, status,
	             COALESCE(from_type,''), COALESCE(extraction_role,'single'),
	             COALESCE(vote_group,''), COALESCE(vote_index,0),
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
			&r.FilePath, &r.Status, &r.FromType, &r.ExtractionRole,
			&r.VoteGroup, &r.VoteIndex,
			&r.FactsJSON, &r.ErrorMessage, &r.Metadata, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListStandingExtractionsByRole returns one writer's CURRENT rows for a domain,
// across every revision.
//
// It exists for the structural phase, whose rows are a standing answer rather
// than a history: SaveStructuralExtraction keeps one row per (domain, file) and
// re-points it at whatever revision last touched the file. Asking for a single
// revision therefore returns only the files THIS run happened to look at, and
// the resolver's file index — which decides what a class name means — has never
// heard of the rest of the repo. A relative import of a file structured three
// commits ago then falls through to class-name inference and mints a second,
// mistyped node for a path that already has one.
//
// Role-scoped on purpose. The rule the index rests on is "whoever is resolving
// indexes their own rows"; this widens the revision scope without mixing two
// writers' accounts of the same file.
func (s *Store) ListStandingExtractionsByRole(domainKey, role string) ([]ExtractionRow, error) {
	q := `SELECT extraction_id, revision_id, domain_key, file_path, status,
	             COALESCE(from_type,''), COALESCE(extraction_role,'single'),
	             COALESCE(vote_group,''), COALESCE(vote_index,0),
	             facts_json, COALESCE(error_message,''), COALESCE(metadata,'{}'), created_at
	      FROM scan_extractions
	      WHERE domain_key = ? AND COALESCE(extraction_role,'single') = ?
	      ORDER BY extraction_id`
	rows, err := s.db.Query(q, domainKey, role)
	if err != nil {
		return nil, fmt.Errorf("ListStandingExtractionsByRole: %w", err)
	}
	defer rows.Close()

	var out []ExtractionRow
	for rows.Next() {
		var r ExtractionRow
		if err := rows.Scan(&r.ExtractionID, &r.RevisionID, &r.DomainKey,
			&r.FilePath, &r.Status, &r.FromType, &r.ExtractionRole,
			&r.VoteGroup, &r.VoteIndex,
			&r.FactsJSON, &r.ErrorMessage, &r.Metadata, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListUnresolvedExtractions returns extractions with status='extracted' (not yet resolved into graph).
func (s *Store) ListUnresolvedExtractions(revisionID int64, domainKey string) ([]ExtractionRow, error) {
	return s.ListUnresolvedExtractionsByRole(revisionID, domainKey, "")
}

// ListUnresolvedExtractionsByRole is ListUnresolvedExtractions narrowed to one
// extraction_role. An empty role means "every role a scan owns", which
// excludes StructuralExtractionRole — see below.
//
// Two writers can now share a revision: the structural phase resolves on the
// revision the refresh or the scan already opened. Without the narrowing, a
// deterministic resolve would pick up an agent's rows on that revision, build
// the graph from facts it never read, and mark them resolved — the agent's
// work consumed by a pass that cannot produce it.
func (s *Store) ListUnresolvedExtractionsByRole(revisionID int64, domainKey, role string) ([]ExtractionRow, error) {
	q := `SELECT extraction_id, revision_id, domain_key, file_path, status,
	             COALESCE(from_type,''), COALESCE(extraction_role,'single'),
	             COALESCE(vote_group,''), COALESCE(vote_index,0),
	             facts_json, COALESCE(error_message,''), created_at
	      FROM scan_extractions
	      WHERE revision_id = ? AND domain_key = ? AND status = 'extracted'`
	args := []any{revisionID, domainKey}
	if role != "" {
		q += ` AND COALESCE(extraction_role,'single') = ?`
		args = append(args, role)
	} else {
		// "Every role" means every role a SCAN owns. The structural phase's
		// standing row is another writer's, exactly as this caller's own rows
		// are not the structural phase's — the exclusion has to run both ways
		// or a scan resolves and closes work it did not do.
		q += ` AND COALESCE(extraction_role,'single') != '` + StructuralExtractionRole + `'`
	}
	q += ` ORDER BY extraction_id`
	rows, err := s.db.Query(q, args...)
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
	return s.MarkExtractionsResolvedByRole(revisionID, domainKey, "")
}

// MarkExtractionsResolvedByRole closes only the rows one writer owns. An empty
// role means "every role a scan owns" and excludes the structural phase's, the
// same way the phase's own role excludes the scan's. A resolve may only mark
// resolved what it actually resolved.
func (s *Store) MarkExtractionsResolvedByRole(revisionID int64, domainKey, role string) error {
	q := `UPDATE scan_extractions SET status = 'resolved'
	      WHERE revision_id = ? AND domain_key = ? AND status = 'extracted'`
	args := []any{revisionID, domainKey}
	if role != "" {
		q += ` AND COALESCE(extraction_role,'single') = ?`
		args = append(args, role)
	} else {
		q += ` AND COALESCE(extraction_role,'single') != '` + StructuralExtractionRole + `'`
	}
	_, err := s.db.Exec(q, args...)
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

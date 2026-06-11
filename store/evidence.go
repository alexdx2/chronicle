package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// EvidenceRow represents a row in graph_evidence.
type EvidenceRow struct {
	EvidenceID       int64   `json:"evidence_id"`
	TargetKind       string  `json:"target_kind"`
	NodeID           int64   `json:"node_id,omitempty"`
	EdgeID           int64   `json:"edge_id,omitempty"`
	SourceKind       string  `json:"source_kind"`
	RepoName         string  `json:"repo_name,omitempty"`
	FilePath         string  `json:"file_path,omitempty"`
	LineStart        int     `json:"line_start,omitempty"`
	LineEnd          int     `json:"line_end,omitempty"`
	ColumnStart      int     `json:"column_start,omitempty"`
	ColumnEnd        int     `json:"column_end,omitempty"`
	Locator          string  `json:"locator,omitempty"`
	ExtractorID      string  `json:"extractor_id"`
	ExtractorVersion string  `json:"extractor_version"`
	ASTRule          string  `json:"ast_rule,omitempty"`
	SnippetHash      string  `json:"snippet_hash,omitempty"`
	CommitSHA        string  `json:"commit_sha,omitempty"`
	ObservedAt       string  `json:"observed_at"`
	VerifiedAt       string  `json:"verified_at,omitempty"`
	Confidence       float64 `json:"confidence"`
	EvidenceStatus   string  `json:"evidence_status"`
	EvidencePolarity string  `json:"evidence_polarity"`
	EvidenceUID              string `json:"evidence_uid,omitempty"`
	ContextID                int64  `json:"context_id,omitempty"`
	ValidFromRevisionID      int64  `json:"valid_from_revision_id,omitempty"`
	ValidToRevisionID        int64  `json:"valid_to_revision_id,omitempty"`
	LastVerifiedRevisionID   int64  `json:"last_verified_revision_id,omitempty"`
	InvalidatedByRevisionID  int64  `json:"invalidated_by_revision_id,omitempty"`
	InvalidatedReason        string `json:"invalidated_reason,omitempty"`
	Metadata         string  `json:"metadata"`
	// Assertion-based verification fields
	Assertion          string `json:"assertion,omitempty"`
	AssertionKind      string `json:"assertion_kind,omitempty"`
	AssertionVersion   string `json:"assertion_version,omitempty"`
	VerificationStatus string `json:"verification_status,omitempty"`
	VerificationReason string `json:"verification_reason,omitempty"`
}

// AddEvidence deduplicates by (target_kind, node_id/edge_id, source_kind, repo_name, file_path,
// line_start, extractor_id). If a match is found, updates observed_at/confidence/commit_sha/
// extractor_version. Otherwise inserts.
func (s *Store) AddEvidence(e EvidenceRow) (int64, error) {
	var nodeID, edgeID *int64
	if e.TargetKind == "node" {
		nodeID = &e.NodeID
	} else {
		edgeID = &e.EdgeID
	}

	// Check for duplicate (polarity is part of dedup key — negative evidence is separate from positive).
	polarity := e.EvidencePolarity
	if polarity == "" {
		polarity = "positive"
	}

	// Resolve owner key + stable evidence identity + domain for journaling.
	// A missing owner is an integrity bug: evidence pointing at a nonexistent
	// node/edge would journal an event with an empty owner_key, which then
	// hard-fails replay on another machine — fail loud here instead. (Callers
	// always pass a real id: zero ids match no row and fail the same way.)
	ownerKey := ""
	if e.TargetKind == "node" {
		if err := s.db.QueryRow(`SELECT node_key FROM graph_nodes WHERE node_id = ?`, e.NodeID).Scan(&ownerKey); err != nil || ownerKey == "" {
			return 0, fmt.Errorf("AddEvidence: owner node %d not found (err=%v)", e.NodeID, err)
		}
	} else {
		if err := s.db.QueryRow(`SELECT edge_key FROM graph_edges WHERE edge_id = ?`, e.EdgeID).Scan(&ownerKey); err != nil || ownerKey == "" {
			return 0, fmt.Errorf("AddEvidence: owner edge %d not found (err=%v)", e.EdgeID, err)
		}
	}
	evKey := EvidenceKey(e.TargetKind, ownerKey, e.SourceKind, e.RepoName, e.FilePath, e.LineStart, e.ExtractorID, polarity)
	domain := DomainFromNodeKey(ownerKey)
	if e.TargetKind == "edge" {
		domain = domainFromEdgeKey(ownerKey)
	}

	const dedupQ = `
		SELECT evidence_id FROM graph_evidence
		WHERE target_kind = ?
		  AND COALESCE(node_id, 0) = COALESCE(?, 0)
		  AND COALESCE(edge_id, 0) = COALESCE(?, 0)
		  AND source_kind = ?
		  AND COALESCE(repo_name,'') = ?
		  AND COALESCE(file_path,'') = ?
		  AND COALESCE(line_start,0) = ?
		  AND extractor_id = ?
		  AND evidence_polarity = ?
		LIMIT 1
	`
	var existingID int64
	err := s.db.QueryRow(dedupQ,
		e.TargetKind, nodeID, edgeID,
		e.SourceKind,
		e.RepoName, e.FilePath, e.LineStart,
		e.ExtractorID,
		polarity,
	).Scan(&existingID)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("AddEvidence dedup lookup: %w", err)
	}

	if err == nil {
		// Update existing — return to valid (revalidation is an event, not a state).
		newStatus := "valid"
		var oldStatus, existingUID string
		s.db.QueryRow("SELECT evidence_status, COALESCE(evidence_uid,'') FROM graph_evidence WHERE evidence_id=?", existingID).Scan(&oldStatus, &existingUID)
		_ = oldStatus // stale → valid on re-observation
		if existingUID != "" {
			evKey = existingUID // respect a caller-supplied uid already on the row
		}

		const updQ = `
			UPDATE graph_evidence
			SET observed_at=strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    confidence=?,
			    commit_sha=?,
			    extractor_version=?,
			    evidence_status=?,
			    last_verified_revision_id=?,
			    evidence_uid=COALESCE(evidence_uid, ?)
			WHERE evidence_id=?
		`
		_, err = s.db.Exec(updQ, e.Confidence, nullableStr(e.CommitSHA), e.ExtractorVersion,
			newStatus, nullableInt64(e.ValidFromRevisionID), evKey, existingID)
		if err != nil {
			return 0, fmt.Errorf("AddEvidence update: %w", err)
		}
		if err := s.appendEvent(journalEvent{
			DomainKey: domain, RevisionID: e.ValidFromRevisionID,
			Kind: EvEvidenceStatus, Key: evKey, OwnerKey: ownerKey,
			Fields: map[string]any{"status": "valid", "confidence": e.Confidence},
		}); err != nil {
			return 0, err
		}
		return existingID, nil
	}

	// Insert new.
	status := e.EvidenceStatus
	if status == "" {
		status = "valid"
	}

	assertionKind := e.AssertionKind
	if assertionKind == "" {
		assertionKind = ""
	}
	assertionVersion := e.AssertionVersion
	if assertionVersion == "" {
		assertionVersion = "v1"
	}
	assertion := e.Assertion
	if assertion == "" {
		assertion = "{}"
	}
	uid := e.EvidenceUID
	if uid == "" {
		uid = evKey
	}

	const insQ = `
		INSERT INTO graph_evidence
		  (target_kind, node_id, edge_id, source_kind, repo_name, file_path,
		   line_start, line_end, column_start, column_end, locator,
		   extractor_id, extractor_version, ast_rule, snippet_hash, commit_sha,
		   confidence, evidence_status, evidence_polarity,
		   valid_from_revision_id, last_verified_revision_id,
		   context_id, evidence_uid,
		   assertion, assertion_kind, assertion_version,
		   verification_status, verification_reason,
		   metadata)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`
	res, err := s.db.Exec(insQ,
		e.TargetKind, nodeID, edgeID,
		e.SourceKind,
		nullableStr(e.RepoName), nullableStr(e.FilePath),
		nullableInt(e.LineStart), nullableInt(e.LineEnd),
		nullableInt(e.ColumnStart), nullableInt(e.ColumnEnd),
		nullableStr(e.Locator),
		e.ExtractorID, e.ExtractorVersion,
		nullableStr(e.ASTRule), nullableStr(e.SnippetHash), nullableStr(e.CommitSHA),
		e.Confidence, status, polarity,
		nullableInt64(e.ValidFromRevisionID), nullableInt64(e.ValidFromRevisionID),
		nullableInt64(e.ContextID), nullableStr(uid),
		assertion, assertionKind, assertionVersion,
		defaultStr(e.VerificationStatus, "unverified"), defaultStr(e.VerificationReason, ""),
		e.Metadata,
	)
	if err != nil {
		return 0, fmt.Errorf("AddEvidence insert: %w", err)
	}
	id, _ := res.LastInsertId()
	e.EvidencePolarity, e.Assertion, e.AssertionKind, e.EvidenceStatus = polarity, assertion, assertionKind, status
	evFields := evidenceEventFields(e)
	if err := s.appendEvent(journalEvent{
		DomainKey: domain, RevisionID: e.ValidFromRevisionID,
		Kind: EvEvidenceAdd, Key: uid, OwnerKey: ownerKey,
		Fields: evFields,
	}); err != nil {
		return 0, err
	}
	return id, nil
}

// evidenceEventFields builds the evidence_add journal payload for a row whose
// polarity/assertion/status are already normalized (shared by AddEvidence and
// BootstrapJournal — the two emitters must stay byte-identical so replay and
// bootstrap produce the same rows).
func evidenceEventFields(e EvidenceRow) map[string]any {
	evFields := map[string]any{
		"target_kind":       e.TargetKind,
		"source_kind":       e.SourceKind,
		"repo_name":         e.RepoName,
		"file_path":         e.FilePath,
		"line_start":        e.LineStart,
		"line_end":          e.LineEnd,
		"locator":           e.Locator,
		"extractor_id":      e.ExtractorID,
		"extractor_version": e.ExtractorVersion,
		"confidence":        e.Confidence,
		"polarity":          e.EvidencePolarity,
		"assertion":         e.Assertion,
		"assertion_kind":    e.AssertionKind,
		"status":            e.EvidenceStatus,
	}
	// Optional columns: omitted when empty/zero to keep journal lines compact.
	if e.Metadata != "" && e.Metadata != "{}" {
		evFields["metadata"] = e.Metadata
	}
	if e.ASTRule != "" {
		evFields["ast_rule"] = e.ASTRule
	}
	if e.SnippetHash != "" {
		evFields["snippet_hash"] = e.SnippetHash
	}
	if e.CommitSHA != "" {
		evFields["commit_sha"] = e.CommitSHA
	}
	if e.ColumnStart != 0 {
		evFields["column_start"] = e.ColumnStart
	}
	if e.ColumnEnd != 0 {
		evFields["column_end"] = e.ColumnEnd
	}
	return evFields
}

// ListEvidenceByNode returns all evidence rows for the given node.
func (s *Store) ListEvidenceByNode(nodeID int64) ([]EvidenceRow, error) {
	return s.listEvidence("node_id", nodeID)
}

// ListEvidenceByEdge returns all evidence rows for the given edge.
func (s *Store) ListEvidenceByEdge(edgeID int64) ([]EvidenceRow, error) {
	return s.listEvidence("edge_id", edgeID)
}

func (s *Store) listEvidence(col string, id int64) ([]EvidenceRow, error) {
	q := `
		SELECT evidence_id, target_kind,
		       COALESCE(node_id,0), COALESCE(edge_id,0),
		       source_kind,
		       COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(line_start,0), COALESCE(line_end,0),
		       COALESCE(column_start,0), COALESCE(column_end,0),
		       COALESCE(locator,''),
		       extractor_id, extractor_version,
		       COALESCE(ast_rule,''), COALESCE(snippet_hash,''), COALESCE(commit_sha,''),
		       observed_at, COALESCE(verified_at,''),
		       confidence, evidence_status, evidence_polarity,
		       COALESCE(valid_from_revision_id,0), COALESCE(valid_to_revision_id,0),
		       COALESCE(last_verified_revision_id,0), COALESCE(invalidated_by_revision_id,0),
		       COALESCE(invalidated_reason,''),
		       COALESCE(assertion,'{}'), COALESCE(assertion_kind,''), COALESCE(assertion_version,'v1'),
		       COALESCE(verification_status,'unverified'), COALESCE(verification_reason,''),
		       metadata
		FROM graph_evidence
		WHERE ` + col + ` = ?
		ORDER BY evidence_id
	`
	rows, err := s.db.Query(q, id)
	if err != nil {
		return nil, fmt.Errorf("listEvidence: %w", err)
	}
	defer rows.Close()

	var out []EvidenceRow
	for rows.Next() {
		var r EvidenceRow
		if err := rows.Scan(
			&r.EvidenceID, &r.TargetKind, &r.NodeID, &r.EdgeID,
			&r.SourceKind, &r.RepoName, &r.FilePath,
			&r.LineStart, &r.LineEnd, &r.ColumnStart, &r.ColumnEnd,
			&r.Locator, &r.ExtractorID, &r.ExtractorVersion,
			&r.ASTRule, &r.SnippetHash, &r.CommitSHA,
			&r.ObservedAt, &r.VerifiedAt,
			&r.Confidence, &r.EvidenceStatus, &r.EvidencePolarity,
			&r.ValidFromRevisionID, &r.ValidToRevisionID,
			&r.LastVerifiedRevisionID, &r.InvalidatedByRevisionID,
			&r.InvalidatedReason,
			&r.Assertion, &r.AssertionKind, &r.AssertionVersion,
			&r.VerificationStatus, &r.VerificationReason,
			&r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("listEvidence scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListStaleEvidenceByFile returns all stale evidence for a given file path.
func (s *Store) ListStaleEvidenceByFile(filePath string) ([]EvidenceRow, error) {
	q := `
		SELECT evidence_id, target_kind,
		       COALESCE(node_id,0), COALESCE(edge_id,0),
		       source_kind,
		       COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(line_start,0), COALESCE(line_end,0),
		       COALESCE(column_start,0), COALESCE(column_end,0),
		       COALESCE(locator,''),
		       extractor_id, extractor_version,
		       COALESCE(ast_rule,''), COALESCE(snippet_hash,''), COALESCE(commit_sha,''),
		       observed_at, COALESCE(verified_at,''),
		       confidence, evidence_status, evidence_polarity,
		       COALESCE(valid_from_revision_id,0), COALESCE(valid_to_revision_id,0),
		       COALESCE(last_verified_revision_id,0), COALESCE(invalidated_by_revision_id,0),
		       COALESCE(invalidated_reason,''),
		       COALESCE(assertion,'{}'), COALESCE(assertion_kind,''), COALESCE(assertion_version,'v1'),
		       COALESCE(verification_status,'unverified'), COALESCE(verification_reason,''),
		       metadata
		FROM graph_evidence
		WHERE file_path = ? AND evidence_status = 'stale'
		ORDER BY evidence_id
	`
	rows, err := s.db.Query(q, filePath)
	if err != nil {
		return nil, fmt.Errorf("ListStaleEvidenceByFile: %w", err)
	}
	defer rows.Close()

	var out []EvidenceRow
	for rows.Next() {
		var r EvidenceRow
		if err := rows.Scan(
			&r.EvidenceID, &r.TargetKind, &r.NodeID, &r.EdgeID,
			&r.SourceKind, &r.RepoName, &r.FilePath,
			&r.LineStart, &r.LineEnd, &r.ColumnStart, &r.ColumnEnd,
			&r.Locator, &r.ExtractorID, &r.ExtractorVersion,
			&r.ASTRule, &r.SnippetHash, &r.CommitSHA,
			&r.ObservedAt, &r.VerifiedAt,
			&r.Confidence, &r.EvidenceStatus, &r.EvidencePolarity,
			&r.ValidFromRevisionID, &r.ValidToRevisionID,
			&r.LastVerifiedRevisionID, &r.InvalidatedByRevisionID,
			&r.InvalidatedReason,
			&r.Assertion, &r.AssertionKind, &r.AssertionVersion,
			&r.VerificationStatus, &r.VerificationReason,
			&r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("ListStaleEvidenceByFile scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateEvidenceVerification updates an evidence row after mechanical verification.
func (s *Store) UpdateEvidenceVerification(evidenceID int64, status, verificationStatus, verificationReason string, lineStart, lineEnd int, revisionID int64) error {
	// Resolve identity + owner for journaling (and uid backfill for legacy rows).
	var (
		uid, targetKind, ownerKey      string
		sourceKind, repoName, filePath string
		rowLineStart                   int
		extractorID, polarity          string
	)
	selErr := s.db.QueryRow(`
		SELECT COALESCE(e.evidence_uid,''), e.target_kind,
		       COALESCE(n.node_key, ed.edge_key, ''),
		       e.source_kind, COALESCE(e.repo_name,''), COALESCE(e.file_path,''),
		       COALESCE(e.line_start,0), e.extractor_id, e.evidence_polarity
		FROM graph_evidence e
		LEFT JOIN graph_nodes n ON e.node_id = n.node_id
		LEFT JOIN graph_edges ed ON e.edge_id = ed.edge_id
		WHERE e.evidence_id = ?
	`, evidenceID).Scan(&uid, &targetKind, &ownerKey,
		&sourceKind, &repoName, &filePath,
		&rowLineStart, &extractorID, &polarity)
	if errors.Is(selErr, sql.ErrNoRows) {
		return nil // row absent — original behavior was a no-op UPDATE
	}
	if selErr != nil {
		return fmt.Errorf("UpdateEvidenceVerification lookup: %w", selErr)
	}
	if ownerKey == "" {
		// Evidence pointing at a missing node/edge is an integrity bug; an
		// event with an empty owner would hard-fail replay — fail loud now.
		return fmt.Errorf("UpdateEvidenceVerification: evidence %d has no owner node/edge", evidenceID)
	}

	_, err := s.db.Exec(`
		UPDATE graph_evidence
		SET evidence_status = ?,
		    verification_status = ?,
		    verification_reason = ?,
		    line_start = CASE WHEN ? > 0 THEN ? ELSE line_start END,
		    line_end = CASE WHEN ? > 0 THEN ? ELSE line_end END,
		    last_verified_revision_id = CASE WHEN ? > 0 THEN ? ELSE last_verified_revision_id END,
		    verified_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE evidence_id = ?
	`, status, verificationStatus, verificationReason,
		lineStart, lineStart, lineEnd, lineEnd,
		revisionID, revisionID,
		evidenceID)
	if err != nil {
		return err
	}

	if uid == "" {
		// Legacy row without a uid — compute from its own dedup tuple and backfill.
		uid = EvidenceKey(targetKind, ownerKey, sourceKind, repoName, filePath, rowLineStart, extractorID, polarity)
		if _, err := s.db.Exec(`UPDATE graph_evidence SET evidence_uid = ? WHERE evidence_id = ?`, uid, evidenceID); err != nil {
			return fmt.Errorf("UpdateEvidenceVerification uid backfill: %w", err)
		}
	}
	domain := DomainFromNodeKey(ownerKey)
	if targetKind == "edge" {
		domain = domainFromEdgeKey(ownerKey)
	}
	verFields := map[string]any{
		"status":              status,
		"verification_status": verificationStatus,
		"verification_reason": verificationReason,
	}
	// The live UPDATE moves line_start/line_end when > 0 — the event must
	// carry them too or replayed rows keep the stale locations.
	if lineStart > 0 {
		verFields["line_start"] = lineStart
	}
	if lineEnd > 0 {
		verFields["line_end"] = lineEnd
	}
	return s.appendEvent(journalEvent{
		DomainKey: domain, RevisionID: revisionID,
		Kind: EvEvidenceStatus, Key: uid, OwnerKey: ownerKey,
		Fields: verFields,
	})
}

// MarkEvidenceStaleByFiles marks all valid/revalidated evidence from the given file paths as stale.
// Returns the count and the affected edge/node IDs.
func (s *Store) MarkEvidenceStaleByFiles(filePaths []string) (staleCount int64, affectedEdgeIDs, affectedNodeIDs []int64, err error) {
	if len(filePaths) == 0 {
		return 0, nil, nil, nil
	}

	// Build placeholders.
	placeholders := ""
	args := make([]any, len(filePaths))
	for i, fp := range filePaths {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = fp
	}

	// Select transitioning rows first (transition-only journaling: rows already
	// stale/invalidated/superseded are excluded by the same status predicate as the UPDATE).
	type staleSubject struct {
		id          int64
		uid         string
		targetKind  string
		ownerKey    string
		sourceKind  string
		repoName    string
		filePath    string
		lineStart   int
		extractorID string
		polarity    string
	}
	selQ := `SELECT e.evidence_id, COALESCE(e.evidence_uid,''), e.target_kind,
			COALESCE(n.node_key, ed.edge_key, ''),
			e.source_kind, COALESCE(e.repo_name,''), COALESCE(e.file_path,''),
			COALESCE(e.line_start,0), e.extractor_id, e.evidence_polarity
		FROM graph_evidence e
		LEFT JOIN graph_nodes n ON e.node_id = n.node_id
		LEFT JOIN graph_edges ed ON e.edge_id = ed.edge_id
		WHERE e.file_path IN (` + placeholders + `)
		AND e.evidence_status IN ('valid','revalidated')`
	selRows, err := s.db.Query(selQ, args...)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFiles select: %w", err)
	}
	var subjects []staleSubject
	for selRows.Next() {
		var sub staleSubject
		if err := selRows.Scan(&sub.id, &sub.uid, &sub.targetKind, &sub.ownerKey,
			&sub.sourceKind, &sub.repoName, &sub.filePath,
			&sub.lineStart, &sub.extractorID, &sub.polarity); err != nil {
			selRows.Close()
			return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFiles select scan: %w", err)
		}
		subjects = append(subjects, sub)
	}
	if err := selRows.Err(); err != nil {
		selRows.Close()
		return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFiles select rows: %w", err)
	}
	selRows.Close()

	// Mark stale.
	updQ := `UPDATE graph_evidence SET evidence_status='stale'
		WHERE file_path IN (` + placeholders + `)
		AND evidence_status IN ('valid','revalidated')`
	res, err := s.db.Exec(updQ, args...)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFiles update: %w", err)
	}
	staleCount, _ = res.RowsAffected()

	// Journal one evidence_status per transitioned row (backfilling legacy NULL uids).
	for _, sub := range subjects {
		if sub.uid == "" {
			sub.uid = EvidenceKey(sub.targetKind, sub.ownerKey, sub.sourceKind, sub.repoName, sub.filePath, sub.lineStart, sub.extractorID, sub.polarity)
			if _, err := s.db.Exec(`UPDATE graph_evidence SET evidence_uid = ? WHERE evidence_id = ?`, sub.uid, sub.id); err != nil {
				return staleCount, nil, nil, fmt.Errorf("MarkEvidenceStaleByFiles uid backfill: %w", err)
			}
		}
		domain := DomainFromNodeKey(sub.ownerKey)
		if sub.targetKind == "edge" {
			domain = domainFromEdgeKey(sub.ownerKey)
		}
		if err := s.appendEvent(journalEvent{
			DomainKey: domain,
			Kind:      EvEvidenceStatus, Key: sub.uid, OwnerKey: sub.ownerKey,
			Fields: map[string]any{"status": "stale"},
		}); err != nil {
			return staleCount, nil, nil, err
		}
	}

	// Get affected edge IDs.
	edgeQ := `SELECT DISTINCT edge_id FROM graph_evidence
		WHERE file_path IN (` + placeholders + `) AND edge_id IS NOT NULL AND evidence_status='stale'`
	rows, err := s.db.Query(edgeQ, args...)
	if err != nil {
		return staleCount, nil, nil, fmt.Errorf("MarkEvidenceStaleByFiles edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		affectedEdgeIDs = append(affectedEdgeIDs, id)
	}

	// Get affected node IDs.
	nodeQ := `SELECT DISTINCT node_id FROM graph_evidence
		WHERE file_path IN (` + placeholders + `) AND node_id IS NOT NULL AND evidence_status='stale'`
	rows2, err := s.db.Query(nodeQ, args...)
	if err != nil {
		return staleCount, affectedEdgeIDs, nil, fmt.Errorf("MarkEvidenceStaleByFiles nodes: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var id int64
		rows2.Scan(&id)
		affectedNodeIDs = append(affectedNodeIDs, id)
	}

	return staleCount, affectedEdgeIDs, affectedNodeIDs, nil
}

// markEvidenceStatusForOwner moves all valid/revalidated evidence rows attached
// to one node or edge to newStatus and journals one evidence_status event per
// transitioned row (transition-only, uid backfilled for legacy rows). Status is
// recomputed from evidence after journal replay — a fact whose status was
// forced (stale-mark, merge-delete) while its evidence stayed valid recomputes
// back to active on the replay side and diverges from live. Callers:
// MarkStaleNodes/MarkStaleEdges → "stale"; node delete chokepoints → "superseded"
// (the fact's representation was replaced, e.g. external_system merged into a
// real service node).
func (s *Store) markEvidenceStatusForOwner(targetKind string, ownerID int64, ownerKey, newStatus string) error {
	ownerCol := "node_id"
	domain := DomainFromNodeKey(ownerKey)
	if targetKind == "edge" {
		ownerCol = "edge_id"
		domain = domainFromEdgeKey(ownerKey)
	}

	type staleSubject struct {
		id          int64
		uid         string
		sourceKind  string
		repoName    string
		filePath    string
		lineStart   int
		extractorID string
		polarity    string
	}
	rows, err := s.db.Query(`
		SELECT evidence_id, COALESCE(evidence_uid,''), source_kind, COALESCE(repo_name,''),
		       COALESCE(file_path,''), COALESCE(line_start,0), extractor_id, evidence_polarity
		FROM graph_evidence
		WHERE `+ownerCol+` = ? AND evidence_status IN ('valid','revalidated')`, ownerID)
	if err != nil {
		return fmt.Errorf("markEvidenceStatusForOwner select: %w", err)
	}
	var subjects []staleSubject
	for rows.Next() {
		var sub staleSubject
		if err := rows.Scan(&sub.id, &sub.uid, &sub.sourceKind, &sub.repoName,
			&sub.filePath, &sub.lineStart, &sub.extractorID, &sub.polarity); err != nil {
			rows.Close()
			return fmt.Errorf("markEvidenceStatusForOwner scan: %w", err)
		}
		subjects = append(subjects, sub)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("markEvidenceStatusForOwner rows: %w", err)
	}

	for _, sub := range subjects {
		if _, err := s.db.Exec(
			`UPDATE graph_evidence SET evidence_status=? WHERE evidence_id = ?`, newStatus, sub.id); err != nil {
			return fmt.Errorf("markEvidenceStatusForOwner update: %w", err)
		}
		if sub.uid == "" {
			sub.uid = EvidenceKey(targetKind, ownerKey, sub.sourceKind, sub.repoName, sub.filePath, sub.lineStart, sub.extractorID, sub.polarity)
			if _, err := s.db.Exec(`UPDATE graph_evidence SET evidence_uid = ? WHERE evidence_id = ?`, sub.uid, sub.id); err != nil {
				return fmt.Errorf("markEvidenceStatusForOwner uid backfill: %w", err)
			}
		}
		if err := s.appendEvent(journalEvent{
			DomainKey: domain,
			Kind:      EvEvidenceStatus, Key: sub.uid, OwnerKey: ownerKey,
			Fields: map[string]any{"status": newStatus},
		}); err != nil {
			return err
		}
	}
	return nil
}

// CountEvidenceByStatus returns counts of evidence grouped by status for a domain.
func (s *Store) CountEvidenceByStatus(domainKey string) (map[string]int, error) {
	q := `SELECT e.evidence_status, COUNT(*)
		FROM graph_evidence e
		LEFT JOIN graph_nodes n ON e.node_id = n.node_id
		LEFT JOIN graph_edges ed ON e.edge_id = ed.edge_id
		LEFT JOIN graph_nodes en ON ed.from_node_id = en.node_id
		WHERE COALESCE(n.domain_key, en.domain_key) = ?
		GROUP BY e.evidence_status`
	rows, err := s.db.Query(q, domainKey)
	if err != nil {
		return nil, fmt.Errorf("CountEvidenceByStatus: %w", err)
	}
	defer rows.Close()
	result := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		rows.Scan(&status, &count)
		result[status] = count
	}
	return result, rows.Err()
}

// CountRecentlyVerifiedEvidence counts evidence that was re-confirmed in a specific revision
// (evidence_status='valid' AND last_verified_revision_id=revisionID).
func (s *Store) CountRecentlyVerifiedEvidence(domainKey string, revisionID int64) (int, error) {
	q := `SELECT COUNT(*)
		FROM graph_evidence e
		LEFT JOIN graph_nodes n ON e.node_id = n.node_id
		LEFT JOIN graph_edges ed ON e.edge_id = ed.edge_id
		LEFT JOIN graph_nodes en ON ed.from_node_id = en.node_id
		WHERE COALESCE(n.domain_key, en.domain_key) = ?
		  AND e.evidence_status = 'valid'
		  AND e.last_verified_revision_id = ?`
	var count int
	err := s.db.QueryRow(q, domainKey, revisionID).Scan(&count)
	return count, err
}

// ListRejectedEvidence returns evidence with verification_status='rejected' for a domain.
func (s *Store) ListRejectedEvidence(domainKey string) ([]EvidenceRow, error) {
	q := `SELECT e.evidence_id, e.target_kind,
	             COALESCE(e.node_id,0), COALESCE(e.edge_id,0),
	             e.source_kind,
	             COALESCE(e.repo_name,''), COALESCE(e.file_path,''),
	             COALESCE(e.line_start,0), COALESCE(e.line_end,0),
	             COALESCE(e.column_start,0), COALESCE(e.column_end,0),
	             COALESCE(e.locator,''),
	             e.extractor_id, e.extractor_version,
	             COALESCE(e.ast_rule,''), COALESCE(e.snippet_hash,''), COALESCE(e.commit_sha,''),
	             e.observed_at, COALESCE(e.verified_at,''),
	             e.confidence, e.evidence_status, e.evidence_polarity,
	             COALESCE(e.valid_from_revision_id,0), COALESCE(e.valid_to_revision_id,0),
	             COALESCE(e.last_verified_revision_id,0), COALESCE(e.invalidated_by_revision_id,0),
	             COALESCE(e.invalidated_reason,''),
	             COALESCE(e.assertion,'{}'), COALESCE(e.assertion_kind,''), COALESCE(e.assertion_version,'v1'),
	             COALESCE(e.verification_status,'unverified'), COALESCE(e.verification_reason,''),
	             e.metadata
	      FROM graph_evidence e
	      LEFT JOIN graph_nodes n ON e.node_id = n.node_id
	      LEFT JOIN graph_edges ed ON e.edge_id = ed.edge_id
	      LEFT JOIN graph_nodes en ON ed.from_node_id = en.node_id
	      WHERE COALESCE(n.domain_key, en.domain_key, ?) = ?
	        AND e.verification_status = 'rejected'
	      ORDER BY e.evidence_id`
	rows, err := s.db.Query(q, domainKey, domainKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EvidenceRow
	for rows.Next() {
		var r EvidenceRow
		if err := rows.Scan(
			&r.EvidenceID, &r.TargetKind, &r.NodeID, &r.EdgeID,
			&r.SourceKind, &r.RepoName, &r.FilePath,
			&r.LineStart, &r.LineEnd, &r.ColumnStart, &r.ColumnEnd,
			&r.Locator, &r.ExtractorID, &r.ExtractorVersion,
			&r.ASTRule, &r.SnippetHash, &r.CommitSHA,
			&r.ObservedAt, &r.VerifiedAt,
			&r.Confidence, &r.EvidenceStatus, &r.EvidencePolarity,
			&r.ValidFromRevisionID, &r.ValidToRevisionID,
			&r.LastVerifiedRevisionID, &r.InvalidatedByRevisionID,
			&r.InvalidatedReason,
			&r.Assertion, &r.AssertionKind, &r.AssertionVersion,
			&r.VerificationStatus, &r.VerificationReason,
			&r.Metadata,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StaleFilePaths returns distinct file paths that have stale evidence.
func (s *Store) StaleFilePaths() ([]string, error) {
	q := `SELECT DISTINCT file_path FROM graph_evidence WHERE evidence_status='stale' AND file_path != ''`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("StaleFilePaths: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var fp string
		rows.Scan(&fp)
		out = append(out, fp)
	}
	return out, rows.Err()
}

// MarkEvidenceStaleByFilesVersioned is the immutable version of MarkEvidenceStaleByFiles.
// Instead of updating evidence status in place, it closes old evidence rows (sets valid_to_revision_id)
// and inserts new rows with 'stale' status.
func (s *Store) MarkEvidenceStaleByFilesVersioned(filePaths []string, revisionID, contextID int64) (staleCount int64, affectedNodeIDs, affectedEdgeIDs []int64, err error) {
	if len(filePaths) == 0 {
		return 0, nil, nil, nil
	}

	// Build placeholders.
	placeholders := ""
	args := make([]any, len(filePaths))
	for i, fp := range filePaths {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = fp
	}

	// SELECT all current valid evidence rows from those files (owner key joined for journaling).
	selQ := `SELECT e.evidence_id, e.target_kind,
	         COALESCE(e.node_id,0), COALESCE(e.edge_id,0),
	         e.source_kind,
	         COALESCE(e.repo_name,''), COALESCE(e.file_path,''),
	         COALESCE(e.line_start,0), COALESCE(e.line_end,0),
	         COALESCE(e.column_start,0), COALESCE(e.column_end,0),
	         COALESCE(e.locator,''),
	         e.extractor_id, e.extractor_version,
	         COALESCE(e.ast_rule,''), COALESCE(e.snippet_hash,''), COALESCE(e.commit_sha,''),
	         e.confidence, e.evidence_polarity,
	         COALESCE(e.evidence_uid,''),
	         COALESCE(e.metadata,'{}'),
	         COALESCE(n.node_key, ed.edge_key, '')
	    FROM graph_evidence e
	    LEFT JOIN graph_nodes n ON e.node_id = n.node_id
	    LEFT JOIN graph_edges ed ON e.edge_id = ed.edge_id
	    WHERE e.file_path IN (` + placeholders + `)
	      AND e.evidence_status IN ('valid','revalidated')
	      AND (e.valid_to_revision_id IS NULL OR e.valid_to_revision_id = 0)`

	rows, err := s.db.Query(selQ, args...)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFilesVersioned select: %w", err)
	}
	defer rows.Close()

	type evidenceInfo struct {
		id               int64
		targetKind       string
		nodeID, edgeID   int64
		sourceKind       string
		repoName         string
		filePath         string
		lineStart        int
		lineEnd          int
		columnStart      int
		columnEnd        int
		locator          string
		extractorID      string
		extractorVersion string
		astRule          string
		snippetHash      string
		commitSHA        string
		confidence       float64
		polarity         string
		evidenceUID      string
		metadata         string
		ownerKey         string
	}

	var found []evidenceInfo
	for rows.Next() {
		var e evidenceInfo
		if err := rows.Scan(
			&e.id, &e.targetKind, &e.nodeID, &e.edgeID,
			&e.sourceKind, &e.repoName, &e.filePath,
			&e.lineStart, &e.lineEnd, &e.columnStart, &e.columnEnd,
			&e.locator, &e.extractorID, &e.extractorVersion,
			&e.astRule, &e.snippetHash, &e.commitSHA,
			&e.confidence, &e.polarity, &e.evidenceUID,
			&e.metadata, &e.ownerKey,
		); err != nil {
			return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFilesVersioned scan: %w", err)
		}
		found = append(found, e)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFilesVersioned rows: %w", err)
	}

	nodeSet := map[int64]bool{}
	edgeSet := map[int64]bool{}

	for _, e := range found {
		// Resolve stable identity; backfill legacy rows so the superseding row
		// carries the SAME uid as the row it replaces.
		if e.evidenceUID == "" {
			e.evidenceUID = EvidenceKey(e.targetKind, e.ownerKey, e.sourceKind, e.repoName, e.filePath, e.lineStart, e.extractorID, e.polarity)
			if _, err := s.db.Exec(`UPDATE graph_evidence SET evidence_uid = ? WHERE evidence_id = ?`, e.evidenceUID, e.id); err != nil {
				return staleCount, nil, nil, fmt.Errorf("MarkEvidenceStaleByFilesVersioned uid backfill: %w", err)
			}
		}

		// Close old row.
		_, err := s.db.Exec(`UPDATE graph_evidence SET valid_to_revision_id = ? WHERE evidence_id = ?`,
			revisionID, e.id)
		if err != nil {
			return staleCount, nil, nil, fmt.Errorf("MarkEvidenceStaleByFilesVersioned close: %w", err)
		}

		// Insert new stale version.
		var nodeID, edgeID *int64
		if e.targetKind == "node" && e.nodeID != 0 {
			nodeID = &e.nodeID
		}
		if e.targetKind == "edge" && e.edgeID != 0 {
			edgeID = &e.edgeID
		}

		const insQ = `
			INSERT INTO graph_evidence
			  (target_kind, node_id, edge_id, source_kind, repo_name, file_path,
			   line_start, line_end, column_start, column_end, locator,
			   extractor_id, extractor_version, ast_rule, snippet_hash, commit_sha,
			   confidence, evidence_status, evidence_polarity, evidence_uid,
			   valid_from_revision_id, context_id,
			   metadata)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`
		_, err = s.db.Exec(insQ,
			e.targetKind, nodeID, edgeID,
			e.sourceKind,
			nullableStr(e.repoName), nullableStr(e.filePath),
			nullableInt(e.lineStart), nullableInt(e.lineEnd),
			nullableInt(e.columnStart), nullableInt(e.columnEnd),
			nullableStr(e.locator),
			e.extractorID, e.extractorVersion,
			nullableStr(e.astRule), nullableStr(e.snippetHash), nullableStr(e.commitSHA),
			e.confidence, "stale", e.polarity, nullableStr(e.evidenceUID),
			revisionID, nullableInt64(contextID),
			e.metadata,
		)
		if err != nil {
			return staleCount, nil, nil, fmt.Errorf("MarkEvidenceStaleByFilesVersioned insert: %w", err)
		}

		// Journal the transition (transition-only: select already excluded stale rows).
		domain := DomainFromNodeKey(e.ownerKey)
		if e.targetKind == "edge" {
			domain = domainFromEdgeKey(e.ownerKey)
		}
		if err := s.appendEvent(journalEvent{
			DomainKey: domain, RevisionID: revisionID,
			Kind: EvEvidenceStatus, Key: e.evidenceUID, OwnerKey: e.ownerKey,
			Fields: map[string]any{"status": "stale"},
		}); err != nil {
			return staleCount, nil, nil, err
		}

		staleCount++

		if e.nodeID != 0 {
			nodeSet[e.nodeID] = true
		}
		if e.edgeID != 0 {
			edgeSet[e.edgeID] = true
		}
	}

	for id := range nodeSet {
		affectedNodeIDs = append(affectedNodeIDs, id)
	}
	for id := range edgeSet {
		affectedEdgeIDs = append(affectedEdgeIDs, id)
	}

	return staleCount, affectedNodeIDs, affectedEdgeIDs, nil
}

// nullableInt returns nil for zero ints so they're stored as NULL.
func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// nullableInt64 returns nil for zero int64s so they're stored as NULL.
func nullableInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

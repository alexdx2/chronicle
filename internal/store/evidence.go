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
		// Update existing — if it was stale, mark as revalidated; otherwise keep valid.
		newStatus := "valid"
		var oldStatus string
		s.db.QueryRow("SELECT evidence_status FROM graph_evidence WHERE evidence_id=?", existingID).Scan(&oldStatus)
		if oldStatus == "stale" {
			newStatus = "revalidated"
		}

		const updQ = `
			UPDATE graph_evidence
			SET observed_at=strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    confidence=?,
			    commit_sha=?,
			    extractor_version=?,
			    evidence_status=?,
			    last_verified_revision_id=?
			WHERE evidence_id=?
		`
		_, err = s.db.Exec(updQ, e.Confidence, nullableStr(e.CommitSHA), e.ExtractorVersion,
			newStatus, nullableInt64(e.ValidFromRevisionID), existingID)
		if err != nil {
			return 0, fmt.Errorf("AddEvidence update: %w", err)
		}
		return existingID, nil
	}

	// Insert new.
	status := e.EvidenceStatus
	if status == "" {
		status = "valid"
	}

	const insQ = `
		INSERT INTO graph_evidence
		  (target_kind, node_id, edge_id, source_kind, repo_name, file_path,
		   line_start, line_end, column_start, column_end, locator,
		   extractor_id, extractor_version, ast_rule, snippet_hash, commit_sha,
		   confidence, evidence_status, evidence_polarity,
		   valid_from_revision_id, last_verified_revision_id,
		   context_id, evidence_uid,
		   metadata)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
		nullableInt64(e.ContextID), nullableStr(e.EvidenceUID),
		e.Metadata,
	)
	if err != nil {
		return 0, fmt.Errorf("AddEvidence insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
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
			&r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("listEvidence scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
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

	// Mark stale.
	updQ := `UPDATE graph_evidence SET evidence_status='stale'
		WHERE file_path IN (` + placeholders + `)
		AND evidence_status IN ('valid','revalidated')`
	res, err := s.db.Exec(updQ, args...)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFiles update: %w", err)
	}
	staleCount, _ = res.RowsAffected()

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

	// SELECT all current valid evidence rows from those files.
	selQ := `SELECT evidence_id, target_kind,
	         COALESCE(node_id,0), COALESCE(edge_id,0),
	         source_kind,
	         COALESCE(repo_name,''), COALESCE(file_path,''),
	         COALESCE(line_start,0), COALESCE(line_end,0),
	         COALESCE(column_start,0), COALESCE(column_end,0),
	         COALESCE(locator,''),
	         extractor_id, extractor_version,
	         COALESCE(ast_rule,''), COALESCE(snippet_hash,''), COALESCE(commit_sha,''),
	         confidence, evidence_polarity,
	         COALESCE(evidence_uid,''),
	         COALESCE(metadata,'{}')
	    FROM graph_evidence
	    WHERE file_path IN (` + placeholders + `)
	      AND evidence_status IN ('valid','revalidated')
	      AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)`

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
			&e.metadata,
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

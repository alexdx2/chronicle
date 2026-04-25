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

	// Check for duplicate.
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
		LIMIT 1
	`
	var existingID int64
	err := s.db.QueryRow(dedupQ,
		e.TargetKind, nodeID, edgeID,
		e.SourceKind,
		e.RepoName, e.FilePath, e.LineStart,
		e.ExtractorID,
	).Scan(&existingID)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("AddEvidence dedup lookup: %w", err)
	}

	if err == nil {
		// Update existing.
		const updQ = `
			UPDATE graph_evidence
			SET observed_at=strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    confidence=?,
			    commit_sha=?,
			    extractor_version=?
			WHERE evidence_id=?
		`
		_, err = s.db.Exec(updQ, e.Confidence, nullableStr(e.CommitSHA), e.ExtractorVersion, existingID)
		if err != nil {
			return 0, fmt.Errorf("AddEvidence update: %w", err)
		}
		return existingID, nil
	}

	// Insert new.
	const insQ = `
		INSERT INTO graph_evidence
		  (target_kind, node_id, edge_id, source_kind, repo_name, file_path,
		   line_start, line_end, column_start, column_end, locator,
		   extractor_id, extractor_version, ast_rule, snippet_hash, commit_sha,
		   confidence, metadata)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
		e.Confidence, e.Metadata,
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
		       confidence, metadata
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
			&r.Confidence, &r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("listEvidence scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// nullableInt returns nil for zero ints so they're stored as NULL.
func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

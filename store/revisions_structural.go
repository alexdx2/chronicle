package store

import "fmt"

// Revision selection for the structural refresh — the deterministic phase that
// re-extracts imports, routes, models and calls on every commit. It gets its
// own pointer because it succeeds and fails independently of the evidence
// re-verification that runs beside it: a verification that landed at B and a
// structural phase that died at A must leave verified@B and structured@A, and
// the next run must diff A..HEAD. One pointer for both would silently claim
// structure over the commits only the verifier saw.

// revisionKindExpr and revisionCompleteExpr read metadata defensively, the way
// revisionLayerExpr does: json_extract raises on malformed JSON, and CASE is
// documented to short-circuit, so a row somebody wrote badly reads as "not
// structural" instead of failing the query for every other row.
const revisionKindExpr = `CASE WHEN json_valid(metadata) THEN json_extract(metadata,'$.kind') ELSE NULL END`

// SQLite's JSON1 maps a JSON true to the integer 1, so `= 1` is the check for
// `"complete": true` and also accepts a writer that stored 1 directly.
const revisionCompleteExpr = `CASE WHEN json_valid(metadata) THEN json_extract(metadata,'$.complete') ELSE NULL END`

// LatestStructuralRevision is the newest completed structural phase of a
// domain (empty domainKey = any domain): the commit up to which deterministic
// structure is guaranteed. ErrNotFound when the domain has none.
//
// Two conditions, both load-bearing. metadata.kind = "structural" keeps plain
// post-commit refreshes (which only re-verify what is already there) from
// claiming structural coverage. metadata.complete = true keeps an interrupted
// phase from claiming it either: the pointer means "the whole diff was
// processed", and a run that stopped halfway processed an unknown prefix of
// it. Whatever a failed run wrote stays in the graph; it just does not move
// the claim.
func (s *Store) LatestStructuralRevision(domainKey string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE trigger_kind = 'git_hook'
	        AND ` + revisionKindExpr + ` = 'structural'
	        AND ` + revisionCompleteExpr + ` = 1`
	var args []any
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY revision_id DESC LIMIT 1`
	return s.oneRevision("LatestStructuralRevision", q, args...)
}

// CountFilesOnOtherExtractorVersion counts the distinct files whose live
// evidence from one deterministic extractor was written by a different version
// of it — the rules-pack backlog. When the pack changes, those files need
// re-extraction even though nothing in them changed, and until the backlog is
// empty the structural claim does not cover them.
//
// Only live rows count ('valid', 'revalidated'): a superseded or invalidated
// row is already not part of what the graph asserts, so re-extracting its file
// on account of it would be work with no claim behind it.
func (s *Store) CountFilesOnOtherExtractorVersion(domainKey, extractorID, version string) (int, error) {
	// Evidence carries no domain of its own — it belongs to the node or edge
	// it is evidence for, the same join CountCodeEvidenceByStatus uses.
	const q = `SELECT COUNT(DISTINCT e.file_path)
		FROM graph_evidence e
		LEFT JOIN graph_nodes n ON e.node_id = n.node_id
		LEFT JOIN graph_edges ed ON e.edge_id = ed.edge_id
		LEFT JOIN graph_nodes en ON ed.from_node_id = en.node_id
		WHERE COALESCE(n.domain_key, en.domain_key) = ?
		  AND e.extractor_id = ?
		  AND e.extractor_version != ?
		  AND e.evidence_status IN ('valid','revalidated')
		  AND COALESCE(e.file_path,'') != ''`
	var n int
	if err := s.db.QueryRow(q, domainKey, extractorID, version).Scan(&n); err != nil {
		return 0, fmt.Errorf("CountFilesOnOtherExtractorVersion: %w", err)
	}
	return n, nil
}

package store

// CoverageReport summarizes the evidence-first acceptance state.
type CoverageReport struct {
	NodesWithoutEvidence int `json:"nodes_without_evidence"`
	EdgesWithoutEvidence int `json:"edges_without_evidence"`
	IncompleteEvidence   int `json:"incomplete_evidence"`
}

// EvidenceCoverageReport counts active current-version entities without any
// evidence row, and evidence rows missing required fields (extractor_id,
// assertion_kind, source_kind, locator-or-reason).
func (s *Store) EvidenceCoverageReport(domainKey string) (*CoverageReport, error) {
	rep := &CoverageReport{}
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM graph_nodes n
		WHERE n.status = 'active' AND n.domain_key = ?
		  AND (n.valid_to_revision_id IS NULL OR n.valid_to_revision_id = 0)
		  AND NOT EXISTS (SELECT 1 FROM graph_evidence e WHERE e.node_id = n.node_id)`,
		domainKey).Scan(&rep.NodesWithoutEvidence)
	if err != nil {
		return nil, err
	}
	// An edge belongs to its source node's domain (same convention as
	// domainFromEdgeKey) — count only edges in the requested domain.
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM graph_edges ed
		JOIN graph_nodes n ON ed.from_node_id = n.node_id
		WHERE ed.active = 1
		  AND n.domain_key = ?
		  AND (ed.valid_to_revision_id IS NULL OR ed.valid_to_revision_id = 0)
		  AND NOT EXISTS (SELECT 1 FROM graph_evidence e WHERE e.edge_id = ed.edge_id)`,
		domainKey).Scan(&rep.EdgesWithoutEvidence)
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM graph_evidence
		WHERE COALESCE(extractor_id,'') = ''
		   OR COALESCE(source_kind,'') = ''
		   OR COALESCE(assertion_kind,'') = ''
		   OR (COALESCE(locator,'') = '' AND COALESCE(file_path,'') = ''
		       AND COALESCE(json_extract(metadata,'$.no_locator_reason'),'') = '')`,
	).Scan(&rep.IncompleteEvidence)
	if err != nil {
		return nil, err
	}
	return rep, nil
}

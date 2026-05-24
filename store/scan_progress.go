package store

// ScanProgressSummary aggregates extraction state for the active scan.
type ScanProgressSummary struct {
	Phase          string         `json:"phase"`
	TotalFiles     int            `json:"total_files"`
	ExtractedFiles int            `json:"extracted_files"`
	RemainingFiles int            `json:"remaining_files"`
	FactsByKind    map[string]int `json:"facts_by_kind"`
	NodesByLayer   map[string]int `json:"nodes_by_layer"`
	NodesByType    map[string]int `json:"nodes_by_type"`
	EdgesByType    map[string]int `json:"edges_by_type"`
	EdgesByDeriv   map[string]int `json:"edges_by_derivation"`
	TotalNodes     int            `json:"total_nodes"`
	TotalEdges     int            `json:"total_edges"`
	TotalEvidence  int            `json:"total_evidence"`
}

// GetScanProgress returns the current scan state for a domain.
func (s *Store) GetScanProgress(domainKey string) (*ScanProgressSummary, error) {
	result := &ScanProgressSummary{
		FactsByKind:  make(map[string]int),
		NodesByLayer: make(map[string]int),
		NodesByType:  make(map[string]int),
		EdgesByType:  make(map[string]int),
		EdgesByDeriv: make(map[string]int),
	}

	// Active scan run
	run, err := s.GetActiveScanRun(domainKey)
	if err == nil && run != nil {
		result.Phase = run.Phase
		result.TotalFiles = run.TotalFiles
		pending, _ := s.CountPendingObligations(run.RevisionID, "scan_file")
		result.ExtractedFiles = run.TotalFiles - pending
		result.RemainingFiles = pending

		// Count facts by kind from extractions table
		rows, err := s.db.Query(`
			SELECT facts_json FROM scan_extractions
			WHERE revision_id = ? AND status = 'extracted'
		`, run.RevisionID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var factsJSON string
				rows.Scan(&factsJSON)
				countFactKinds(factsJSON, result.FactsByKind)
			}
		}
	} else {
		result.Phase = "idle"
	}

	// Current graph totals
	nodeRows, err := s.db.Query(`
		SELECT layer, node_type, COUNT(*) FROM graph_nodes
		WHERE status = 'active'
		GROUP BY layer, node_type
	`)
	if err == nil {
		defer nodeRows.Close()
		for nodeRows.Next() {
			var layer, nodeType string
			var count int
			nodeRows.Scan(&layer, &nodeType, &count)
			result.NodesByLayer[layer] += count
			result.NodesByType[nodeType] += count
			result.TotalNodes += count
		}
	}

	edgeRows, err := s.db.Query(`
		SELECT edge_type, derivation_kind, COUNT(*) FROM graph_edges
		WHERE active = 1
		GROUP BY edge_type, derivation_kind
	`)
	if err == nil {
		defer edgeRows.Close()
		for edgeRows.Next() {
			var edgeType, deriv string
			var count int
			edgeRows.Scan(&edgeType, &deriv, &count)
			result.EdgesByType[edgeType] += count
			result.EdgesByDeriv[deriv] += count
			result.TotalEdges += count
		}
	}

	// Evidence count
	s.db.QueryRow(`SELECT COUNT(*) FROM graph_evidence WHERE evidence_status = 'valid'`).Scan(&result.TotalEvidence)

	return result, nil
}

// countFactKinds parses facts JSON and increments kind counts via string scanning.
func countFactKinds(factsJSON string, counts map[string]int) {
	needle := `"kind":"`
	i := 0
	for i < len(factsJSON) {
		idx := indexOfStr(factsJSON[i:], needle)
		if idx < 0 {
			break
		}
		start := i + idx + len(needle)
		end := indexOfStr(factsJSON[start:], `"`)
		if end < 0 {
			break
		}
		kind := factsJSON[start : start+end]
		counts[kind]++
		i = start + end + 1
	}
}

func indexOfStr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

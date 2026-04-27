package graph

// InvalidateResult is returned by InvalidateChanged.
type InvalidateResult struct {
	StaleEvidence int      `json:"stale_evidence"`
	AffectedEdges int      `json:"affected_edges"`
	AffectedNodes int      `json:"affected_nodes"`
	FilesToRescan []string `json:"files_to_rescan"`
}

// FinalizeResult is returned by FinalizeIncrementalScan.
type FinalizeResult struct {
	Revalidated        int `json:"revalidated"`
	StillStale         int `json:"still_stale"`
	Contradicted       int `json:"contradicted"`
	EdgesStatusChanged int `json:"edges_status_changed"`
}

// InvalidateChanged marks evidence from changed files as stale and recalculates trust.
func (g *Graph) InvalidateChanged(domainKey string, revisionID int64, changedFiles []string) (*InvalidateResult, error) {
	if len(changedFiles) == 0 {
		return &InvalidateResult{}, nil
	}

	staleCount, affectedEdgeIDs, affectedNodeIDs, err := g.store.MarkEvidenceStaleByFiles(changedFiles)
	if err != nil {
		return nil, err
	}

	// Recalculate trust for all affected entities.
	for _, edgeID := range affectedEdgeIDs {
		if err := g.RecalculateEdgeTrust(edgeID); err != nil {
			return nil, err
		}
	}
	for _, nodeID := range affectedNodeIDs {
		if err := g.RecalculateNodeTrust(nodeID); err != nil {
			return nil, err
		}
	}

	// Get files to rescan.
	filesToRescan, err := g.store.StaleFilePaths()
	if err != nil {
		return nil, err
	}

	return &InvalidateResult{
		StaleEvidence: int(staleCount),
		AffectedEdges: len(affectedEdgeIDs),
		AffectedNodes: len(affectedNodeIDs),
		FilesToRescan: filesToRescan,
	}, nil
}

// FinalizeIncrementalScan completes an incremental scan.
// Stale evidence stays stale (not auto-invalidated). Only counts stats and recalculates trust.
func (g *Graph) FinalizeIncrementalScan(domainKey string, revisionID int64) (*FinalizeResult, error) {
	counts, err := g.store.CountEvidenceByStatus(domainKey)
	if err != nil {
		return nil, err
	}

	return &FinalizeResult{
		Revalidated:  counts["revalidated"],
		StillStale:   counts["stale"],
		Contradicted: counts["invalidated"],
	}, nil
}

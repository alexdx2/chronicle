package graph

import "github.com/alexdx2/chronicle-core/store"

// InvalidateResult is returned by InvalidateChanged.
type InvalidateResult struct {
	StaleEvidence    int                `json:"stale_evidence"`
	AffectedEdges    int                `json:"affected_edges"`
	AffectedNodes    int                `json:"affected_nodes"`
	FilesToRescan    []string           `json:"files_to_rescan"`
	AutoVerified     []VerifyFileResult `json:"auto_verified,omitempty"`
	NeedsClaude      []string           `json:"needs_claude,omitempty"`
}

// FinalizeResult is returned by FinalizeIncrementalScan.
type FinalizeResult struct {
	ScanStatus         string              `json:"scan_status"` // clean, review_required, incomplete
	Revalidated        int                 `json:"revalidated"`
	StillStale         int                 `json:"still_stale"`
	Contradicted       int                 `json:"contradicted"`
	EdgesStatusChanged int                 `json:"edges_status_changed"`
	UncoveredFiles     []UncoveredFile     `json:"uncovered_files,omitempty"`
	NeedsReviewEdges   []NeedsReviewEdge   `json:"needs_review_edges,omitempty"`
	RejectedEvidence   []RejectedEvidence  `json:"rejected_evidence,omitempty"`
	Obligations        ObligationSummary   `json:"obligations"`
}

// RejectedEvidence is evidence that failed verification at creation time.
// Claude claimed something that the verifier couldn't confirm.
type RejectedEvidence struct {
	EvidenceID    int64  `json:"evidence_id"`
	EdgeKey       string `json:"edge_key,omitempty"`
	NodeKey       string `json:"node_key,omitempty"`
	FilePath      string `json:"file_path"`
	AssertionKind string `json:"assertion_kind"`
	Reason        string `json:"reason"`
	Action        string `json:"action"` // "re-read file and verify", "remove edge", "update assertion"
}

// UncoveredFile is a file with stale evidence that was not verified or rescanned.
type UncoveredFile struct {
	File               string `json:"file"`
	Reason             string `json:"reason"`
	StaleEvidenceCount int    `json:"stale_evidence_count"`
}

// NeedsReviewEdge is an edge that lost all valid evidence.
type NeedsReviewEdge struct {
	EdgeKey         string `json:"edge_key"`
	Reason          string `json:"reason"`
	LastValidRevision int64 `json:"last_valid_revision,omitempty"`
}

// ObligationSummary counts obligation statuses.
type ObligationSummary struct {
	Total     int `json:"total"`
	Satisfied int `json:"satisfied"`
	Open      int `json:"open"`
	Deferred  int `json:"deferred"`
	Skipped   int `json:"skipped"`
}

// InvalidateChanged marks evidence from changed files as stale (legacy, non-versioned).
func (g *Graph) InvalidateChanged(domainKey string, revisionID int64, changedFiles []string) (*InvalidateResult, error) {
	return g.InvalidateChangedInContext(domainKey, revisionID, changedFiles, 0)
}

// InvalidateChangedInContext marks evidence from changed files as stale, then
// auto-verifies files where mechanical verifiers can check assertions.
func (g *Graph) InvalidateChangedInContext(domainKey string, revisionID int64, changedFiles []string, contextID int64) (*InvalidateResult, error) {
	if len(changedFiles) == 0 {
		return &InvalidateResult{}, nil
	}

	var staleCount int64
	var affectedEdgeIDs, affectedNodeIDs []int64
	var err error

	if contextID > 0 {
		var nodeIDs, edgeIDs []int64
		staleCount, nodeIDs, edgeIDs, err = g.store.MarkEvidenceStaleByFilesVersioned(changedFiles, revisionID, contextID)
		affectedNodeIDs = nodeIDs
		affectedEdgeIDs = edgeIDs
	} else {
		staleCount, affectedEdgeIDs, affectedNodeIDs, err = g.store.MarkEvidenceStaleByFiles(changedFiles)
	}
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

	// Create verify_file obligations for the changed files (not all stale files from prior revisions).
	for _, fp := range changedFiles {
		_, _ = g.store.CreateObligation(revisionID, domainKey, "verify_file", fp, "stale evidence from changed files")
	}

	// Auto-verify: run mechanical verifiers on changed files immediately.
	var autoVerified []VerifyFileResult
	var needsClaude []string

	for _, fp := range changedFiles {
		result, verifyErr := g.VerifyFileEvidence(fp, revisionID, domainKey)
		if verifyErr != nil {
			// File can't be verified mechanically — Claude needs to read it
			needsClaude = append(needsClaude, fp)
			continue
		}
		if result.Summary.Checked == 0 {
			continue // no stale evidence in this file
		}
		autoVerified = append(autoVerified, *result)
		// If anything is unresolved (missing/ambiguous/unsupported), Claude needs this file
		if result.Summary.Missing > 0 || result.Summary.Ambiguous > 0 || result.Summary.Unsupported > 0 {
			needsClaude = append(needsClaude, fp)
		}
	}

	return &InvalidateResult{
		StaleEvidence: int(staleCount),
		AffectedEdges: len(affectedEdgeIDs),
		AffectedNodes: len(affectedNodeIDs),
		FilesToRescan: filesToRescan,
		AutoVerified:  autoVerified,
		NeedsClaude:   needsClaude,
	}, nil
}

// FinalizeIncrementalScan completes an incremental scan.
// Reports scan status, uncovered files, needs_review edges, and obligation state.
func (g *Graph) FinalizeIncrementalScan(domainKey string, revisionID int64) (*FinalizeResult, error) {
	counts, err := g.store.CountEvidenceByStatus(domainKey)
	if err != nil {
		return nil, err
	}

	// Count revalidated evidence (both legacy 'revalidated' rows + recently verified)
	revalidated := counts["revalidated"]
	if revisionID > 0 {
		verified, _ := g.store.CountRecentlyVerifiedEvidence(domainKey, revisionID)
		revalidated += verified
	}

	result := &FinalizeResult{
		Revalidated:  revalidated,
		StillStale:   counts["stale"],
		Contradicted: counts["invalidated"],
	}

	// Check obligations
	if revisionID > 0 {
		allObligations, _ := g.store.ListAllObligations(revisionID)
		for _, ob := range allObligations {
			result.Obligations.Total++
			switch ob.Status {
			case "satisfied":
				result.Obligations.Satisfied++
			case "open":
				result.Obligations.Open++
			case "deferred":
				result.Obligations.Deferred++
			case "skipped":
				result.Obligations.Skipped++
			}
		}

		// Find uncovered files (open verify_file obligations)
		openObligations, _ := g.store.ListOpenObligations(revisionID)
		for _, ob := range openObligations {
			if ob.ObligationType == "verify_file" {
				staleCount := countStaleEvidenceForFile(g.store, ob.TargetKey)
				result.UncoveredFiles = append(result.UncoveredFiles, UncoveredFile{
					File:               ob.TargetKey,
					Reason:             "verify_file obligation still open",
					StaleEvidenceCount: staleCount,
				})
			}
		}
	}

	// Find edges with no valid positive evidence (needs_review candidates)
	needsReview, _ := g.store.GetEdgesWithNoValidEvidence(domainKey)
	for _, edge := range needsReview {
		g.store.UpdateEdgeVerificationStatus(edge.EdgeKey, "needs_review")
		result.NeedsReviewEdges = append(result.NeedsReviewEdges, NeedsReviewEdge{
			EdgeKey:           edge.EdgeKey,
			Reason:            "all positive evidence stale or missing",
			LastValidRevision: edge.LastSeenRevisionID,
		})
	}
	result.EdgesStatusChanged = len(needsReview)

	// Find rejected evidence (Claude hallucinations caught at creation time)
	rejected, _ := g.store.ListRejectedEvidence(domainKey)
	for _, ev := range rejected {
		edgeKey := ""
		if ev.EdgeID > 0 {
			edges, _ := g.store.ListEdges(store.EdgeFilter{})
			for _, e := range edges {
				if e.EdgeID == ev.EdgeID {
					edgeKey = e.EdgeKey
					break
				}
			}
		}
		result.RejectedEvidence = append(result.RejectedEvidence, RejectedEvidence{
			EvidenceID:    ev.EvidenceID,
			EdgeKey:       edgeKey,
			FilePath:      ev.FilePath,
			AssertionKind: ev.AssertionKind,
			Reason:        ev.VerificationReason,
			Action:        "re-read file and verify — assertion was not found at creation time",
		})
	}

	// Compute scan status
	switch {
	case result.Obligations.Open == 0 && len(result.NeedsReviewEdges) == 0 && len(result.RejectedEvidence) == 0:
		result.ScanStatus = "clean"
	case result.Obligations.Open == 0 && (len(result.NeedsReviewEdges) > 0 || len(result.RejectedEvidence) > 0):
		result.ScanStatus = "review_required"
	default:
		result.ScanStatus = "incomplete"
	}

	return result, nil
}

func countStaleEvidenceForFile(s *store.Store, filePath string) int {
	rows, err := s.ListStaleEvidenceByFile(filePath)
	if err != nil {
		return 0
	}
	return len(rows)
}

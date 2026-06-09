package mcp

import (
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
)

// applyCheckpointAnswer performs the scan-run phase transition for a checkpoint
// answer. Shared by chronicle_scan_confirm (human) and the autopilot path in
// chronicle_scan_next_file. Returns the same response map scan_confirm returns.
func applyCheckpointAnswer(g *graph.Graph, runID int64, checkpointID, answer string) (map[string]any, error) {
	switch checkpointID {
	case "scope":
		lower := strings.ToLower(answer)
		if strings.Contains(lower, "yes") || strings.Contains(lower, "ok") ||
			strings.Contains(lower, "confirm") || strings.Contains(lower, "proceed") || answer == "1" {
			run, err := g.Store().GetScanRun(runID)
			if err != nil {
				return nil, fmt.Errorf("get scan run: %w", err)
			}
			if run.Phase != "confirm_scope" {
				return nil, fmt.Errorf("scan run %d is in phase %q, expected confirm_scope — re-run chronicle_discover_files", runID, run.Phase)
			}
			// User confirmed — transition to extraction (preserve total_files from confirm_scope)
			if err := g.Store().TransitionScanRun(runID, "phase1_extract", 0); err != nil {
				return nil, fmt.Errorf("transition to phase1_extract: %w", err)
			}
			return map[string]any{
				"status":            "confirmed",
				"message":           "Scope confirmed. Call chronicle_scan_next_file to start extraction.",
				"total_obligations": run.TotalFiles,
			}, nil
		}
		// User wants changes — keep in confirm_scope, they'll re-discover
		return map[string]any{
			"status":  "needs_change",
			"message": "Update the manifest with chronicle_save_manifest, then re-run chronicle_discover_files.",
		}, nil

	case "phase1_review":
		run, err := g.Store().GetScanRun(runID)
		if err != nil {
			return nil, fmt.Errorf("get scan run: %w", err)
		}
		if run.Phase != "phase1_review" {
			return nil, fmt.Errorf("scan run %d is in phase %q, expected phase1_review", runID, run.Phase)
		}
		answerLower := strings.ToLower(answer)
		if strings.Contains(answerLower, "re-review") || strings.Contains(answerLower, "retry") || strings.Contains(answerLower, "again") {
			candidates, _ := g.ScanReviewCandidates(run.DomainKey, run.RevisionID)
			for _, c := range candidates {
				_, _ = g.Store().CreateObligation(run.RevisionID, run.DomainKey, "scan_file", c.FilePath, "phase1_review: "+c.Reason)
			}
			if err := g.Store().TransitionScanRun(runID, "phase1_extract", 0); err != nil {
				return nil, fmt.Errorf("transition to phase1_extract: %w", err)
			}
			return map[string]any{
				"status":            "re_review",
				"message":           "Re-extract review candidates, then resolve again.",
				"review_candidates": len(candidates),
			}, nil
		}
		if strings.Contains(answerLower, "proceed") || strings.Contains(answerLower, "yes") ||
			strings.Contains(answerLower, "ok") || strings.Contains(answerLower, "confirm") || answer == "1" {
			if err := g.Store().TransitionScanRun(runID, "endpoint_reconcile", 0); err != nil {
				return nil, fmt.Errorf("transition to endpoint_reconcile: %w", err)
			}
			return map[string]any{
				"status":  "confirmed",
				"message": "Review gate passed. Call chronicle_scan_next_file for endpoint reconciliation.",
			}, nil
		}
		return map[string]any{
			"status":  "needs_answer",
			"message": "Answer 'proceed' to continue or 're-review files' to re-extract flagged files.",
		}, nil

	case "phase1_summary":
		_, err := g.Store().GetScanRun(runID)
		if err != nil {
			return nil, fmt.Errorf("get scan run: %w", err)
		}
		answerLower := strings.ToLower(answer)
		// "skip" MUST be checked before the yes/ok/confirm/proceed block — order matters.
		if strings.Contains(answerLower, "skip") {
			if err := g.Store().CompleteScanRun(runID); err != nil {
				return nil, fmt.Errorf("complete scan run: %w", err)
			}
			return map[string]any{
				"status":  "skipped",
				"message": "Flow tracing skipped. Scan complete.",
			}, nil
		}
		if strings.Contains(answerLower, "yes") || strings.Contains(answerLower, "ok") ||
			strings.Contains(answerLower, "confirm") || strings.Contains(answerLower, "proceed") || answer == "1" {
			run, err := g.Store().GetScanRun(runID)
			if err != nil {
				return nil, fmt.Errorf("get scan run: %w", err)
			}
			if run.Phase != "phase2_confirm" {
				return nil, fmt.Errorf("scan run %d is in phase %q, expected phase2_confirm", runID, run.Phase)
			}
			if err := g.Store().TransitionScanRun(runID, "phase2_extract", 0); err != nil {
				return nil, fmt.Errorf("transition to phase2_extract: %w", err)
			}
			return map[string]any{
				"status":  "confirmed",
				"message": "Flow tracing confirmed. Call chronicle_scan_next_file to trace flows.",
			}, nil
		}
		return map[string]any{
			"status":  "needs_answer",
			"message": "Answer 'yes' to trace flows or 'skip flows' to finish without phase 2.",
		}, nil

	default:
		return nil, fmt.Errorf("unknown checkpoint: %s", checkpointID)
	}
}

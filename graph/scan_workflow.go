package graph

// ScanAction is what chronicle_scan_next_file returns to Claude.
type ScanAction struct {
	ScanRunID int64         `json:"scan_run_id,omitempty"`
	Phase     string        `json:"phase"`
	Action    string        `json:"action"` // start_scan, extract_files, call_resolve_extractions, discover_files, none
	Files     []string      `json:"files,omitempty"`
	Blocked   bool          `json:"blocked,omitempty"`
	Reason    string        `json:"reason,omitempty"`
	Done      bool          `json:"done,omitempty"`
	Progress  *ScanProgress `json:"progress,omitempty"`
}

// ScanProgress tracks extraction progress within a scan run.
type ScanProgress struct {
	Total     int `json:"total"`
	Extracted int `json:"extracted"`
	Remaining int `json:"remaining"`
}

// ScanNextAction returns what Claude should do next based on scan run state.
func (g *Graph) ScanNextAction(domainKey string) (*ScanAction, error) {
	run, err := g.store.GetActiveScanRun(domainKey)
	if err != nil {
		return nil, err
	}

	// No active run — tell Claude to start one.
	if run == nil {
		return &ScanAction{
			Phase:  "",
			Action: "start_scan",
		}, nil
	}

	switch run.Phase {
	case "setup":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "setup",
			Action:    "discover_files",
		}, nil

	case "phase1_extract":
		// Check open scan_file obligations for pending files.
		open, err := g.store.ListOpenObligations(run.RevisionID)
		if err != nil {
			return nil, err
		}

		var pending []string
		for _, ob := range open {
			if ob.ObligationType == "scan_file" {
				pending = append(pending, ob.TargetKey)
			}
		}

		if len(pending) > 0 {
			// Return batch of up to 10 files.
			batch := pending
			if len(batch) > 10 {
				batch = batch[:10]
			}
			return &ScanAction{
				ScanRunID: run.RunID,
				Phase:     "phase1_extract",
				Action:    "extract_files",
				Files:     batch,
				Progress: &ScanProgress{
					Total:     run.TotalFiles,
					Extracted: run.TotalFiles - len(pending),
					Remaining: len(pending),
				},
			}, nil
		}

		// All files done — transition to phase1_resolve.
		if err := g.store.TransitionScanRun(run.RunID, "phase1_resolve", 0); err != nil {
			return nil, err
		}
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase1_resolve",
			Action:    "call_resolve_extractions",
			Blocked:   true,
		}, nil

	case "phase1_resolve":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase1_resolve",
			Action:    "call_resolve_extractions",
			Blocked:   true,
		}, nil

	case "phase2_select":
		// Auto-finalize for now (phase 2 not yet implemented).
		if err := g.store.CompleteScanRun(run.RunID); err != nil {
			return nil, err
		}
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "finalized",
			Action:    "none",
			Done:      true,
		}, nil

	case "phase2_extract":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase2_extract",
			Action:    "none",
			Done:      true,
		}, nil

	case "phase2_resolve":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase2_resolve",
			Action:    "none",
			Done:      true,
		}, nil

	case "finalized":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "finalized",
			Action:    "none",
			Done:      true,
		}, nil

	default:
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     run.Phase,
			Action:    "none",
			Reason:    "unknown phase: " + run.Phase,
		}, nil
	}
}

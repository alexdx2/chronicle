package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/verify"
)

// VerifyFileResult is the result of verifying all stale evidence in a file.
type VerifyFileResult struct {
	File                string              `json:"file"`
	VerifierID          string              `json:"verifier_id"`
	VerifierVersion     string              `json:"verifier_version"`
	Results             []EvidenceVerifyItem `json:"results"`
	Summary             VerifySummary        `json:"summary"`
	ObligationSatisfied bool                `json:"obligation_satisfied"`
}

// EvidenceVerifyItem is the result for a single evidence row.
type EvidenceVerifyItem struct {
	EvidenceID     int64  `json:"evidence_id"`
	Target         string `json:"target"`
	AssertionKind  string `json:"assertion_kind"`
	Status         string `json:"status"`
	ChangeType     string `json:"change_type,omitempty"`
	Reason         string `json:"reason,omitempty"`
	ActionTaken    string `json:"action_taken"`
	LocatorUpdated bool   `json:"locator_updated"`
	NeedsClaude    bool   `json:"needs_claude"`
}

// VerifySummary counts results.
type VerifySummary struct {
	Checked     int `json:"checked"`
	Valid       int `json:"valid"`
	Moved       int `json:"moved"`
	Missing     int `json:"missing"`
	Changed     int `json:"changed"`
	Ambiguous   int `json:"ambiguous"`
	Unsupported int `json:"unsupported"`
}

// VerifyFileEvidence runs mechanical verification on all stale evidence for a file.
func (g *Graph) VerifyFileEvidence(filePath string, revisionID int64, domainKey string) (*VerifyFileResult, error) {
	startedAt := time.Now().UTC().Format(time.RFC3339)

	// Get stale evidence for this file
	evidence, err := g.store.ListStaleEvidenceByFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("VerifyFileEvidence: %w", err)
	}

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		// File doesn't exist — all evidence is missing
		result := &VerifyFileResult{
			File:    filePath,
			Summary: VerifySummary{Checked: len(evidence), Missing: len(evidence)},
		}
		for _, ev := range evidence {
			_ = g.store.UpdateEvidenceVerification(ev.EvidenceID, "stale", "missing", "file not found", 0, 0, revisionID)
			result.Results = append(result.Results, EvidenceVerifyItem{
				EvidenceID:    ev.EvidenceID,
				Target:        formatEvidenceTarget(ev),
				AssertionKind: ev.AssertionKind,
				Status:        "missing",
				Reason:        "file not found",
				ActionTaken:   "kept_stale",
			})
		}
		g.recordVerificationRun(revisionID, domainKey, filePath, "file_check", "v1", "succeeded", result.Summary, startedAt)
		g.SatisfyFileObligation(revisionID, filePath)
		result.ObligationSatisfied = true
		return result, nil
	}

	reg := verify.DefaultRegistry()
	result := &VerifyFileResult{
		File: filePath,
	}

	for _, ev := range evidence {
		item := g.verifyOneEvidence(ev, content, reg, revisionID)
		result.Results = append(result.Results, item)

		switch item.Status {
		case "valid":
			if item.LocatorUpdated {
				result.Summary.Moved++
			} else {
				result.Summary.Valid++
			}
		case "missing":
			result.Summary.Missing++
		case "ambiguous":
			result.Summary.Ambiguous++
		case "unsupported":
			result.Summary.Unsupported++
		default:
			result.Summary.Changed++
		}
		result.Summary.Checked++
	}

	// Determine primary verifier for the run record
	verifierID := "mixed"
	if len(evidence) > 0 && evidence[0].AssertionKind != "" {
		v := reg.Get(evidence[0].AssertionKind)
		if v != nil {
			verifierID = v.Kind()
		}
	}
	result.VerifierID = verifierID
	result.VerifierVersion = "v1"

	finishedAt := time.Now().UTC().Format(time.RFC3339)
	g.recordVerificationRun(revisionID, domainKey, filePath, verifierID, "v1", "succeeded", result.Summary, startedAt)
	_ = finishedAt

	// Satisfy obligation
	g.SatisfyFileObligation(revisionID, filePath)
	result.ObligationSatisfied = true

	// Recalculate trust for affected edges
	g.recalcAffectedEdges(evidence)

	return result, nil
}

func (g *Graph) verifyOneEvidence(ev store.EvidenceRow, fileContent []byte, reg *verify.Registry, revisionID int64) EvidenceVerifyItem {
	item := EvidenceVerifyItem{
		EvidenceID:    ev.EvidenceID,
		Target:        formatEvidenceTarget(ev),
		AssertionKind: ev.AssertionKind,
	}

	// If no assertion, can't verify mechanically
	if ev.AssertionKind == "" || ev.Assertion == "" || ev.Assertion == "{}" {
		item.Status = "unsupported"
		item.Reason = "no assertion — needs Claude"
		item.ActionTaken = "kept_stale"
		item.NeedsClaude = true
		_ = g.store.UpdateEvidenceVerification(ev.EvidenceID, "stale", "unsupported", "no assertion", 0, 0, revisionID)
		return item
	}

	// Run verifier
	vResult, err := reg.Verify(ev.AssertionKind, fileContent, json.RawMessage(ev.Assertion), locatorFromEvidence(ev))
	if err != nil {
		item.Status = "unsupported"
		item.Reason = err.Error()
		item.ActionTaken = "kept_stale"
		item.NeedsClaude = true
		_ = g.store.UpdateEvidenceVerification(ev.EvidenceID, "stale", "unsupported", err.Error(), 0, 0, revisionID)
		return item
	}

	item.Status = vResult.Status
	item.ChangeType = vResult.ChangeType
	item.Reason = vResult.Reason

	switch vResult.Status {
	case "valid":
		// Evidence confirmed — return to valid
		lineStart, lineEnd := 0, 0
		if vResult.NewLocator != nil {
			lineStart = vResult.NewLocator.LineStart
			lineEnd = vResult.NewLocator.LineEnd
			if lineStart != ev.LineStart || lineEnd != ev.LineEnd {
				item.LocatorUpdated = true
			}
		}
		_ = g.store.UpdateEvidenceVerification(ev.EvidenceID, "valid", "verified", vResult.Reason, lineStart, lineEnd, revisionID)
		item.ActionTaken = "verified"

	case "missing":
		_ = g.store.UpdateEvidenceVerification(ev.EvidenceID, "stale", "missing", vResult.Reason, 0, 0, revisionID)
		item.ActionTaken = "kept_stale"

	case "ambiguous":
		_ = g.store.UpdateEvidenceVerification(ev.EvidenceID, "stale", "ambiguous", vResult.Reason, 0, 0, revisionID)
		item.ActionTaken = "kept_stale"
		item.NeedsClaude = true

	default: // "unsupported" or other
		_ = g.store.UpdateEvidenceVerification(ev.EvidenceID, "stale", vResult.Status, vResult.Reason, 0, 0, revisionID)
		item.ActionTaken = "kept_stale"
		item.NeedsClaude = true
	}

	return item
}

func locatorFromEvidence(ev store.EvidenceRow) *verify.Locator {
	if ev.LineStart == 0 && ev.LineEnd == 0 {
		return nil
	}
	return &verify.Locator{
		LineStart: ev.LineStart,
		LineEnd:   ev.LineEnd,
	}
}

func formatEvidenceTarget(ev store.EvidenceRow) string {
	if ev.TargetKind == "edge" {
		return fmt.Sprintf("edge:%d", ev.EdgeID)
	}
	return fmt.Sprintf("node:%d", ev.NodeID)
}

func (g *Graph) recordVerificationRun(revisionID int64, domainKey, filePath, verifierID, version, status string, summary VerifySummary, startedAt string) {
	finishedAt := time.Now().UTC().Format(time.RFC3339)
	_, _ = g.store.CreateVerificationRun(store.VerificationRunRow{
		RevisionID:      revisionID,
		DomainKey:       domainKey,
		FilePath:        filePath,
		VerifierID:      verifierID,
		VerifierVersion: version,
		Status:          status,
		EvidenceChecked: summary.Checked,
		ValidCount:      summary.Valid + summary.Moved,
		MovedCount:      summary.Moved,
		MissingCount:    summary.Missing,
		ChangedCount:    summary.Changed,
		AmbiguousCount:  summary.Ambiguous,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
	})
}

// SatisfyFileObligation marks a verify_file obligation as satisfied.
func (g *Graph) SatisfyFileObligation(revisionID int64, filePath string) error {
	return g.store.SatisfyObligation(revisionID, "verify_file", filePath)
}

// LiveCheckItem is the result of a read-only evidence verification against the current file.
type LiveCheckItem struct {
	EvidenceID    int64  `json:"evidence_id"`
	FilePath      string `json:"file_path"`
	AssertionKind string `json:"assertion_kind"`
	Status        string `json:"status"`        // valid, missing, moved, ambiguous, unsupported
	ChangeType    string `json:"change_type,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// LiveCheckEvidence performs read-only mechanical verification of evidence assertions
// against the current file contents on disk. Does NOT modify the database.
// Groups evidence by file_path, reads each file once, and verifies each assertion.
// Evidence without assertions or without file_path is skipped.
func (g *Graph) LiveCheckEvidence(evidence []store.EvidenceRow) []LiveCheckItem {
	if len(evidence) == 0 {
		return nil
	}

	// Group evidence by file path.
	byFile := make(map[string][]store.EvidenceRow)
	for _, ev := range evidence {
		if ev.FilePath == "" || ev.AssertionKind == "" || ev.Assertion == "" || ev.Assertion == "{}" {
			continue
		}
		byFile[ev.FilePath] = append(byFile[ev.FilePath], ev)
	}
	if len(byFile) == 0 {
		return nil
	}

	reg := verify.DefaultRegistry()
	var items []LiveCheckItem

	for filePath, evs := range byFile {
		content, err := os.ReadFile(filePath)
		if err != nil {
			// File gone — all evidence is missing.
			for _, ev := range evs {
				items = append(items, LiveCheckItem{
					EvidenceID:    ev.EvidenceID,
					FilePath:      ev.FilePath,
					AssertionKind: ev.AssertionKind,
					Status:        "missing",
					Reason:        "file not found",
				})
			}
			continue
		}

		for _, ev := range evs {
			vResult, verr := reg.Verify(ev.AssertionKind, content, json.RawMessage(ev.Assertion), locatorFromEvidence(ev))
			if verr != nil {
				continue // verifier error — skip
			}
			// Only report non-valid results (something changed).
			if vResult.Status != "valid" {
				items = append(items, LiveCheckItem{
					EvidenceID:    ev.EvidenceID,
					FilePath:      ev.FilePath,
					AssertionKind: ev.AssertionKind,
					Status:        vResult.Status,
					ChangeType:    vResult.ChangeType,
					Reason:        vResult.Reason,
				})
			}
		}
	}

	return items
}

func (g *Graph) recalcAffectedEdges(evidence []store.EvidenceRow) {
	seen := map[int64]bool{}
	for _, ev := range evidence {
		if ev.TargetKind == "edge" && ev.EdgeID > 0 && !seen[ev.EdgeID] {
			seen[ev.EdgeID] = true
			g.RecalculateEdgeTrust(ev.EdgeID)
		}
	}
}

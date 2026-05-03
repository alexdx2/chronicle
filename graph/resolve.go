package graph

import (
	"encoding/json"
	"fmt"

	"github.com/alexdx2/chronicle-core/validate"
)

// ResolveReviewResult is returned from ResolveReview.
type ResolveReviewResult struct {
	ActionTaken           string `json:"action_taken"`
	TargetKey             string `json:"target_key"`
	NewVerificationStatus string `json:"new_verification_status"`
}

// ResolveReview resolves a needs_review edge or node.
func (g *Graph) ResolveReview(targetKind, targetKey, resolution, reason, evidenceJSON, replacementKey string, revisionID int64) (*ResolveReviewResult, error) {
	switch resolution {
	case "confirmed_valid":
		return g.resolveConfirmedValid(targetKind, targetKey, reason, evidenceJSON, revisionID)
	case "confirmed_removed":
		return g.resolveConfirmedRemoved(targetKind, targetKey, reason, evidenceJSON, revisionID)
	case "replaced_by":
		return g.resolveReplacedBy(targetKind, targetKey, reason, evidenceJSON, replacementKey, revisionID)
	case "deferred":
		return g.resolveDeferred(targetKind, targetKey, reason, revisionID)
	default:
		return nil, fmt.Errorf("invalid resolution: %q (must be confirmed_valid, confirmed_removed, replaced_by, deferred)", resolution)
	}
}

func (g *Graph) resolveConfirmedValid(targetKind, targetKey, reason, evidenceJSON string, revisionID int64) (*ResolveReviewResult, error) {
	if evidenceJSON == "" {
		return nil, fmt.Errorf("confirmed_valid requires positive evidence")
	}

	// Parse and validate the evidence has assertion_kind if possible
	var evData map[string]any
	if err := json.Unmarshal([]byte(evidenceJSON), &evData); err != nil {
		return nil, fmt.Errorf("invalid evidence JSON: %w", err)
	}

	// Add the positive evidence
	input := validate.EvidenceInput{
		TargetKind:       targetKind,
		SourceKind:       strOrDefault(evData, "source_kind", "claude"),
		FilePath:         strOrDefault(evData, "file_path", ""),
		ExtractorID:      "chronicle-resolve-review",
		ExtractorVersion: "v1",
		Confidence:       floatOrDefault(evData, "confidence", 0.85),
		Polarity:         "positive",
		RevisionID:       revisionID,
		Assertion:        strOrDefault(evData, "assertion", ""),
		AssertionKind:    strOrDefault(evData, "assertion_kind", ""),
	}

	var err error
	switch targetKind {
	case "edge":
		_, err = g.AddEdgeEvidence(targetKey, input)
	case "node":
		_, err = g.AddNodeEvidence(targetKey, input)
	}
	if err != nil {
		return nil, fmt.Errorf("confirmed_valid: %w", err)
	}

	// Update edge verification_status
	if targetKind == "edge" {
		g.store.UpdateEdgeVerificationStatus(targetKey, "verified")
	}

	return &ResolveReviewResult{
		ActionTaken:           "evidence added",
		TargetKey:             targetKey,
		NewVerificationStatus: "verified",
	}, nil
}

func (g *Graph) resolveConfirmedRemoved(targetKind, targetKey, reason, evidenceJSON string, revisionID int64) (*ResolveReviewResult, error) {
	// Safety: confirmed_removed MUST have negative evidence with checked_scope
	if evidenceJSON == "" {
		return nil, fmt.Errorf("confirmed_removed requires negative evidence with checked_scope (removal requires proof, not narrative)")
	}

	var evData map[string]any
	if err := json.Unmarshal([]byte(evidenceJSON), &evData); err != nil {
		return nil, fmt.Errorf("invalid evidence JSON: %w", err)
	}

	// Validate polarity
	polarity := strOrDefault(evData, "polarity", "")
	if polarity != "negative" {
		return nil, fmt.Errorf("confirmed_removed evidence must have polarity='negative'")
	}

	// Validate checked_scope exists in assertion
	assertionStr := strOrDefault(evData, "assertion", "")
	if assertionStr == "" {
		// Try as nested object
		if assertionObj, ok := evData["assertion"]; ok {
			b, _ := json.Marshal(assertionObj)
			assertionStr = string(b)
		}
	}
	if assertionStr != "" && assertionStr != "{}" {
		var assertion map[string]any
		if err := json.Unmarshal([]byte(assertionStr), &assertion); err == nil {
			if _, hasScope := assertion["checked_scope"]; !hasScope {
				return nil, fmt.Errorf("confirmed_removed negative evidence must include 'checked_scope' in assertion")
			}
		}
	} else {
		return nil, fmt.Errorf("confirmed_removed requires assertion with checked_scope")
	}

	// Add negative evidence
	input := validate.EvidenceInput{
		TargetKind:       targetKind,
		SourceKind:       strOrDefault(evData, "source_kind", "claude"),
		FilePath:         strOrDefault(evData, "file_path", ""),
		ExtractorID:      "chronicle-resolve-review",
		ExtractorVersion: "v1",
		Confidence:       floatOrDefault(evData, "confidence", 0.95),
		Polarity:         "negative",
		RevisionID:       revisionID,
		Assertion:        assertionStr,
		AssertionKind:    strOrDefault(evData, "assertion_kind", "absence_confirmed"),
	}

	switch targetKind {
	case "edge":
		_, err := g.AddEdgeEvidence(targetKey, input)
		if err != nil {
			return nil, fmt.Errorf("confirmed_removed: %w", err)
		}
		// Deactivate the edge
		g.store.DeleteEdge(targetKey)
		g.store.UpdateEdgeVerificationStatus(targetKey, "verified")
	case "node":
		_, err := g.AddNodeEvidence(targetKey, input)
		if err != nil {
			return nil, fmt.Errorf("confirmed_removed: %w", err)
		}
	}

	return &ResolveReviewResult{
		ActionTaken:           "edge deactivated",
		TargetKey:             targetKey,
		NewVerificationStatus: "verified",
	}, nil
}

func (g *Graph) resolveReplacedBy(targetKind, targetKey, reason, evidenceJSON, replacementKey string, revisionID int64) (*ResolveReviewResult, error) {
	if replacementKey == "" {
		return nil, fmt.Errorf("replaced_by requires replacement_target_key")
	}
	if evidenceJSON == "" {
		return nil, fmt.Errorf("replaced_by requires evidence recording the replacement")
	}

	// Build replacement assertion
	replacementAssertion, _ := json.Marshal(map[string]any{
		"old_claim":  targetKey,
		"new_claim":  replacementKey,
		"reason":     reason,
	})

	input := validate.EvidenceInput{
		TargetKind:       targetKind,
		SourceKind:       "claude",
		ExtractorID:      "chronicle-resolve-review",
		ExtractorVersion: "v1",
		Confidence:       0.95,
		Polarity:         "negative",
		RevisionID:       revisionID,
		Assertion:        string(replacementAssertion),
		AssertionKind:    "replaced_by",
	}

	switch targetKind {
	case "edge":
		_, err := g.AddEdgeEvidence(targetKey, input)
		if err != nil {
			return nil, fmt.Errorf("replaced_by: %w", err)
		}
		// Deactivate old edge
		g.store.DeleteEdge(targetKey)
	case "node":
		_, err := g.AddNodeEvidence(targetKey, input)
		if err != nil {
			return nil, fmt.Errorf("replaced_by: %w", err)
		}
	}

	return &ResolveReviewResult{
		ActionTaken:           "edge superseded",
		TargetKey:             targetKey,
		NewVerificationStatus: "verified",
	}, nil
}

func (g *Graph) resolveDeferred(targetKind, targetKey, reason string, revisionID int64) (*ResolveReviewResult, error) {
	// Mark obligation as deferred
	if revisionID > 0 {
		_ = g.store.DeferObligation(revisionID, "review_"+targetKind, targetKey, reason)
	}

	return &ResolveReviewResult{
		ActionTaken:           "deferred",
		TargetKey:             targetKey,
		NewVerificationStatus: "needs_review",
	}, nil
}

// helpers

func strOrDefault(m map[string]any, key, def string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

func floatOrDefault(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key]; ok {
		switch f := v.(type) {
		case float64:
			return f
		case int:
			return float64(f)
		}
	}
	return def
}

package verify

import (
	"encoding/json"
	"fmt"

	"github.com/alexdx2/chronicle-core/extract/ast"
)

// ComplexityVerifier re-checks a Tier-A complexity assertion by recomputing the
// exact metrics from the current source and comparing them to the stored values.
// Because the metrics are exact AST counts, a match is fully trustworthy
// (confidence 1.0) and a divergence means the assertion is stale — the
// evidence-first guarantee that complexity claims are mechanically re-checkable.
type ComplexityVerifier struct{}

func (v *ComplexityVerifier) Kind() string { return "complexity" }

// ComplexityAssertion is the stored exact Tier-A complexity for a unit.
type ComplexityAssertion struct {
	Cyclomatic int `json:"cyclomatic"`
	LoopCount  int `json:"loop_count"`
	LoopDepth  int `json:"loop_depth"`
}

func (v *ComplexityVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a ComplexityAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid complexity assertion: %w", err)
	}

	agg := ast.AggregateComplexity(ast.ExtractComplexity(fileContent))
	if !agg.Present {
		// Nothing measurable in the file — can't confirm or refute; defer.
		return &Result{
			Status:          "unsupported",
			Confidence:      0,
			Reason:          "no functions/methods to measure",
			SuggestedAction: "needs_claude",
		}, nil
	}

	if agg.Cyclomatic == a.Cyclomatic && agg.LoopCount == a.LoopCount && agg.LoopDepth == a.LoopDepth {
		return &Result{
			Status:          "valid",
			Confidence:      1.0,
			NewLocator:      &Locator{LineStart: agg.StartLine, LineEnd: agg.EndLine},
			SuggestedAction: "revalidate",
		}, nil
	}

	return &Result{
		Status:     "missing",
		ChangeType: "value_changed",
		Confidence: 0.9,
		Reason: fmt.Sprintf("complexity changed: asserted {cyc:%d loops:%d depth:%d}, source recomputes to {cyc:%d loops:%d depth:%d}",
			a.Cyclomatic, a.LoopCount, a.LoopDepth, agg.Cyclomatic, agg.LoopCount, agg.LoopDepth),
		NewLocator:      &Locator{LineStart: agg.StartLine, LineEnd: agg.EndLine},
		SuggestedAction: "mark_stale",
	}, nil
}

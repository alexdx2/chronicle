package verify

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/ast"
)

// heuristicConfidence caps confidence for heuristic (smell/cognitive) signals.
// Exact AST counts get 1.0; heuristics are interpretive, so even a confirmed
// match stays below that ceiling.
const heuristicConfidence = 0.6

// ComplexitySmellsVerifier re-checks the heuristic Tier-A signals (cognitive
// complexity + smell tags) by recomputing them from current source. Reproducible
// like the exact metrics, but confirmed matches verify at heuristic confidence,
// not the 1.0 reserved for exact counts.
type ComplexitySmellsVerifier struct{}

func (v *ComplexitySmellsVerifier) Kind() string { return "complexity_smells" }

// ComplexitySmellsAssertion is the stored heuristic signal for a unit.
type ComplexitySmellsAssertion struct {
	Cognitive int      `json:"cognitive"`
	Smells    []string `json:"smells"`
}

func (v *ComplexitySmellsVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a ComplexitySmellsAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid complexity_smells assertion: %w", err)
	}

	agg := ast.AggregateComplexity(ast.ExtractComplexity(fileContent))
	if !agg.Present {
		return &Result{
			Status:          "unsupported",
			Confidence:      0,
			Reason:          "no functions/methods to measure",
			SuggestedAction: "needs_claude",
		}, nil
	}

	if agg.Cognitive == a.Cognitive && sameSmells(agg.Smells, a.Smells) {
		return &Result{
			Status:          "valid",
			Confidence:      heuristicConfidence,
			NewLocator:      &Locator{LineStart: agg.StartLine, LineEnd: agg.EndLine},
			SuggestedAction: "revalidate",
		}, nil
	}

	return &Result{
		Status:     "missing",
		ChangeType: "value_changed",
		Confidence: heuristicConfidence,
		Reason: fmt.Sprintf("heuristic signal changed: asserted {cognitive:%d smells:%v}, source recomputes to {cognitive:%d smells:%v}",
			a.Cognitive, a.Smells, agg.Cognitive, agg.Smells),
		NewLocator:      &Locator{LineStart: agg.StartLine, LineEnd: agg.EndLine},
		SuggestedAction: "mark_stale",
	}, nil
}

// sameSmells compares two smell sets order-independently.
func sameSmells(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	return strings.Join(ac, ",") == strings.Join(bc, ",")
}

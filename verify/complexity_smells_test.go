package verify

import (
	"encoding/json"
	"testing"
)

const smellSource = `
class Svc {
  scan(items, ids) {
    for (const id of ids) {
      const hit = items.find(x => x.id === id);
    }
  }
}
`

// TestComplexitySmellsVerifier_Valid proves a matching heuristic assertion
// verifies — but at heuristic confidence, never the 1.0 reserved for exact
// counts.
func TestComplexitySmellsVerifier_Valid(t *testing.T) {
	v := &ComplexitySmellsVerifier{}
	if v.Kind() != "complexity_smells" {
		t.Fatalf("Kind() = %q, want complexity_smells", v.Kind())
	}
	// scan(): for-of -> cognitive 1; find-in-loop -> linear_scan_in_loop.
	assertion := json.RawMessage(`{"cognitive":1,"smells":["linear_scan_in_loop"]}`)
	result, err := v.Verify([]byte(smellSource), assertion, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Status != "valid" {
		t.Fatalf("status = %q, want valid (reason: %s)", result.Status, result.Reason)
	}
	if result.Confidence >= 1.0 || result.Confidence <= 0 {
		t.Errorf("heuristic confidence = %v, want in (0,1)", result.Confidence)
	}
}

// TestComplexitySmellsVerifier_Diverged proves a stale smell set is caught.
func TestComplexitySmellsVerifier_Diverged(t *testing.T) {
	v := &ComplexitySmellsVerifier{}
	// Claims a smell that the source does not exhibit.
	assertion := json.RawMessage(`{"cognitive":1,"smells":["unguarded_recursion"]}`)
	result, err := v.Verify([]byte(smellSource), assertion, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Status != "missing" {
		t.Fatalf("status = %q, want missing for diverged smells", result.Status)
	}
}

// TestComplexitySmellsVerifier_Registered proves it is wired into the registry.
func TestComplexitySmellsVerifier_Registered(t *testing.T) {
	if DefaultRegistry().Get("complexity_smells") == nil {
		t.Fatal("complexity_smells verifier not registered in DefaultRegistry")
	}
}

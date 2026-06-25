package verify

import (
	"encoding/json"
	"testing"
)

const cxSource = `
class OrderService {
  process(items) {
    for (const i of items) {
      while (i > 0) {
        if (i % 2 === 0) { doThing(); }
      }
    }
  }
}
`

// TestComplexityVerifier_Valid proves an assertion matching the recomputed exact
// Tier-A aggregate verifies with full confidence (the evidence-first guarantee:
// complexity claims are re-checkable against source).
func TestComplexityVerifier_Valid(t *testing.T) {
	v := &ComplexityVerifier{}
	if v.Kind() != "complexity" {
		t.Fatalf("Kind() = %q, want complexity", v.Kind())
	}
	// process(): for-of + while + if = cyclomatic 4; 2 loops; depth 2.
	assertion := json.RawMessage(`{"cyclomatic":4,"loop_count":2,"loop_depth":2}`)
	result, err := v.Verify([]byte(cxSource), assertion, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Status != "valid" {
		t.Fatalf("status = %q, want valid (reason: %s)", result.Status, result.Reason)
	}
	if result.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0 for an exact recompute match", result.Confidence)
	}
}

// TestComplexityVerifier_Diverged proves a stale assertion (source changed so the
// metrics no longer hold) is reported as missing — caught, not silently trusted.
func TestComplexityVerifier_Diverged(t *testing.T) {
	v := &ComplexityVerifier{}
	// Claims depth 5 / cyclomatic 9, but the source recomputes to 2/4/2.
	assertion := json.RawMessage(`{"cyclomatic":9,"loop_count":2,"loop_depth":5}`)
	result, err := v.Verify([]byte(cxSource), assertion, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Status != "missing" {
		t.Fatalf("status = %q, want missing for a diverged assertion", result.Status)
	}
}

// TestComplexityVerifier_NoFunctions proves a file with nothing to measure is
// unsupported (no fabricated verdict), not a false rejection.
func TestComplexityVerifier_NoFunctions(t *testing.T) {
	v := &ComplexityVerifier{}
	assertion := json.RawMessage(`{"cyclomatic":1,"loop_count":0,"loop_depth":0}`)
	result, err := v.Verify([]byte("export const x = 1;\n"), assertion, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Status != "unsupported" {
		t.Fatalf("status = %q, want unsupported when no functions are present", result.Status)
	}
}

// TestComplexityVerifier_Registered proves the verifier is wired into the default
// registry so creation-time verification picks it up.
func TestComplexityVerifier_Registered(t *testing.T) {
	if DefaultRegistry().Get("complexity") == nil {
		t.Fatal("complexity verifier not registered in DefaultRegistry")
	}
}

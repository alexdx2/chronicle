package registry

import (
	"testing"
)

func TestLoadValid(t *testing.T) {
	r, err := LoadFile("../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsValidLayer("code") {
		t.Error("expected code to be valid layer")
	}
	if r.IsValidLayer("nonexistent") {
		t.Error("expected nonexistent to be invalid layer")
	}
	if !r.IsValidNodeType("code", "controller") {
		t.Error("expected code:controller to be valid")
	}
	if r.IsValidNodeType("code", "nonexistent") {
		t.Error("expected code:nonexistent to be invalid")
	}
	if r.IsValidNodeType("service", "controller") {
		t.Error("expected service:controller to be invalid")
	}
	if !r.IsValidEdgeType("INJECTS") {
		t.Error("expected INJECTS to be valid edge type")
	}
	if r.IsValidEdgeType("NONEXISTENT") {
		t.Error("expected NONEXISTENT to be invalid edge type")
	}
}

func TestValidateEdgeLayers(t *testing.T) {
	r, err := LoadFile("../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := r.ValidateEdgeLayers("INJECTS", "code", "code"); err != nil {
		t.Errorf("INJECTS code->code should be valid: %v", err)
	}
	if err := r.ValidateEdgeLayers("INJECTS", "service", "code"); err == nil {
		t.Error("INJECTS service->code should be invalid")
	}
	if err := r.ValidateEdgeLayers("EXPOSES_ENDPOINT", "code", "contract"); err != nil {
		t.Errorf("EXPOSES_ENDPOINT code->contract should be valid: %v", err)
	}
	if err := r.ValidateEdgeLayers("EXPOSES_ENDPOINT", "code", "code"); err == nil {
		t.Error("EXPOSES_ENDPOINT code->code should be invalid")
	}
}

func TestLoadInvalidEdge(t *testing.T) {
	_, err := LoadFile("../testdata/registry/invalid_edge.yaml")
	if err == nil {
		t.Fatal("expected error for edge referencing nonexistent layer")
	}
}

func TestIsValidDerivation(t *testing.T) {
	r, err := LoadFile("../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsValidDerivation("hard") {
		t.Error("expected hard to be valid")
	}
	if r.IsValidDerivation("bogus") {
		t.Error("expected bogus to be invalid")
	}
}

func TestIsValidSourceKind(t *testing.T) {
	r, err := LoadFile("../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsValidSourceKind("file") {
		t.Error("expected file to be valid")
	}
	if r.IsValidSourceKind("bogus") {
		t.Error("expected bogus to be invalid")
	}
}

func TestTraversalPolicy(t *testing.T) {
	r, err := LoadFile("../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	policy := r.TraversalPolicy()

	if !policy.IsStructural("CONTAINS") {
		t.Error("expected CONTAINS to be structural")
	}
	if policy.IsStructural("INJECTS") {
		t.Error("expected INJECTS to not be structural")
	}
	if policy.AllowsReverseImpact("EXPOSES_ENDPOINT") {
		t.Error("expected EXPOSES_ENDPOINT to not allow reverse impact")
	}
	if policy.AllowsReverseImpact("PUBLISHES_TOPIC") {
		t.Error("expected PUBLISHES_TOPIC to not allow reverse impact")
	}
	if !policy.AllowsReverseImpact("INJECTS") {
		t.Error("expected INJECTS to allow reverse impact")
	}
	if !policy.AllowsForwardPath("INJECTS") {
		t.Error("expected INJECTS to allow forward path")
	}
	if policy.AllowsForwardPath("CONTAINS") {
		t.Error("expected CONTAINS to not allow forward path")
	}
}

func TestLoadDefaults(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	if !r.IsValidLayer("code") {
		t.Error("expected code to be valid layer")
	}
	if !r.IsValidEdgeType("INJECTS") {
		t.Error("expected INJECTS to be valid")
	}
	if !r.IsValidNodeType("contract", "endpoint") {
		t.Error("expected contract:endpoint to be valid")
	}
}

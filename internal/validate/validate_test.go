package validate

import (
	"testing"

	"github.com/alexdx2/chronicle-core/internal/registry"
)

func loadTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("loading test registry: %v", err)
	}
	return r
}

func TestValidateNodeInput_Valid(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:   "code:controller:orders:OrdersController",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "orders",
		Name:      "OrdersController",
	}
	result, err := ValidateNodeInput(input, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeKey != "code:controller:orders:orderscontroller" {
		t.Errorf("node_key = %q, want normalized", result.NodeKey)
	}
}

func TestValidateNodeInput_MissingName(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:   "code:controller:orders:OrdersController",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "orders",
		Name:      "",
	}
	_, err := ValidateNodeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidateNodeInput_BadLayer(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:   "bogus:controller:orders:OrdersController",
		Layer:     "bogus",
		NodeType:  "controller",
		DomainKey: "orders",
		Name:      "OrdersController",
	}
	_, err := ValidateNodeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for invalid layer")
	}
}

func TestValidateNodeInput_BadNodeType(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:   "code:bogus:orders:OrdersController",
		Layer:     "code",
		NodeType:  "bogus",
		DomainKey: "orders",
		Name:      "OrdersController",
	}
	_, err := ValidateNodeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for invalid node_type")
	}
}

func TestValidateNodeInput_BadConfidence(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:    "code:controller:orders:OrdersController",
		Layer:      "code",
		NodeType:   "controller",
		DomainKey:  "orders",
		Name:       "OrdersController",
		Confidence: 1.5,
	}
	_, err := ValidateNodeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for confidence > 1")
	}
}

func TestValidateEdgeInput_Valid(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EdgeInput{
		FromNodeKey:    "code:controller:orders:orderscontroller",
		ToNodeKey:      "code:provider:orders:ordersservice",
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "code",
	}
	result, err := ValidateEdgeInput(input, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.EdgeKey == "" {
		t.Error("edge_key should be auto-generated")
	}
}

func TestValidateEdgeInput_BadEdgeType(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EdgeInput{
		FromNodeKey:    "code:controller:orders:orderscontroller",
		ToNodeKey:      "code:provider:orders:ordersservice",
		EdgeType:       "NONEXISTENT",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "code",
	}
	_, err := ValidateEdgeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for invalid edge type")
	}
}

func TestValidateEdgeInput_LayerMismatch(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EdgeInput{
		FromNodeKey:    "service:service:orders:ordersservice",
		ToNodeKey:      "code:provider:orders:ordersservice",
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		FromLayer:      "service",
		ToLayer:        "code",
	}
	_, err := ValidateEdgeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for layer mismatch")
	}
}

func TestValidateEvidenceInput_Valid(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "file",
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
	}
	err := ValidateEvidenceInput(input, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateEvidenceInput_BadSourceKind(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "bogus",
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
	}
	err := ValidateEvidenceInput(input, reg)
	if err == nil {
		t.Fatal("expected error for invalid source_kind")
	}
}

func TestValidateEvidenceInput_BadTargetKind(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EvidenceInput{
		TargetKind:       "answer",
		SourceKind:       "file",
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
	}
	err := ValidateEvidenceInput(input, reg)
	if err == nil {
		t.Fatal("expected error for answer target_kind in foundation")
	}
}

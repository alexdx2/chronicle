package registry

import (
	"strings"
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

func TestToSchemaJSON_Full(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	schema := r.ToSchemaJSON("", "", nil)

	// Full schema must include all sections
	if schema.Layers == nil || len(schema.Layers) == 0 {
		t.Error("expected layers in full schema")
	}
	if schema.NodeTypes == nil || len(schema.NodeTypes) == 0 {
		t.Error("expected node_types in full schema")
	}
	if schema.EdgeTypes == nil || len(schema.EdgeTypes) == 0 {
		t.Error("expected edge_types in full schema")
	}
	if schema.DerivationKinds == nil || len(schema.DerivationKinds) == 0 {
		t.Error("expected derivation_kinds in full schema")
	}
	if schema.NodeStatuses == nil || len(schema.NodeStatuses) == 0 {
		t.Error("expected node_statuses in full schema")
	}

	// Check all 44 edge types are present
	if len(schema.EdgeTypes) < 40 {
		t.Errorf("expected >= 40 edge types, got %d", len(schema.EdgeTypes))
	}

	// Check REQUIRES has correct constraints
	req, ok := schema.EdgeTypes["REQUIRES"]
	if !ok {
		t.Fatal("missing REQUIRES edge type")
	}
	if !contains(req.FromLayers, "flow") {
		t.Error("REQUIRES should allow from_layer flow")
	}
	if !contains(req.ToLayers, "data") {
		t.Error("REQUIRES should allow to_layer data")
	}
}

func TestToSchemaJSON_FilterFromLayer(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	schema := r.ToSchemaJSON("flow", "", nil)

	// Filtered: should omit layers, derivation_kinds, node_statuses
	if schema.Layers != nil {
		t.Error("filtered schema should not include layers")
	}
	if schema.DerivationKinds != nil {
		t.Error("filtered schema should not include derivation_kinds")
	}
	if schema.NodeStatuses != nil {
		t.Error("filtered schema should not include node_statuses")
	}

	// Should only include flow node types
	if len(schema.NodeTypes) != 1 {
		t.Errorf("expected 1 layer in node_types, got %d", len(schema.NodeTypes))
	}
	if _, ok := schema.NodeTypes["flow"]; !ok {
		t.Error("expected flow in filtered node_types")
	}

	// All edge types must allow from_layer flow
	for name, et := range schema.EdgeTypes {
		if !contains(et.FromLayers, "flow") {
			t.Errorf("edge type %s should allow from_layer flow", name)
		}
	}

	// REQUIRES should be present (flow -> *)
	if _, ok := schema.EdgeTypes["REQUIRES"]; !ok {
		t.Error("REQUIRES should be in flow-filtered results")
	}

	// INJECTS should NOT be present (code -> code only)
	if _, ok := schema.EdgeTypes["INJECTS"]; ok {
		t.Error("INJECTS should NOT be in flow-filtered results")
	}
}

func TestToSchemaJSON_FilterBothLayers(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	schema := r.ToSchemaJSON("flow", "data", nil)

	// Every edge type must allow from=flow AND to=data
	for name, et := range schema.EdgeTypes {
		if !contains(et.FromLayers, "flow") || !contains(et.ToLayers, "data") {
			t.Errorf("edge type %s doesn't match filter flow->data", name)
		}
	}

	// REQUIRES allows flow->data, should be present
	if _, ok := schema.EdgeTypes["REQUIRES"]; !ok {
		t.Error("REQUIRES should be in flow->data results")
	}

	// PRECEDES is flow->flow, should NOT be present
	if _, ok := schema.EdgeTypes["PRECEDES"]; ok {
		t.Error("PRECEDES should NOT be in flow->data results")
	}
}

func TestToSchemaJSON_IncludeFilter(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	schema := r.ToSchemaJSON("", "", []string{"edges"})

	// Only edges requested
	if schema.EdgeTypes == nil || len(schema.EdgeTypes) == 0 {
		t.Error("expected edge_types when include=[edges]")
	}
	if schema.NodeTypes != nil {
		t.Error("should not include node_types when include=[edges]")
	}
	if schema.Layers != nil {
		t.Error("should not include layers when include=[edges]")
	}
}

func TestToSchemaJSON_IncludeNodes(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	schema := r.ToSchemaJSON("", "", []string{"nodes"})

	if schema.NodeTypes == nil || len(schema.NodeTypes) == 0 {
		t.Error("expected node_types when include=[nodes]")
	}
	if schema.EdgeTypes != nil {
		t.Error("should not include edge_types when include=[nodes]")
	}
}

func TestSuggestEdgeTypes(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	suggestions := r.SuggestEdgeTypes("flow", "service")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions for flow->service")
	}

	// INVOKES, REQUIRES, DEPENDS_ON should all be suggested
	names := make(map[string]bool)
	for _, s := range suggestions {
		names[s.EdgeType] = true
	}
	for _, expected := range []string{"INVOKES", "REQUIRES", "DEPENDS_ON"} {
		if !names[expected] {
			t.Errorf("expected %s in suggestions", expected)
		}
	}

	// Each suggestion should have an intent label
	for _, s := range suggestions {
		if s.Intent == "" {
			t.Errorf("suggestion %s missing intent label", s.EdgeType)
		}
	}
}

func TestSuggestSimilarEdgeTypes(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	// Known edge type — should use similarity map
	suggestions := r.SuggestSimilarEdgeTypes("CALLS_SERVICE")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions for CALLS_SERVICE")
	}
	// INVOKES should be first (closest semantic match)
	if suggestions[0].EdgeType != "INVOKES" {
		t.Errorf("expected INVOKES as first suggestion for CALLS_SERVICE, got %s", suggestions[0].EdgeType)
	}

	// Unknown edge type — should fuzzy match
	suggestions = r.SuggestSimilarEdgeTypes("USES")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions for USES")
	}
	names := make(map[string]bool)
	for _, s := range suggestions {
		names[s.EdgeType] = true
	}
	if !names["USES_MODEL"] && !names["USES_ENUM"] && !names["USES_QUEUE"] {
		t.Error("expected USES_* suggestions for 'USES'")
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func TestValidateEdgeLayers_UnknownType(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	errMsg := r.ValidateEdgeLayers("USES", "code", "data")
	if errMsg == nil {
		t.Fatal("expected error for unknown edge type USES")
	}
	msg := errMsg.Error()
	// Should say "unknown" not "does not allow"
	if !strings.Contains(msg, "unknown edge type") {
		t.Errorf("expected 'unknown edge type' in error, got: %s", msg)
	}
	// Should suggest similar types
	if !strings.Contains(msg, "USES_MODEL") {
		t.Errorf("expected USES_MODEL suggestion in error, got: %s", msg)
	}
}

func TestValidateEdgeLayers_WrongLayer(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	errMsg := r.ValidateEdgeLayers("CALLS_SERVICE", "flow", "service")
	if errMsg == nil {
		t.Fatal("expected error for CALLS_SERVICE from flow")
	}
	msg := errMsg.Error()
	// Should mention allowed from_layers
	if !strings.Contains(msg, "allowed from") {
		t.Errorf("expected 'allowed from' in error, got: %s", msg)
	}
	// Should suggest alternatives for flow->service
	if !strings.Contains(msg, "INVOKES") || !strings.Contains(msg, "REQUIRES") {
		t.Errorf("expected INVOKES and REQUIRES suggestions, got: %s", msg)
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

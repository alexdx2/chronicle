package validate

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
)

func reg(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	return r
}

func TestValidateEdgeInput_Errors(t *testing.T) {
	r := reg(t)
	base := EdgeInput{FromNodeKey: "a", ToNodeKey: "b", EdgeType: "CONTAINS", FromLayer: "code", ToLayer: "code"}

	cases := []struct {
		name string
		mut  func(*EdgeInput)
		want string
	}{
		{"no from", func(e *EdgeInput) { e.FromNodeKey = "" }, "from_node_key"},
		{"no to", func(e *EdgeInput) { e.ToNodeKey = "" }, "to_node_key"},
		{"no type", func(e *EdgeInput) { e.EdgeType = "" }, "edge_type is required"},
		{"bad type", func(e *EdgeInput) { e.EdgeType = "BOGUS_EDGE" }, "invalid edge_type"},
		{"bad derivation", func(e *EdgeInput) { e.DerivationKind = "telepathy" }, "invalid derivation_kind"},
		{"bad layers", func(e *EdgeInput) { e.FromLayer = "data"; e.ToLayer = "data"; e.EdgeType = "INJECTS" }, "validation:"},
		{"confidence high", func(e *EdgeInput) { e.Confidence = 2 }, "out of range"},
		{"confidence neg", func(e *EdgeInput) { e.Confidence = -0.5 }, "out of range"},
		{"bad edge key", func(e *EdgeInput) { e.EdgeKey = "::::bad::::" }, "validation:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.mut(&in)
			_, err := ValidateEdgeInput(in, r)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestValidateEdgeInput_Defaults(t *testing.T) {
	r := reg(t)
	v, err := ValidateEdgeInput(EdgeInput{
		FromNodeKey: "code:module:d:a", ToNodeKey: "code:module:d:b",
		EdgeType: "CONTAINS", FromLayer: "code", ToLayer: "code",
	}, r)
	if err != nil {
		t.Fatalf("valid edge: %v", err)
	}
	if v.DerivationKind != "hard" {
		t.Errorf("derivation default: got %q", v.DerivationKind)
	}
	if v.Confidence != 1.0 {
		t.Errorf("confidence default: got %v", v.Confidence)
	}
	if v.Metadata != "{}" {
		t.Errorf("metadata default: got %q", v.Metadata)
	}
	if v.EdgeKey == "" {
		t.Error("edge key should be auto-built")
	}
}

func TestValidateEvidenceInput(t *testing.T) {
	r := reg(t)
	ok := EvidenceInput{TargetKind: "node", SourceKind: "file", ExtractorID: "x", ExtractorVersion: "1"}
	if err := ValidateEvidenceInput(ok, r); err != nil {
		t.Fatalf("valid evidence: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*EvidenceInput)
		want string
	}{
		{"bad target", func(e *EvidenceInput) { e.TargetKind = "thing" }, "target_kind"},
		{"bad source", func(e *EvidenceInput) { e.SourceKind = "ouija" }, "invalid source_kind"},
		{"no extractor id", func(e *EvidenceInput) { e.ExtractorID = "" }, "extractor_id is required"},
		{"no extractor ver", func(e *EvidenceInput) { e.ExtractorVersion = "" }, "extractor_version is required"},
		{"confidence range", func(e *EvidenceInput) { e.Confidence = 1.5 }, "out of range"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := ok
			c.mut(&in)
			err := ValidateEvidenceInput(in, r)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

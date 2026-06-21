package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAsFile_ParsesDefaults(t *testing.T) {
	f, err := AsFile()
	if err != nil {
		t.Fatalf("AsFile: %v", err)
	}
	if len(f.Layers) == 0 || len(f.NodeTypes) == 0 || len(f.EdgeTypes) == 0 {
		t.Fatalf("AsFile returned empty registry file: %+v", f)
	}
}

func TestLoadFile_OK_and_Error(t *testing.T) {
	// Error: nonexistent path.
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("LoadFile on missing path should error")
	}
	// OK: write a minimal valid registry and load it.
	p := filepath.Join(t.TempDir(), "types.yaml")
	if err := os.WriteFile(p, []byte("version: \"1\"\nlayers: [code]\nnode_types:\n  code: [module]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile valid: %v", err)
	}
	if !r.IsValidNodeType("code", "module") {
		t.Fatal("expected code/module valid")
	}
}

func TestValidators_KnownAndUnknown(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	// IsValidNodeType: unknown layer → false; known → true.
	if r.IsValidNodeType("no-such-layer", "x") {
		t.Error("unknown layer should be invalid")
	}
	if !r.IsValidNodeType("code", "module") {
		t.Error("code/module should be valid")
	}
	// ValidNodeTypes: unknown layer → nil; known → non-empty.
	if r.ValidNodeTypes("no-such-layer") != nil {
		t.Error("unknown layer ValidNodeTypes should be nil")
	}
	if len(r.ValidNodeTypes("code")) == 0 {
		t.Error("code ValidNodeTypes should be non-empty")
	}
	// ValidSourceKinds non-empty + role_classification present (added for salience).
	ks := r.ValidSourceKinds()
	if len(ks) == 0 {
		t.Fatal("ValidSourceKinds empty")
	}
	found := false
	for _, k := range ks {
		if k == "role_classification" {
			found = true
		}
	}
	if !found {
		t.Error("role_classification should be a valid source kind")
	}
	if !r.IsValidSourceKind("file") || r.IsValidSourceKind("bogus-kind") {
		t.Error("IsValidSourceKind known/unknown mismatch")
	}
	// Status / trigger validators.
	if !r.IsValidStatus("active") || r.IsValidStatus("bogus-status") {
		t.Error("IsValidStatus known/unknown mismatch")
	}
	if r.IsValidTrigger("definitely-not-a-trigger") {
		t.Error("unknown trigger should be invalid")
	}
	if !r.IsValidLayer("code") || r.IsValidLayer("bogus-layer") {
		t.Error("IsValidLayer known/unknown mismatch")
	}
	if !r.IsValidDerivation("hard") && !r.IsValidDerivation("inferred") {
		t.Error("expected at least one known derivation kind")
	}
}

func TestSaliencePolicy_NilReceivers(t *testing.T) {
	var p *SaliencePolicy // nil
	if _, ok := p.Rule("type:x", "default"); ok {
		t.Error("nil policy Rule should return ok=false")
	}
	if _, ok := p.Role("x"); ok {
		t.Error("nil policy Role should return ok=false")
	}
	if p.IsNoiseRole("generated") {
		t.Error("nil policy IsNoiseRole should be false")
	}
	if got := p.KnownRoles(); len(got) != 1 || got[0] != "unknown" {
		t.Errorf("nil policy KnownRoles should be [unknown], got %v", got)
	}
	// Registry with nil salience returns a non-nil empty policy.
	r := &Registry{}
	if r.SaliencePolicy() == nil {
		t.Fatal("SaliencePolicy() must never be nil")
	}
}

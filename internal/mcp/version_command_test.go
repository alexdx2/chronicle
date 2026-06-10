package mcp

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/version"
)

func TestVersionCommandRegistered(t *testing.T) {
	if _, ok := UserCommands["version"]; !ok {
		t.Fatal("UserCommands missing version")
	}
	if _, ok := CommandInstructions["version"]; !ok {
		t.Fatal("CommandInstructions missing version")
	}
	instr := CommandInstructions["version"]
	if !strings.Contains(instr, "chronicle_mcp_identity") {
		t.Errorf("version instructions should mention chronicle_mcp_identity, got: %s", instr)
	}
}

func TestVersionIdentityFingerprintStable(t *testing.T) {
	id := version.Identity()
	if id.ReleaseCodename != "osprey-vw1" {
		t.Fatalf("release_codename = %q, want osprey-vw1", id.ReleaseCodename)
	}
	if id.Fingerprint != "1f8b19d6ea28" {
		t.Fatalf("fingerprint = %q, want 1f8b19d6ea28", id.Fingerprint)
	}
}

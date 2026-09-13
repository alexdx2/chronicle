package mcpserver

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
	if id.ReleaseCodename != "kestrel-fresh1" {
		t.Fatalf("release_codename = %q, want kestrel-fresh1", id.ReleaseCodename)
	}
	if id.Fingerprint != "b7fe4b4406ba" {
		t.Fatalf("fingerprint = %q, want b7fe4b4406ba", id.Fingerprint)
	}
}

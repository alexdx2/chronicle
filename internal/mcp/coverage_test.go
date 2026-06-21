package mcp

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long: %q", got)
	}
	if got := truncate("hi", 10); got != "hi" {
		t.Errorf("truncate short: %q", got)
	}
	if got := truncate("exact", 5); got != "exact" {
		t.Errorf("truncate exact: %q", got)
	}
}

func TestMakeSummary(t *testing.T) {
	if got := makeSummary("chronicle_import_all", `{"nodes_created":2,"edges_created":3,"evidence_created":1}`); !strings.Contains(got, "2n") || !strings.Contains(got, "3e") {
		t.Errorf("import summary: %q", got)
	}
	if got := makeSummary("chronicle_import_all", `{"dry_run":true,"valid":true}`); got != "dry_run: valid" {
		t.Errorf("dry_run valid: %q", got)
	}
	if got := makeSummary("chronicle_import_all", `{"dry_run":true,"valid":false,"errors":["a","b"]}`); got != "dry_run: 2 errors" {
		t.Errorf("dry_run errors: %q", got)
	}
	if got := makeSummary("anything", `[1,2,3,4]`); got != "4 items" {
		t.Errorf("array summary: %q", got)
	}
	if got := makeSummary("anything", `not json at all`); got != "" {
		t.Errorf("invalid json summary should be empty: %q", got)
	}
}

func TestScanCheckpointsInstruction(t *testing.T) {
	if BuildScanCheckpointsInstruction() == "" {
		t.Error("BuildScanCheckpointsInstruction should be non-empty")
	}
	if len(GetScanCheckpoints()) == 0 {
		t.Error("expected scan checkpoints")
	}
}

func TestServerSetters(t *testing.T) {
	SetManifestPath("/tmp/x/chronicle.domain.yaml")
	if manifestFilePath != "/tmp/x/chronicle.domain.yaml" {
		t.Error("SetManifestPath")
	}
	SetAdminPort(4321)
	if adminPortValue != 4321 {
		t.Error("SetAdminPort")
	}
	SetLiveCheck(true)
	if !liveCheckEnabled {
		t.Error("SetLiveCheck")
	}
	SetLiveCheck(false)
	// SetGuideStore accepts nil without panicking.
	SetGuideStore(nil)
}

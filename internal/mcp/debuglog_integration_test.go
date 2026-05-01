package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugLoggerIntegration(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, "debug")

	dl, err := NewDebugLogger(debugDir, "0.3.0")
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	SetDebugLogger(dl)
	defer func() {
		SetDebugLogger(nil)
	}()

	// Simulate a session: scan flow
	dl.LogToolCall("chronicle_scan_status", map[string]any{"domain": "test"}, "domain: test", 10, "")
	dl.LogToolCall("chronicle_revision_create", map[string]any{"domain": "test", "after_sha": "abc"}, "rev #1", 5, "")
	dl.LogToolCall("chronicle_import_all", map[string]any{"revision_id": 1.0}, "3n 2e 5ev", 120, "")
	dl.LogToolCall("chronicle_node_upsert", map[string]any{"name": "TestService"}, "", 3, "missing required field: kind")
	dl.LogToolCall("chronicle_node_upsert", map[string]any{"name": "TestService", "kind": "service"}, "node_id: 1", 8, "")

	// Claude writes some notes
	dl.LogClaudeMessage("scanned 3 files, found 3 services")
	dl.LogClaudeMessage("node_upsert failed because I forgot kind param — error message was clear")

	// Test abandoned detection
	dl.LogAbandoned(1)

	// Close and read files
	dl.Close()

	entries, _ := os.ReadDir(debugDir)
	var jsonlPath, mdPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlPath = filepath.Join(debugDir, e.Name())
		}
		if strings.HasSuffix(e.Name(), ".claude.md") {
			mdPath = filepath.Join(debugDir, e.Name())
		}
	}

	// Verify JSONL
	jsonlData, _ := os.ReadFile(jsonlPath)
	lines := strings.Split(strings.TrimSpace(string(jsonlData)), "\n")

	// session_start + 5 tool_calls + 1 inferred_retry + 1 inferred_abandoned = 8
	if len(lines) < 7 {
		t.Fatalf("expected at least 7 JSONL lines, got %d", len(lines))
	}

	// Verify session_start is first
	var first DebugEntry
	json.Unmarshal([]byte(lines[0]), &first)
	if first.Type != "session_start" {
		t.Errorf("first entry should be session_start, got %s", first.Type)
	}
	if first.ChronicleVersion != "0.3.0" {
		t.Errorf("expected version 0.3.0, got %s", first.ChronicleVersion)
	}

	// Verify error was logged
	hasError := false
	for _, line := range lines {
		var e DebugEntry
		json.Unmarshal([]byte(line), &e)
		if e.IsError && e.Tool == "chronicle_node_upsert" {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected an error entry for chronicle_node_upsert")
	}

	// Verify inferred_retry
	hasRetry := false
	for _, line := range lines {
		var e DebugEntry
		json.Unmarshal([]byte(line), &e)
		if e.Type == "inferred_retry" && e.Tool == "chronicle_node_upsert" {
			hasRetry = true
		}
	}
	if !hasRetry {
		t.Error("expected inferred_retry entry for chronicle_node_upsert")
	}

	// Verify abandoned
	hasAbandoned := false
	for _, line := range lines {
		var e DebugEntry
		json.Unmarshal([]byte(line), &e)
		if e.Type == "inferred_abandoned" {
			hasAbandoned = true
			if e.RevisionID != 1 {
				t.Errorf("expected revision_id 1, got %d", e.RevisionID)
			}
		}
	}
	if !hasAbandoned {
		t.Error("expected inferred_abandoned entry")
	}

	// Verify markdown
	mdData, _ := os.ReadFile(mdPath)
	mdContent := string(mdData)
	if !strings.Contains(mdContent, "scanned 3 files") {
		t.Error("missing Claude note in markdown")
	}
	if !strings.Contains(mdContent, "error message was clear") {
		t.Error("missing second Claude note")
	}
}

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewDebugLogger(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, ".depbot", "debug")

	dl, err := NewDebugLogger(debugDir, "0.3.0")
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer dl.Close()

	// Check files were created
	entries, err := os.ReadDir(debugDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 files, got %d", len(entries))
	}

	var hasJSONL, hasMD bool
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			hasJSONL = true
		}
		if strings.HasSuffix(e.Name(), ".claude.md") {
			hasMD = true
		}
	}
	if !hasJSONL {
		t.Error("missing .jsonl file")
	}
	if !hasMD {
		t.Error("missing .claude.md file")
	}
}

func TestDebugLoggerToolCall(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, ".depbot", "debug")

	dl, err := NewDebugLogger(debugDir, "0.3.0")
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer dl.Close()

	// Log a successful call
	dl.LogToolCall("chronicle_node_upsert", map[string]any{"key": "test"}, "node_id: 1", 42, "")

	// Log an error call
	dl.LogToolCall("chronicle_edge_upsert", map[string]any{"from": "a"}, "", 3, "missing to_node_key")

	// Log a retry (same tool, within 3 calls)
	dl.LogToolCall("chronicle_edge_upsert", map[string]any{"from": "a", "to": "b"}, "edge_id: 1", 5, "")

	// Read the JSONL file
	entries, _ := os.ReadDir(debugDir)
	var jsonlPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlPath = filepath.Join(debugDir, e.Name())
		}
	}

	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// session_start + tool_call + tool_call(error) + tool_call(success) + inferred_retry = 5 lines
	if len(lines) != 5 {
		t.Fatalf("expected 5 JSONL lines, got %d:\n%s", len(lines), string(data))
	}

	// Verify inferred_retry is present
	var lastEntry DebugEntry
	if err := json.Unmarshal([]byte(lines[4]), &lastEntry); err != nil {
		t.Fatalf("unmarshal last entry: %v", err)
	}
	if lastEntry.Type != "inferred_retry" {
		t.Errorf("expected inferred_retry, got %s", lastEntry.Type)
	}
	if lastEntry.Tool != "chronicle_edge_upsert" {
		t.Errorf("expected chronicle_edge_upsert, got %s", lastEntry.Tool)
	}
}

func TestDebugLoggerClaudeMessage(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, ".depbot", "debug")

	dl, err := NewDebugLogger(debugDir, "0.3.0")
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}

	dl.LogClaudeMessage("wasn't sure if kind should be model or entity")
	dl.LogClaudeMessage("import_all returned success but node count didn't change")
	dl.Close()

	entries, _ := os.ReadDir(debugDir)
	var mdPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".claude.md") {
			mdPath = filepath.Join(debugDir, e.Name())
		}
	}

	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# Debug Session") {
		t.Error("missing heading")
	}
	if !strings.Contains(content, "- wasn't sure if kind should be model or entity") {
		t.Error("missing first message")
	}
	if !strings.Contains(content, "- import_all returned success but node count didn't change") {
		t.Error("missing second message")
	}
}

func TestDebugLoggerGlobalAccessor(t *testing.T) {
	dir := t.TempDir()
	debugDir := filepath.Join(dir, ".depbot", "debug")

	// Initially nil
	if GetDebugLogger() != nil {
		t.Fatal("expected nil before init")
	}

	dl, err := NewDebugLogger(debugDir, "0.3.0")
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	SetDebugLogger(dl)
	defer func() {
		SetDebugLogger(nil)
		dl.Close()
	}()

	if GetDebugLogger() == nil {
		t.Fatal("expected non-nil after SetDebugLogger")
	}
}

func TestDebugLogTool(t *testing.T) {
	tool := debugLogTool()
	if tool.Name != "chronicle_debug_log" {
		t.Errorf("expected chronicle_debug_log, got %s", tool.Name)
	}
}

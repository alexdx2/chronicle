package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func newLabTestGraph(t *testing.T) *graph.Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	// Write a minimal manifest so the graph has a project root.
	manifest := filepath.Join(dir, "chronicle.domain.yaml")
	_ = os.WriteFile(manifest, []byte("domain: test\nrepositories:\n  - name: test\n    path: .\n"), 0644)
	return graph.New(s, reg)
}

func makeRevisionRequest(args map[string]any) mcplib.CallToolRequest {
	var req mcplib.CallToolRequest
	req.Params.Arguments = args
	return req
}

func TestRevisionCreate_LabMetadata(t *testing.T) {
	g := newLabTestGraph(t)

	h := revisionCreateHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"domain":      "tom-and-jerry__lab1",
		"after_sha":   "abc",
		"autopilot":   true,
		"answers":     `{"phase1_summary":"skip flows"}`,
		"base_domain": "tom-and-jerry",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned error: %v", res.Content)
	}

	// Extract revision_id from JSON result.
	if len(res.Content) == 0 {
		t.Fatal("no content in result")
	}
	text, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	revID := int64(out["revision_id"].(float64))

	cfg, err := g.Store().LabConfigForRevision(revID)
	if err != nil {
		t.Fatalf("LabConfigForRevision: %v", err)
	}
	if !cfg.Autopilot || cfg.BaseDomain != "tom-and-jerry" || cfg.Answers["phase1_summary"] != "skip flows" {
		t.Errorf("lab config not persisted: %+v", cfg)
	}
}

func TestRevisionCreate_NoLabParams_KeepsEmptyMetadata(t *testing.T) {
	g := newLabTestGraph(t)

	h := revisionCreateHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"domain":    "tom-and-jerry",
		"after_sha": "def",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned error: %v", res.Content)
	}

	text, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	revID := int64(out["revision_id"].(float64))

	cfg, err := g.Store().LabConfigForRevision(revID)
	if err != nil {
		t.Fatalf("LabConfigForRevision: %v", err)
	}
	// No lab params → zero config (metadata stays "{}").
	if cfg.Autopilot || cfg.BaseDomain != "" || len(cfg.Answers) != 0 {
		t.Errorf("expected zero lab config, got: %+v", cfg)
	}
}

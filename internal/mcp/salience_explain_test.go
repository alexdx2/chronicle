package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/alexdx2/chronicle-core/store"
)

func TestSalienceExplainTool(t *testing.T) {
	g := newLabTestGraph(t)
	if _, err := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "data:dto:d:x", Layer: "data", NodeType: "dto", DomainKey: "d",
		Name: "XDto", FilePath: "app/x.dto.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1,
		Metadata: `{"role":"request_dto","role_confidence":0.9}`,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	h := salienceExplainHandler(g)
	var req mcplib.CallToolRequest
	req.Params.Arguments = map[string]any{"node_key": "data:dto:d:x", "level": "focus"}
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent)
	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["render_mode"] != "attached_detail" {
		t.Errorf("render_mode=%v want attached_detail", out["render_mode"])
	}
	if tr, ok := out["trace"].([]any); !ok || len(tr) == 0 {
		t.Errorf("trace missing or empty: %v", out["trace"])
	}

	// node_key is required.
	req.Params.Arguments = map[string]any{}
	res, err = h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Error("missing node_key must be an error result")
	}
}

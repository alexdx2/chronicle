package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// The query command teaches the agent (the brain) how to compose Chronicle's
// deterministic retrieval tools. NL understanding lives here, in instructions —
// not inside any tool.

func TestQueryCommand_Registered(t *testing.T) {
	if _, ok := UserCommands["query"]; !ok {
		t.Fatal("query command missing from UserCommands")
	}
	if _, ok := CommandInstructions["query"]; !ok {
		t.Fatal("query command missing from CommandInstructions")
	}
}

func TestQueryCommand_TeachesSearchFirst(t *testing.T) {
	instr := CommandInstructions["query"]
	for _, want := range []string{
		"chronicle_node_search",
		"chronicle_subgraph",
		"chronicle_impact",
		"chronicle_query_path",
		"Never guess",
		"trust_score",
	} {
		assertContains(t, instr, want, "query pack must mention "+want)
	}
}

func TestQueryCommand_HandlerReturnsInstructions(t *testing.T) {
	g := newSearchTestGraph(t)
	h := commandHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{"command": "query"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	instr, _ := out["instructions"].(string)
	if instr == "" {
		t.Fatal("expected non-empty instructions")
	}
	assertContains(t, instr, "chronicle_node_search", "handler output must teach node_search")
}

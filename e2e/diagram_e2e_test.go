package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/mcpserver"
)

// callDiagram calls chronicle_diagram_build through the real MCP server
// (JSON-RPC) against the tom-and-jerry graph.
func callDiagram(t *testing.T, args map[string]any) (map[string]any, bool) {
	t.Helper()
	g, _ := setupTomAndJerry(t)
	s := mcpserver.NewServer(g)
	reqBytes, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "chronicle_diagram_build", "arguments": args},
	})
	resp := s.HandleMessage(context.Background(), reqBytes)
	respBytes, _ := json.Marshal(resp)
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &rpc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, respBytes)
	}
	if rpc.Error != nil {
		t.Fatalf("rpc error: %s", rpc.Error.Message)
	}
	if rpc.Result.IsError {
		return map[string]any{"error": rpc.Result.Content[0].Text}, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(rpc.Result.Content[0].Text), &out); err != nil {
		t.Fatalf("tool result is not JSON: %v\n%s", err, rpc.Result.Content[0].Text)
	}
	return out, false
}

// The diagram tool answers the C4 questions on the showcase graph with the
// diagram itself (mermaid + text) and a self-describing dashboard link.
func TestDiagram_C2_ContainersFromGraph(t *testing.T) {
	out, isErr := callDiagram(t, map[string]any{"title": "Containers", "preset": "c2"})
	if isErr {
		t.Fatalf("tool error: %v", out["error"])
	}
	m, _ := out["mermaid"].(string)
	for _, want := range []string{"flowchart TB", "arena_api[", "tom_api[", `|"HTTP`} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid missing %q:\n%s", want, m)
		}
	}
	if lvl, _ := out["level"].(string); lvl != "c2" {
		t.Errorf("level = %q", lvl)
	}
	if u, _ := out["url"].(string); !strings.Contains(u, "#v/") {
		t.Errorf("url = %q, want #v/ deep link", u)
	}
	if txt, _ := out["text"].(string); !strings.Contains(txt, "## Arrows") {
		t.Errorf("text has no arrows section:\n%s", txt)
	}
}

func TestDiagram_C3_OneService(t *testing.T) {
	out, isErr := callDiagram(t, map[string]any{"title": "Inside arena-api", "preset": "c3", "target": "arena-api"})
	if isErr {
		t.Fatalf("tool error: %v", out["error"])
	}
	txt, _ := out["text"].(string)
	if !strings.Contains(txt, "# Inside arena-api") {
		t.Errorf("title missing:\n%s", txt)
	}
	if strings.Contains(txt, "## tom-api (service)") {
		t.Errorf("a C3 of arena-api must not frame other services:\n%s", txt)
	}
}

func TestDiagram_Impact_SubjectOnTop(t *testing.T) {
	out, isErr := callDiagram(t, map[string]any{"title": "Impact", "preset": "impact", "target": "contract:topic:tomandjerry:battle-results"})
	if isErr {
		t.Fatalf("tool error: %v", out["error"])
	}
	if m, _ := out["mermaid"].(string); !strings.HasPrefix(m, "flowchart BT") {
		t.Errorf("impact renders bottom-up:\n%s", m)
	}
}

func TestDiagram_UnknownPresetIsAnError(t *testing.T) {
	if _, isErr := callDiagram(t, map[string]any{"title": "x", "preset": "overview"}); !isErr {
		t.Fatal("unknown preset must error (there is no hand-authored overview any more)")
	}
}

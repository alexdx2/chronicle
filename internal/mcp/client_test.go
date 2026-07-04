package mcp

import (
	"context"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestClientSupportsSubagents(t *testing.T) {
	cases := []struct {
		client string
		want   bool
	}{
		{"claude-code", true},
		{"claude-ai", true},
		{"Claude Code", true},
		{"codex", false},
		{"codex-cli", false},
		{"cursor", false},
		{"gemini-cli", false},
		{"", false}, // unknown client → safe default is the single-agent flow
	}
	for _, c := range cases {
		setConnectedClientForTest(t, c.client)
		if got := clientSupportsSubagents(); got != c.want {
			t.Errorf("clientSupportsSubagents() with client %q = %v; want %v", c.client, got, c.want)
		}
	}
}

func TestConnectedClientRoundtrip(t *testing.T) {
	setConnectedClientForTest(t, "codex 0.75.0")
	if got := ConnectedClient(); got != "codex 0.75.0" {
		t.Errorf("ConnectedClient() = %q; want %q", got, "codex 0.75.0")
	}
}

// mcp_identity must report which client was detected — that's the debugging
// handle when a scan behaves unexpectedly on a new client.
func TestMCPIdentity_ReportsConnectedClient(t *testing.T) {
	setConnectedClientForTest(t, "codex")

	result := callToolText(t, mcpIdentityHandler(), map[string]any{})
	if !strings.Contains(result, `"connected_client":"codex"`) {
		t.Errorf("identity payload missing connected_client:\n%s", result)
	}
}

// setConnectedClientForTest sets the detected client and restores the previous
// value when the test finishes.
func setConnectedClientForTest(t *testing.T, name string) {
	t.Helper()
	prev := ConnectedClient()
	SetConnectedClient(name)
	t.Cleanup(func() { SetConnectedClient(prev) })
}

// callToolText invokes a tool handler and returns the raw text payload.
func callToolText(t *testing.T, h server.ToolHandlerFunc, args map[string]any) string {
	t.Helper()
	var req mcplib.CallToolRequest
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return text.Text
}

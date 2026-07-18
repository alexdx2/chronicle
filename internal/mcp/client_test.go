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

// The scan command must dispatch per client: solo flow for clients without a
// Task tool (Codex, Cursor), orchestrator flow for Claude. This is the
// Claude↔Codex contract the lab parity gate builds on.
func TestScanCommandDispatchPerClient(t *testing.T) {
	g := newSearchTestGraph(t)
	h := commandHandler(g)

	setConnectedClientForTest(t, "codex")
	solo := callToolText(t, h, map[string]any{"command": "scan"})
	if !strings.Contains(solo, "sole extractor") {
		t.Errorf("codex client must get the solo scan flow; got:\n%.400s", solo)
	}
	if strings.Contains(strings.ToLower(solo), "spawn") {
		t.Errorf("solo scan flow must not mention spawning")
	}

	setConnectedClientForTest(t, "claude-code")
	orch := callToolText(t, h, map[string]any{"command": "scan"})
	if strings.Contains(orch, "sole extractor") {
		t.Errorf("claude client must get the orchestrator flow")
	}
	if !strings.Contains(strings.ToLower(orch), "spawn") {
		t.Errorf("orchestrator flow must tell the agent to spawn extractors")
	}
}

// Discoveries reported without an explicit source must carry the connected
// client's name, not a hardcoded "claude" (naming parity for Codex et al.).
func TestReportDiscoveryDefaultsSourceToClient(t *testing.T) {
	g := newSearchTestGraph(t)
	h := reportDiscoveryHandler(g)

	setConnectedClientForTest(t, "codex")
	callToolText(t, h, map[string]any{
		"domain":   "testapp",
		"category": "pattern",
		"title":    "solo scan finding",
		"content":  "codex discovered something",
	})

	discoveries, err := g.Store().ListDiscoveries("testapp", "")
	if err != nil {
		t.Fatalf("ListDiscoveries: %v", err)
	}
	if len(discoveries) == 0 {
		t.Fatal("no discovery stored")
	}
	if discoveries[0].Source != "codex" {
		t.Errorf("discovery source = %q, want codex", discoveries[0].Source)
	}
}

package mcpserver

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

func TestToolsUniqueAndComplete(t *testing.T) {
	g := newSearchTestGraph(t)
	tools := Tools(g)
	if len(tools) < 60 {
		t.Fatalf("expected the full toolset (>=60), got %d", len(tools))
	}
	seen := map[string]bool{}
	for _, st := range tools {
		if st.Tool.Name == "" {
			t.Fatal("tool with empty name")
		}
		if seen[st.Tool.Name] {
			t.Fatalf("duplicate tool name %q", st.Tool.Name)
		}
		seen[st.Tool.Name] = true
	}
	for _, want := range []string{
		"chronicle_scan_next_file", "chronicle_commit_scan_outbox",
		"chronicle_import_all", "chronicle_command", "chronicle_node_search",
	} {
		if !seen[want] {
			t.Fatalf("toolset missing %q", want)
		}
	}
}

func TestToolsWithLoggingSameNames(t *testing.T) {
	g := newSearchTestGraph(t)
	plain, logged := Tools(g), ToolsWithLogging(g, g.Store())
	if len(plain) != len(logged) {
		t.Fatalf("name sets differ: %d vs %d", len(plain), len(logged))
	}
	for i := range plain {
		if plain[i].Tool.Name != logged[i].Tool.Name {
			t.Fatalf("order/name drift at %d: %q vs %q", i, plain[i].Tool.Name, logged[i].Tool.Name)
		}
	}
}

func TestServerInstructionsNonEmpty(t *testing.T) {
	if !strings.Contains(ServerInstructions(), "chronicle_command") {
		t.Fatal("instructions must reference chronicle_command")
	}
}

func TestToolsWithLoggingDebugToolMirrorsLogger(t *testing.T) {
	g := newSearchTestGraph(t)
	has := func(tools []server.ServerTool) bool {
		for _, st := range tools {
			if st.Tool.Name == "chronicle_debug_log" {
				return true
			}
		}
		return false
	}
	if has(ToolsWithLogging(g, g.Store())) {
		t.Fatal("debug tool must be absent without an active debug logger")
	}
	if has(Tools(g)) {
		t.Fatal("plain Tools never carries the debug tool (mirrors NewServer)")
	}
}

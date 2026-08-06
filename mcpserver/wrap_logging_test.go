package mcpserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// WrapWithLogging is the exported form of loggingWrap: embedders
// (chronicle-pro's single-repo superset) use it so their tools land in
// mcp_request_log exactly like core's own.

func TestWrapWithLogging_WritesRequestLogRow(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	inner := func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return jsonResult(map[string]any{"ok": true}), nil
	}
	var wrapped server.ToolHandlerFunc = WrapWithLogging(s, "pro_test_tool", inner)

	var req mcplib.CallToolRequest
	req.Params.Arguments = map[string]any{"a": "b"}
	if _, err := wrapped(context.Background(), req); err != nil {
		t.Fatalf("wrapped handler: %v", err)
	}

	rows, err := s.ListRecentRequests(1)
	if err != nil {
		t.Fatalf("ListRecentRequests: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 request-log row, got %d", len(rows))
	}
	if rows[0].ToolName != "pro_test_tool" {
		t.Fatalf("logged tool name = %q, want pro_test_tool", rows[0].ToolName)
	}
	if rows[0].ParamsJSON == "" {
		t.Fatal("params not logged")
	}
}

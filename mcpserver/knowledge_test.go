package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/store"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func callReq(name string, args map[string]any) mcplib.CallToolRequest {
	var req mcplib.CallToolRequest
	req.Params.Name = name
	if args == nil {
		args = map[string]any{}
	}
	req.Params.Arguments = args
	return req
}

type fixedLiner string

func (f fixedLiner) KnowledgeLine(string, string) string { return string(f) }

func TestKnowledgeBlockAppendedToQueryTools(t *testing.T) {
	SetKnowledgeLiner(fixedLiner("knowledge: r scanned@abc · current"))
	t.Cleanup(func() { SetKnowledgeLiner(nil) })
	s := newTestStore(t)
	inner := func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText(`[{"a":1}]`), nil
	}
	res, _ := loggingWrap(s, "chronicle_node_search", inner)(context.Background(), callReq("chronicle_node_search", nil))
	if len(res.Content) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(res.Content))
	}
	if res.Content[0].(mcplib.TextContent).Text != `[{"a":1}]` {
		t.Fatalf("first block changed")
	}
	if res.Content[1].(mcplib.TextContent).Text != "knowledge: r scanned@abc · current" {
		t.Fatalf("second block: %+v", res.Content[1])
	}
	res, _ = loggingWrap(s, "chronicle_node_upsert", inner)(context.Background(), callReq("chronicle_node_upsert", nil))
	if len(res.Content) != 1 {
		t.Fatalf("non-query tool must stay single-block")
	}
}

func TestKnowledgeBlockSkippedOnError(t *testing.T) {
	SetKnowledgeLiner(fixedLiner("knowledge: r scanned@abc · current"))
	t.Cleanup(func() { SetKnowledgeLiner(nil) })
	s := newTestStore(t)

	isErr := func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultError("boom"), nil
	}
	res, _ := loggingWrap(s, "chronicle_impact", isErr)(context.Background(), callReq("chronicle_impact", nil))
	if len(res.Content) != 1 {
		t.Fatalf("IsError result must stay single-block, got %d", len(res.Content))
	}

	failed := func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText("{}"), context.Canceled
	}
	res, err := loggingWrap(s, "chronicle_impact", failed)(context.Background(), callReq("chronicle_impact", nil))
	if err == nil {
		t.Fatal("expected the inner error to propagate")
	}
	if len(res.Content) != 1 {
		t.Fatalf("errored call must stay single-block, got %d", len(res.Content))
	}
}

func TestKnowledgeBlockNoLinerNoBlock(t *testing.T) {
	SetKnowledgeLiner(nil)
	s := newTestStore(t)
	inner := func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText(`[]`), nil
	}
	res, _ := loggingWrap(s, "chronicle_node_search", inner)(context.Background(), callReq("chronicle_node_search", nil))
	if len(res.Content) != 1 {
		t.Fatalf("no liner installed must append nothing, got %d blocks", len(res.Content))
	}
}

// Pro's federation server has no request-log store — it wraps its tools with
// WrapWithKnowledge and must get the same block from the same allowlist.
func TestWrapWithKnowledgeWithoutLogging(t *testing.T) {
	SetKnowledgeLiner(fixedLiner("knowledge: fed · current"))
	t.Cleanup(func() { SetKnowledgeLiner(nil) })
	inner := func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText(`{"nodes":[]}`), nil
	}
	res, err := WrapWithKnowledge("chronicle_subgraph", inner)(context.Background(), callReq("chronicle_subgraph", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 2 || res.Content[1].(mcplib.TextContent).Text != "knowledge: fed · current" {
		t.Fatalf("want the knowledge block, got %+v", res.Content)
	}
	res, _ = WrapWithKnowledge("chronicle_import_all", inner)(context.Background(), callReq("chronicle_import_all", nil))
	if len(res.Content) != 1 {
		t.Fatalf("non-query tool must stay single-block")
	}
}

// The liner reads the real store: a scan at HEAD reads as current, and the
// answer is cached for ttl so a query burst does not shell out to git per call.
func TestStoreLinerReportsAndCaches(t *testing.T) {
	g, dir := newTestGraphInGitRepo(t)
	if _, err := g.Store().CreateRevision("d", "", gitHead(t, dir), "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	l := NewStoreLiner(dir, "tandj", g.Store(), time.Minute)
	line := l.KnowledgeLine("chronicle_node_search", "")
	if !strings.HasPrefix(line, "knowledge: tandj scanned@") || !strings.HasSuffix(line, "· current") {
		t.Fatalf("line = %q", line)
	}
	// A second revision must not show up while the cached line is still warm.
	if _, err := g.Store().CreateRevision("d", "", "0000000000000000000000000000000000000000", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	if again := l.KnowledgeLine("chronicle_node_search", ""); again != line {
		t.Fatalf("cache not used: %q then %q", line, again)
	}
}

func TestStoreLinerRecomputesAfterTTL(t *testing.T) {
	g, dir := newTestGraphInGitRepo(t)
	l := NewStoreLiner(dir, "tandj", g.Store(), 0)
	if first := l.KnowledgeLine("chronicle_node_search", ""); !strings.Contains(first, "no scan yet") {
		t.Fatalf("first = %q", first)
	}
	if _, err := g.Store().CreateRevision("d", "", gitHead(t, dir), "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	if second := l.KnowledgeLine("chronicle_node_search", ""); !strings.Contains(second, "current") {
		t.Fatalf("ttl 0 must recompute, got %q", second)
	}
}

func TestQueryToolsCoversTheReadOnlyTools(t *testing.T) {
	for _, name := range []string{
		"chronicle_node_search", "chronicle_query_deps", "chronicle_query_reverse_deps",
		"chronicle_impact", "chronicle_query_path", "chronicle_subgraph",
		"chronicle_insights", "chronicle_review_report",
		"chronicle_wiki_federated", "chronicle_wiki_page",
	} {
		if !QueryTools[name] {
			t.Errorf("%s missing from QueryTools", name)
		}
	}
	for _, name := range []string{"chronicle_node_upsert", "chronicle_import_all", "chronicle_scan_status"} {
		if QueryTools[name] {
			t.Errorf("%s must not carry a knowledge block", name)
		}
	}
}

// A host that wraps one handler with both wrappers (logging + knowledge) must
// still get exactly one knowledge block.
func TestKnowledgeBlockNotAppendedTwice(t *testing.T) {
	SetKnowledgeLiner(fixedLiner("knowledge: r scanned@abc · current"))
	t.Cleanup(func() { SetKnowledgeLiner(nil) })
	s := newTestStore(t)
	inner := func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText(`[{"a":1}]`), nil
	}
	doubled := WrapWithKnowledge("chronicle_node_search", WrapWithLogging(s, "chronicle_node_search", inner))
	res, err := doubled(context.Background(), callReq("chronicle_node_search", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 2 {
		t.Fatalf("want 2 blocks (answer + one knowledge line), got %d: %+v", len(res.Content), res.Content)
	}
	if res.Content[1].(mcplib.TextContent).Text != "knowledge: r scanned@abc · current" {
		t.Fatalf("second block: %+v", res.Content[1])
	}
}

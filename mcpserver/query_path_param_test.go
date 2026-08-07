package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// buildSvcMemberTargetGraph builds the same shape as graph.TestQueryPath_StructuralTerminals:
// service --CONTAINS--> member --INJECTS--> target. Under Structural:"none" the service is
// unreachable (its only out-edge is structural); under "terminals" the CONTAINS descent from
// the path's own endpoint is allowed, so the path resolves.
func buildSvcMemberTargetGraph(t *testing.T) *graph.Graph {
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
	g := graph.New(s, reg)

	revID, err := s.CreateRevision("d", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	nodes := []validate.NodeInput{
		{NodeKey: "service:service:d:svc", Layer: "service", NodeType: "service", DomainKey: "d", Name: "svc"},
		{NodeKey: "code:provider:d:member", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "member"},
		{NodeKey: "code:provider:d:target", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "target"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.NodeKey, err)
		}
	}
	edges := []validate.EdgeInput{
		{FromNodeKey: "service:service:d:svc", ToNodeKey: "code:provider:d:member", EdgeType: "CONTAINS", DerivationKind: "hard", FromLayer: "service", ToLayer: "code"},
		{FromNodeKey: "code:provider:d:member", ToNodeKey: "code:provider:d:target", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge %s->%s: %v", e.FromNodeKey, e.ToNodeKey, err)
		}
	}
	return g
}

func TestQueryPathHandler_StructuralParam(t *testing.T) {
	g := buildSvcMemberTargetGraph(t)
	h := queryPathHandler(g)

	args := map[string]any{
		"from_node_key": "service:service:d:svc",
		"to_node_key":   "code:provider:d:target",
	}
	req := makeRevisionRequest(args)
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	got := decodePathResult(t, res)
	if len(got.Paths) != 1 {
		t.Fatalf("default structural: want 1 path (terminals), got %d", len(got.Paths))
	}

	args["structural"] = "none"
	res, err = h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	got = decodePathResult(t, res)
	if len(got.Paths) != 0 {
		t.Fatalf("structural=none: want 0 paths, got %d", len(got.Paths))
	}
}

func TestQueryPathHandler_StructuralParam_Invalid(t *testing.T) {
	g := buildSvcMemberTargetGraph(t)
	h := queryPathHandler(g)

	req := makeRevisionRequest(map[string]any{
		"from_node_key": "service:service:d:svc",
		"to_node_key":   "code:provider:d:target",
		"structural":    "bogus",
	})
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for invalid structural value")
	}
}

func decodePathResult(t *testing.T, res *mcplib.CallToolResult) graph.PathResult {
	t.Helper()
	text, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var out graph.PathResult
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, text.Text)
	}
	return out
}

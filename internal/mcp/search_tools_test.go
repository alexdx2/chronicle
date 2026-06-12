package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func newSearchTestGraph(t *testing.T) *graph.Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	g := graph.New(s, reg)

	revID, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	nodes := []validate.NodeInput{
		{NodeKey: "code:controller:orders:order-controller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrderController"},
		{NodeKey: "code:provider:orders:order-service", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrderService"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:controller:orders:order-controller",
		ToNodeKey:   "code:provider:orders:order-service",
		EdgeType:    "INJECTS", DerivationKind: "hard",
		FromLayer: "code", ToLayer: "code",
	}, revID); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	return g
}

func TestNodeSearchHandler(t *testing.T) {
	g := newSearchTestGraph(t)
	h := nodeSearchHandler(g)

	res, err := h(context.Background(), makeRevisionRequest(map[string]any{"q": "OrderController"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var out []graph.SearchResult
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, text)
	}
	if len(out) == 0 || out[0].NodeKey != "code:controller:orders:order-controller" {
		t.Fatalf("expected order-controller first, got %+v", out)
	}
}

func TestNodeSearchHandlerRequiresQuery(t *testing.T) {
	g := newSearchTestGraph(t)
	h := nodeSearchHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing q")
	}
}

func TestSubgraphHandler(t *testing.T) {
	g := newSearchTestGraph(t)
	h := subgraphHandler(g)

	// Accepts a name (resolveKey), not only a full key.
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"node_key": "OrderController", "depth": float64(1),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var out graph.SubgraphResult
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Nodes) != 2 || len(out.Edges) != 1 {
		t.Fatalf("expected 2 nodes 1 edge, got %+v", out)
	}
}

// listToolNames asks a built server for its tool list over JSON-RPC.
func listToolNames(t *testing.T, s interface {
	HandleMessage(ctx context.Context, message json.RawMessage) mcplib.JSONRPCMessage
}) map[string]bool {
	t.Helper()
	resp := s.HandleMessage(context.Background(),
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var parsed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse tools/list: %v (%s)", err, raw)
	}
	names := map[string]bool{}
	for _, tool := range parsed.Result.Tools {
		names[tool.Name] = true
	}
	return names
}

// TestServerRegistrationParity guards the documented bug class: tools must be
// registered in BOTH NewServer (tests) and NewServerWithLogging (production).
func TestServerRegistrationParity(t *testing.T) {
	g := newSearchTestGraph(t)
	plain := listToolNames(t, NewServer(g))
	logged := listToolNames(t, NewServerWithLogging(g, g.Store()))

	var missing []string
	for name := range plain {
		if !logged[name] {
			missing = append(missing, name+" (only in NewServer)")
		}
	}
	for name := range logged {
		if !plain[name] {
			missing = append(missing, name+" (only in NewServerWithLogging)")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("tool registration drift:\n  %s", strings.Join(missing, "\n  "))
	}
	for _, required := range []string{"chronicle_node_search", "chronicle_subgraph"} {
		if !plain[required] {
			t.Errorf("missing tool %s", required)
		}
	}
}

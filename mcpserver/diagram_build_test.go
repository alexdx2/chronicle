package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/store"
)

// seedTomJerry creates two service nodes connected by CALLS_SERVICE and
// returns their keys.
func seedTomJerry(t *testing.T, g *graph.Graph) (string, string) {
	t.Helper()
	st := g.Store()

	tomKey := "service:service:test:tom"
	jerryKey := "service:service:test:jerry"

	tomID, err := st.UpsertNode(store.NodeRow{
		NodeKey: tomKey, Layer: "service", NodeType: "service",
		DomainKey: "test", Name: "tom", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("upsert tom: %v", err)
	}
	jerryID, err := st.UpsertNode(store.NodeRow{
		NodeKey: jerryKey, Layer: "service", NodeType: "service",
		DomainKey: "test", Name: "jerry", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("upsert jerry: %v", err)
	}
	if _, err := st.UpsertEdge(store.EdgeRow{
		EdgeKey:    tomKey + "->" + jerryKey + ":CALLS_SERVICE",
		FromNodeID: tomID, ToNodeID: jerryID, EdgeType: "CALLS_SERVICE",
		DerivationKind: "hard", Active: true,
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("upsert edge: %v", err)
	}
	return tomKey, jerryKey
}

func toolResultJSON(t *testing.T, res *mcplib.CallToolResult) map[string]any {
	t.Helper()
	if res.IsError {
		t.Fatalf("handler returned error: %v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("no content in result")
	}
	text, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return out
}



func TestQueryDeps_ViewURL(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, _ := seedTomJerry(t, g)

	h := queryDepsHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"node_key": tomKey,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := toolResultJSON(t, res)

	if nodes, ok := out["nodes"].([]any); !ok || len(nodes) == 0 {
		t.Errorf("nodes = %v, want non-empty dep list", out["nodes"])
	}

	viewURL, _ := out["view_url"].(string)
	idx := strings.Index(viewURL, "#v/")
	if idx < 0 {
		t.Fatalf("view_url = %q, want '#v/<base64url-spec>'", viewURL)
	}
	data, err := base64.RawURLEncoding.DecodeString(viewURL[idx+len("#v/"):])
	if err != nil {
		t.Fatalf("view_url payload is not base64url: %v", err)
	}
	var spec viewmodel.ViewSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("decoded view_url is not a ViewSpec: %v (%s)", err, data)
	}
	if spec.Scope.Domain != "test" {
		t.Errorf("spec.scope.domain = %q, want test", spec.Scope.Domain)
	}
	if len(spec.Scope.Nodes) != 1 || spec.Scope.Nodes[0] != tomKey {
		t.Errorf("spec.scope.nodes = %v, want [%s]", spec.Scope.Nodes, tomKey)
	}
	if spec.Expand == nil || spec.Expand.Direction != "out" || spec.Expand.Depth <= 0 {
		t.Errorf("spec.expand = %+v, want direction=out with unbounded depth", spec.Expand)
	}
	if spec.Group.By != "service" || !spec.Collapse {
		t.Errorf("spec group/collapse = %q/%v, want service/true", spec.Group.By, spec.Collapse)
	}

	// The decoded spec must be a VALID ViewSpec — BuildView accepts it.
	if _, err := viewmodel.BuildView(g.Store(), spec); err != nil {
		t.Errorf("decoded spec rejected by BuildView: %v", err)
	}
}


// An agent cannot open the dashboard: the tool answers with the diagram
// itself — mermaid and structured text rendered from the SAME View the
// URL shows — and a named preset is enough to ask for it.
func TestDiagramBuild_Preset_ReturnsMermaidAndText(t *testing.T) {
	g := newLabTestGraph(t)
	seedTomJerry(t, g)

	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"title":  "Containers",
		"domain": "test",
		"preset": "c2",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := toolResultJSON(t, res)
	mermaid, _ := out["mermaid"].(string)
	// The seeded edge has no evidence rows, so the arrow is honestly dashed.
	if !strings.Contains(mermaid, "flowchart TB") || !strings.Contains(mermaid, `tom -.->|"HTTP"| jerry`) {
		t.Errorf("mermaid = %q, want a flowchart with a dashed (no-evidence) tom → jerry HTTP arrow", mermaid)
	}
	text, _ := out["text"].(string)
	if !strings.Contains(text, "# Containers") || !strings.Contains(text, "- tom → jerry: HTTP") {
		t.Errorf("text = %q, want the title and the arrow line", text)
	}
	if url, _ := out["url"].(string); !strings.Contains(url, "#v/") {
		t.Errorf("url should be a self-describing #v/ deep link, got %q", url)
	}
	if lvl, _ := out["level"].(string); lvl != "c2" {
		t.Errorf("level = %q, want c2", lvl)
	}
}

// deps/impact/path/flow presets take their subject from target.
func TestDiagramBuild_Preset_Target(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, jerryKey := seedTomJerry(t, g)

	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"title":  "Impact",
		"domain": "test",
		"preset": "impact",
		"target": jerryKey,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := toolResultJSON(t, res)
	text, _ := out["text"].(string)
	if !strings.Contains(text, "tom") || !strings.Contains(text, "jerry") {
		t.Errorf("impact of jerry should show tom depending on it:\n%s", text)
	}
	res, err = h(context.Background(), makeRevisionRequest(map[string]any{
		"title": "Path", "domain": "test", "preset": "path", "target": tomKey + "," + jerryKey,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out = toolResultJSON(t, res)
	if mermaid, _ := out["mermaid"].(string); !strings.HasPrefix(mermaid, "flowchart LR") {
		t.Errorf("path renders left→right: %q", mermaid)
	}
}

func TestDiagramBuild_UnknownPreset(t *testing.T) {
	g := newLabTestGraph(t)
	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"title": "x", "domain": "test", "preset": "c9",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("unknown preset must be an error, got %v", res.Content)
	}
}

// A custom selection (node_keys) is a ViewSpec too: scope.nodes, edges
// discovered, unknown keys reported, mermaid/text rendered, #v/ URL.
func TestDiagramBuild_NodeKeys_IsAView(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, jerryKey := seedTomJerry(t, g)
	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"title":     "Pair",
		"node_keys": `["` + tomKey + `","` + jerryKey + `","service:service:test:nope"]`,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := toolResultJSON(t, res)
	if nc := int(out["node_count"].(float64)); nc != 2 {
		t.Errorf("node_count = %d, want 2", nc)
	}
	if ec := int(out["edge_count"].(float64)); ec < 1 {
		t.Errorf("edge_count = %d, want >= 1", ec)
	}
	missing, _ := out["missing"].([]any)
	if len(missing) != 1 {
		t.Errorf("missing = %v, want the unknown key", out["missing"])
	}
	if lvl, _ := out["level"].(string); lvl != "custom" {
		t.Errorf("level = %q, want custom", lvl)
	}
	if m, _ := out["mermaid"].(string); !strings.Contains(m, "tom") || !strings.Contains(m, "jerry") {
		t.Errorf("mermaid should draw both nodes:\n%s", m)
	}
	url, _ := out["url"].(string)
	if !strings.Contains(url, "#v/") {
		t.Fatalf("url = %q, want a #v/ deep link", url)
	}
	// The URL decodes back to the spec that built the view.
	raw, err := base64.RawURLEncoding.DecodeString(url[strings.Index(url, "#v/")+3:])
	if err != nil {
		t.Fatalf("decode url: %v", err)
	}
	var spec viewmodel.ViewSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("spec json: %v", err)
	}
	if len(spec.Scope.Nodes) != 3 || spec.Layout.Preset != "custom" {
		t.Errorf("decoded spec = %+v", spec)
	}
}

func TestDiagramBuild_RequiresSomething(t *testing.T) {
	g := newLabTestGraph(t)
	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{"title": "x"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatal("a request with no preset/view_spec/node_keys must error")
	}
}

func TestLargestDomain(t *testing.T) {
	g := newLabTestGraph(t)
	st := g.Store()
	if got := largestDomain(st); got != "" {
		t.Errorf("empty store: got %q", got)
	}
	mk := func(key, domain string) {
		if _, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: "service", NodeType: "service", DomainKey: domain,
			Name: key, Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("service:service:b:one", "b")
	mk("service:service:a:one", "a")
	if got := largestDomain(st); got != "a" {
		t.Errorf("tie breaks alphabetically: got %q", got)
	}
	mk("service:service:b:two", "b")
	if got := largestDomain(st); got != "b" {
		t.Errorf("most active nodes wins: got %q", got)
	}
}

func TestDiagramBuild_NodeKeysAcrossDomainsIsAnError(t *testing.T) {
	g := newLabTestGraph(t)
	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"title": "x", "node_keys": `["service:service:a:one","service:service:b:one"]`,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatal("keys from two domains must be an explicit error, not silent missing")
	}
}

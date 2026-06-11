package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/alexdx2/chronicle-core/internal/diagrams"
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

func TestDiagramBuild_NodeKeys_ViewmodelSession(t *testing.T) {
	g := newLabTestGraph(t)
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

	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"title":     "Tom & Jerry Selection",
		"domain":    "test",
		"node_keys": `["` + tomKey + `","` + jerryKey + `"]`,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := toolResultJSON(t, res)

	// --- Result shape ---
	sessionID, _ := out["session_id"].(string)
	if sessionID == "" {
		t.Fatal("missing session_id in result")
	}
	url, _ := out["url"].(string)
	if !strings.Contains(url, "#c4/s/"+sessionID) {
		t.Errorf("url %q does not contain #c4/s/%s", url, sessionID)
	}
	if nc := int(out["node_count"].(float64)); nc != 2 {
		t.Errorf("node_count = %d, want 2", nc)
	}
	if ec := int(out["edge_count"].(float64)); ec < 1 {
		t.Errorf("edge_count = %d, want >= 1", ec)
	}
	if missing, ok := out["missing"].([]any); !ok || len(missing) != 0 {
		t.Errorf("missing = %v, want empty array", out["missing"])
	}

	// --- Session in the in-process registry (not persisted to SQLite) ---
	title, dataJSON, ok := diagrams.Default.Get(sessionID)
	if !ok {
		t.Fatalf("session %s not in registry", sessionID)
	}
	if title != "Tom & Jerry Selection" {
		t.Errorf("stored title = %q", title)
	}
	t.Logf("session JSON: %s", dataJSON)

	var session struct {
		Kind      string `json:"kind"`
		Level     string `json:"level"`
		Title     string `json:"title"`
		Domain    string `json:"domain"`
		Selection struct {
			Level      string `json:"level"`
			Components []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"components"`
			InternalEdges []struct {
				From string `json:"from"`
				To   string `json:"to"`
				Kind string `json:"kind"`
			} `json:"internal_edges"`
		} `json:"selection"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &session); err != nil {
		t.Fatalf("unmarshal stored session: %v", err)
	}
	if session.Kind != "viewmodel" {
		t.Errorf("kind = %q, want viewmodel", session.Kind)
	}
	if session.Level != "custom" || session.Selection.Level != "custom" {
		t.Errorf("level = %q / selection.level = %q, want custom", session.Level, session.Selection.Level)
	}
	if session.Domain != "test" {
		t.Errorf("domain = %q, want test", session.Domain)
	}
	if len(session.Selection.Components) != 2 {
		t.Fatalf("selection has %d components, want 2", len(session.Selection.Components))
	}
	if len(session.Selection.InternalEdges) < 1 {
		t.Fatalf("selection has %d internal edges, want >= 1", len(session.Selection.InternalEdges))
	}
	ie := session.Selection.InternalEdges[0]
	if ie.From != tomKey || ie.To != jerryKey || ie.Kind != "calls_service" {
		t.Errorf("internal edge = %+v, want %s -> %s (calls_service)", ie, tomKey, jerryKey)
	}
}

func TestDiagramBuild_ViewSpec_Session(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, jerryKey := seedTomJerry(t, g)

	spec := `{"scope":{"domain":"test","nodes":["` + tomKey + `","` + jerryKey + `"]},"group":{"by":"none"},"layout":{"preset":"custom"}}`

	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"title":     "View Spec Session",
		"view_spec": spec,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := toolResultJSON(t, res)

	sessionID, _ := out["session_id"].(string)
	if sessionID == "" {
		t.Fatal("missing session_id")
	}
	if url, _ := out["url"].(string); !strings.Contains(url, "#c4/s/"+sessionID) {
		t.Errorf("url %q does not contain #c4/s/%s", url, sessionID)
	}
	if nc := int(out["node_count"].(float64)); nc != 2 {
		t.Errorf("node_count = %d, want 2", nc)
	}
	if ec := int(out["edge_count"].(float64)); ec != 1 {
		t.Errorf("edge_count = %d, want 1", ec)
	}

	_, dataJSON, ok := diagrams.Default.Get(sessionID)
	if !ok {
		t.Fatalf("session %s not in registry", sessionID)
	}
	var session map[string]any
	if err := json.Unmarshal([]byte(dataJSON), &session); err != nil {
		t.Fatalf("unmarshal stored session: %v", err)
	}
	if session["kind"] != "viewmodel" {
		t.Errorf("kind = %v, want viewmodel", session["kind"])
	}
	if session["level"] != "custom" {
		t.Errorf("level = %v, want custom (layout.preset)", session["level"])
	}

	// --- "view": the full View JSON ---
	view, ok := session["view"].(map[string]any)
	if !ok {
		t.Fatalf("session missing 'view' object: %s", dataJSON)
	}
	if nodes, ok := view["nodes"].([]any); !ok || len(nodes) != 2 {
		t.Errorf("view.nodes = %v, want 2 entries", view["nodes"])
	}
	if edges, ok := view["edges"].([]any); !ok || len(edges) != 1 {
		t.Errorf("view.edges = %v, want 1 entry", view["edges"])
	}
	if _, ok := view["spec"].(map[string]any); !ok {
		t.Errorf("view.spec missing (views must be self-describing)")
	}

	// --- "selection": best-effort projection for the current renderer ---
	sel, ok := session["selection"].(map[string]any)
	if !ok {
		t.Fatalf("session missing 'selection' projection: %s", dataJSON)
	}
	if comps, ok := sel["components"].([]any); !ok || len(comps) != 2 {
		t.Errorf("selection.components = %v, want 2 entries", sel["components"])
	}
	ies, ok := sel["internal_edges"].([]any)
	if !ok || len(ies) != 1 {
		t.Fatalf("selection.internal_edges = %v, want 1 entry", sel["internal_edges"])
	}
	ie, _ := ies[0].(map[string]any)
	if ie["from"] != tomKey || ie["to"] != jerryKey || ie["kind"] != "calls_service" {
		t.Errorf("projected edge = %v, want %s -> %s (calls_service)", ie, tomKey, jerryKey)
	}
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

func TestDiagramBuild_LegacyNodes_LegacySession(t *testing.T) {
	g := newLabTestGraph(t)

	h := diagramBuildHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"title": "Legacy Overview",
		"nodes": `[{"key":"a","label":"A","kind":"domain"},{"key":"b","label":"B","kind":"external"}]`,
		"edges": `[{"from":"a","to":"b","label":"HTTP","kind":"http"}]`,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := toolResultJSON(t, res)

	sessionID, _ := out["session_id"].(string)
	if sessionID == "" {
		t.Fatal("missing session_id")
	}
	if nc := int(out["node_count"].(float64)); nc != 2 {
		t.Errorf("node_count = %d, want 2", nc)
	}
	if ec := int(out["edge_count"].(float64)); ec != 1 {
		t.Errorf("edge_count = %d, want 1", ec)
	}

	_, dataJSON, ok := diagrams.Default.Get(sessionID)
	if !ok {
		t.Fatalf("session %s not in registry", sessionID)
	}
	var session map[string]any
	if err := json.Unmarshal([]byte(dataJSON), &session); err != nil {
		t.Fatalf("unmarshal stored session: %v", err)
	}
	if session["kind"] != "legacy" {
		t.Errorf("kind = %v, want legacy", session["kind"])
	}
	if nodes, ok := session["nodes"].([]any); !ok || len(nodes) != 2 {
		t.Errorf("stored nodes = %v, want 2 entries", session["nodes"])
	}
}

package mcp

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
)

// Confidence redesign Task 4: the caller's confidence input on
// chronicle_node_upsert / chronicle_edge_upsert expresses itself through the
// auto-created manual evidence row. The node/edge confidence column is then
// recomputed from evidence (manual tier cap 0.95), not taken from the raw input.

func newUpsertTestRevision(t *testing.T, g *graph.Graph) int64 {
	t.Helper()
	revID, err := g.Store().CreateRevision("test", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	return revID
}

func callUpsertHandler(t *testing.T, h func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error), args map[string]any) map[string]any {
	t.Helper()
	res, err := h(context.Background(), makeRevisionRequest(args))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned error: %v", res.Content)
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

func manualEvidenceRow(t *testing.T, rows []store.EvidenceRow, extractorID string) store.EvidenceRow {
	t.Helper()
	for _, e := range rows {
		if e.SourceKind == "manual" && e.ExtractorID == extractorID {
			return e
		}
	}
	t.Fatalf("no manual evidence row with extractor_id %q in %d rows", extractorID, len(rows))
	return store.EvidenceRow{}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestNodeUpsert_ConfidenceFlowsThroughEvidence(t *testing.T) {
	g := newLabTestGraph(t)
	revID := newUpsertTestRevision(t, g)

	h := nodeUpsertHandler(g)
	callUpsertHandler(t, h, map[string]any{
		"revision_id": float64(revID),
		"name":        "svc-a",
		"node_key":    "service:service:test:svc-a",
		"layer":       "service",
		"node_type":   "service",
		"domain":      "test",
		"confidence":  0.5,
	})

	node, err := g.Store().GetNodeByKey("service:service:test:svc-a")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	rows, err := g.Store().ListEvidenceByNode(node.NodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}

	// The manual evidence row carries the caller's confidence, not the 0.9 default.
	ev := manualEvidenceRow(t, rows, "mcp:node_upsert")
	if !almostEqual(ev.Confidence, 0.5) {
		t.Errorf("manual evidence confidence = %v, want 0.5", ev.Confidence)
	}

	// Node confidence is the recompute output from that single evidence row.
	if !almostEqual(node.Confidence, 0.5) {
		t.Errorf("node confidence = %v, want 0.5 (recomputed from evidence)", node.Confidence)
	}
}

func TestNodeUpsert_RecomputeRespectsManualTierCap(t *testing.T) {
	g := newLabTestGraph(t)
	revID := newUpsertTestRevision(t, g)

	h := nodeUpsertHandler(g)
	callUpsertHandler(t, h, map[string]any{
		"revision_id": float64(revID),
		"name":        "svc-capped",
		"node_key":    "service:service:test:svc-capped",
		"layer":       "service",
		"node_type":   "service",
		"domain":      "test",
		"confidence":  0.99,
	})

	node, err := g.Store().GetNodeByKey("service:service:test:svc-capped")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	rows, err := g.Store().ListEvidenceByNode(node.NodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}

	// Evidence carries the raw input...
	ev := manualEvidenceRow(t, rows, "mcp:node_upsert")
	if !almostEqual(ev.Confidence, 0.99) {
		t.Errorf("manual evidence confidence = %v, want 0.99", ev.Confidence)
	}

	// ...but the node's confidence is recomputed and capped at the manual
	// tier (0.95). A raw column write would have left 0.99.
	if !almostEqual(node.Confidence, 0.95) {
		t.Errorf("node confidence = %v, want 0.95 (manual tier cap)", node.Confidence)
	}
}

func TestNodeUpsert_NoConfidenceDefaultsEvidenceTo09(t *testing.T) {
	g := newLabTestGraph(t)
	revID := newUpsertTestRevision(t, g)

	h := nodeUpsertHandler(g)
	callUpsertHandler(t, h, map[string]any{
		"revision_id": float64(revID),
		"name":        "svc-default",
		"node_key":    "service:service:test:svc-default",
		"layer":       "service",
		"node_type":   "service",
		"domain":      "test",
	})

	node, err := g.Store().GetNodeByKey("service:service:test:svc-default")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	rows, err := g.Store().ListEvidenceByNode(node.NodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}

	ev := manualEvidenceRow(t, rows, "mcp:node_upsert")
	if !almostEqual(ev.Confidence, 0.9) {
		t.Errorf("manual evidence confidence = %v, want 0.9 default", ev.Confidence)
	}
	if !almostEqual(node.Confidence, 0.9) {
		t.Errorf("node confidence = %v, want 0.9 (recomputed from default evidence)", node.Confidence)
	}
}

func TestEdgeUpsert_ConfidenceFlowsThroughEvidence(t *testing.T) {
	g := newLabTestGraph(t)
	revID := newUpsertTestRevision(t, g)

	nh := nodeUpsertHandler(g)
	for _, name := range []string{"svc-from", "svc-to"} {
		callUpsertHandler(t, nh, map[string]any{
			"revision_id": float64(revID),
			"name":        name,
			"node_key":    "service:service:test:" + name,
			"layer":       "service",
			"node_type":   "service",
			"domain":      "test",
		})
	}

	eh := edgeUpsertHandler(g)
	callUpsertHandler(t, eh, map[string]any{
		"revision_id":     float64(revID),
		"from_node_key":   "service:service:test:svc-from",
		"to_node_key":     "service:service:test:svc-to",
		"edge_type":       "CALLS_SERVICE",
		"derivation_kind": "hard",
		"confidence":      0.5,
	})

	edgeKey := "service:service:test:svc-from->service:service:test:svc-to:CALLS_SERVICE"
	edge, err := g.Store().GetEdgeByKey(edgeKey)
	if err != nil {
		t.Fatalf("GetEdgeByKey(%q): %v", edgeKey, err)
	}
	rows, err := g.Store().ListEvidenceByEdge(edge.EdgeID)
	if err != nil {
		t.Fatalf("ListEvidenceByEdge: %v", err)
	}

	ev := manualEvidenceRow(t, rows, "mcp:edge_upsert")
	if !almostEqual(ev.Confidence, 0.5) {
		t.Errorf("manual evidence confidence = %v, want 0.5", ev.Confidence)
	}
	if !almostEqual(edge.Confidence, 0.5) {
		t.Errorf("edge confidence = %v, want 0.5 (recomputed from evidence)", edge.Confidence)
	}
}

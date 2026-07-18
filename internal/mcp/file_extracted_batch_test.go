package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The 2026-07-18 Codex field test: agents naturally send "facts" as a JSON
// ARRAY (not a pre-encoded string) and invent status values ("ok"). The old
// handler silently DROPPED array facts (type assertion to string) and
// surfaced constraint violations as an unactionable "tool error" — the agent
// cannot self-correct on that. Contract now: arrays accepted, real errors
// returned per item.

func TestFileExtractedBatch_AcceptsFactsArray(t *testing.T) {
	g := newSearchTestGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha", "manual", "full", "{}")

	items := `[{"file_path":"arena-api/package.json","status":"extracted","from_type":"",
		"facts":[{"kind":"declares_service","to":"arena-api"}],"obligation_id":0}]`
	out := callToolText(t, fileExtractedBatchHandler(g), map[string]any{
		"domain": "orders", "revision_id": float64(revID), "items": items,
	})
	var res struct {
		Committed int      `json:"committed"`
		Failures  []string `json:"failures"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("bad response: %v\n%s", err, out)
	}
	if res.Committed != 1 || len(res.Failures) != 0 {
		t.Fatalf("array facts must commit: %s", out)
	}

	// The facts must actually be STORED, not silently dropped.
	rows, err := g.Store().ListUnresolvedExtractions(revID, "orders")
	if err != nil {
		t.Fatalf("ListUnresolvedExtractions: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.FilePath == "arena-api/package.json" && strings.Contains(r.FactsJSON, "declares_service") {
			found = true
		}
	}
	if !found {
		t.Errorf("array-shaped facts were dropped; rows: %+v", rows)
	}
}

func TestFileExtractedBatch_RealErrorPerItem(t *testing.T) {
	g := newSearchTestGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha", "manual", "full", "{}")

	items := `[{"file_path":"a.ts","status":"ok","from_type":"","facts":[],"obligation_id":0}]`
	out := callToolText(t, fileExtractedBatchHandler(g), map[string]any{
		"domain": "orders", "revision_id": float64(revID), "items": items,
	})
	if strings.Contains(out, "tool error") {
		t.Fatalf("bare 'tool error' is unactionable: %s", out)
	}
	if !strings.Contains(out, "invalid status") || !strings.Contains(out, "allowed: extracted") {
		t.Errorf("failure must name the bad status and the allowed values: %s", out)
	}
}

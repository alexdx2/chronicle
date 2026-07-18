package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/paths"
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

// The fallback path must apply the SAME server-side AST merge as the outbox
// commit path — otherwise clients that report via batch (read-only sandboxes)
// get systematically worse graphs (2026-07-18 Codex run: missing enums,
// gateway from_type, DbContext override — all AST-merge products).
func TestFileExtractedBatch_AppliesASTMerge(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "prisma"), 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `model Battle {
  id     String @id
  result BattleResult
}

enum BattleResult {
  TOM_HITS
}
`
	if err := os.WriteFile(filepath.Join(root, "prisma", "schema.prisma"), []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })

	g := newSearchTestGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha", "manual", "full", "{}")

	// LLM reported only the model — AST merge must add the enum + fields.
	items := `[{"file_path":"prisma/schema.prisma","status":"extracted","from_type":"",
		"facts":[{"kind":"model","to":"Battle"}],"obligation_id":0}]`
	out := callToolText(t, fileExtractedBatchHandler(g), map[string]any{
		"domain": "orders", "revision_id": float64(revID), "items": items,
	})
	if !strings.Contains(out, `"committed": 1`) && !strings.Contains(out, `"committed":1`) {
		t.Fatalf("commit failed: %s", out)
	}

	rows, err := g.Store().ListUnresolvedExtractions(revID, "orders")
	if err != nil {
		t.Fatal(err)
	}
	var facts string
	for _, r := range rows {
		if r.FilePath == "prisma/schema.prisma" {
			facts = r.FactsJSON
		}
	}
	if !strings.Contains(facts, `"enum"`) || !strings.Contains(facts, "BattleResult") {
		t.Errorf("AST merge missing on batch path — no enum fact: %s", facts)
	}
	if !strings.Contains(facts, "model_field") {
		t.Errorf("AST merge missing on batch path — no model_field facts: %s", facts)
	}
}

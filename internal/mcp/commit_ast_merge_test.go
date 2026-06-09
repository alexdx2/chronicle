package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// repoRootFromWD walks up from cwd to find the repo root (directory containing .git).
func repoRootFromWD(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// writeOutboxArtifact writes an artifact JSON to outboxDir for filePath.
func writeOutboxArtifact(t *testing.T, outboxDir, filePath, status, fromType string, facts []map[string]any, domain string, revisionID int64) {
	t.Helper()
	safe := strings.ReplaceAll(filePath, "/", "__")
	payload := map[string]any{
		"file_path":   filePath,
		"status":      status,
		"from_type":   fromType,
		"facts":       facts,
		"domain":      domain,
		"revision_id": revisionID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outboxDir, safe+".json"), data, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
}

// TestCommitOutbox_MergesASTFacts verifies that committing an artifact with empty
// facts for a real TypeScript fixture file produces AST import facts and from_type.
func TestCommitOutbox_MergesASTFacts(t *testing.T) {
	root := repoRootFromWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}

	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/arena.module.ts"
	if _, err := os.Stat(filepath.Join(root, relPath)); err != nil {
		t.Skipf("fixture file absent: %s", relPath)
	}

	// Change CWD so graph.ProjectRoot() and readFileContent resolve correctly.
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	g := newLabTestGraph(t)
	st := g.Store()

	revID, err := st.CreateRevision("testdomain", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	// Create the outbox directory where the handler expects artifacts.
	outboxDir := filepath.Join(root, ".depbot", "scan-outbox", fmt.Sprintf("%d", revID))
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outboxDir) })

	// Write artifact with empty facts — only LLM facts absent.
	writeOutboxArtifact(t, outboxDir, relPath, "extracted", "", []map[string]any{}, "testdomain", revID)

	// Run the commit handler.
	h := commitScanOutboxHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"domain":       "testdomain",
		"revision_id":  float64(revID),
		"remove_after": false,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := decodeActionResult(t, res)
	if imported, _ := out["imported"].(float64); imported == 0 {
		t.Fatalf("handler imported 0 artifacts, errors: %v", out["error_msgs"])
	}

	// Verify the saved extraction has AST facts.
	rows, err := st.ListUnresolvedExtractions(revID, "testdomain")
	if err != nil {
		t.Fatalf("ListUnresolvedExtractions: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no extractions saved")
	}

	row := rows[0]

	// from_type should have been set by AST.
	if row.FromType != "module" {
		t.Errorf("from_type = %q, want \"module\"", row.FromType)
	}

	// facts_json must contain an import fact referencing arena.controller.
	var facts []map[string]any
	if err := json.Unmarshal([]byte(row.FactsJSON), &facts); err != nil {
		t.Fatalf("unmarshal facts: %v", err)
	}
	found := false
	for _, f := range facts {
		kind, _ := f["kind"].(string)
		to, _ := f["to"].(string)
		if kind == "import" && strings.Contains(strings.ToLower(to), "arena.controller") {
			found = true
			break
		}
	}
	if !found {
		b, _ := json.MarshalIndent(facts, "", "  ")
		t.Errorf("no import fact for arena.controller found:\n%s", string(b))
	}
}

// TestCommitOutbox_DedupesLLMOverAST verifies that when LLM already emits an
// import for arena.controller with to_type="controller", only one fact survives
// and it retains the LLM's to_type.
func TestCommitOutbox_DedupesLLMOverAST(t *testing.T) {
	root := repoRootFromWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}

	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/arena.module.ts"
	if _, err := os.Stat(filepath.Join(root, relPath)); err != nil {
		t.Skipf("fixture file absent: %s", relPath)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	g := newLabTestGraph(t)
	st := g.Store()

	revID, err := st.CreateRevision("testdomain2", "", "def", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	outboxDir := filepath.Join(root, ".depbot", "scan-outbox", fmt.Sprintf("%d", revID))
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outboxDir) })

	// LLM fact already has to_type="controller" for arena.controller.
	llmFacts := []map[string]any{
		{"kind": "import", "to": "./arena.controller", "to_type": "controller"},
	}
	writeOutboxArtifact(t, outboxDir, relPath, "extracted", "", llmFacts, "testdomain2", revID)

	h := commitScanOutboxHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"domain":       "testdomain2",
		"revision_id":  float64(revID),
		"remove_after": false,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := decodeActionResult(t, res)
	if imported, _ := out["imported"].(float64); imported == 0 {
		t.Fatalf("handler imported 0 artifacts, errors: %v", out["error_msgs"])
	}

	rows, err := st.ListUnresolvedExtractions(revID, "testdomain2")
	if err != nil {
		t.Fatalf("ListUnresolvedExtractions: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no extractions saved")
	}

	var facts []map[string]any
	if err := json.Unmarshal([]byte(rows[0].FactsJSON), &facts); err != nil {
		t.Fatalf("unmarshal facts: %v", err)
	}

	// Count and validate arena.controller import facts.
	var controllerFacts []map[string]any
	for _, f := range facts {
		kind, _ := f["kind"].(string)
		to, _ := f["to"].(string)
		if kind == "import" && strings.Contains(strings.ToLower(to), "arena.controller") {
			controllerFacts = append(controllerFacts, f)
		}
	}

	if len(controllerFacts) != 1 {
		b, _ := json.MarshalIndent(facts, "", "  ")
		t.Fatalf("expected exactly 1 arena.controller import, got %d:\n%s", len(controllerFacts), string(b))
	}

	toType, _ := controllerFacts[0]["to_type"].(string)
	if toType != "controller" {
		t.Errorf("LLM to_type not retained: got %q, want \"controller\"", toType)
	}
}

// ---------------------------------------------------------------------------
// Scope tolerance tests
// ---------------------------------------------------------------------------

// TestDiscoverFiles_ScopeAsArray verifies that discoverFilesHandler accepts
// scope as a native []any (JSON array) rather than a JSON-encoded string.
func TestDiscoverFiles_ScopeAsArray(t *testing.T) {
	root := repoRootFromWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	g := newLabTestGraph(t)
	st := g.Store()

	revID, err := st.CreateRevision("lab", "", "scope-array-test", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	h := discoverFilesHandler(g)

	// Pass scope as a native []any — what the MCP framework delivers when the
	// JSON call has "scope": ["fixtures/tom-and-jerry/arena-api/**"].
	req := makeRevisionRequest(map[string]any{
		"revision_id":  float64(revID),
		"domain":       "lab",
		"votes_needed": float64(1),
		// scope as native array, not JSON-encoded string
		"scope": []any{"fixtures/tom-and-jerry/arena-api/**"},
	})

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Handler must not error — previously the type mismatch silently dropped scope.
	if res.IsError {
		t.Fatalf("handler returned error result: %v", res.Content)
	}

	var out map[string]any
	if len(res.Content) > 0 {
		text, ok := res.Content[0].(mcplib.TextContent)
		if ok {
			_ = json.Unmarshal([]byte(text.Text), &out)
		}
	}

	// The scope should appear in the response echoed back.
	scopeVal, _ := out["scope"]
	if scopeVal == nil {
		t.Error("scope not echoed in response — handler likely dropped array scope")
	}

	// total_files must be > 0 (arena-api has TypeScript files).
	totalFiles, _ := out["total_files"].(float64)
	if totalFiles == 0 {
		t.Error("expected total_files > 0 with arena-api scope")
	}
}

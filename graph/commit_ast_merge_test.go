package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRootFromCWD walks up from cwd until it finds a .git directory.
// Returns "" if not found after 8 levels.
func repoRootFromCWD(t *testing.T) string {
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

// TestMergeASTFacts_ImportFacts verifies that MergeASTFacts computes import facts
// for the real arena.module.ts fixture file, even when llmFacts is empty.
func TestMergeASTFacts_ImportFacts(t *testing.T) {
	root := repoRootFromCWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}

	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/arena.module.ts"
	fullPath := filepath.Join(root, relPath)
	if _, err := os.Stat(fullPath); err != nil {
		t.Skipf("fixture file absent: %s", fullPath)
	}

	// Change CWD to repo root so readFileContent resolves relative paths correctly.
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	merged, fromType := MergeASTFacts(relPath, nil, "", nil)

	if fromType != "module" {
		t.Errorf("from_type = %q, want \"module\"", fromType)
	}

	if len(merged) == 0 {
		t.Fatal("expected merged facts, got none")
	}

	// At least one import fact referencing arena.controller must be present.
	found := false
	for _, f := range merged {
		kind, _ := f["kind"].(string)
		to, _ := f["to"].(string)
		if kind == "import" && strings.Contains(strings.ToLower(to), "arena.controller") {
			found = true
			break
		}
	}
	if !found {
		b, _ := json.MarshalIndent(merged, "", "  ")
		t.Errorf("no import fact for arena.controller found in merged facts:\n%s", string(b))
	}
}

// TestMergeASTFacts_DeduplicatesLLMFacts verifies that when the LLM already
// emitted an import for arena.controller (with to_type="controller"), the
// merge keeps exactly ONE fact for that import and retains to_type.
func TestMergeASTFacts_DeduplicatesLLMFacts(t *testing.T) {
	root := repoRootFromCWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}

	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/arena.module.ts"
	fullPath := filepath.Join(root, relPath)
	if _, err := os.Stat(fullPath); err != nil {
		t.Skipf("fixture file absent: %s", fullPath)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// LLM already emitted an import for ./arena.controller with extra to_type.
	llmFacts := []map[string]any{
		{
			"kind":    "import",
			"to":      "./arena.controller",
			"to_type": "controller",
		},
	}

	merged, _ := MergeASTFacts(relPath, llmFacts, "", nil)

	// Count import facts whose "to" resolves to arena.controller.
	count := 0
	var retainedToType string
	for _, f := range merged {
		kind, _ := f["kind"].(string)
		to, _ := f["to"].(string)
		if kind == "import" && strings.Contains(strings.ToLower(to), "arena.controller") {
			count++
			retainedToType, _ = f["to_type"].(string)
		}
	}

	if count != 1 {
		b, _ := json.MarshalIndent(merged, "", "  ")
		t.Errorf("expected exactly 1 import fact for arena.controller, got %d:\n%s", count, string(b))
	}
	if retainedToType != "controller" {
		t.Errorf("LLM to_type not retained: got %q, want \"controller\"", retainedToType)
	}
}

// TestMergeASTFactsJSON_NonTSFile verifies that non-TS/non-Prisma files are
// returned unchanged.
func TestMergeASTFactsJSON_NonTSFile(t *testing.T) {
	llmJSON := `[{"kind":"endpoint","method":"GET","path":"/health"}]`
	got, fromType := MergeASTFactsJSON("src/main.go", llmJSON, "provider", nil)
	if got != llmJSON {
		t.Errorf("non-TS file changed: got %s", got)
	}
	if fromType != "provider" {
		t.Errorf("from_type changed: got %q, want \"provider\"", fromType)
	}
}

// TestFactKey_Dedup verifies the dedup key logic used in unionFacts.
func TestFactKey_Dedup(t *testing.T) {
	f1 := map[string]any{"kind": "import", "to": "./Arena.Controller"}
	f2 := map[string]any{"kind": "import", "to": "./arena.controller", "to_type": "controller"}

	// Both should produce the same key (lowercased).
	if factKey(f1) != factKey(f2) {
		t.Errorf("factKey mismatch: %q vs %q", factKey(f1), factKey(f2))
	}
}

// TestUnionFacts_LLMWinsOnOverlap verifies that on duplicate, the LLM fact is kept.
func TestUnionFacts_LLMWinsOnOverlap(t *testing.T) {
	astFacts := []map[string]any{
		{"kind": "import", "to": "./arena.controller"},
		{"kind": "import", "to": "./arena.service"},
	}
	llmFacts := []map[string]any{
		{"kind": "import", "to": "./arena.controller", "to_type": "controller"},
	}

	result := unionFacts(astFacts, llmFacts)

	// Result should contain:
	// - arena.service from AST (no LLM overlap)
	// - arena.controller from LLM (with to_type)
	if len(result) != 2 {
		b, _ := json.MarshalIndent(result, "", "  ")
		t.Fatalf("expected 2 facts, got %d:\n%s", len(result), string(b))
	}

	controllerFact := map[string]any(nil)
	for _, f := range result {
		to, _ := f["to"].(string)
		if strings.Contains(strings.ToLower(to), "arena.controller") {
			controllerFact = f
		}
	}
	if controllerFact == nil {
		t.Fatal("arena.controller fact missing from result")
	}
	if controllerFact["to_type"] != "controller" {
		t.Errorf("LLM to_type not retained: %v", controllerFact)
	}
}

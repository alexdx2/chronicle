package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
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

// TestMergeASTFacts_BullTransportField verifies that the Bull ruleset's transport field
// passes through the full MergeASTFacts pipeline (AST → rules → JSON → map[string]any).
func TestMergeASTFacts_BullTransportField(t *testing.T) {
	root := repoRootFromCWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}

	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/battle.queue.ts"
	fullPath := filepath.Join(root, relPath)
	if _, err := os.Stat(fullPath); err != nil {
		t.Skipf("fixture file absent: %s", fullPath)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	merged, _ := MergeASTFacts(relPath, nil, "", nil)

	var foundProduces, foundConsumes bool
	for _, f := range merged {
		kind, _ := f["kind"].(string)
		to, _ := f["to"].(string)
		transport, _ := f["transport"].(string)
		if kind == "produces" && to == "battle-queue" && transport == "queue" {
			foundProduces = true
		}
		if kind == "consumes" && to == "battle-queue" && transport == "queue" {
			foundConsumes = true
		}
	}

	if !foundProduces {
		b, _ := json.MarshalIndent(merged, "", "  ")
		t.Errorf("no produces[queue] fact for battle-queue in merged facts:\n%s", string(b))
	}
	if !foundConsumes {
		b, _ := json.MarshalIndent(merged, "", "  ")
		t.Errorf("no consumes[queue] fact for battle-queue in merged facts:\n%s", string(b))
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

// TestR7d_TransportPreserved_OnLLMWin verifies that when an LLM fact wins over a
// duplicate AST fact, the AST-only "transport" field is copied to the LLM fact.
// This prevents local events from creating broker topic nodes when the LLM omits transport.
func TestR7d_TransportPreserved_OnLLMWin(t *testing.T) {
	// AST fact has transport=local (tagged by rules layer from @OnEvent decorator).
	// LLM fact has no transport field (common LLM omission).
	// After merge, the LLM fact should have transport=local.
	astFacts := []map[string]any{
		{"kind": "consumes", "to": "battle.result", "transport": "local"},
	}
	llmFacts := []map[string]any{
		{"kind": "consumes", "to": "battle.result"}, // no transport
	}

	result := unionFacts(astFacts, llmFacts)

	if len(result) != 1 {
		b, _ := json.MarshalIndent(result, "", "  ")
		t.Fatalf("expected 1 merged fact, got %d:\n%s", len(result), string(b))
	}

	transport, _ := result[0]["transport"].(string)
	if transport != "local" {
		t.Errorf("R7d: transport not preserved; got %q, want \"local\"\nfact: %v", transport, result[0])
	}
}

// TestR7d_TransportPreserved_ToType verifies that to_type is also preserved from AST.
func TestR7d_TransportPreserved_ToType(t *testing.T) {
	astFacts := []map[string]any{
		{"kind": "provides", "to": "ArenaController", "to_type": "controller"},
	}
	llmFacts := []map[string]any{
		{"kind": "provides", "to": "ArenaController"}, // no to_type
	}

	result := unionFacts(astFacts, llmFacts)

	if len(result) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(result))
	}
	toType, _ := result[0]["to_type"].(string)
	if toType != "controller" {
		t.Errorf("R7d: to_type not preserved; got %q, want \"controller\"", toType)
	}
}

// TestR7d_LLMTransport_NotOverridden verifies that if the LLM already has a transport
// field, the AST value does NOT override it.
func TestR7d_LLMTransport_NotOverridden(t *testing.T) {
	astFacts := []map[string]any{
		{"kind": "consumes", "to": "battle.result", "transport": "local"},
	}
	llmFacts := []map[string]any{
		{"kind": "consumes", "to": "battle.result", "transport": "kafka"}, // LLM says kafka
	}

	result := unionFacts(astFacts, llmFacts)

	transport, _ := result[0]["transport"].(string)
	if transport != "kafka" {
		t.Errorf("R7d: LLM transport should not be overridden; got %q, want \"kafka\"", transport)
	}
}

// TestR7d_LocalTransport_NoTopicCreated verifies the full pipeline: LLM fact without
// transport + AST duplicate with transport=local → merged fact → no topic node in graph.
func TestR7d_LocalTransport_NoTopicCreated(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// Simulate: LLM submitted "consumes battle.result" without transport.
	// AST computed "consumes battle.result transport=local".
	// After MergeASTFacts, transport=local should be in the merged facts.
	// After ResolveExtractions, no topic node should be created.

	// We directly test via merged facts in resolve (bypass the file-based merge).
	// The merged result should have transport=local because AST wins the field.
	mergedFacts := `[{"kind":"consumes","to":"battle.result","transport":"local","from_type":"provider"}]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.events.ts", "extracted", "provider", mergedFacts, "")

	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatal(err)
	}

	topics, _ := s.ListNodes(store.NodeFilter{Domain: domain, NodeType: "topic"})
	if len(topics) > 0 {
		keys := make([]string, 0)
		for _, tp := range topics {
			keys = append(keys, tp.NodeKey)
		}
		t.Errorf("R7d: local transport consumes must not create topic nodes; got %v", keys)
	}
}

// TestR6_ProvidesDedup_NoDoubles verifies that when AST emits provides facts from
// @Module and the LLM also emits a provides fact for the same member, unionFacts
// deduplicates on kind|to — leaving exactly one fact per member.
func TestR6_ProvidesDedup_NoDoubles(t *testing.T) {
	astFacts := []map[string]any{
		{"kind": "provides", "to": "ArenaController", "to_type": "controller", "confidence": 0.95},
		{"kind": "provides", "to": "ArenaService", "to_type": "provider", "confidence": 0.95},
	}
	llmFacts := []map[string]any{
		// LLM also provides ArenaController (e.g. from inference)
		{"kind": "provides", "to": "ArenaController", "to_type": "controller", "confidence": 0.80},
	}

	result := unionFacts(astFacts, llmFacts)

	// Should be 2: ArenaService (AST only) + ArenaController (LLM wins)
	if len(result) != 2 {
		b, _ := json.MarshalIndent(result, "", "  ")
		t.Fatalf("expected 2 facts (no double ArenaController), got %d:\n%s", len(result), string(b))
	}

	count := 0
	for _, f := range result {
		to, _ := f["to"].(string)
		if to == "ArenaController" {
			count++
			// LLM fact should win (lower confidence = 0.80)
			if c, _ := f["confidence"].(float64); c != 0.80 {
				t.Errorf("LLM fact should win on overlap; confidence = %v, want 0.80", c)
			}
		}
	}
	if count != 1 {
		t.Errorf("ArenaController appears %d times, want 1", count)
	}
}

// ---------------------------------------------------------------------------
// Defect 3 — AST from_type classification beats LLM from_type
// ---------------------------------------------------------------------------

// TestMergeASTFacts_ASTFromType_WinsOnConflict verifies that when the AST detects
// a from_type (e.g. "provider" from @WebSocketGateway) and the LLM has already
// supplied a different from_type (e.g. "controller"), the AST value wins.
// Deterministic decorator classification is ground truth; LLMs mislabel gateways.
func TestMergeASTFacts_ASTFromType_WinsOnConflict(t *testing.T) {
	root := repoRootFromCWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}
	// battle.gateway.ts has @WebSocketGateway — AST should detect from_type="provider".
	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/battle.gateway.ts"
	if _, err := os.Stat(filepath.Join(root, relPath)); err != nil {
		t.Skipf("fixture absent: %s", relPath)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// LLM artifact says "controller" (the bug: gateways are not controllers).
	_, fromType := MergeASTFacts(relPath, nil, "controller", nil)

	if fromType != "provider" {
		t.Errorf("Defect3: AST from_type must win; got %q, want \"provider\" (AST sees @WebSocketGateway)", fromType)
	}
}

// TestMergeASTFacts_ASTFromType_NoConflict_LLMKept verifies that when the LLM has
// a from_type and the AST has NO opinion (semantic.FromType == ""), the LLM value
// is kept unchanged.
func TestMergeASTFacts_ASTFromType_NoConflict_LLMKept(t *testing.T) {
	// For a non-TS file, AST has no opinion — LLM from_type must be preserved.
	_, fromType := MergeASTFactsJSON("src/main.go", "[]", "controller", nil)
	if fromType != "controller" {
		t.Errorf("non-TS file: LLM from_type should be kept; got %q, want \"controller\"", fromType)
	}
}

// TestMergeASTFacts_ASTFromType_SameValue_Unchanged verifies that when AST and LLM
// agree on from_type, the result is unchanged (no spurious flip).
func TestMergeASTFacts_ASTFromType_SameValue_Unchanged(t *testing.T) {
	root := repoRootFromCWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}
	// arena.module.ts — AST says "module", LLM says "module" — should stay "module".
	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/arena.module.ts"
	if _, err := os.Stat(filepath.Join(root, relPath)); err != nil {
		t.Skipf("fixture absent: %s", relPath)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	_, fromType := MergeASTFacts(relPath, nil, "module", nil)
	if fromType != "module" {
		t.Errorf("same from_type: expected \"module\", got %q", fromType)
	}
}

// TestMergeASTFacts_ASTFromType_EmptyLLM_ASTFills verifies that when LLM has no
// from_type ("") and AST detects one, the AST value fills the gap (original behavior).
func TestMergeASTFacts_ASTFromType_EmptyLLM_ASTFills(t *testing.T) {
	root := repoRootFromCWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}
	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/battle.gateway.ts"
	if _, err := os.Stat(filepath.Join(root, relPath)); err != nil {
		t.Skipf("fixture absent: %s", relPath)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// LLM has no opinion → AST from_type should fill it.
	_, fromType := MergeASTFacts(relPath, nil, "", nil)
	if fromType != "provider" {
		t.Errorf("empty LLM from_type: AST should fill with \"provider\"; got %q", fromType)
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

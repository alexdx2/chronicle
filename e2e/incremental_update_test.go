package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/graph"
	"github.com/alexdx2/chronicle-core/internal/registry"
	"github.com/alexdx2/chronicle-core/internal/store"
)

// setupTJIncremental sets up a full scan baseline and returns the graph ready for incremental testing.
func setupTJIncremental(t *testing.T) (*graph.Graph, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "tj.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	g := graph.New(s, reg)
	payload := loadTomAndJerryPayload(t)

	// Full scan at commit "aaa111"
	revID, err := s.CreateRevision("tomandjerry", "", "aaa111", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if _, err := g.ImportAll(payload, revID); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	return g, s
}

// Test: Invalidating a changed file marks its evidence stale and returns it in files_to_rescan.
func TestIncrementalInvalidateChangedFile(t *testing.T) {
	g, s := setupTJIncremental(t)

	// Baseline: TomController evidence is valid
	baseStats, _ := g.QueryStats("tomandjerry")
	if baseStats.NodeCount == 0 {
		t.Fatal("baseline has no nodes")
	}

	// Create incremental revision
	revID, err := s.CreateRevision("tomandjerry", "aaa111", "bbb222", "manual", "incremental", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	// Simulate: tom.controller.ts changed
	result, err := g.InvalidateChanged("tomandjerry", revID, []string{"src/tom/tom.controller.ts"})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}

	// Assertion 1: evidence from that file should be stale
	if result.StaleEvidence == 0 {
		t.Error("expected stale evidence > 0 after invalidating tom.controller.ts")
	}

	// Assertion 2: files_to_rescan should include the changed file
	found := false
	for _, f := range result.FilesToRescan {
		if f == "src/tom/tom.controller.ts" {
			found = true
		}
	}
	if !found {
		t.Errorf("files_to_rescan = %v, want to include src/tom/tom.controller.ts", result.FilesToRescan)
	}

	// Assertion 3: affected nodes should be > 0 (TomController node has evidence from that file)
	if result.AffectedNodes == 0 {
		t.Error("expected affected nodes > 0")
	}

	t.Logf("Invalidated: %d evidence, %d nodes, %d edges, rescan: %v",
		result.StaleEvidence, result.AffectedNodes, result.AffectedEdges, result.FilesToRescan)
}

// Test: Invalidating a file does NOT affect unrelated files' evidence.
func TestIncrementalOnlyAffectsChangedFiles(t *testing.T) {
	g, s := setupTJIncremental(t)

	revID, _ := s.CreateRevision("tomandjerry", "aaa111", "bbb222", "manual", "incremental", "{}")

	// Only invalidate arena controller
	result, err := g.InvalidateChanged("tomandjerry", revID, []string{"src/arena/arena.controller.ts"})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}

	// files_to_rescan should NOT include tom or jerry files
	for _, f := range result.FilesToRescan {
		if f == "src/tom/tom.controller.ts" || f == "src/jerry/jerry.controller.ts" {
			t.Errorf("files_to_rescan includes unrelated file: %s", f)
		}
	}

	// Jerry evidence should still be valid — query Jerry deps to confirm graph is intact
	deps, err := g.QueryDeps("code:controller:tomandjerry:jerrycontroller", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	foundJerryService := false
	for _, d := range deps {
		if d.NodeKey == "code:provider:tomandjerry:jerryservice" {
			foundJerryService = true
		}
	}
	if !foundJerryService {
		t.Error("JerryController → JerryService dep should be unaffected by arena invalidation")
	}
}

// Test: Reimporting after invalidation revalidates evidence.
func TestIncrementalReimportRevalidates(t *testing.T) {
	g, s := setupTJIncremental(t)

	revID, _ := s.CreateRevision("tomandjerry", "aaa111", "bbb222", "manual", "incremental", "{}")

	// Invalidate tom.controller.ts
	g.InvalidateChanged("tomandjerry", revID, []string{"src/tom/tom.controller.ts"})

	// Re-import the same nodes/edges for TomController (simulating re-extraction)
	rePayload := graph.ImportPayload{
		Nodes: []graph.ImportNode{
			{NodeKey: "code:controller:tomandjerry:tomcontroller", Layer: "code", NodeType: "controller", DomainKey: "tomandjerry", Name: "TomController", RepoName: "tom-api", FilePath: "src/tom/tom.controller.ts"},
		},
		Edges: []graph.ImportEdge{
			{FromNodeKey: "code:controller:tomandjerry:tomcontroller", ToNodeKey: "code:provider:tomandjerry:tomservice", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:controller:tomandjerry:tomcontroller", ToNodeKey: "contract:endpoint:tomandjerry:get:/tom/status", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
		},
		Evidence: []graph.ImportEvidence{
			{TargetKind: "node", NodeKey: "code:controller:tomandjerry:tomcontroller", SourceKind: "file", RepoName: "tom-api", FilePath: "src/tom/tom.controller.ts", LineStart: 4, LineEnd: 23, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.99},
			{TargetKind: "node", NodeKey: "contract:endpoint:tomandjerry:get:/tom/status", SourceKind: "file", RepoName: "tom-api", FilePath: "src/tom/tom.controller.ts", LineStart: 8, LineEnd: 11, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.95},
		},
	}

	_, err := g.ImportAll(rePayload, revID)
	if err != nil {
		t.Fatalf("ImportAll re-import: %v", err)
	}

	// Finalize
	finalResult, err := g.FinalizeIncrementalScan("tomandjerry", revID)
	if err != nil {
		t.Fatalf("FinalizeIncrementalScan: %v", err)
	}

	// Assertion: revalidated count should be > 0 (evidence was re-confirmed)
	if finalResult.Revalidated == 0 {
		t.Error("expected revalidated > 0 after re-importing same facts")
	}

	t.Logf("Finalize: revalidated=%d, still_stale=%d, contradicted=%d",
		finalResult.Revalidated, finalResult.StillStale, finalResult.Contradicted)
}

// Test: Adding a new node in incremental scan — new endpoint shows up in graph.
func TestIncrementalAddNewNode(t *testing.T) {
	g, s := setupTJIncremental(t)

	// Baseline: no GET /tom/health endpoint
	baseNodes, _ := g.Store().ListNodes(store.NodeFilter{Layer: "contract", Domain: "tomandjerry"})
	for _, n := range baseNodes {
		if n.NodeKey == "contract:endpoint:tomandjerry:get:/tom/health" {
			t.Fatal("GET /tom/health should not exist in baseline")
		}
	}

	// Incremental scan: developer added a new health endpoint to TomController
	revID, _ := s.CreateRevision("tomandjerry", "aaa111", "bbb222", "manual", "incremental", "{}")
	g.InvalidateChanged("tomandjerry", revID, []string{"src/tom/tom.controller.ts"})

	addPayload := graph.ImportPayload{
		Nodes: []graph.ImportNode{
			// Re-import existing controller
			{NodeKey: "code:controller:tomandjerry:tomcontroller", Layer: "code", NodeType: "controller", DomainKey: "tomandjerry", Name: "TomController", RepoName: "tom-api", FilePath: "src/tom/tom.controller.ts"},
			// New endpoint
			{NodeKey: "contract:endpoint:tomandjerry:get:/tom/health", Layer: "contract", NodeType: "endpoint", DomainKey: "tomandjerry", Name: "GET /tom/health", RepoName: "tom-api"},
		},
		Edges: []graph.ImportEdge{
			// Existing edge
			{FromNodeKey: "code:controller:tomandjerry:tomcontroller", ToNodeKey: "code:provider:tomandjerry:tomservice", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			// New edge: controller exposes the health endpoint
			{FromNodeKey: "code:controller:tomandjerry:tomcontroller", ToNodeKey: "contract:endpoint:tomandjerry:get:/tom/health", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
		},
		Evidence: []graph.ImportEvidence{
			{TargetKind: "node", NodeKey: "contract:endpoint:tomandjerry:get:/tom/health", SourceKind: "file", RepoName: "tom-api", FilePath: "src/tom/tom.controller.ts", LineStart: 25, LineEnd: 30, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.95},
		},
	}

	_, err := g.ImportAll(addPayload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Assertion: new endpoint now exists
	afterNodes, _ := g.Store().ListNodes(store.NodeFilter{Layer: "contract", Domain: "tomandjerry"})
	foundNew := false
	for _, n := range afterNodes {
		if n.NodeKey == "contract:endpoint:tomandjerry:get:/tom/health" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Error("GET /tom/health endpoint should exist after incremental import")
	}

	// Assertion: TomController should now have a dep on the new endpoint
	deps, _ := g.QueryDeps("code:controller:tomandjerry:tomcontroller", 1, nil)
	foundHealthDep := false
	for _, d := range deps {
		if d.NodeKey == "contract:endpoint:tomandjerry:get:/tom/health" {
			foundHealthDep = true
		}
	}
	if !foundHealthDep {
		t.Error("TomController should depend on new GET /tom/health endpoint via EXPOSES_ENDPOINT")
	}
}

// Test: Node count stays the same when no new nodes added (idempotent re-import).
func TestIncrementalIdempotentReimport(t *testing.T) {
	g, s := setupTJIncremental(t)

	baseBefore, _ := g.QueryStats("tomandjerry")

	revID, _ := s.CreateRevision("tomandjerry", "aaa111", "bbb222", "manual", "incremental", "{}")
	g.InvalidateChanged("tomandjerry", revID, []string{"src/tom/tom.service.ts"})

	// Re-import exact same TomService facts
	rePayload := graph.ImportPayload{
		Nodes: []graph.ImportNode{
			{NodeKey: "code:provider:tomandjerry:tomservice", Layer: "code", NodeType: "provider", DomainKey: "tomandjerry", Name: "TomService", RepoName: "tom-api", FilePath: "src/tom/tom.service.ts"},
		},
		Edges: []graph.ImportEdge{
			{FromNodeKey: "code:provider:tomandjerry:tomservice", ToNodeKey: "code:provider:tomandjerry:prismaservice-tom", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:provider:tomandjerry:tomservice", ToNodeKey: "data:model:tomandjerry:cat", EdgeType: "USES_MODEL", DerivationKind: "hard", FromLayer: "code", ToLayer: "data"},
			{FromNodeKey: "code:provider:tomandjerry:tomservice", ToNodeKey: "data:model:tomandjerry:catweapon", EdgeType: "USES_MODEL", DerivationKind: "hard", FromLayer: "code", ToLayer: "data"},
		},
		Evidence: []graph.ImportEvidence{
			{TargetKind: "node", NodeKey: "code:provider:tomandjerry:tomservice", SourceKind: "file", RepoName: "tom-api", FilePath: "src/tom/tom.service.ts", LineStart: 4, LineEnd: 27, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.99},
		},
	}

	g.ImportAll(rePayload, revID)
	g.FinalizeIncrementalScan("tomandjerry", revID)

	baseAfter, _ := g.QueryStats("tomandjerry")

	// Node count should be identical
	if baseBefore.NodeCount != baseAfter.NodeCount {
		t.Errorf("node count changed: before=%d, after=%d (should be idempotent)", baseBefore.NodeCount, baseAfter.NodeCount)
	}
}

// Test: Invalidating multiple files from different repos works correctly.
func TestIncrementalMultiFileMultiRepo(t *testing.T) {
	g, s := setupTJIncremental(t)

	revID, _ := s.CreateRevision("tomandjerry", "aaa111", "bbb222", "manual", "incremental", "{}")

	// Invalidate files from two different repos
	result, err := g.InvalidateChanged("tomandjerry", revID, []string{
		"src/tom/tom.service.ts",              // tom-api
		"src/arena/arena.service.ts",          // arena-api
		"prisma/schema.prisma",                // appears in multiple repos
	})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}

	// Should have stale evidence from both files
	if result.StaleEvidence < 2 {
		t.Errorf("stale evidence = %d, want >= 2 (from two different files)", result.StaleEvidence)
	}

	// files_to_rescan should include both service files
	rescanSet := map[string]bool{}
	for _, f := range result.FilesToRescan {
		rescanSet[f] = true
	}
	if !rescanSet["src/tom/tom.service.ts"] {
		t.Error("files_to_rescan missing src/tom/tom.service.ts")
	}
	if !rescanSet["src/arena/arena.service.ts"] {
		t.Error("files_to_rescan missing src/arena/arena.service.ts")
	}

	t.Logf("Multi-file invalidation: %d stale, %d files to rescan", result.StaleEvidence, len(result.FilesToRescan))
}

// Test: Empty changeset produces no invalidation.
func TestIncrementalEmptyChangeset(t *testing.T) {
	g, s := setupTJIncremental(t)

	revID, _ := s.CreateRevision("tomandjerry", "aaa111", "bbb222", "manual", "incremental", "{}")

	result, err := g.InvalidateChanged("tomandjerry", revID, []string{})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}

	if result.StaleEvidence != 0 {
		t.Errorf("stale evidence = %d, want 0 for empty changeset", result.StaleEvidence)
	}
	if len(result.FilesToRescan) != 0 {
		t.Errorf("files_to_rescan = %v, want empty for empty changeset", result.FilesToRescan)
	}
}

// Test: Invalidating a non-existent file has no effect.
func TestIncrementalUnknownFile(t *testing.T) {
	g, s := setupTJIncremental(t)

	revID, _ := s.CreateRevision("tomandjerry", "aaa111", "bbb222", "manual", "incremental", "{}")

	result, err := g.InvalidateChanged("tomandjerry", revID, []string{"src/brand-new-file.ts"})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}

	if result.StaleEvidence != 0 {
		t.Errorf("stale evidence = %d, want 0 for unknown file", result.StaleEvidence)
	}
}

// Test: Full end-to-end incremental update flow.
// Simulates: baseline scan → code change → invalidate → re-extract → finalize → verify.
func TestIncrementalEndToEnd(t *testing.T) {
	g, s := setupTJIncremental(t)

	// --- Baseline assertions ---
	baseStats, _ := g.QueryStats("tomandjerry")
	baseNodeCount := baseStats.NodeCount
	baseEdgeCount := baseStats.EdgeCount
	t.Logf("Baseline: %d nodes, %d edges", baseNodeCount, baseEdgeCount)

	// TomController → 3 endpoints (status, weapons, arm)
	tomDeps, _ := g.QueryDeps("code:controller:tomandjerry:tomcontroller", 1, nil)
	tomEndpoints := 0
	for _, d := range tomDeps {
		if d.Layer == "contract" {
			tomEndpoints++
		}
	}
	if tomEndpoints != 3 {
		t.Fatalf("baseline: TomController exposes %d endpoints, want 3", tomEndpoints)
	}

	// --- Incremental update: add GET /tom/health, remove POST /tom/arm ---
	revID, _ := s.CreateRevision("tomandjerry", "aaa111", "ccc333", "manual", "incremental", "{}")

	// Step 1: Invalidate changed file
	invResult, err := g.InvalidateChanged("tomandjerry", revID, []string{"src/tom/tom.controller.ts"})
	if err != nil {
		t.Fatalf("InvalidateChanged: %v", err)
	}
	if invResult.StaleEvidence == 0 {
		t.Fatal("expected stale evidence after invalidation")
	}

	// Step 2: Re-import with changes (new health endpoint, arm endpoint still exists but not re-imported — stays stale)
	updatePayload := graph.ImportPayload{
		Nodes: []graph.ImportNode{
			{NodeKey: "code:controller:tomandjerry:tomcontroller", Layer: "code", NodeType: "controller", DomainKey: "tomandjerry", Name: "TomController", RepoName: "tom-api", FilePath: "src/tom/tom.controller.ts"},
			{NodeKey: "contract:endpoint:tomandjerry:get:/tom/health", Layer: "contract", NodeType: "endpoint", DomainKey: "tomandjerry", Name: "GET /tom/health", RepoName: "tom-api"},
		},
		Edges: []graph.ImportEdge{
			{FromNodeKey: "code:controller:tomandjerry:tomcontroller", ToNodeKey: "code:provider:tomandjerry:tomservice", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:controller:tomandjerry:tomcontroller", ToNodeKey: "contract:endpoint:tomandjerry:get:/tom/status", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
			{FromNodeKey: "code:controller:tomandjerry:tomcontroller", ToNodeKey: "contract:endpoint:tomandjerry:get:/tom/weapons", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
			// POST /tom/arm NOT re-imported → its evidence stays stale
			// New endpoint added:
			{FromNodeKey: "code:controller:tomandjerry:tomcontroller", ToNodeKey: "contract:endpoint:tomandjerry:get:/tom/health", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
		},
		Evidence: []graph.ImportEvidence{
			{TargetKind: "node", NodeKey: "code:controller:tomandjerry:tomcontroller", SourceKind: "file", RepoName: "tom-api", FilePath: "src/tom/tom.controller.ts", LineStart: 4, LineEnd: 30, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.99},
			{TargetKind: "node", NodeKey: "contract:endpoint:tomandjerry:get:/tom/health", SourceKind: "file", RepoName: "tom-api", FilePath: "src/tom/tom.controller.ts", LineStart: 25, LineEnd: 30, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.95},
		},
	}

	_, err = g.ImportAll(updatePayload, revID)
	if err != nil {
		t.Fatalf("ImportAll incremental: %v", err)
	}

	// Step 3: Finalize
	finalResult, err := g.FinalizeIncrementalScan("tomandjerry", revID)
	if err != nil {
		t.Fatalf("FinalizeIncrementalScan: %v", err)
	}
	t.Logf("Finalize: revalidated=%d, still_stale=%d, contradicted=%d",
		finalResult.Revalidated, finalResult.StillStale, finalResult.Contradicted)

	// --- Post-update assertions ---

	// New endpoint should exist
	afterNodes, _ := g.Store().ListNodes(store.NodeFilter{Layer: "contract", Domain: "tomandjerry"})
	foundHealth := false
	for _, n := range afterNodes {
		if n.NodeKey == "contract:endpoint:tomandjerry:get:/tom/health" {
			foundHealth = true
		}
	}
	if !foundHealth {
		t.Error("GET /tom/health should exist after incremental update")
	}

	// Total node count should increase by 1 (added health endpoint)
	afterStats, _ := g.QueryStats("tomandjerry")
	expectedNodes := baseNodeCount + 1
	if afterStats.NodeCount != expectedNodes {
		t.Errorf("node count = %d, want %d (baseline %d + 1 new endpoint)", afterStats.NodeCount, expectedNodes, baseNodeCount)
	}

	// Jerry graph should be completely unaffected
	jerryDeps, _ := g.QueryDeps("code:controller:tomandjerry:jerrycontroller", 1, nil)
	jerryHasService := false
	for _, d := range jerryDeps {
		if d.NodeKey == "code:provider:tomandjerry:jerryservice" {
			jerryHasService = true
		}
	}
	if !jerryHasService {
		t.Error("JerryController → JerryService should be unaffected by tom-api incremental update")
	}

	// Kafka flow should be unaffected
	kafkaPath, _ := g.QueryPath(
		"code:provider:tomandjerry:battleresultproducer",
		"code:provider:tomandjerry:battleresultconsumer",
		graph.PathOptions{MaxDepth: 4, TopK: 3, Mode: "connected"},
	)
	if len(kafkaPath.Paths) == 0 {
		t.Error("Kafka flow (producer → consumer) should be unaffected by incremental update")
	}
}

// Test: Revision chain tracks before/after SHAs correctly.
func TestIncrementalRevisionChain(t *testing.T) {
	_, s := setupTJIncremental(t)

	// First revision was created in setup with after_sha="aaa111"
	rev1, err := s.GetLatestRevision("tomandjerry")
	if err != nil {
		t.Fatalf("GetLatestRevision: %v", err)
	}
	if rev1.GitAfterSHA != "aaa111" {
		t.Errorf("rev1 after_sha = %q, want aaa111", rev1.GitAfterSHA)
	}

	// Create incremental revision chaining from rev1
	_, err = s.CreateRevision("tomandjerry", rev1.GitAfterSHA, "bbb222", "manual", "incremental", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	rev2, _ := s.GetLatestRevision("tomandjerry")
	if rev2.GitBeforeSHA != "aaa111" {
		t.Errorf("rev2 before_sha = %q, want aaa111 (chained from rev1)", rev2.GitBeforeSHA)
	}
	if rev2.GitAfterSHA != "bbb222" {
		t.Errorf("rev2 after_sha = %q, want bbb222", rev2.GitAfterSHA)
	}
	if rev2.Mode != "incremental" {
		t.Errorf("rev2 mode = %q, want incremental", rev2.Mode)
	}
}

// helper to load the expected-graph.json payload (reuses existing helper pattern)
func loadPayloadFromJSON(t *testing.T, path string) graph.ImportPayload {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading payload: %v", err)
	}
	var payload graph.ImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parsing payload: %v", err)
	}
	return payload
}

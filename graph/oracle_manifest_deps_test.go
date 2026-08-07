package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Task 4 (SQ-Contracts 1+2): manifest dependency extraction is workspace-aware.
// A "dependency" fact from a monorepo package's package.json must originate at
// the OWNING package's service node (the declares_service node minted from
// the SAME file), not a generic per-file code node — and its section decides
// dependency_source: dependencies/optionalDependencies -> "manifest",
// peerDependencies -> "manifest_peer", devDependencies -> skipped unless the
// domain opts in via scan.include_dev_deps.
func TestOracleManifestDeps_WorkspaceOwnerAndSections(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"declares_service","to":"@okeep/foo"},
		{"kind":"dependency","to":"@okeep/ui","section":"dependencies"},
		{"kind":"dependency","to":"@okeep/tokens","section":"peerDependencies"},
		{"kind":"dependency","to":"vitest","section":"devDependencies"}
	]`
	g.SaveFileExtraction(revID, "testapp", "packages/foo/package.json", "extracted", "manifest", facts, "")
	g.ResolveExtractions("testapp", revID)

	// (a) owner: edge FROM @okeep/foo's service node, not from a repo/root node
	deps, _ := s.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	var fromKeys, sources []string
	for _, e := range deps {
		fromKeys = append(fromKeys, e.FromNodeKey)
		sources = append(sources, e.DependencySource)
	}
	// exactly 2 edges: ui (manifest) + tokens (manifest_peer); vitest skipped
	if len(deps) != 2 {
		t.Fatalf("want 2 DEPENDS_ON edges (dev skipped), got %d: %v / %v", len(deps), fromKeys, sources)
	}
	for _, fk := range fromKeys {
		if !strings.Contains(fk, "@okeep/foo") {
			t.Errorf("dependency edge must originate at the OWNING package node, got from=%q", fk)
		}
	}

	// (b) section policy: dependencies -> manifest, peerDependencies -> manifest_peer
	bySource := map[string]string{} // to package name suffix -> dependency_source
	for _, e := range deps {
		bySource[e.ToNodeKey] = e.DependencySource
	}
	var gotUI, gotTokens bool
	for toKey, src := range bySource {
		switch {
		case strings.HasSuffix(toKey, ":@okeep/ui"):
			gotUI = true
			if src != "manifest" {
				t.Errorf("@okeep/ui dependency_source = %q, want manifest", src)
			}
		case strings.HasSuffix(toKey, ":@okeep/tokens"):
			gotTokens = true
			if src != "manifest_peer" {
				t.Errorf("@okeep/tokens dependency_source = %q, want manifest_peer", src)
			}
		}
	}
	if !gotUI || !gotTokens {
		t.Fatalf("missing expected package edges, got to-keys: %v", bySource)
	}
}

// Second oracle (brief step 1): devDependencies is skipped by DEFAULT, and
// only processed when the domain's scan config sets include_dev_deps: true.
// The manifest flag reaches ResolveExtractions through ResolveOptions —
// mcpserver loads chronicle.domain.yaml the same way discovery does and
// passes the derived bool through (see mcpserver/server.go resolveExtractionsHandler).
//
// Uses "@okeep/build-tools" rather than the brief's literal "vitest": vitest
// is in dependency_filter.go's infrastructure denylist (it's a real dev-tool
// name, matched there for source imports) and ShouldTrackDependency still
// gates manifest facts too (per the task's interface contract), so a
// dev-flag-enabled "vitest" fact would be filtered by THAT gate regardless of
// this test's flag — it would not isolate the include_dev_deps behavior this
// oracle exists to prove. A workspace-scoped name that isn't on the
// infrastructure/architectural denylist isolates the flag correctly.
func TestOracleManifestDeps_IncludeDevDeps(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"declares_service","to":"@okeep/foo"},
		{"kind":"dependency","to":"@okeep/build-tools","section":"devDependencies"}
	]`
	g.SaveFileExtraction(revID, "testapp", "packages/foo/package.json", "extracted", "manifest", facts, "")
	if _, err := g.ResolveExtractionsWithOptions("testapp", revID, ResolveOptions{IncludeDevDeps: true}); err != nil {
		t.Fatal(err)
	}

	deps, _ := s.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	var found *store.EdgeRow
	for i := range deps {
		if strings.HasSuffix(deps[i].ToNodeKey, ":@okeep/build-tools") {
			found = &deps[i]
		}
	}
	if found == nil {
		t.Fatalf("want a DEPENDS_ON edge to @okeep/build-tools when include_dev_deps is set, got edges: %+v", deps)
	}
	if found.DependencySource != "manifest" {
		t.Errorf("DependencySource = %q, want manifest (devDependencies is not peer)", found.DependencySource)
	}
	if !strings.Contains(found.FromNodeKey, "@okeep/foo") {
		t.Errorf("dev dependency edge must still originate at the owning package node, got from=%q", found.FromNodeKey)
	}
}

// Companion negative case: the SAME devDependencies fact, without the flag,
// produces no edge at all — devDependencies is skipped, not silently
// reclassified into some other source.
func TestOracleManifestDeps_DevDepsSkippedByDefault(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"declares_service","to":"@okeep/foo"},
		{"kind":"dependency","to":"@okeep/build-tools","section":"devDependencies"}
	]`
	g.SaveFileExtraction(revID, "testapp", "packages/foo/package.json", "extracted", "manifest", facts, "")
	g.ResolveExtractions("testapp", revID) // ResolveOptions{} zero value -> IncludeDevDeps: false

	deps, _ := s.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	for _, e := range deps {
		if strings.HasSuffix(e.ToNodeKey, ":@okeep/build-tools") {
			t.Fatalf("devDependencies must be skipped without include_dev_deps, got edge: %+v", e)
		}
	}
}

// Third oracle (brief step 1): evidence for a manifest dependency records the
// SINGLE actual section it was declared under, not the historical lumped
// ["dependencies","devDependencies","peerDependencies"] triple — a peer
// dependency's evidence must not claim it was ALSO seen as a regular or dev
// dependency.
func TestOracleManifestDeps_SingleSectionEvidence(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"declares_service","to":"@okeep/foo"},
		{"kind":"dependency","to":"@okeep/tokens","section":"peerDependencies"}
	]`
	g.SaveFileExtraction(revID, "testapp", "packages/foo/package.json", "extracted", "manifest", facts, "")
	g.ResolveExtractions("testapp", revID)

	evRows, err := s.ListEvidenceByAssertionKind("manifest_dependency")
	if err != nil {
		t.Fatal(err)
	}
	var found *store.EvidenceRow
	for i := range evRows {
		if evRows[i].FilePath == "packages/foo/package.json" {
			found = &evRows[i]
		}
	}
	if found == nil {
		t.Fatalf("no manifest_dependency evidence found for packages/foo/package.json, got: %+v", evRows)
	}
	var assertion struct {
		Package  string   `json:"package"`
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(found.Assertion), &assertion); err != nil {
		t.Fatalf("assertion JSON: %v (%s)", err, found.Assertion)
	}
	if len(assertion.Sections) != 1 || assertion.Sections[0] != "peerDependencies" {
		t.Fatalf("sections = %v, want exactly [\"peerDependencies\"] (single actual section, not the lumped default)", assertion.Sections)
	}
}

// Ledger pointer (Task 3 review): UpsertEdge's update path has no
// dependency_source precedence, so a later re-upsert of the same edge_key
// silently overwrites an earlier one's source. This is only safe here because
// a manifest dependency edge's from-node (the OWNING service node) differs
// from a code import edge's from-node (a per-file code node) for the exact
// same package — different from-node means a different edge_key, so the two
// facts land on two DISTINCT edges instead of colliding on one.
func TestOracleManifestDeps_ManifestAndCodeImportProduceTwoEdges(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"declares_service","to":"@okeep/foo"},
		{"kind":"dependency","to":"@okeep/ui","section":"dependencies"}
	]`
	g.SaveFileExtraction(revID, "testapp", "packages/foo/package.json", "extracted", "manifest", facts, "")
	g.SaveFileExtraction(revID, "testapp", "packages/foo/src/index.ts", "extracted", "provider",
		`[{"kind":"import","to":"@okeep/ui","symbols":["Button"]}]`, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}

	all, _ := s.ListEdges(store.EdgeFilter{})
	var toUI []store.EdgeRow
	for _, e := range all {
		if strings.HasSuffix(e.ToNodeKey, ":@okeep/ui") {
			toUI = append(toUI, e)
		}
	}
	if len(toUI) != 2 {
		t.Fatalf("want 2 edges into the @okeep/ui package node (manifest dep + code import), got %d: %+v", len(toUI), toUI)
	}

	sources := map[string]bool{}
	fromKeys := map[string]bool{}
	for _, e := range toUI {
		sources[e.DependencySource] = true
		fromKeys[e.FromNodeKey] = true
	}
	if !sources["manifest"] || !sources["code"] {
		t.Fatalf("want one edge with dependency_source=manifest and one with =code, got sources: %v", sources)
	}
	if len(fromKeys) != 2 {
		t.Fatalf("want 2 distinct from-nodes (service node vs code node), got: %v", fromKeys)
	}
}

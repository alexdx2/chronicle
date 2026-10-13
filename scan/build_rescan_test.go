package scan

import (
	"context"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"path/filepath"
)

// openSnapshotGraphT loads a built snapshot into a temporary store, the way
// BuildSnapshot restores a base.
func openSnapshotGraphT(t *testing.T, f snapshot.Files) *graph.Graph {
	t.Helper()
	snap := decodeT(t, f)
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	g, closer, err := snapshot.Load(snap, reg, snapshot.LoadOptions{DBPath: filepath.Join(t.TempDir(), "chronicle.db"), SkipJournalSync: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer() })
	return g
}

// A rescan is an incremental build whose base is the snapshot of the same sha:
// the diff is empty, so only files whose coverage version differs are sent.
func TestBuildSnapshot_RescanSameShaSendsOnlyStaleVersions(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1", "src/b.cs": "class B {}"})
	fake := llm.NewFake("testdata/fake")
	first, err := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), fake))
	if err != nil {
		t.Fatal(err)
	}
	// Second build at the same sha, same packs: nothing to send.
	in := buildIn(root, head, t.TempDir(), fake)
	in.Base = &Base{CodeSha: head, Files: first.Files}
	var plans []Plan
	in.OnPlan = func(p Plan) { plans = append(plans, p) }
	second, err := BuildSnapshot(context.Background(), in)
	if err != nil || !second.Incremental || len(plans) == 0 || plans[0].ToSend != 0 {
		t.Fatalf("same packs: %+v %v", plans, err)
	}

	t.Skip("Task 10: BuildInput.Profile")
	// A fleet pack for .cs files changes the version of b.cs only.
	cat := prompts.BuiltinCatalog()
	cat.Add(prompts.CatalogPack{ID: "dotnet", Content: "# DOTNET v2\n\n## Match\n- *.cs\n\n## Entry points (→ endpoint facts)\ny\n", Source: prompts.SourceFleet})
	_ = cat
	// in = buildIn(root, head, t.TempDir(), fake)
	// in.Base = &Base{CodeSha: head, Files: second.Files}
	// in.Profile.Catalog = cat
	// in.Profile.Off = true
	// plans = nil
	// in.OnPlan = func(p Plan) { plans = append(plans, p) }
	// if _, err := BuildSnapshot(ctx, in); err != nil || plans[0].ToSend != 1 { ... }
}

func TestBuildSnapshot_ScopeDropMarksEvidenceStale(t *testing.T) {
	t.Skip("Task 10: BuildInput.Profile")
	root, head := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1", "legacy/old.ts": "export const o = 1"})
	fake := llm.NewFake("testdata/fake")
	first, _ := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), fake))
	in := buildIn(root, head, t.TempDir(), fake)
	in.Base = &Base{CodeSha: head, Files: first.Files}
	// Task 10: in.Profile.Backend = &cannedBackend{answers: scope without legacy/}; in.Profile.ForceReprofile = true
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	g := openSnapshotGraphT(t, res.Files)
	stale, _ := g.Store().ListStaleEvidenceByFile("legacy/old.ts")
	if len(stale) == 0 {
		t.Fatal("evidence of the dropped file is not stale")
	}
}

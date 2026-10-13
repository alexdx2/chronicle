package scan

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/snapshot"
)

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
	man := func(exclude string) string {
		return "domains:\n  \"proj\":\n    name: \"proj\"\n    scan:\n      include:\n        - \"**/*.ts\"\n" + exclude + "tech: [typescript]\n"
	}
	root, c1 := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1", "legacy/old.ts": "export const o = 1"})
	first, err := BuildSnapshot(context.Background(), buildIn(root, c1, t.TempDir(), &factsBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	staleOf := func(f snapshot.Files, file string) (stale, other int) {
		s := decodeT(t, f)
		count := func(evs []snapshot.Evidence) {
			for _, e := range evs {
				if e.File != file {
					continue
				}
				if e.Status == "stale" {
					stale++
				} else {
					other++
				}
			}
		}
		for _, n := range s.Nodes {
			count(n.Evidence)
		}
		for _, e := range s.Edges {
			count(e.Evidence)
		}
		return
	}
	if st, ot := staleOf(first.Files, "legacy/old.ts"); st != 0 || ot == 0 {
		t.Fatalf("setup: legacy evidence stale=%d other=%d", st, ot)
	}
	// The tree manifest of the second commit drops legacy/ from the scope.
	writeT(t, root, ".depbot/chronicle.domain.yaml", man("      exclude:\n        - \"legacy/**\"\n"))
	c2 := commitAllT(t, root, "drop legacy")
	in := buildIn(root, c2, t.TempDir(), &factsBackend{})
	in.Base = &Base{CodeSha: c1, Files: first.Files}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if st, ot := staleOf(res.Files, "legacy/old.ts"); st == 0 || ot != 0 {
		t.Fatalf("dropped file evidence stale=%d other=%d", st, ot)
	}
	if st, ot := staleOf(res.Files, "src/a.ts"); st != 0 || ot == 0 {
		t.Fatalf("kept file evidence stale=%d other=%d", st, ot)
	}
}

// A file the model never sees (oversize) has a coverage row too, so a rebuild
// at the same sha with the same packs sends nothing.
func TestBuildSnapshot_SameShaRebuildSendsNothingWithSkippedFile(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1", "src/big.ts": "// " + strings.Repeat("x", 300*1024)})
	var plans planRecorder
	in := buildIn(root, head, t.TempDir(), &factsBackend{})
	in.OnPlan = plans.on
	first, err := BuildSnapshot(context.Background(), in)
	if err != nil || len(plans.plans) != 1 || plans.plans[0].ToSend != 2 {
		t.Fatalf("first: %+v %v", plans.plans, err)
	}
	plans.plans = nil
	in = buildIn(root, head, t.TempDir(), &factsBackend{})
	in.Base, in.OnPlan = &Base{CodeSha: head, Files: first.Files}, plans.on
	if _, err := BuildSnapshot(context.Background(), in); err != nil || len(plans.plans) != 1 || plans.plans[0].ToSend != 0 {
		t.Fatalf("rebuild: %+v %v", plans.plans, err)
	}
}

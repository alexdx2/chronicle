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
	// The manifest names both technologies (the seed would pick just one).
	man := "domains:\n  \"proj\":\n    name: \"proj\"\n    scan:\n      include:\n        - \"**/*.ts\"\n        - \"**/*.cs\"\ntech: [typescript, dotnet]\n"
	root, head := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1", "src/b.cs": "class B {}", ".depbot/chronicle.domain.yaml": man})
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

	// A fleet pack for .cs files changes the version of b.cs only.
	cat := prompts.BuiltinCatalog()
	cat.Add(prompts.CatalogPack{ID: "dotnet", Content: "# DOTNET v2\n\n## Match\n- *.cs\n\n## Entry points (→ endpoint facts)\ny\n", Source: prompts.SourceFleet})
	in = buildIn(root, head, t.TempDir(), fake)
	in.Base = &Base{CodeSha: head, Files: second.Files}
	in.Profile.Catalog = cat
	in.Profile.Off = true
	plans = nil
	in.OnPlan = func(p Plan) { plans = append(plans, p) }
	if _, err := BuildSnapshot(context.Background(), in); err != nil || len(plans) == 0 || plans[0].ToSend != 1 {
		t.Fatalf("new dotnet pack: %+v %v", plans, err)
	}
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

// A file dropped from the scope and admitted again must be extracted again:
// its evidence was stale-marked, and an intact coverage row would keep it so.
func TestBuildSnapshot_DropThenReadmitSendsTheFileAgain(t *testing.T) {
	man := func(exclude string) string {
		return "domains:\n  \"proj\":\n    name: \"proj\"\n    scan:\n      include:\n        - \"**/*.ts\"\n" + exclude + "tech: [typescript]\n"
	}
	root, c1 := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1", "legacy/old.ts": "export const o = 1"})
	first, err := BuildSnapshot(context.Background(), buildIn(root, c1, t.TempDir(), &factsBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, root, ".depbot/chronicle.domain.yaml", man("      exclude:\n        - \"legacy/**\"\n"))
	c2 := commitAllT(t, root, "drop legacy")
	in := buildIn(root, c2, t.TempDir(), &factsBackend{})
	in.Base = &Base{CodeSha: c1, Files: first.Files}
	second, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, root, ".depbot/chronicle.domain.yaml", man(""))
	c3 := commitAllT(t, root, "readmit legacy")
	var plans planRecorder
	in = buildIn(root, c3, t.TempDir(), &factsBackend{})
	in.Base, in.OnPlan = &Base{CodeSha: c2, Files: second.Files}, plans.on
	if _, err := BuildSnapshot(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(plans.plans) != 1 || plans.plans[0].ToSend != 1 {
		t.Fatalf("re-admitted file must be sent again: %+v", plans.plans)
	}
}

// A rescan at the same sha inherits the profile; a pack that appeared since for
// one of its technologies (named "new" or left without a pack) is adopted: the
// profile names it, profile.json applies it, and its files are sent again.
func TestBuildSnapshot_RescanAdoptsAPackThatAppeared(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{"Api/Api.csproj": "<Project Sdk=\"Microsoft.NET.Sdk.Web\"><ItemGroup><PackageReference Include=\"Confluent.Kafka\" Version=\"2.3.0\" /></ItemGroup></Project>", "Api/A.cs": "class A {}", "Api/B.cs": "class B {}"})
	answer := `{"technologies":[{"id":"dotnet","language":"dotnet","role":"http","evidence":["Api/Api.csproj:1"],"pack":"dotnet"},{"id":"kafka","language":"dotnet","role":"messaging","evidence":["Api/Api.csproj:1 PackageReference Confluent.Kafka 2.3.0"],"pack":"new"}],"scope":{"include":[{"glob":"**/*.cs","reason":"code"},{"glob":"**/*.csproj","reason":"manifest"}],"exclude":[]}}`
	fake := llm.NewFake("testdata/fake")
	in := buildIn(root, head, t.TempDir(), fake)
	in.Profile.Backend = &cannedBackend{answers: []string{answer}}
	first, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := ParseProfileFile(first.Files.Profile)
	for _, p := range pf.Packs {
		if p.ID == "kafka-dotnet" {
			t.Fatal("no kafka pack exists yet")
		}
	}
	// the pack appears in the fleet registry; the rescan inherits the profile (no model)
	cat := prompts.BuiltinCatalog()
	cat.Add(prompts.CatalogPack{ID: "kafka-dotnet", Content: "# KAFKA DOTNET\n\n## Match\nmatch: ext=.cs\n\n## Producers (→ produces facts)\ny\n", Source: prompts.SourceFleet})
	in = buildIn(root, head, t.TempDir(), fake)
	in.Base = &Base{CodeSha: head, Files: first.Files}
	in.Profile.Catalog = cat
	in.Profile.Off = true
	var plans []Plan
	in.OnPlan = func(p Plan) { plans = append(plans, p) }
	second, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ = ParseProfileFile(second.Files.Profile)
	var kafka Technology
	for _, tc := range pf.Profile.Technologies {
		if tc.ID == "kafka" {
			kafka = tc
		}
	}
	applied := false
	for _, p := range pf.Packs {
		applied = applied || p.ID == "kafka-dotnet"
	}
	if kafka.Pack != "kafka-dotnet" || !applied || len(plans) == 0 || plans[0].ToSend != 2 {
		t.Fatalf("kafka=%+v packs=%+v plans=%+v", kafka, pf.Packs, plans)
	}
	// a third build with the same catalog: nothing new, nothing sent
	in = buildIn(root, head, t.TempDir(), fake)
	in.Base = &Base{CodeSha: head, Files: second.Files}
	in.Profile.Catalog = cat
	in.Profile.Off = true
	plans = nil
	in.OnPlan = func(p Plan) { plans = append(plans, p) }
	if _, err := BuildSnapshot(context.Background(), in); err != nil || len(plans) == 0 || plans[0].ToSend != 0 {
		t.Fatalf("third build: %+v %v", plans, err)
	}
}

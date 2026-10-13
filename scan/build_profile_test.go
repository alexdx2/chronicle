package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/manifest"
)

func hasLimitation(ls []string, sub string) bool {
	for _, l := range ls {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func commitFileT(t *testing.T, root, path, content string) string {
	t.Helper()
	writeT(t, root, path, content)
	return commitAllT(t, root, "change "+path)
}

func TestBuildSnapshot_ProfileDrivesScopePacksAndFile(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{
		"Api/Api.csproj":       "<Project><ItemGroup><PackageReference Include=\"Confluent.Kafka\" Version=\"2.3.0\" /></ItemGroup></Project>",
		"Api/Consumer.cs":      "class C {}",
		"Api/Migrations/M1.cs": "class M {}",
	})
	answer := `{"technologies":[{"id":"dotnet","language":"dotnet","role":"http","evidence":["Api/Api.csproj:1"],"pack":"dotnet"},{"id":"kafka","language":"dotnet","role":"messaging","evidence":["Api/Api.csproj:1 PackageReference Confluent.Kafka 2.3.0"],"pack":"new"}],"scope":{"include":[{"glob":"**/*.cs","reason":"code"},{"glob":"**/*.csproj","reason":"manifest"}],"exclude":[{"glob":"**/Migrations/**","reason":"generated"}]}}`
	b := &cannedBackend{answers: []string{answer}}
	var hooked *Profile
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile = ProfileOptions{Backend: b, OnProfile: func(_ context.Context, p *Profile) (*prompts.Catalog, error) {
		hooked = p
		c := prompts.BuiltinCatalog()
		c.Add(prompts.CatalogPack{ID: "kafka-dotnet", Content: "# KD\n\n## Match\n- *.cs\n\n## Entry points (→ endpoint facts)\nz\n", Source: prompts.SourceFleet})
		p.Technologies[1].Pack = "kafka-dotnet"
		return c, nil
	}}
	var plan Plan
	in.OnPlan = func(p Plan) { plan = p }
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if hooked == nil || b.calls != 1 {
		t.Fatalf("profile requested %d times, hook=%v", b.calls, hooked != nil)
	}
	if plan.InScope != 2 { // Api.csproj + Consumer.cs; Migrations excluded
		t.Fatalf("plan = %+v", plan)
	}
	pf, err := ParseProfileFile(res.Files.Profile)
	if err != nil || pf.ScopeSource != "model" || pf.Seeded || len(pf.Deps.Depends) != 1 || pf.ProfiledAt != head {
		t.Fatalf("profile file = %+v %v", pf, err)
	}
	if len(pf.Packs) == 0 || pf.Packs[len(pf.Packs)-1].ID != "kafka-dotnet" {
		t.Fatalf("applied packs = %+v", pf.Packs)
	}
	if len(pf.TopDirs) == 0 || len(pf.Checks) == 0 {
		t.Fatalf("top dirs / checks missing: %+v", pf)
	}
	if res.Profile == nil || res.Profile.ProfiledAt != head {
		t.Fatalf("BuildResult.Profile = %+v", res.Profile)
	}
	if res.SnapshotID == "" || len(res.Files.Profile) == 0 {
		t.Fatal("profile.json missing from the snapshot")
	}
}

func TestBuildSnapshot_NoBackendMeansSeedAndLimitation(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1"})
	res, err := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake")))
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := ParseProfileFile(res.Files.Profile)
	if !pf.Seeded || pf.ScopeSource != "seed" || pf.SeedReason != "no profile model configured" {
		t.Fatalf("%+v", pf)
	}
	if !hasLimitation(res.Limitations, "profile taken from the seed: no profile model configured") {
		t.Fatalf("limitations = %v", res.Limitations)
	}
}

func TestBuildSnapshot_ModelErrorMeansSeedWithReason(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1"})
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = &cannedBackend{}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := ParseProfileFile(res.Files.Profile)
	if !pf.Seeded || !strings.Contains(pf.SeedReason, "no more answers") || !hasLimitation(res.Limitations, "profile taken from the seed: ") {
		t.Fatalf("%+v %v", pf, res.Limitations)
	}
}

func TestBuildSnapshot_IncrementalInheritsProfileUnlessManifestsChanged(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{"package.json": `{"name":"a","dependencies":{"kafkajs":"1"}}`, "src/a.ts": "export const a = 1"})
	ans := `{"technologies":[{"id":"typescript","language":"typescript","role":"other","evidence":["src/a.ts"],"pack":"typescript"}],"scope":{"include":[{"glob":"src/**","reason":"app"},{"glob":"package.json","reason":"manifest"}],"exclude":[]}}`
	b := &cannedBackend{answers: []string{ans, ans}}
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = b
	first, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	head2 := commitFileT(t, root, "src/b.ts", "export const b = 2")
	in = buildIn(root, head2, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = b
	in.Base = &Base{CodeSha: head, Files: first.Files}
	second, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := ParseProfileFile(second.Files.Profile)
	if b.calls != 1 || pf.ProfiledAt != head || pf.ScopeSource != "model" {
		t.Fatalf("source change must not re-profile: calls=%d %+v", b.calls, pf)
	}
	head3 := commitFileT(t, root, "package.json", `{"name":"a","dependencies":{"kafkajs":"1","express":"4"}}`)
	in = buildIn(root, head3, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = b
	in.Base = &Base{CodeSha: head2, Files: second.Files}
	third, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ = ParseProfileFile(third.Files.Profile)
	if b.calls != 2 || pf.ProfiledAt != head3 || len(pf.Deps.Depends) != 2 {
		t.Fatalf("manifest change must re-profile: calls=%d %+v", b.calls, pf)
	}
}

type recordBackend struct {
	llm.Backend
	mu   sync.Mutex
	seen []llm.Request
}

func (r *recordBackend) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	r.mu.Lock()
	r.seen = append(r.seen, req)
	r.mu.Unlock()
	return r.Backend.Complete(ctx, req)
}

func TestBuildSnapshot_TestGlobFilesGetTheTestNote(t *testing.T) {
	man := "domains:\n  \"proj\":\n    name: \"proj\"\n    scan:\n      include:\n        - \"src/**\"\n      tests:\n        - \"src/**/*.spec.ts\"\ntech: [typescript]\n"
	root, head := newCodeRepoT(t, map[string]string{".depbot/chronicle.domain.yaml": man, "src/a.ts": "export const a = 1", "src/a.spec.ts": "it('x', () => {})"})
	rb := &recordBackend{Backend: llm.NewFake("testdata/fake")}
	if _, err := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), rb)); err != nil {
		t.Fatal(err)
	}
	var withNote, without int
	for _, r := range rb.seen {
		if strings.Contains(r.System, "This file is a test: report only contracts and calls") {
			withNote++
		} else {
			without++
		}
	}
	if withNote != 1 || without < 1 {
		t.Fatalf("test note on %d requests, absent on %d", withNote, without)
	}
}

const tsProfileAnswer = `{"technologies":[{"id":"typescript","language":"typescript","role":"other","evidence":["src/a.ts"],"pack":"typescript"}],"scope":{"include":[{"glob":"src/**","reason":"app"},{"glob":"package.json","reason":"manifest"}],"exclude":[]}}`

func tsRepoT(t *testing.T) (string, string) {
	t.Helper()
	return newCodeRepoT(t, map[string]string{"package.json": `{"name":"a","dependencies":{"kafkajs":"1"}}`, "src/a.ts": "export const a = 1"})
}

func TestBuildSnapshot_ResumeReusesTheSavedProfile(t *testing.T) {
	repo, c1 := newCodeRepoT(t, sixFiles())
	stateDir := filepath.Join(t.TempDir(), "state")
	pb := &cannedBackend{answers: []string{tsProfileAnswer, tsProfileAnswer}}
	hooks := 0
	opts := ProfileOptions{Backend: pb, OnProfile: func(context.Context, *Profile) (*prompts.Catalog, error) { hooks++; return nil, nil }}
	cancelledFirstRun(t, repo, c1, stateDir, 3, func(in *BuildInput) { in.Profile = opts })
	if pb.calls != 1 || hooks != 1 {
		t.Fatalf("first run: profile calls=%d hooks=%d", pb.calls, hooks)
	}
	in := buildIn(repo, c1, t.TempDir(), &factsBackend{})
	in.StateDir = stateDir
	in.Profile = opts
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if pb.calls != 1 || hooks != 1 {
		t.Fatalf("resumed run asked again: profile calls=%d hooks=%d", pb.calls, hooks)
	}
	pf, _ := ParseProfileFile(res.Files.Profile)
	if pf.ScopeSource != "model" || pf.Seeded || pf.ProfiledAt != c1 {
		t.Fatalf("resumed profile = %+v", pf)
	}
}

func TestBuildSnapshot_ModelFailureOnIncrementalInheritsBaseProfileAndManifest(t *testing.T) {
	root, head := tsRepoT(t)
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = &cannedBackend{answers: []string{tsProfileAnswer}}
	first, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	head2 := commitFileT(t, root, "package.json", `{"name":"a","dependencies":{"kafkajs":"1","express":"4"}}`)
	in = buildIn(root, head2, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = &cannedBackend{} // every request fails
	in.Base = &Base{CodeSha: head, Files: first.Files}
	second, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.Files.Domain, first.Files.Domain) {
		t.Fatalf("base manifest was replaced:\n%s\nvs\n%s", second.Files.Domain, first.Files.Domain)
	}
	a, _ := ParseProfileFile(first.Files.Profile)
	b, _ := ParseProfileFile(second.Files.Profile)
	ja, _ := json.Marshal(a.Profile)
	jb, _ := json.Marshal(b.Profile)
	if string(ja) != string(jb) || b.Seeded || b.ScopeSource != "model" || b.ProfiledAt != head {
		t.Fatalf("profile not inherited: %+v", b)
	}
	if !hasLimitation(second.Limitations, "profile taken from the previous snapshot: ") {
		t.Fatalf("limitations = %v", second.Limitations)
	}
}

func TestBuildSnapshot_InheritedProfileKeepsBaseManifestBytes(t *testing.T) {
	root, head := tsRepoT(t)
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = &cannedBackend{answers: []string{tsProfileAnswer}}
	first, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	head2 := commitFileT(t, root, "src/b.ts", "export const b = 2")
	in = buildIn(root, head2, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Base = &Base{CodeSha: head, Files: first.Files}
	second, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.Files.Domain, first.Files.Domain) || second.Profile.ProfiledAt != head {
		t.Fatalf("manifest changed or profile re-taken: %+v", second.Profile)
	}
}

func TestBuildSnapshot_TreeManifestWinsOverModelScope(t *testing.T) {
	man := "domains:\n  \"proj\":\n    name: \"proj\"\n    scan:\n      include:\n        - \"src/**\"\ntech: [typescript]\n"
	root, head := newCodeRepoT(t, map[string]string{".depbot/chronicle.domain.yaml": man, "package.json": `{"name":"a"}`, "src/a.ts": "export const a = 1", "lib/x.ts": "export const x = 1"})
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = &cannedBackend{answers: []string{`{"technologies":[{"id":"typescript","language":"typescript","role":"other","evidence":["src/a.ts"],"pack":"typescript"}],"scope":{"include":[{"glob":"lib/**","reason":"model"}],"exclude":[]}}`}}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := ParseProfileFile(res.Files.Profile)
	if pf.ScopeSource != "repo" || pf.ScopeProposed != nil || string(res.Files.Domain) != man {
		t.Fatalf("scope source %q, manifest:\n%s", pf.ScopeSource, res.Files.Domain)
	}
}

func TestBuildSnapshot_OnProfileErrorIsALimitation(t *testing.T) {
	root, head := tsRepoT(t)
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile = ProfileOptions{Backend: &cannedBackend{answers: []string{tsProfileAnswer}}, OnProfile: func(context.Context, *Profile) (*prompts.Catalog, error) {
		return nil, errors.New("registry down")
	}}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLimitation(res.Limitations, "pack authoring: registry down") {
		t.Fatalf("limitations = %v", res.Limitations)
	}
}

func TestBuildSnapshot_TwoInvalidAnswersFallBackToSeed(t *testing.T) {
	root, head := tsRepoT(t)
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = &cannedBackend{answers: []string{"garbage", "garbage"}}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := ParseProfileFile(res.Files.Profile)
	if !pf.Seeded || pf.ScopeSource != "seed" || !hasLimitation(res.Limitations, "profile taken from the seed: profile: ") {
		t.Fatalf("%+v %v", pf, res.Limitations)
	}
}

func TestBuildSnapshot_ProfileFileCarriesChecksAndPacks(t *testing.T) {
	root, head := tsRepoT(t)
	res, err := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake")))
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := ParseProfileFile(res.Files.Profile)
	if len(pf.Checks) != 5 || len(pf.Packs) == 0 {
		t.Fatalf("checks=%d packs=%+v", len(pf.Checks), pf.Packs)
	}
}

// The first incremental build over a base without profile.json has no model
// answer to use; the base's manifest scopes the scan and profile.json says so.
func TestBuildSnapshot_IncrementalOverV1BaseReportsBaseScope(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1", "legacy/old.js": "x"})
	first, err := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake")))
	if err != nil {
		t.Fatal(err)
	}
	base := first.Files
	base.Profile = nil // a version 1 snapshot has none
	head2 := commitFileT(t, root, "src/b.ts", "export const b = 2")
	in := buildIn(root, head2, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Base = &Base{CodeSha: head, Files: base}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	pf, err := ParseProfileFile(res.Files.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if pf.ScopeSource != "base" {
		t.Fatalf("scope source = %q, want base", pf.ScopeSource)
	}
	m, err := manifest.Load(res.Files.Domain)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := m.ScanConfigFor(m.Domains[0].Name)
	want := scopeOf(cfg)
	if len(pf.Profile.Scope.Include) == 0 || !reflect.DeepEqual(pf.Profile.Scope.Include, want.Include) || !reflect.DeepEqual(pf.Profile.Scope.Exclude, want.Exclude) {
		t.Fatalf("scope %+v does not match the manifest in effect %+v", pf.Profile.Scope, want)
	}
}

// windowedCanned is a canned backend with a hard window, like a local runtime.
type windowedCanned struct {
	cannedBackend
	window int
}

func (w *windowedCanned) ContextWindow() int { return w.window }

func TestBuildSnapshot_ProfilePromptTruncationIsALimitation(t *testing.T) {
	var body strings.Builder
	body.WriteString("<Project><ItemGroup>\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&body, "<PackageReference Include=\"Vendor.Package%02d\" Version=\"1.0.0\" />\n", i)
	}
	body.WriteString("</ItemGroup></Project>")
	root, head := newCodeRepoT(t, map[string]string{"Api/Api.csproj": body.String(), "Api/C.cs": "class C {}"})
	b := &windowedCanned{window: windowFor(300)}
	b.answers = []string{goodProfileFor("Api/Api.csproj")}
	in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
	in.Profile.Backend = b
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if b.calls != 1 {
		t.Fatalf("profile requests = %d", b.calls)
	}
	pf, _ := ParseProfileFile(res.Files.Profile)
	if !hasLimitation(pf.Limitations, "profile prompt truncated: ") || !hasLimitation(res.Limitations, "dependency lines dropped") {
		t.Fatalf("profile limitations %v, build limitations %v", pf.Limitations, res.Limitations)
	}
	if pf.Seeded {
		t.Errorf("the model answered; the profile is not a seed: %+v", pf)
	}
}

func goodProfileFor(manifestPath string) string {
	return `{"technologies":[{"id":"dotnet","language":"dotnet","role":"http","evidence":["` + manifestPath + `:1"],"pack":"dotnet"}],"scope":{"include":[{"glob":"**/*.cs","reason":"code"},{"glob":"**/*.csproj","reason":"manifest"}],"exclude":[]}}`
}

// An unreadable packs directory (here: a file where the directory should be)
// costs the project packs, not the build.
func TestBuildSnapshot_UnreadableProjectPacksIsALimitation(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{"src/a.ts": "export const a = 1", ".depbot/packs": "not a directory"})
	res, err := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake")))
	if err != nil {
		t.Fatalf("build must go on: %v", err)
	}
	if !hasLimitation(res.Limitations, "project packs: ") {
		t.Fatalf("limitations = %v", res.Limitations)
	}
}

// The same repository, commit and model answer build the same profile.json.
func TestBuildSnapshot_ProfileFileIsDeterministic(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{
		"Api/Api.csproj":  "<Project><ItemGroup><PackageReference Include=\"Confluent.Kafka\" Version=\"2.3.0\" /></ItemGroup></Project>",
		"Api/Consumer.cs": "class C {}",
	})
	answer := goodProfileFor("Api/Api.csproj")
	var files [2][]byte
	for i := range files {
		in := buildIn(root, head, t.TempDir(), llm.NewFake("testdata/fake"))
		in.Profile.Backend = &cannedBackend{answers: []string{answer}}
		res, err := BuildSnapshot(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		files[i] = res.Files.Profile
	}
	if len(files[0]) == 0 || !bytes.Equal(files[0], files[1]) {
		t.Fatalf("profile.json differs between identical builds:\n%s\n---\n%s", files[0], files[1])
	}
}

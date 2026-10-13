package scan

import (
	"os"
	"path/filepath"
	"regexp"

	"context"
	"errors"
	"fmt"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/manifest"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/llm"
)

type cannedBackend struct {
	answers []string
	calls   int
	seen    []llm.Request
}

func (c *cannedBackend) Name() string  { return "canned" }
func (c *cannedBackend) Model() string { return "t" }
func (c *cannedBackend) Complete(_ context.Context, r llm.Request) (llm.Response, error) {
	c.seen = append(c.seen, r)
	if c.calls >= len(c.answers) {
		return llm.Response{}, errors.New("no more answers")
	}
	a := c.answers[c.calls]
	c.calls++
	return llm.Response{Text: a}, nil
}

const goodProfile = `{"technologies":[{"id":"kafka","language":"dotnet","role":"messaging","evidence":["Api/Api.csproj:6 PackageReference Confluent.Kafka 2.3.0"],"pack":"new"}],
"scope":{"include":[{"glob":"src/**/*.cs","reason":"app code"}],"exclude":[{"glob":"**/Migrations/**","reason":"generated"}],"tests":[{"glob":"tests/**/*.cs","reason":"xunit"}]},"notes":[]}`

func TestParseProfile_ExtractsJSONFromProse(t *testing.T) {
	p, err := ParseProfile("Here is the profile:\n" + goodProfile + "\nHope this helps.")
	if err != nil || len(p.Technologies) != 1 || p.Technologies[0].Pack != "new" || len(p.Scope.Tests) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestValidateProfile_RejectsUnknownPackRoleAndGlob(t *testing.T) {
	known := map[string]bool{"dotnet": true}
	p, _ := ParseProfile(goodProfile)
	if err := ValidateProfile(&p, known); err != nil {
		t.Fatalf("new is allowed: %v", err)
	}
	p.Technologies[0].Pack = "kafka-dotnet"
	if err := ValidateProfile(&p, known); err == nil || !strings.Contains(err.Error(), "pack") {
		t.Fatalf("unknown pack accepted: %v", err)
	}
	p.Technologies[0].Pack = "new"
	p.Technologies[0].Role = "database"
	if err := ValidateProfile(&p, known); err == nil || !strings.Contains(err.Error(), "role") {
		t.Fatalf("bad role accepted: %v", err)
	}
	p.Technologies[0].Role = "storage"
	p.Scope.Include[0].Glob = "../**"
	if err := ValidateProfile(&p, known); err == nil || !strings.Contains(err.Error(), "glob") {
		t.Fatalf("escaping glob accepted: %v", err)
	}
}

func TestRequestProfile_RetriesOnceWithTheError(t *testing.T) {
	b := &cannedBackend{answers: []string{`{"technologies":[{"id":"x","role":"nope","pack":"new"}],"scope":{}}`, goodProfile}}
	req := llm.Request{System: "sys", User: "user"}
	p, err := RequestProfile(context.Background(), b, req)
	if err != nil || b.calls != 2 || len(p.Technologies) != 1 {
		t.Fatalf("calls=%d err=%v", b.calls, err)
	}
	if !strings.Contains(b.seen[1].User, "role") {
		t.Fatalf("retry must carry the validation error: %q", b.seen[1].User)
	}
	b = &cannedBackend{answers: []string{"garbage", "garbage"}}
	if _, err := RequestProfile(context.Background(), b, req); err == nil || b.calls != 2 {
		t.Fatalf("two bad answers must fail after exactly two calls: calls=%d err=%v", b.calls, err)
	}
}

func TestGuardScope_SeedWinsWhenModelExcludesEverything(t *testing.T) {
	inv := &Inventory{}
	for i := 0; i < 10; i++ {
		inv.Files = append(inv.Files, InvFile{Path: "src/f" + string(rune('a'+i)) + ".cs"})
	}
	seed := SeedResult{Include: []string{"**/*.cs"}, Exclude: bootstrapExclude}
	p, _ := ParseProfile(goodProfile)
	p.Scope.Exclude = []ScopeRule{{Glob: "src/**", Reason: "oops"}}
	scope, source, proposed, warn := GuardScope(p, seed, inv)
	if source != "seed" || proposed == nil || warn == "" || len(scope.Include) != 1 || scope.Include[0].Glob != "**/*.cs" {
		t.Fatalf("source=%s scope=%+v warn=%q", source, scope, warn)
	}
	p.Scope.Exclude = []ScopeRule{{Glob: "**/Migrations/**", Reason: "generated"}}
	if _, source, _, _ = GuardScope(p, seed, inv); source != "model" {
		t.Fatalf("sane scope rejected: %s", source)
	}
}

func TestBuildProfilePrompt_CarriesDepsCatalogAndSeed(t *testing.T) {
	inv := &Inventory{ByExt: map[string]int{".cs": 3}, TopDirs: map[string]int{"src": 3}}
	rep := deps.Report{Depends: []deps.Dependency{{Name: "confluent.kafka", Version: "2.3.0", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 6, Text: "PackageReference Confluent.Kafka"}}, Publishes: []deps.Package{{Name: "acme.api", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 2, Text: "PackageId"}}}
	req, dropped := BuildProfilePrompt(inv, rep, SeedResult{Tech: []string{"dotnet"}}, []prompts.PackMeta{{ID: "dotnet", Description: "ASP.NET"}}, 0)
	if dropped != 0 {
		t.Errorf("dropped %d", dropped)
	}
	if strings.Contains(req.User, "{Name:") || !strings.Contains(req.User, "PackageId") {
		t.Errorf("publishes rendered badly: %q", req.User)
	}
	for _, want := range []string{"confluent.kafka 2.3.0", "Api/Api.csproj:6", "PackageReference Confluent.Kafka", "dotnet", "ASP.NET", "\"pack\"", "seed:"} {
		if !strings.Contains(req.User, want) && !strings.Contains(req.System, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestSeedProfile_FromClassifier(t *testing.T) {
	p := SeedProfile(SeedResult{Tech: []string{"dotnet", "kafka"}}, deps.Report{})
	if len(p.Technologies) != 2 || p.Technologies[0].Pack != "dotnet" || p.Technologies[1].Pack != "kafka" {
		t.Fatalf("%+v", p.Technologies)
	}
	p.Scope.Include = []ScopeRule{{Glob: "**/*.cs"}}
	if err := ValidateProfile(&p, map[string]bool{"dotnet": true, "kafka": true}); err != nil {
		t.Fatalf("seed profile must validate: %v", err)
	}
}

func TestScopeInScope_TestsAdmittedExcludeWins(t *testing.T) {
	cfg := ScopeToScanConfig(Scope{
		Include: []ScopeRule{{Glob: "src/**/*.cs"}},
		Exclude: []ScopeRule{{Glob: "**/gen/**"}},
		Tests:   []ScopeRule{{Glob: "tests/**/*.cs"}},
	})
	for path, want := range map[string]bool{
		"src/a/b.cs": true, "tests/x/y.cs": true, "tests/gen/y.cs": false,
		"docs/readme.md": false, "src/node_modules/x.cs": false,
	} {
		if got := graph.InScope(path, &cfg); got != want {
			t.Errorf("InScope(%s)=%v want %v", path, got, want)
		}
	}
	if !cfg.IsTest("tests/x/y.cs") || cfg.IsTest("src/a/b.cs") {
		t.Fatal("IsTest")
	}
}

func TestGuardScope_ZeroInScopeSeedNeverTriggers(t *testing.T) {
	inv := &Inventory{Files: []InvFile{{Path: "a.md"}}}
	p, _ := ParseProfile(goodProfile)
	_, source, _, _ := GuardScope(p, SeedResult{Include: []string{"**/*.cs"}}, inv)
	if source != "model" {
		t.Fatal(source)
	}
}

func TestWriteProfiledManifest_BootstrapBytesUnchangedAndTests(t *testing.T) {
	root := bootRepo(t, map[string]string{"a.ts": "x", "package.json": "{}"})
	res, err := BootstrapManifest(BootstrapInput{RepoRoot: root, ProjectPath: "grp/app"})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(res.Path)
	out := filepath.Join(t.TempDir(), "m.yaml")
	include := []string{"**/*.ts", "**/package.json"}
	if err := WriteProfiledManifest(out, res.Domain, manifest.ScanConfig{Include: include, Exclude: bootstrapExclude}, res.Tech); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); string(got) != string(want) {
		t.Fatalf("differs:\n%s\nvs\n%s", got, want)
	}
	if err := WriteProfiledManifest(out, "app", manifest.ScanConfig{Include: []string{"src/**"}, Tests: []string{"tests/**"}}, nil); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.LoadFile(out)
	if err != nil || len(m.Domains) != 1 || len(m.Domains[0].Scan.Tests) != 1 || m.Domains[0].Scan.Tests[0] != "tests/**" {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestParseProfileFile_RoundTripIgnoresUnknown(t *testing.T) {
	pf, err := ParseProfileFile([]byte(`{"profile":{"technologies":[],"scope":{}},"scope_source":"seed","seeded":true,"future":1}`))
	if err != nil || pf.ScopeSource != "seed" || !pf.Seeded {
		t.Fatalf("%+v %v", pf, err)
	}
}

func TestRequestProfileWith_UnknownPackIsRetried(t *testing.T) {
	bad := strings.Replace(goodProfile, `"pack":"new"`, `"pack":"kafka-dotnet"`, 1)
	b := &cannedBackend{answers: []string{bad, goodProfile}}
	if _, err := RequestProfileWith(context.Background(), b, llm.Request{User: "u"}, map[string]bool{"dotnet": true}); err != nil || b.calls != 2 {
		t.Fatalf("calls=%d err=%v", b.calls, err)
	}
	if !strings.Contains(b.seen[1].User, "pack") {
		t.Fatal("retry lacks the pack error")
	}
}

func TestParseProfile_SkipsEmptyObjectInProse(t *testing.T) {
	p, err := ParseProfile("text {} more " + goodProfile)
	if err != nil || len(p.Technologies) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestValidateProfile_RejectsEmptyProfile(t *testing.T) {
	p, err := ParseProfile(`{"technologies":[],"scope":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateProfile(&p, nil); err == nil || !strings.Contains(err.Error(), "technologies") {
		t.Fatalf("%v", err)
	}
	p, _ = ParseProfile(goodProfile)
	p.Scope.Include = nil
	if err := ValidateProfile(&p, nil); err == nil || !strings.Contains(err.Error(), "scope.include") {
		t.Fatalf("%v", err)
	}
}

func TestRequestProfile_EmptyObjectThenGood(t *testing.T) {
	b := &cannedBackend{answers: []string{`{}`, goodProfile}}
	if _, err := RequestProfile(context.Background(), b, llm.Request{User: "u"}); err != nil || b.calls != 2 {
		t.Fatalf("calls=%d err=%v", b.calls, err)
	}
}

func TestSeedProfile_EvidenceByEcosystem(t *testing.T) {
	rep := deps.Report{Depends: []deps.Dependency{
		{Name: "express", Ecosystem: "npm", File: "package.json", Line: 5, Text: `"express"`},
		{Name: "confluent.kafka", Ecosystem: "nuget", File: "A.csproj", Line: 3, Text: "PackageReference Confluent.Kafka"},
	}}
	p := SeedProfile(SeedResult{Tech: []string{"typescript", "kafka", "prisma"}}, rep)
	if p.Technologies[0].Evidence[0] != `package.json:5 "express"` || p.Technologies[0].Language != "typescript" {
		t.Fatalf("%+v", p.Technologies[0])
	}
	if !strings.Contains(p.Technologies[1].Evidence[0], "A.csproj:3") || p.Technologies[1].Language != "dotnet" {
		t.Fatalf("%+v", p.Technologies[1])
	}
}

// repeatedDeps is 20 distinct dependencies each listed by 20 manifests.
func repeatedDeps() deps.Report {
	var rep deps.Report
	for m := 0; m < 20; m++ {
		for d := 0; d < 20; d++ {
			rep.Depends = append(rep.Depends, deps.Dependency{
				Name: fmt.Sprintf("dep-%02d", d), Version: "1.0.0", Ecosystem: "nuget",
				File: fmt.Sprintf("svc%02d/Svc.csproj", m), Line: d + 3,
				Text: `PackageReference Include="` + strings.Repeat("x", 60) + `"`,
			})
		}
	}
	return rep
}

// windowFor is a model window whose prompt budget is the system prompt plus extra characters.
func windowFor(extra int) int {
	return profileOutputReserve + profileMaxTokens + (len(profilePrompt)+extra)/profileCharsPerToken
}

func TestBuildProfilePrompt_SmallWindowDeduplicatesDependencies(t *testing.T) {
	inv := &Inventory{ByExt: map[string]int{".cs": 3}, TopDirs: map[string]int{"src": 3}}
	rep := repeatedDeps()
	full, _ := BuildProfilePrompt(inv, rep, SeedResult{}, nil, 0)
	if strings.Count(full.User, "dep-00 ") != 20 {
		t.Fatalf("without a window every line is kept: %d", strings.Count(full.User, "dep-00 "))
	}
	req, dropped := BuildProfilePrompt(inv, rep, SeedResult{}, nil, windowFor(8000))
	if dropped != 0 {
		t.Fatalf("dedupe is enough here, dropped %d", dropped)
	}
	for d := 0; d < 20; d++ {
		name := fmt.Sprintf("dep-%02d ", d)
		if strings.Count(req.User, name) != 1 {
			t.Errorf("%s appears %d times, want once", name, strings.Count(req.User, name))
		}
	}
	if !strings.Contains(req.User, "svc00/Svc.csproj:3") {
		t.Errorf("the first file:line of a dependency is kept:\n%s", req.User)
	}
	if len(req.System)+len(req.User) > (windowFor(8000)-profileOutputReserve-profileMaxTokens)*profileCharsPerToken {
		t.Errorf("prompt exceeds the budget")
	}
}

// Without a window nothing but inventory samples may shrink: 1000 distinct
// dependency lines (far over the soft 60000-char target) are all kept.
func TestBuildProfilePrompt_NoWindowNeverDropsDependencyLines(t *testing.T) {
	inv := &Inventory{ByExt: map[string]int{".cs": 3}, TopDirs: map[string]int{"src": 3}}
	var rep deps.Report
	for i := 0; i < 1000; i++ {
		rep.Depends = append(rep.Depends, deps.Dependency{Ecosystem: "nuget", Name: fmt.Sprintf("dep-%04d", i), Version: "1.0.0", File: "svc/Svc.csproj", Line: i + 1, Text: strings.Repeat("x", 80)})
	}
	req, dropped := BuildProfilePrompt(inv, rep, SeedResult{}, nil, 0)
	if dropped != 0 {
		t.Fatalf("dropped = %d", dropped)
	}
	if got := strings.Count(req.User, "nuget dep-"); got != 1000 {
		t.Fatalf("kept %d of 1000 lines", got)
	}
	// Also a repeated dependency is not collapsed without a window.
	rep.Depends = append(rep.Depends, deps.Dependency{Ecosystem: "nuget", Name: "dep-0000", Version: "2.0.0", File: "other/Other.csproj", Line: 1})
	req, _ = BuildProfilePrompt(inv, rep, SeedResult{}, nil, 0)
	if got := strings.Count(req.User, "nuget dep-0000 "); got != 2 {
		t.Fatalf("repeated dependency kept %d times, want 2", got)
	}
}

func TestBuildProfilePrompt_TinyWindowDropsDependencyLinesAndSaysHowMany(t *testing.T) {
	inv := &Inventory{ByExt: map[string]int{".cs": 3}, TopDirs: map[string]int{"src": 3}}
	rep := repeatedDeps()
	budgetExtra := 900
	req, dropped := BuildProfilePrompt(inv, rep, SeedResult{}, nil, windowFor(budgetExtra))
	if dropped <= 0 || dropped >= 20 {
		t.Fatalf("dropped = %d, want some of the 20 distinct lines", dropped)
	}
	if got := strings.Count(req.User, "nuget dep-"); got != 20-dropped {
		t.Errorf("kept %d lines, dropped %d of 20", got, dropped)
	}
	if len(req.System)+len(req.User) > len(profilePrompt)+budgetExtra {
		t.Errorf("prompt is %d chars, budget %d", len(req.System)+len(req.User), len(profilePrompt)+budgetExtra)
	}
}

// The prompt's own example must be an answer the validator accepts.
func TestProfilePrompt_ExamplePackIsInTheBuiltinCatalog(t *testing.T) {
	m := regexp.MustCompile(`"pack":\s*"([^"]+)"`).FindStringSubmatch(profilePrompt)
	if m == nil {
		t.Fatal("no pack in the prompt's example")
	}
	if m[1] != "new" && !prompts.BuiltinCatalog().Known()[m[1]] {
		t.Fatalf("the example pack %q is not a built-in id", m[1])
	}
	if !strings.Contains(profilePrompt, `or "new"`) {
		t.Error(`the prompt must name "new" as the other allowed pack value`)
	}
}

func TestWriteProfiledManifest_ErrorHasOneScanPrefix(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteProfiledManifest(filepath.Join(blocker, "sub", "chronicle.domain.yaml"), "app", manifest.ScanConfig{Include: []string{"src/**"}}, nil)
	if err == nil || strings.Count(err.Error(), "scan:") != 1 {
		t.Fatalf("err = %v", err)
	}
}

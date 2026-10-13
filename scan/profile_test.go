package scan

import (
	"os"
	"path/filepath"

	"context"
	"errors"
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
	rep := deps.Report{Depends: []deps.Dependency{{Name: "confluent.kafka", Version: "2.3.0", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 6}}}
	req := BuildProfilePrompt(inv, rep, SeedResult{Tech: []string{"dotnet"}}, []prompts.PackMeta{{ID: "dotnet", Description: "ASP.NET"}})
	for _, want := range []string{"confluent.kafka 2.3.0", "Api/Api.csproj:6", "dotnet", "ASP.NET", "\"pack\"", "seed:"} {
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

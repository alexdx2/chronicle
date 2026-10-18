package scan

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/manifest"
)

// fourServices is a monorepo of four NestJS services, one controller each.
func fourServices() map[string]string {
	files := map[string]string{}
	for _, d := range []string{"jerry-api", "tom-api", "spectators-api", "arena-api"} {
		files[d+"/package.json"] = `{"name":"` + d + `","scripts":{"start":"node dist/main"},"dependencies":{"@nestjs/core":"^10"}}`
		files[d+"/src/"+d+".controller.ts"] = "export class C {}"
		files[d+"/src/"+d+".service.ts"] = "export class S {}"
		files[d+"/src/main.ts"] = "export const main = 1"
	}
	return files
}

func fourServicesInv() *Inventory {
	inv := &Inventory{}
	for p := range fourServices() {
		inv.Files = append(inv.Files, InvFile{Path: p})
		if strings.HasSuffix(p, "package.json") {
			inv.Manifests = append(inv.Manifests, p)
		}
	}
	return inv
}

const partialScopeProfile = `{"technologies":[{"id":"typescript","language":"typescript","role":"other","evidence":["arena-api/src/main.ts"],"pack":"typescript"}],"scope":{"include":[{"glob":"arena-api/src/**/*.ts","reason":"app"}],"exclude":[]}}`

func appsReport(dirs ...string) deps.Report {
	var r deps.Report
	for _, d := range dirs {
		r.Publishes = append(r.Publishes, deps.Package{Name: d, Ecosystem: "npm", File: d + "/package.json", App: true})
	}
	return r
}

func TestGuardScope_ModelCoveringOneOfFourServicesKeepsItsScopeAndReAddsTheRest(t *testing.T) {
	inv := fourServicesInv()
	seed := SeedResult{Include: []string{"**/*.ts", "**/package.json"}, Exclude: bootstrapExclude}
	p, _ := ParseProfile(partialScopeProfile)
	p.Scope.Exclude = []ScopeRule{{Glob: "**/*.spec.ts", Reason: "tests"}}
	rep := appsReport("jerry-api", "tom-api", "spectators-api", "arena-api")
	// 4 of 16 files kept: above the 10% floor, the old guard let it through.
	scope, source, proposed, warn := GuardScope(p, seed, inv, rep)
	if source != "model" || proposed == nil {
		t.Fatalf("source=%s scope=%+v", source, scope)
	}
	// arena-api's include plus, per re-added dir, its source and manifest globs.
	if len(scope.Include) != 7 || len(scope.Exclude) != 1+len(bootstrapExclude) || scope.Exclude[0].Glob != "**/*.spec.ts" {
		t.Fatalf("model excludes must survive and dirs be appended: %+v", scope)
	}
	for _, d := range []string{"jerry-api", "tom-api", "spectators-api"} {
		if !strings.Contains(warn, d) {
			t.Errorf("warning %q does not name %s", warn, d)
		}
	}
	if strings.Contains(warn, "arena-api") {
		t.Errorf("warning names a covered directory: %q", warn)
	}
	// Covering every service directory leaves the scope alone.
	p.Scope.Include = []ScopeRule{{Glob: "*-api/src/**/*.ts", Reason: "app"}, {Glob: "*-api/package.json", Reason: "manifests"}}
	if scope, source, proposed, _ = GuardScope(p, seed, inv, rep); source != "model" || proposed != nil || len(scope.Include) != 2 {
		t.Fatalf("complete model scope changed: %s %+v", source, scope)
	}
}

func TestGuardScope_ApplicationDirectoryMayNotBeDropped(t *testing.T) {
	inv := &Inventory{Manifests: []string{"b/package.json"}}
	for _, p := range []string{"a/x.ts", "a/y.ts", "a/z.ts", "b/x.ts"} {
		inv.Files = append(inv.Files, InvFile{Path: p})
	}
	seed := SeedResult{Include: []string{"**/*.ts"}, Exclude: bootstrapExclude}
	p, _ := ParseProfile(partialScopeProfile)
	p.Scope.Include = []ScopeRule{{Glob: "a/**/*.ts", Reason: "app"}}
	scope, source, _, warn := GuardScope(p, seed, inv, appsReport("b"))
	if source != "model" || !strings.Contains(warn, "b") || scope.Include[len(scope.Include)-1].Glob != "b/**/*.ts" {
		t.Fatalf("source=%s scope=%+v warn=%q", source, scope, warn)
	}
	// A library package in that directory does not protect it.
	lib := deps.Report{Publishes: []deps.Package{{Name: "b", Ecosystem: "npm", File: "b/package.json"}}}
	if _, source, _, warn := GuardScope(p, seed, inv, lib); source != "model" || warn != "" {
		t.Fatalf("library dir re-added: %s %q", source, warn)
	}
}

func TestGuardScope_ExamplesWithLibraryManifestAndManyFilesIsNotReAdded(t *testing.T) {
	inv := &Inventory{Manifests: []string{"examples/package.json"}}
	for _, p := range []string{"app/main.ts", "app/a.ts", "app/b.ts", "examples/package.json", "examples/1.ts", "examples/2.ts", "examples/3.ts", "examples/4.ts", "examples/5.ts"} {
		inv.Files = append(inv.Files, InvFile{Path: p})
	}
	seed := SeedResult{Include: []string{"**/*.ts"}, Exclude: bootstrapExclude}
	p, _ := ParseProfile(partialScopeProfile)
	p.Scope.Include = []ScopeRule{{Glob: "app/**/*.ts", Reason: "app"}}
	lib := deps.Report{Publishes: []deps.Package{{Name: "examples", Ecosystem: "npm", File: "examples/package.json"}}}
	scope, source, proposed, warn := GuardScope(p, seed, inv, lib)
	if source != "model" || proposed != nil || warn != "" || len(scope.Include) != 1 {
		t.Fatalf("examples/ re-added: %s %+v %q", source, scope, warn)
	}
}

func TestGuardScope_ControllerDirectoryIsReAddedWithoutAManifestAndExcludeHidingItGoes(t *testing.T) {
	inv := &Inventory{}
	for _, p := range []string{"app/main.ts", "app/a.ts", "app/b.ts", "svc/users.controller.ts", "svc/util.ts"} {
		inv.Files = append(inv.Files, InvFile{Path: p})
	}
	seed := SeedResult{Include: []string{"**/*.ts"}, Exclude: bootstrapExclude}
	p, _ := ParseProfile(partialScopeProfile)
	p.Scope.Include = []ScopeRule{{Glob: "app/**/*.ts", Reason: "app"}}
	p.Scope.Exclude = []ScopeRule{{Glob: "**/*.spec.ts", Reason: "t"}, {Glob: "svc/*.controller.ts", Reason: "hides it"}, {Glob: "svc/legacy/**", Reason: "old"}}
	scope, source, _, _ := GuardScope(p, seed, inv, deps.Report{})
	globs := func(rs []ScopeRule) string {
		var g []string
		for _, r := range rs {
			g = append(g, r.Glob)
		}
		return strings.Join(g, " ")
	}
	if source != "model" || globs(scope.Exclude) != "**/*.spec.ts svc/legacy/** "+strings.Join(bootstrapExclude, " ") ||
		scope.Include[len(scope.Include)-1].Glob != "svc/**/*.ts" {
		t.Fatalf("%s %+v", source, scope)
	}
}

// Re-adding a protected directory keeps the model's excludes of generated and
// built output inside it, keeps the seed's exclusions, and limits the include to
// code: json, markdown and images next to the controller stay out of scope.
func TestGuardScope_ReAddedDirectoryKeepsGeneratedExcludesAndStaysCodeOnly(t *testing.T) {
	files := []string{
		"app/main.ts", "app/a.ts", "app/b.ts",
		"svc/users.controller.ts", "svc/util.ts", "svc/package.json",
		"svc/generated/api.ts", "svc/dist/main.ts", "svc/node_modules/x/index.ts",
		"svc/data.json", "svc/README.md", "svc/logo.svg",
	}
	inv := &Inventory{}
	for _, f := range files {
		inv.Files = append(inv.Files, InvFile{Path: f})
	}
	seed := SeedResult{Include: []string{"**/*.ts", "**/package.json"}, Exclude: bootstrapExclude}
	p, _ := ParseProfile(partialScopeProfile)
	p.Scope.Include = []ScopeRule{{Glob: "app/**/*.ts", Reason: "app"}}
	p.Scope.Exclude = []ScopeRule{{Glob: "svc/generated/**", Reason: "g"}, {Glob: "svc/dist/**", Reason: "d"}, {Glob: "svc/node_modules/**", Reason: "n"}}
	scope, source, _, warn := GuardScope(p, seed, inv, deps.Report{})
	if source != "model" || !strings.Contains(warn, "svc") {
		t.Fatalf("%s %q %+v", source, warn, scope)
	}
	cfg := ScopeToScanConfig(scope)
	in := map[string]bool{}
	for _, f := range files {
		in[f] = graph.InScope(f, &cfg)
	}
	for _, f := range []string{"svc/users.controller.ts", "svc/util.ts", "svc/package.json"} {
		if !in[f] {
			t.Errorf("%s must be in scope", f)
		}
	}
	for _, f := range []string{"svc/generated/api.ts", "svc/dist/main.ts", "svc/node_modules/x/index.ts", "svc/data.json", "svc/README.md", "svc/logo.svg"} {
		if in[f] {
			t.Errorf("%s must stay out of scope", f)
		}
	}
	// Even a model exclude list that lacks the seed's floor ends up with it.
	have := map[string]bool{}
	for _, r := range scope.Exclude {
		have[r.Glob] = true
	}
	for _, g := range bootstrapExclude {
		if !have[g] {
			t.Errorf("seed exclude %s missing from %+v", g, scope.Exclude)
		}
	}
}

func TestLayerChecks_ScopeFailsWhenControllersAreOutside(t *testing.T) {
	g, _ := checksGraph(t)
	inv := fourServicesInv()
	inv.Files = append(inv.Files, InvFile{Path: "shared/user.controller.ts"})
	cfg := &manifest.ScanConfig{Include: []string{"arena-api/**/*.ts"}}
	cs := LayerChecksScoped(g, "d", Profile{}, &FullReport{}, inv, cfg, nil)
	c := byLayer(cs)["scope"]
	if c.Status != CheckFailed || !strings.Contains(c.Detail, "4 of 5 controller or handler files are outside the scan scope") ||
		!strings.Contains(c.Detail, "jerry-api, shared, spectators-api, tom-api") {
		t.Fatalf("%+v", c)
	}
	if lim := checkLimitations(cs); len(lim) == 0 || !strings.Contains(strings.Join(lim, "|"), "layer scope:") {
		t.Fatalf("limitations %v", lim)
	}
	names := []string{}
	for _, c := range cs {
		names = append(names, c.Layer)
	}
	if strings.Join(names, ",") != "data,structure,scope,messaging,integrations,coverage" {
		t.Fatalf("order %v", names)
	}
	cov := byLayer(cs)["coverage"]
	if cov.Status != CheckFailed || !strings.Contains(cov.Detail, "3 of 13 inventory source files in scope") {
		t.Fatalf("coverage must report against the inventory: %+v", cov)
	}
	// Everything in scope: ok.
	cfg = &manifest.ScanConfig{Include: []string{"**/*.ts"}}
	cs = LayerChecksScoped(g, "d", Profile{}, &FullReport{}, inv, cfg, nil)
	if c := byLayer(cs)["scope"]; c.Status != CheckOK {
		t.Fatalf("%+v", c)
	}
	if c := byLayer(cs)["coverage"]; c.Status != CheckOK || !strings.Contains(c.Detail, "13 of 13") {
		t.Fatalf("%+v", c)
	}
}

func coveredDirs(t *testing.T, res *BuildResult) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	for _, c := range decodeT(t, res.Files).Coverage {
		got[topDirOf(c.Path)] = true
	}
	return got
}

func TestBuildSnapshot_PartialProfileScopeStillCoversEveryServiceDirectory(t *testing.T) {
	root, head := newCodeRepoT(t, fourServices())
	// No profile model: the seed.
	in := buildIn(root, head, t.TempDir(), &countingBackend{})
	in.Profile = ProfileOptions{Off: true}
	seeded, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	// A profile model that forgot three of the four services.
	in = buildIn(root, head, t.TempDir(), &countingBackend{})
	in.Profile = ProfileOptions{Backend: &cannedBackend{answers: []string{partialScopeProfile}}}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile.ScopeSource != "model" || res.Profile.ScopeProposed == nil ||
		!strings.Contains(res.Profile.SeedReason, "jerry-api, spectators-api, tom-api") {
		t.Fatalf("source=%s proposed=%v reason=%q", res.Profile.ScopeSource, res.Profile.ScopeProposed, res.Profile.SeedReason)
	}
	if !hasLimitation(res.Limitations, "re-added application directories") {
		t.Fatalf("limitations %v", res.Limitations)
	}
	for name, r := range map[string]*BuildResult{"no-profile": seeded, "partial-model": res} {
		dirs := coveredDirs(t, r)
		for _, d := range []string{"jerry-api", "tom-api", "spectators-api", "arena-api"} {
			if !dirs[d] {
				t.Errorf("%s: %s missing from coverage (%v)", name, d, dirs)
			}
		}
	}
}

func TestLayerChecks_UserManifestScopeIsReportedOKNotFailed(t *testing.T) {
	g, _ := checksGraph(t)
	inv := fourServicesInv()
	inv.Files = append(inv.Files, InvFile{Path: "shared/user.controller.ts"})
	cfg := &manifest.ScanConfig{Include: []string{"arena-api/**/*.ts"}}
	cs := LayerChecksScopedFrom(g, "d", Profile{}, &FullReport{}, inv, cfg, nil, true)
	c := byLayer(cs)["scope"]
	if c.Status != CheckOK || !strings.Contains(c.Detail, "excluded by the scan scope; endpoints in jerry-api, shared, spectators-api, tom-api are not in the graph") {
		t.Fatalf("%+v", c)
	}
	cov := byLayer(cs)["coverage"]
	if cov.Status != CheckOK || !strings.Contains(cov.Detail, "3 of 13 inventory source files in scope") {
		t.Fatalf("%+v", cov)
	}
	if lim := strings.Join(checkLimitations(cs), "|"); strings.Contains(lim, "layer scope:") || strings.Contains(lim, "layer coverage:") {
		t.Fatalf("user scope must not be a limitation: %s", lim)
	}
	// The same scope from the model or the seed still fails.
	cs = LayerChecksScopedFrom(g, "d", Profile{}, &FullReport{}, inv, cfg, nil, false)
	if byLayer(cs)["scope"].Status != CheckFailed || byLayer(cs)["coverage"].Status != CheckFailed {
		t.Fatalf("%+v", cs)
	}
}

package scan

import (
	"context"
	"strings"
	"testing"

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

func TestGuardScope_ModelCoveringOneOfFourServicesLosesToSeed(t *testing.T) {
	inv := fourServicesInv()
	seed := SeedResult{Include: []string{"**/*.ts", "**/package.json"}, Exclude: bootstrapExclude}
	p, _ := ParseProfile(partialScopeProfile)
	// 4 of 16 files kept: above the 10% floor, the old guard let it through.
	scope, source, proposed, warn := GuardScope(p, seed, inv)
	if source != "seed" || proposed == nil || len(scope.Include) != 2 {
		t.Fatalf("source=%s scope=%+v", source, scope)
	}
	for _, d := range []string{"jerry-api", "tom-api", "spectators-api"} {
		if !strings.Contains(warn, d) {
			t.Errorf("warning %q does not name %s", warn, d)
		}
	}
	if strings.Contains(warn, "arena-api") {
		t.Errorf("warning names a covered directory: %q", warn)
	}
	// Covering every service directory keeps the model's scope.
	p.Scope.Include = []ScopeRule{{Glob: "*-api/src/**/*.ts", Reason: "app"}, {Glob: "*-api/package.json", Reason: "manifests"}}
	if _, source, _, _ = GuardScope(p, seed, inv); source != "model" {
		t.Fatalf("complete model scope rejected: %s", source)
	}
}

func TestGuardScope_DirectoryWithItsOwnManifestMayNotBeDropped(t *testing.T) {
	inv := &Inventory{Manifests: []string{"b/package.json"}}
	for _, p := range []string{"a/x.ts", "a/y.ts", "a/z.ts", "b/x.ts"} {
		inv.Files = append(inv.Files, InvFile{Path: p})
	}
	seed := SeedResult{Include: []string{"**/*.ts"}, Exclude: bootstrapExclude}
	p, _ := ParseProfile(partialScopeProfile)
	p.Scope.Include = []ScopeRule{{Glob: "a/**/*.ts", Reason: "app"}}
	if _, source, _, warn := GuardScope(p, seed, inv); source != "seed" || !strings.Contains(warn, "b") {
		t.Fatalf("source=%s warn=%q", source, warn)
	}
	// A lone stray file in a directory without a manifest may be dropped.
	inv.Manifests = nil
	if _, source, _, _ := GuardScope(p, seed, inv); source != "model" {
		t.Fatalf("stray directory forced the seed: %s", source)
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
	if res.Profile.ScopeSource != "seed" || res.Profile.ScopeProposed == nil ||
		!strings.Contains(res.Profile.SeedReason, "jerry-api, spectators-api, tom-api") {
		t.Fatalf("source=%s proposed=%v reason=%q", res.Profile.ScopeSource, res.Profile.ScopeProposed, res.Profile.SeedReason)
	}
	if !hasLimitation(res.Limitations, "scope taken from the seed") {
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

package graph

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
)

func TestBoundaryPriority_Order(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		{"orders-api/package.json", 0},
		{"Dockerfile", 1},
		{"prisma/schema.prisma", 2},
		{"src/main.ts", 3},
		{"src/orders/orders.module.ts", 4},
		{"src/orders/orders.service.ts", 5},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := boundaryPriority(tc.path)
			if got != tc.want {
				t.Errorf("boundaryPriority(%q) = %d, want %d", tc.path, got, tc.want)
			}
		})
	}

	// Verify ordering: each priority level should be <= the next
	for i := 1; i < len(cases); i++ {
		prev := boundaryPriority(cases[i-1].path)
		curr := boundaryPriority(cases[i].path)
		if prev > curr {
			t.Errorf("ordering violation: %q (priority %d) > %q (priority %d)",
				cases[i-1].path, prev, cases[i].path, curr)
		}
	}
}

func TestDiscoverFilesDomainAssignment(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	// Create a temp git repo with test files
	tmpDir := t.TempDir()

	// git init
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	// Configure git user for commits
	for _, args := range [][]string{
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd = exec.Command("git", args...)
		cmd.Dir = tmpDir
		cmd.Run()
	}

	// Create files in different directories
	files := map[string]string{
		"api/main.go":      "package main",
		"web/index.html":   "<html></html>",
		"other/readme.md":  "# readme",
	}
	for f, content := range files {
		dir := filepath.Join(tmpDir, filepath.Dir(f))
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(tmpDir, f), []byte(content), 0644)
	}

	// git add + commit so files are tracked
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "commit", "-m", "init")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Create manifest with 2 domains
	m, err := manifest.Load([]byte(`
domains:
  - name: api
    scan:
      include: ["api/**"]
  - name: web
    scan:
      include: ["web/**"]
`))
	if err != nil {
		t.Fatalf("manifest load: %v", err)
	}

	result, err := g.DiscoverFiles(tmpDir, "_unassigned", revID, m)
	if err != nil {
		t.Fatalf("DiscoverFiles: %v", err)
	}

	// Should find 2 files (api/main.go and web/index.html match includes; other/readme.md does not)
	if result.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d, want 2 (files: %v)", result.TotalFiles, result.Files)
	}

	// Check obligations for domain assignment
	obligations, err := g.store.ListAllObligations(revID)
	if err != nil {
		t.Fatalf("ListAllObligations: %v", err)
	}

	domainByFile := map[string]string{}
	for _, ob := range obligations {
		if ob.ObligationType == "scan_file" {
			domainByFile[ob.TargetKey] = ob.DomainKey
		}
	}

	if domainByFile["api/main.go"] != "api" {
		t.Errorf("api/main.go domain = %q, want %q", domainByFile["api/main.go"], "api")
	}
	if domainByFile["web/index.html"] != "web" {
		t.Errorf("web/index.html domain = %q, want %q", domainByFile["web/index.html"], "web")
	}
	// other/readme.md should not be in obligations (excluded by include patterns)
	if _, found := domainByFile["other/readme.md"]; found {
		t.Errorf("other/readme.md should not have an obligation (not matching any include pattern)")
	}
}

// 2026-07-18 otopoint finding: discover(domain='api') pulled ALL manifest
// domains' includes (820 files instead of ~350). A domain-scoped discover must
// use only THAT domain's scan config; unknown domains keep the merged view.
func TestDiscoverFilesScopedToRequestedDomain(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	tmpDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	for _, f := range []string{"api/src/a.ts", "web/src/b.tsx"} {
		p := filepath.Join(tmpDir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-m", "c")

	m := &manifest.Manifest{Domains: []manifest.DomainEntry{
		{Name: "api", Scan: manifest.ScanConfig{Include: []string{"api/**"}}},
		{Name: "web", Scan: manifest.ScanConfig{Include: []string{"web/**"}}},
	}}

	res, err := g.DiscoverFilesOpts(tmpDir, "api", revID, m, DiscoverOpts{VotesNeeded: 1})
	if err != nil {
		t.Fatalf("DiscoverFilesOpts: %v", err)
	}
	for _, f := range res.Files {
		if strings.HasPrefix(f, "web/") {
			t.Errorf("domain=api discover included other domain's file %s", f)
		}
	}
	if res.TotalFiles != 1 {
		t.Errorf("want 1 api file, got %d: %+v", res.TotalFiles, res.Files)
	}

	// Unknown domain (no manifest entry) keeps the merged view.
	res2, err := g.DiscoverFilesOpts(tmpDir, "everything", revID, m, DiscoverOpts{VotesNeeded: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res2.TotalFiles != 2 {
		t.Errorf("unknown domain must fall back to merged config, got %d files", res2.TotalFiles)
	}
}

// gitFixtureRepo creates a one-file git repo discovery can walk and returns its
// root. Discovery reads git ls-files, so a manifest test needs a real repo.
func gitFixtureRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	p := filepath.Join(tmpDir, "src/a.ts")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "c")
	return tmpDir
}

// SQ-Contract 3 at the manifest writer. discover.go used to spell a manifest
// service key verbatim ("service:service:d:tom.api") while the resolver's
// calls_service lookup canonicalizes ("...:tom-api") — the lookup misses and a
// second node is minted for one service. Both writers must spell it one way.
func TestDiscoverManifestServiceKeyIsCanonical(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	tmpDir := gitFixtureRepo(t)

	m := &manifest.Manifest{
		Domains:  []manifest.DomainEntry{{Key: "test-domain", Name: "Test", Scan: manifest.ScanConfig{Include: []string{"src/**"}}}},
		Services: []manifest.ServiceEntry{{Key: "tom.api", Path: "src"}},
	}
	if _, err := g.DiscoverFilesOpts(tmpDir, "test-domain", revID, m, DiscoverOpts{VotesNeeded: 1}); err != nil {
		t.Fatalf("DiscoverFilesOpts: %v", err)
	}

	const wantKey = "service:service:test-domain:tom-api"
	if _, err := g.Store().GetNodeByKey(wantKey); err != nil {
		rows, _ := g.Store().ListNodes(store.NodeFilter{Layer: "service"})
		var got []string
		for _, n := range rows {
			got = append(got, n.NodeKey)
		}
		t.Fatalf("manifest service node not stored under canonical key %q; got %v", wantKey, got)
	}

	// The resolver must land on that same node, not mint a twin.
	facts := `[{"kind":"calls_service","to":"tom.api"}]`
	// src/a.ts is the file discovery created the obligation for — extract it and
	// satisfy the obligation so the resolve gate opens.
	if _, err := g.SaveFileExtraction(revID, "test-domain", "src/a.ts", "extracted", "provider", facts, ""); err != nil {
		t.Fatal(err)
	}
	if err := g.Store().SatisfyObligation(revID, "scan_file", "src/a.ts"); err != nil {
		t.Fatalf("SatisfyObligation: %v", err)
	}
	if _, err := g.ResolveExtractions("test-domain", revID); err != nil {
		t.Fatal(err)
	}

	rows, _ := g.Store().ListNodes(store.NodeFilter{Layer: "service"})
	var services []string
	for _, n := range rows {
		if n.NodeType == "service" {
			services = append(services, n.NodeKey)
		}
	}
	if len(services) != 1 || services[0] != wantKey {
		t.Errorf("calls_service %q must resolve to the manifest node; service nodes = %v, want exactly [%s]", "tom.api", services, wantKey)
	}

	edges, _ := g.Store().ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	if len(edges) == 0 {
		t.Fatal("no CALLS_SERVICE edge — assertion would pass vacuously")
	}
	for _, e := range edges {
		if e.ToNodeKey != wantKey {
			t.Errorf("CALLS_SERVICE to_node_key = %q, want %q", e.ToNodeKey, wantKey)
		}
	}
}

// 2026-07-18 codex-fixture finding: manifest infra nodes landed in domain
// "Tom and Jerry" (the DISPLAY name) with unregistered node type
// "message_broker". Infra created during a scan belongs to the scan's domain
// and must carry a registry-valid type.
func TestDiscoverManifestInfraDomainAndType(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	tmpDir := gitFixtureRepo(t)

	m := &manifest.Manifest{
		Domains: []manifest.DomainEntry{{Name: "Tom and Jerry", Scan: manifest.ScanConfig{Include: []string{"src/**"}}}},
		Infrastructure: []manifest.InfraEntry{{Name: "kafka:9092", Type: "message_broker"}},
	}

	if _, err := g.DiscoverFilesOpts(tmpDir, "test-domain", revID, m, DiscoverOpts{VotesNeeded: 1}); err != nil {
		t.Fatalf("DiscoverFilesOpts: %v", err)
	}

	rows, _ := g.Store().ListNodes(store.NodeFilter{Layer: "infra"})
	if len(rows) == 0 {
		t.Fatal("no infra node created")
	}
	for _, n := range rows {
		if n.DomainKey == "Tom and Jerry" {
			t.Errorf("infra node stamped with display-name domain: %s", n.NodeKey)
		}
		if n.DomainKey != "test-domain" {
			t.Errorf("infra node domain = %q, want the scan's domain test-domain", n.DomainKey)
		}
		if n.NodeType == "message_broker" {
			t.Errorf("unregistered node type passed through: %s", n.NodeType)
		}
	}
}

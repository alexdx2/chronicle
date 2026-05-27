package graph

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
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

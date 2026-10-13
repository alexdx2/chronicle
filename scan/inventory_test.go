package scan

import (
	"os"
	"strings"
	"testing"
)

func TestBuildInventory_ClassifiesAndSummarises(t *testing.T) {
	root, _ := newCodeRepoT(t, map[string]string{
		"src/Api/Api.csproj": "<Project/>",
		"src/Api/Program.cs": "class P{}",
		"src/Api/Order.cs":   "class O{}",
		"infra/main.tf":      "resource {}",
		"Dockerfile":         "FROM x",
		"assets/logo.png":    "\x89PNG\x00\x00binary",
		"docs/big.md":        strings.Repeat("x", 300),
	})
	inv, err := BuildInventory(root, 200)
	if err != nil {
		t.Fatal(err)
	}
	if inv.ByExt[".cs"] != 2 || inv.ByExt[".csproj"] != 1 || inv.TopDirs["src"] != 3 || inv.TopDirs["."] != 1 {
		t.Fatalf("counts: %+v %+v", inv.ByExt, inv.TopDirs)
	}
	if len(inv.Manifests) != 1 || inv.Manifests[0] != "src/Api/Api.csproj" {
		t.Fatalf("manifests = %v", inv.Manifests)
	}
	if len(inv.Infra) != 2 { // Dockerfile, infra/main.tf
		t.Fatalf("infra = %v", inv.Infra)
	}
	if len(inv.Skipped) != 2 { // png (binary), big.md (over 200 bytes)
		t.Fatalf("skipped = %v", inv.Skipped)
	}
	if len(inv.Paths()) != 5 {
		t.Fatalf("paths = %v", inv.Paths())
	}
	s := inv.Summary(2)
	for _, want := range []string{".cs: 2", "src: 3", "src/Api/Api.csproj", "Dockerfile", "skipped: 2"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	if s != inv.Summary(2) {
		t.Error("summary is not deterministic")
	}
}

func TestBuildInventory_SymlinkSkippedAndInfraNames(t *testing.T) {
	root, _ := newCodeRepoT(t, map[string]string{
		"a.txt":                    "x",
		"svc/Dockerfile.prod":      "FROM x",
		"deploy/helm/values.yaml":  "a: 1",
		"charts/api/Chart.yaml":    "name: api",
		".gitlab-ci.yml":           "stages: []",
		"cdk.json":                 "{}",
		"notes/dockerfile-tips.md": "x",
	})
	if err := os.Symlink("a.txt", root+"/link.txt"); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	gitT(t, root, "add", "link.txt")
	gitT(t, root, "commit", "-q", "-m", "link")
	inv, err := BuildInventory(root, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Skipped) != 1 || inv.Skipped[0] != "link.txt" {
		t.Fatalf("skipped = %v", inv.Skipped)
	}
	if len(inv.Infra) != 5 {
		t.Fatalf("infra = %v", inv.Infra)
	}
	for _, p := range inv.Paths() {
		if p == "link.txt" {
			t.Fatal("symlink in Paths")
		}
	}
}

func TestBuildInventory_NonPositiveMaxBytesIsDefault(t *testing.T) {
	root, _ := newCodeRepoT(t, map[string]string{
		"small.txt": "0123456789",
		"big.txt":   strings.Repeat("x", 300000),
	})
	inv, err := BuildInventory(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p := inv.Paths(); len(p) != 1 || p[0] != "small.txt" {
		t.Fatalf("paths = %v", p)
	}
	if s := inv.Summary(2); !strings.Contains(s, "skipped: 1 (binary or over 262144 bytes)") {
		t.Fatalf("summary:\n%s", s)
	}
}

package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/manifest"
)

// bootRepo is a committed repo made of the given files.
func bootRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "t@example.com")
	gitT(t, root, "config", "user.name", "t")
	gitT(t, root, "config", "commit.gpgsign", "false")
	for rel, content := range files {
		writeT(t, root, rel, content)
	}
	commitAllT(t, root, "base")
	return root
}

func manifestPathOf(root string) string {
	return filepath.Join(root, ".depbot", "chronicle.domain.yaml")
}

func TestDomainKey(t *testing.T) {
	for in, want := range map[string]string{
		"platform/billing": "platform-billing",
		"Platform/Billing": "platform-billing",
		"a b_c":            "a-b-c",
		"--x--":            "x",
		"":                 "repo",
		"///":              "repo",
	} {
		if got := DomainKey(in); got != want {
			t.Errorf("DomainKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBootstrapManifest_NestPrismaRepo(t *testing.T) {
	root := bootRepo(t, map[string]string{
		"package.json":            `{"dependencies":{"@nestjs/core":"1","@prisma/client":"5"}}`,
		"src/a.ts":                "export const a = 1;\n",
		"prisma/schema.prisma":    "model A { id Int @id }\n",
		"node_modules/x/index.js": "module.exports = 1;\n",
		"dist/b.js":               "exports.b = 1;\n",
	})
	res, err := BootstrapManifest(BootstrapInput{RepoRoot: root, ProjectPath: "platform/billing"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Domain != "platform-billing" || res.Path != manifestPathOf(root) {
		t.Fatalf("result = %+v", res)
	}
	want := []string{"typescript", "nestjs", "prisma"}
	if !reflect.DeepEqual(res.Tech, want) {
		t.Fatalf("Tech = %v, want %v", res.Tech, want)
	}
	m, err := manifest.LoadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Tech, want) {
		t.Fatalf("manifest tech = %v", m.Tech)
	}
	// P11: ScanConfigFor matches on the name, which must be the normalized key.
	if m.Domains[0].Name != "platform-billing" {
		t.Fatalf("name = %q", m.Domains[0].Name)
	}
	cfg, ok := m.ScanConfigFor("platform-billing")
	if !ok {
		t.Fatal("ScanConfigFor(platform-billing) not found")
	}
	if !graph.InScope("src/a.ts", cfg) {
		t.Error("src/a.ts must be in scope")
	}
	for _, p := range []string{"node_modules/x/index.js", "dist/b.js"} {
		if graph.InScope(p, cfg) {
			t.Errorf("%s must be out of scope", p)
		}
	}
	wantEx := []string{"**/node_modules/**", "**/vendor/**", "**/dist/**", "**/generated/**"}
	if !reflect.DeepEqual(cfg.Exclude, wantEx) {
		t.Errorf("exclude = %v", cfg.Exclude)
	}
}

func TestBootstrapManifest_GoRepoHasNoTech(t *testing.T) {
	root := bootRepo(t, map[string]string{"go.mod": "module x\n", "main.go": "package main\n"})
	res, err := BootstrapManifest(BootstrapInput{RepoRoot: root, ProjectPath: "g/x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tech) != 0 {
		t.Fatalf("Tech = %v", res.Tech)
	}
	m, err := manifest.LoadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	inc := m.Domains[0].Scan.Include
	for _, w := range []string{"**/*.go", "**/go.mod"} {
		found := false
		for _, i := range inc {
			found = found || i == w
		}
		if !found {
			t.Errorf("include %v lacks %s", inc, w)
		}
	}
}

func TestBootstrapManifest_Deterministic(t *testing.T) {
	files := map[string]string{
		"package.json": `{"dependencies":{"graphql":"1","kafkajs":"2"}}`,
		"src/a.ts":     "x\n", "svc/Svc.csproj": "<Project/>\n", "Dockerfile": "FROM x\n",
	}
	a := bootRepo(t, files)
	b := bootRepo(t, files)
	if _, err := BootstrapManifest(BootstrapInput{RepoRoot: a, ProjectPath: "p/q"}); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapManifest(BootstrapInput{RepoRoot: b, ProjectPath: "p/q"}); err != nil {
		t.Fatal(err)
	}
	ba, _ := os.ReadFile(manifestPathOf(a))
	bb, _ := os.ReadFile(manifestPathOf(b))
	if string(ba) != string(bb) {
		t.Fatalf("manifests differ:\n%s\n---\n%s", ba, bb)
	}
	if !strings.Contains(string(ba), "tech: [typescript, graphql, dotnet, kafka]") {
		t.Fatalf("tech order wrong:\n%s", ba)
	}
}

func TestBootstrapManifest_NeverOverwrites(t *testing.T) {
	existing := "domains:\n  mine:\n    name: mine\ntech: [go]\npublishers:\n  allowed_signers: |\n    k ssh-ed25519 AAAA\n"
	root := bootRepo(t, map[string]string{"main.go": "package main\n", ".depbot/chronicle.domain.yaml": existing})
	res, err := BootstrapManifest(BootstrapInput{RepoRoot: root, ProjectPath: "p/q"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.Domain != "mine" || !reflect.DeepEqual(res.Tech, []string{"go"}) {
		t.Fatalf("result = %+v", res)
	}
	got, _ := os.ReadFile(manifestPathOf(root))
	if string(got) != existing {
		t.Fatalf("existing manifest touched:\n%s", got)
	}

	bad := bootRepo(t, map[string]string{"main.go": "package main\n", ".depbot/chronicle.domain.yaml": "domains: [\n"})
	_, err = BootstrapManifest(BootstrapInput{RepoRoot: bad, ProjectPath: "p/q"})
	if err == nil || !strings.Contains(err.Error(), manifestPathOf(bad)) {
		t.Fatalf("want an error naming the path, got %v", err)
	}
	got, _ = os.ReadFile(manifestPathOf(bad))
	if string(got) != "domains: [\n" {
		t.Fatalf("invalid manifest touched: %q", got)
	}
}

func TestBootstrapManifest_EmptyRepoStillScoped(t *testing.T) {
	root := bootRepo(t, map[string]string{"README.md": "hi\n"})
	res, err := BootstrapManifest(BootstrapInput{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Domain != DomainKey(filepath.Base(root)) {
		t.Fatalf("result = %+v", res)
	}
	m, err := manifest.LoadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Domains[0].Scan.Exclude) == 0 {
		t.Fatal("exclude must not be empty")
	}
	if len(m.Domains[0].Scan.Include) != 0 {
		t.Fatalf("include = %v", m.Domains[0].Scan.Include)
	}
}

func TestBootstrapManifest_NotARepo(t *testing.T) {
	if _, err := BootstrapManifest(BootstrapInput{RepoRoot: t.TempDir()}); err == nil {
		t.Fatal("expected an error outside a git repository")
	}
}

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a git repo with src/{a,b,c}.ts and node_modules/x.ts, committed.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "user.email", "t@example.com")
	git(t, root, "config", "user.name", "t")
	git(t, root, "config", "commit.gpgsign", "false")
	write(t, root, "src/a.ts", "export const a = 1;\n")
	write(t, root, "src/b.ts", "import { a } from './a';\n")
	write(t, root, "src/c.ts", "export const c = 3;\n")
	write(t, root, "node_modules/x.ts", "export const x = 0;\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "base")
	return root
}

func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", msg)
}

// newGraph seeds a:provider (src/a.ts), b:controller (src/b.ts), b INJECTS a,
// and an unconnected c:provider (src/c.ts).
func newGraph(t *testing.T) *graph.Graph {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	g := graph.New(s, reg)
	rev, err := s.CreateRevision("d", "", "abc", "full_scan", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	nodes := []validate.NodeInput{
		{NodeKey: "code:provider:d:a", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "a", FilePath: "src/a.ts"},
		{NodeKey: "code:controller:d:b", Layer: "code", NodeType: "controller", DomainKey: "d", Name: "b", FilePath: "src/b.ts"},
		{NodeKey: "code:provider:d:c", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "c", FilePath: "src/c.ts"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, rev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:controller:d:b", ToNodeKey: "code:provider:d:a",
		EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code",
	}, rev); err != nil {
		t.Fatal(err)
	}
	return g
}

// extractEnv is a committed temp repo with a seeded graph and a one-domain
// manifest under <root>/.depbot, with paths pointed at the repo.
type extractEnv struct {
	root, dir string
	g         *graph.Graph
}

func newExtractEnv(t *testing.T) *extractEnv {
	t.Helper()
	root := newRepo(t)
	write(t, root, "src/a.ts", "export const a = 2;\n")
	commitAll(t, root, "change a")
	write(t, root, ".depbot/chronicle.domain.yaml", "domains:\n  d:\n    scan:\n      include: [\"src/**\"]\ntech: [typescript]\n")
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })
	return &extractEnv{root: root, dir: filepath.Join(root, ".depbot"), g: newGraph(t)}
}

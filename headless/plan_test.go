package headless

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/manifest"
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

func scan() *manifest.ScanConfig { return &manifest.ScanConfig{} }

func TestBuildPlan_ChangedPlusNeighbors(t *testing.T) {
	root := newRepo(t)
	write(t, root, "src/a.ts", "export const a = 2;\n")
	write(t, root, "node_modules/x.ts", "export const x = 1;\n")
	commitAll(t, root, "change")

	p, err := BuildPlan(PlanInput{G: newGraph(t), RepoRoot: root, Domain: "d", Scan: scan(), Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	want := []PlanFile{
		{Path: "src/a.ts", Reason: "changed", Bytes: 20},
		{Path: "src/b.ts", Reason: "neighbor:code:provider:d:a", Bytes: 25},
	}
	if !reflect.DeepEqual(p.Files, want) {
		t.Fatalf("Files = %+v, want %+v", p.Files, want)
	}
	if len(p.Skipped) != 1 || p.Skipped[0].Path != "node_modules/x.ts" || p.Skipped[0].Reason != "skipped:scope" {
		t.Fatalf("Skipped = %+v", p.Skipped)
	}
	if p.Changed != 1 || p.Neighbors != 1 || p.TotalBytes != 45 || p.Radius != 1 {
		t.Fatalf("counts: %+v", p)
	}
	if !reflect.DeepEqual(p.Paths(), []string{"src/a.ts", "src/b.ts"}) {
		t.Fatalf("Paths = %v", p.Paths())
	}
}

func TestBuildPlan_DeletedAndRenamed(t *testing.T) {
	root := newRepo(t)
	git(t, root, "rm", "-q", "src/c.ts")
	git(t, root, "mv", "src/b.ts", "src/bb.ts")
	commitAll(t, root, "del+rename")

	p, err := BuildPlan(PlanInput{G: newGraph(t), RepoRoot: root, Domain: "d", Scan: scan(), Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Paths(), []string{"src/bb.ts"}) {
		t.Fatalf("Paths = %v (plan %+v)", p.Paths(), p)
	}
	if p.Files[0].Reason != "changed" {
		t.Fatalf("reason = %q", p.Files[0].Reason)
	}
	for _, s := range p.Skipped {
		if s.Path == "src/c.ts" {
			t.Fatalf("deleted file must be dropped silently, got %+v", s)
		}
	}
}

func TestBuildPlan_Caps(t *testing.T) {
	root := newRepo(t)
	write(t, root, "src/a.ts", "export const a = 2;\n")
	commitAll(t, root, "change")
	in := PlanInput{G: newGraph(t), RepoRoot: root, Domain: "d", Scan: scan(), Base: "HEAD~1", Head: "HEAD"}

	in.MaxFiles = 1
	p, err := BuildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Paths(), []string{"src/a.ts"}) || len(p.Skipped) != 1 ||
		p.Skipped[0].Path != "src/b.ts" || p.Skipped[0].Reason != "skipped:budget" {
		t.Fatalf("MaxFiles=1: files %v skipped %+v", p.Paths(), p.Skipped)
	}

	in.MaxFiles = 0
	in.MaxBytes = 1
	p, err = BuildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 0 || len(p.Skipped) != 2 || p.TotalBytes != 0 {
		t.Fatalf("MaxBytes=1: files %v skipped %+v", p.Paths(), p.Skipped)
	}
	for _, s := range p.Skipped {
		if s.Reason != "skipped:budget" {
			t.Fatalf("reason = %q", s.Reason)
		}
	}
}

// A neighbour node may store file_path without extension; the plan resolves it
// to the single on-disk file with that stem.
func TestBuildPlan_ExtensionlessNeighbor(t *testing.T) {
	root := newRepo(t)
	write(t, root, "src/a.ts", "export const a = 2;\n")
	commitAll(t, root, "change")

	g := newGraph(t)
	rev, err := g.Store().CreateRevision("d", "", "def", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:d:bx", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "bx", FilePath: "src/b"}, rev); err != nil {
		t.Fatal(err)
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:provider:d:bx", ToNodeKey: "code:provider:d:a",
		EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code",
	}, rev); err != nil {
		t.Fatal(err)
	}

	p, err := BuildPlan(PlanInput{G: g, RepoRoot: root, Domain: "d", Scan: scan(), Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Paths(), []string{"src/a.ts", "src/b.ts"}) {
		t.Fatalf("Paths = %v, skipped %+v", p.Paths(), p.Skipped)
	}
	if got := p.Files[1].Reason; got != "neighbor:code:provider:d:a" {
		t.Fatalf("reason = %q", got)
	}
}

func TestBuildPlan_MissingNeighbor(t *testing.T) {
	root := newRepo(t)
	write(t, root, "src/a.ts", "export const a = 2;\n")
	commitAll(t, root, "change")
	if err := os.Remove(filepath.Join(root, "src/b.ts")); err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(PlanInput{G: newGraph(t), RepoRoot: root, Domain: "d", Scan: scan(), Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skipped) != 1 || p.Skipped[0].Path != "src/b.ts" || p.Skipped[0].Reason != "skipped:missing" {
		t.Fatalf("Skipped = %+v", p.Skipped)
	}
}

package scan

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeT(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAllT(t *testing.T, root, msg string) string {
	t.Helper()
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", msg)
	return gitT(t, root, "rev-parse", "HEAD")
}

// scanRepo is a one-commit repo with src/a.ts, a manifest scoping domain d to
// src/**, and an open store in the project's .depbot directory.
func scanRepo(t *testing.T) (root, head string, env Env, s *store.Store) {
	t.Helper()
	root = t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "t@example.com")
	gitT(t, root, "config", "user.name", "t")
	gitT(t, root, "config", "commit.gpgsign", "false")
	writeT(t, root, "src/a.ts", "export const a = 1;\n")
	manifestPath := filepath.Join(root, ".depbot", "chronicle.domain.yaml")
	writeT(t, root, ".depbot/chronicle.domain.yaml", "domains:\n  d:\n    scan:\n      include: [\"src/**\"]\ntech: [typescript]\n")
	head = commitAllT(t, root, "base")

	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })

	var err error
	s, err = store.Open(filepath.Join(root, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return root, head, Env{RepoDir: root, ManifestPath: manifestPath}, s
}

func newGraphT(t *testing.T, s *store.Store) *graph.Graph {
	t.Helper()
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	return graph.New(s, reg)
}

func TestEnv_StructuralPhaseSweepOnFreshRepo(t *testing.T) {
	_, head, env, s := scanRepo(t)
	g := newGraphT(t, s)
	res, err := env.StructuralPhase(g, "d", head, -1, true)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("sweep on a repo with a supported file returned no result")
	}
	if res.Processed < 1 || !res.Complete || res.Backlog != 0 {
		t.Fatalf("sweep result = processed %d, complete %v, backlog %d; want >=1, true, 0", res.Processed, res.Complete, res.Backlog)
	}
}

func TestEnv_StructuralFnHookIsUsed(t *testing.T) {
	_, head, env, s := scanRepo(t)
	g := newGraphT(t, s)
	boom := errors.New("boom")
	env.StructuralFn = func(*graph.Graph, graph.StructuralInput) (*graph.StructuralResult, error) {
		return nil, boom
	}
	if _, err := env.StructuralPhase(g, "d", head, -1, true); !errors.Is(err, boom) {
		t.Fatalf("StructuralPhase error = %v, want boom", err)
	}
}

func TestEnv_StructuralBaseAfterRestoreLikeRevision(t *testing.T) {
	root, ancestor, env, s := scanRepo(t)
	writeT(t, root, "src/b.ts", "export const b = 2;\n")
	commitAllT(t, root, "next")
	if _, err := s.CreateRevision("d", "", ancestor, "ci_pipeline", "full",
		`{"kind":"restore","structural":{"complete":true,"processed":0}}`); err != nil {
		t.Fatal(err)
	}
	base, sweep, forget := env.StructuralBase(s, "d")
	if base != ancestor || sweep || forget != 0 {
		t.Fatalf("StructuralBase = (%q, %v, %d), want (%q, false, 0)", base, sweep, forget, ancestor)
	}
}

func TestFilterRefreshable(t *testing.T) {
	got := FilterRefreshable([]string{"a.ts", "b.rb", "c.go"})
	if want := []string{"a.ts", "c.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterRefreshable = %v, want %v", got, want)
	}
}

func TestShortSHA(t *testing.T) {
	for in, want := range map[string]string{"0123456789abcdef": "0123456", "abc": "abc", "1234567": "1234567"} {
		if got := ShortSHA(in); got != want {
			t.Errorf("ShortSHA(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterRefreshable_ConfigFilesAreRefreshable(t *testing.T) {
	got := FilterRefreshable([]string{"a.ts", "b.rb", ".env", "src/main/resources/application.yml", "appsettings.json", "README.md"})
	want := []string{"a.ts", ".env", "src/main/resources/application.yml", "appsettings.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterRefreshable = %v, want %v", got, want)
	}
}

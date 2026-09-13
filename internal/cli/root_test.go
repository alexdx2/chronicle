package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/store"
)

func TestJournalActorNonEmpty(t *testing.T) {
	actor := journalActor()
	if actor == "" {
		t.Fatal("journalActor() returned empty string; want git user.email, hostname, or \"unknown\"")
	}
	t.Logf("journalActor() = %q", actor)
}

// resetWorktreeGlobals restores the package-level flag state resolveWorktreeGraph
// reads/writes, so tests that poke it don't leak into whichever test runs next.
func resetWorktreeGlobals(t *testing.T, cwd string) {
	t.Helper()
	os.Chdir(cwd)
	projectPath = ""
	chronicleDir = ".depbot"
	chronicleDirExplicit = false
	paths.SetProjectRoot("")
	paths.SetChronicleDir(".depbot")
}

// buildLinkedWorktree makes a main checkout with a graph at .depbot/chronicle.db
// (one revision, recorded at the main checkout's HEAD) plus a linked worktree
// on a branch one commit ahead of that HEAD. Returns (main, worktree, baseSHA).
func buildLinkedWorktree(t *testing.T) (main, wt, baseSHA string) {
	t.Helper()
	root := t.TempDir()
	main = filepath.Join(root, "main")
	if err := os.MkdirAll(main, 0755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, main, "init", "-q", "-b", "main")
	gitRun(t, main, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "base")
	baseSHA = strings.TrimSpace(gitCapture(t, main, "rev-parse", "HEAD"))

	if err := os.MkdirAll(filepath.Join(main, ".depbot"), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(main, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("d", "", baseSHA, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	wt = filepath.Join(root, "wt")
	gitRun(t, main, "worktree", "add", "-q", wt, "-b", "feat")
	gitRun(t, wt, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "ahead")
	return main, wt, baseSHA
}

// TestResolveWorktreeGraphSkipsWhenExplicit covers the CLI-level half of the
// custom --chronicle-dir fix: resolveWorktreeGraph must leave paths' project
// root alone whenever --project or --chronicle-dir was given explicitly, and
// must resolve to the main checkout only when neither was.
func TestResolveWorktreeGraphSkipsWhenExplicit(t *testing.T) {
	main, wt, _ := buildLinkedWorktree(t)
	cwd, _ := os.Getwd()
	defer resetWorktreeGlobals(t, cwd)

	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	// --chronicle-dir explicit (even at the default value) -> must not resolve.
	projectPath = ""
	chronicleDir = ".depbot"
	chronicleDirExplicit = true
	paths.SetProjectRoot("")
	paths.SetChronicleDir(".depbot")
	resolveWorktreeGraph()
	if paths.Root() != "" {
		t.Fatalf("explicit --chronicle-dir must skip worktree resolution, got root=%q", paths.Root())
	}

	// --project explicit -> must not resolve.
	projectPath = "somewhere"
	chronicleDirExplicit = false
	paths.SetProjectRoot("somewhere")
	resolveWorktreeGraph()
	if paths.Root() != "somewhere" {
		t.Fatalf("explicit --project must skip worktree resolution, got root=%q", paths.Root())
	}

	// Neither explicit -> resolves to main.
	projectPath = ""
	chronicleDirExplicit = false
	paths.SetProjectRoot("")
	resolveWorktreeGraph()
	if paths.Root() != main {
		t.Fatalf("expected resolution to main %s, got %q", main, paths.Root())
	}
}

// TestHookAdvisoryWrapperMeasuresWorktreeHEADAfterResolution exercises the
// real regression: hookAdvisory() resolves the graph to the main checkout via
// resolveWorktreeGraph(), then must still compute "commits behind" against the
// actual worktree's HEAD (repoDirForGit), not main's — main's checked-out ref
// never moves just because the worktree committed.
func TestHookAdvisoryWrapperMeasuresWorktreeHEADAfterResolution(t *testing.T) {
	_, wt, _ := buildLinkedWorktree(t)
	cwd, _ := os.Getwd()
	defer resetWorktreeGlobals(t, cwd)

	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}
	projectPath = ""
	chronicleDir = ".depbot"
	chronicleDirExplicit = false
	paths.SetProjectRoot("")
	paths.SetChronicleDir(".depbot")

	got := hookAdvisory()
	if !strings.Contains(got, "1 commit(s) behind") {
		t.Fatalf("expected 1 commit(s) behind measured against the worktree's HEAD after graph resolution, got %q", got)
	}
}

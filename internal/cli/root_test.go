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
	if !strings.Contains(got, "1 unscanned commit") {
		t.Fatalf("expected 1 unscanned commit measured against the worktree's HEAD after graph resolution, got %q", got)
	}
}

// A commit in ANY worktree runs the main checkout's post-commit hook, so
// `chronicle refresh --quiet` fires inside linked worktrees all day. It must
// write nothing: the revision it would create names the feature branch's tip
// and lands in main's graph, which then reports main as sitting on a commit
// nobody merged.
func TestRefreshFromALinkedWorktreeWritesNothing(t *testing.T) {
	main, wt, _ := buildLinkedWorktree(t)
	cwd, _ := os.Getwd()
	defer resetWorktreeGlobals(t, cwd)
	defer paths.SetGitDir("")

	db := filepath.Join(main, ".depbot", "chronicle.db")
	before := revisionCount(t, db)

	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}
	paths.SetGitDir("")

	cmd := newRefreshCmd()
	cmd.SetArgs([]string{"--quiet"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("refresh must not fail the commit it is hooked to: %v", err)
	}

	if after := revisionCount(t, db); after != before {
		t.Fatalf("main's graph gained %d revision(s) from a linked worktree", after-before)
	}
	if _, err := os.Stat(filepath.Join(wt, ".depbot")); err == nil {
		t.Error("refresh created a graph directory in the worktree")
	}
}

// A worktree that owns a graph of its own is a deliberate setup — the guard
// must not stop it writing its own knowledge.
func TestWorktreeWithItsOwnGraphIsNotRefused(t *testing.T) {
	_, wt, _ := buildLinkedWorktree(t)
	cwd, _ := os.Getwd()
	defer resetWorktreeGlobals(t, cwd)
	defer paths.SetGitDir("")

	if err := os.MkdirAll(filepath.Join(wt, ".depbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(wt, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}
	paths.SetGitDir("")
	if notice, linked := linkedWorktreeRefusal(); linked {
		t.Fatalf("a worktree with its own graph must be writable, got %q", notice)
	}
}

func TestMainCheckoutIsNeverRefused(t *testing.T) {
	main, _, _ := buildLinkedWorktree(t)
	cwd, _ := os.Getwd()
	defer resetWorktreeGlobals(t, cwd)
	defer paths.SetGitDir("")

	if err := os.Chdir(main); err != nil {
		t.Fatal(err)
	}
	paths.SetGitDir("")
	if notice, linked := linkedWorktreeRefusal(); linked {
		t.Fatalf("the main checkout must be writable, got %q", notice)
	}
}

// `hook install --git` run from a linked worktree used to write into that
// worktree's private git directory, where git never looks for hooks — the hook
// was installed and then never fired again.
func TestGitHookInstallFromAWorktreeWritesToTheHooksGitActuallyRuns(t *testing.T) {
	main, wt, _ := buildLinkedWorktree(t)
	cwd, _ := os.Getwd()
	defer resetWorktreeGlobals(t, cwd)
	defer paths.SetGitDir("")

	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}
	paths.SetGitDir("")
	if err := installGitPostCommit(); err != nil {
		t.Fatalf("installGitPostCommit: %v", err)
	}

	if _, err := os.Stat(filepath.Join(main, ".git", "hooks", "post-commit")); err != nil {
		t.Fatalf("post-commit hook is not where git looks for it: %v", err)
	}
}

func revisionCount(t *testing.T, dbPath string) int {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	revs := 0
	for id := int64(1); ; id++ {
		if _, err := s.GetRevision(id); err != nil {
			break
		}
		revs++
	}
	return revs
}

package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestResolveProjectDirFromLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	os.MkdirAll(main, 0755)
	run(t, main, "init", "-q", "-b", "main")
	run(t, main, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init")
	os.MkdirAll(filepath.Join(main, ".depbot"), 0755)
	os.WriteFile(filepath.Join(main, ".depbot", "chronicle.db"), []byte{}, 0644)
	wt := filepath.Join(root, "wt")
	run(t, main, "worktree", "add", "-q", wt, "-b", "feat")

	if got := ResolveProjectDir(wt, ".depbot"); got != main {
		t.Fatalf("worktree must resolve to main checkout %s, got %s", main, got)
	}
	if got := ResolveProjectDir(main, ".depbot"); got != main {
		t.Fatalf("main checkout resolves to itself, got %s", got)
	}
	if _, err := os.Stat(filepath.Join(wt, ".depbot")); !os.IsNotExist(err) {
		t.Fatalf("resolution must not create .depbot in the worktree")
	}
}

func TestResolveProjectDirPlainDir(t *testing.T) {
	d := t.TempDir()
	if got := ResolveProjectDir(d, ".depbot"); got != d {
		t.Fatalf("non-git dir resolves to itself, got %s", got)
	}
}

// TestResolveProjectDirCustomChronicleDir guards against the bug where a user
// in a linked worktree with a non-default --chronicle-dir gets redirected to
// the main checkout via a check against the DEFAULT name (".depbot"), which
// would not reflect where the main checkout's graph actually lives, and could
// lead ensureDepbotDir to create a stray "<main>/<custom>/" directory.
func TestResolveProjectDirCustomChronicleDir(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	os.MkdirAll(main, 0755)
	run(t, main, "init", "-q", "-b", "main")
	run(t, main, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init")
	os.MkdirAll(filepath.Join(main, ".mychron"), 0755)
	os.WriteFile(filepath.Join(main, ".mychron", "chronicle.db"), []byte{}, 0644)
	wt := filepath.Join(root, "wt")
	run(t, main, "worktree", "add", "-q", wt, "-b", "feat")

	if got := ResolveProjectDir(wt, ".mychron"); got != main {
		t.Fatalf("worktree must resolve to main checkout %s using the configured dir name, got %s", main, got)
	}
	// The default name must NOT match here — main only has ".mychron", not ".depbot".
	if got := ResolveProjectDir(wt, ".depbot"); got != wt {
		t.Fatalf("must not resolve via the default dir name when a different one is configured, got %s", got)
	}
	if _, err := os.Stat(filepath.Join(main, ".depbot")); !os.IsNotExist(err) {
		t.Fatalf("resolution must not create .depbot in main either")
	}
}

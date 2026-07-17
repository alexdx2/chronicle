package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a throwaway git repo with one committed file a.txt (v1).
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "c1")
	return dir
}

func TestChangedFilesWorktree(t *testing.T) {
	dir := initRepo(t)
	// Modify a.txt, add b.txt (untracked files need staging to show in diff;
	// ChangedFiles must include untracked worktree files too).
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ChangedFiles(dir, "HEAD", "")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Status
	}
	if byPath["a.txt"] != "M" {
		t.Errorf("a.txt status: got %q want M (files: %v)", byPath["a.txt"], files)
	}
	if byPath["b.txt"] != "A" {
		t.Errorf("b.txt status: got %q want A (files: %v)", byPath["b.txt"], files)
	}
}

func TestShow(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Show(dir, "HEAD", "a.txt")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if string(got) != "v1\n" {
		t.Errorf("Show HEAD:a.txt = %q, want v1", got)
	}
}

func TestMergeBase(t *testing.T) {
	dir := initRepo(t)
	sha, err := MergeBase(dir, "main")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if len(sha) < 7 {
		t.Errorf("merge-base sha too short: %q", sha)
	}
}

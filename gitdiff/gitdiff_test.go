package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Paths git has to quote — a space, a non-ASCII byte — must come back whole.
//
// Two defaults conspire here: core.quotePath renders "src/café.ts" as
// "src/caf\303\251.ts" WITH the quotes, and splitting --name-status on
// whitespace loses everything after the first space in a path. Both failures
// are silent: the file simply is not in the diff, and every consumer concludes
// nothing changed in it.
func TestChangedFilesHandlesQuotedAndSpacedPaths(t *testing.T) {
	dir := initRepo(t)
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
	const spaced = "src/my component.ts"
	const accented = "src/café.ts"
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{spaced, accented} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("export const x = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-m", "c2")

	files, err := ChangedFiles(dir, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Status
	}
	for _, name := range []string{spaced, accented} {
		if got[name] != "A" {
			t.Errorf("%q: status %q, want A (files: %+v)", name, got[name], files)
		}
	}
}

// A rename of such a path has to carry both halves: the new name to read and
// the old one to close.
func TestChangedFilesRenameCarriesBothPaths(t *testing.T) {
	dir := initRepo(t)
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
	const before = "src/old name.ts"
	const after = "src/nouveau café.ts"
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "export const x = 1\n// keep the similarity index high\n"
	if err := os.WriteFile(filepath.Join(dir, before), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "c2")
	run("mv", before, after)
	run("commit", "-m", "c3")

	files, err := ChangedFiles(dir, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	var ren *ChangedFile
	for i := range files {
		if files[i].Status == "R" {
			ren = &files[i]
		}
	}
	if ren == nil {
		t.Fatalf("no rename in %+v", files)
	}
	if ren.Path != after {
		t.Errorf("new path = %q, want %q", ren.Path, after)
	}
	if ren.OldPath != before {
		t.Errorf("old path = %q, want %q", ren.OldPath, before)
	}
}

// A base or head that begins with "-" is data, not a flag. Unflagged, git reads
// `--output=<path>` as an option: it truncates that file, writes an empty diff
// and exits 0, so the caller is told "nothing changed" while a file on disk has
// been destroyed. base and head arrive from a review_report tool argument an
// agent chose, so this is reachable without touching the repo.
func TestChangedFilesRefusesAnOptionShapedRef(t *testing.T) {
	dir := initRepo(t)
	canary := filepath.Join(dir, "canary.txt")
	if err := os.WriteFile(canary, []byte("intact\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, base, head string }{
		{"base only", "--output=" + canary, ""},
		{"base with head", "--output=" + canary, "HEAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := ChangedFiles(dir, tc.base, tc.head)
			if err == nil {
				t.Fatalf("an option-shaped ref was accepted, returning %d files", len(files))
			}
			got, rerr := os.ReadFile(canary)
			if rerr != nil {
				t.Fatalf("canary unreadable after the call: %v", rerr)
			}
			if string(got) != "intact\n" {
				t.Fatalf("canary = %q, want it untouched", got)
			}
		})
	}
}

func TestShowAndMergeBaseRefuseAnOptionShapedRef(t *testing.T) {
	dir := initRepo(t)
	canary := filepath.Join(dir, "canary.txt")
	if err := os.WriteFile(canary, []byte("intact\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Show(dir, "--output="+canary, "a.txt"); err == nil {
		t.Fatal("Show accepted an option-shaped ref")
	}
	if _, err := MergeBase(dir, "--output="+canary); err == nil {
		t.Fatal("MergeBase accepted an option-shaped ref")
	}
	got, err := os.ReadFile(canary)
	if err != nil || string(got) != "intact\n" {
		t.Fatalf("canary = %q (err %v), want it untouched", got, err)
	}
}

// gitRunT and gitCaptureT are this file's own -C-based helpers (mirroring
// initRepo's inline closures) for tests that need branching, which initRepo's
// single-branch fixture doesn't give them.
func gitRunT(t *testing.T, dir string, args ...string) {
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

func gitCaptureT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestMergeBaseOf exercises the free two-argument form MergeBase itself can
// never offer (MergeBase always merge-bases against HEAD): a fork point
// followed by two branches that both add a commit, plus a THIRD branch that
// forks from an EARLIER commit and becomes HEAD. If MergeBaseOf secretly
// depended on HEAD (e.g. a copy-paste that merge-based one argument against
// HEAD instead of against the other argument), it would report the earlier,
// wrong ancestor; only a real two-argument `git merge-base branch-a branch-b`
// reports the later, correct fork point regardless of where HEAD sits.
func TestMergeBaseOf(t *testing.T) {
	dir := initRepo(t) // main @ c1 ("root")
	root := gitCaptureT(t, dir, "rev-parse", "HEAD")

	// A branch that forks from root BEFORE main advances — HEAD ends up here,
	// deliberately unrelated to the fork point MergeBaseOf must find.
	gitRunT(t, dir, "checkout", "-b", "head-branch")
	gitRunT(t, dir, "checkout", "main")

	// main advances: this is the fork point branch-a/branch-b actually share.
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, dir, "add", ".")
	gitRunT(t, dir, "commit", "-m", "main advances")
	forkPoint := gitCaptureT(t, dir, "rev-parse", "HEAD")
	if forkPoint == root {
		t.Fatal("test setup: fork point must differ from root")
	}

	gitRunT(t, dir, "checkout", "-b", "branch-a")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, dir, "add", ".")
	gitRunT(t, dir, "commit", "-m", "a")

	gitRunT(t, dir, "checkout", "main")
	gitRunT(t, dir, "checkout", "-b", "branch-b")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, dir, "add", ".")
	gitRunT(t, dir, "commit", "-m", "b")

	// HEAD is now on head-branch (forked from root, before forkPoint existed)
	// — unrelated to the branch-a/branch-b comparison MergeBaseOf is asked for.
	gitRunT(t, dir, "checkout", "head-branch")

	got, err := MergeBaseOf(dir, "branch-a", "branch-b")
	if err != nil {
		t.Fatalf("MergeBaseOf: %v", err)
	}
	if got != forkPoint {
		t.Errorf("MergeBaseOf(branch-a, branch-b) = %q, want the fork point %q (not root %q, and not HEAD-dependent)", got, forkPoint, root)
	}
}

// TestMergeBaseOf_RefusesOptionShapedRef mirrors
// TestShowAndMergeBaseRefuseAnOptionShapedRef: the brief's own single
// --end-of-options marker must shield BOTH positional arguments, not just the
// first, since a or b can each independently be a review_report/CLI argument
// an agent or user chose.
func TestMergeBaseOf_RefusesOptionShapedRef(t *testing.T) {
	dir := initRepo(t)
	canary := filepath.Join(dir, "canary.txt")
	if err := os.WriteFile(canary, []byte("intact\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, a, b string }{
		{"option-shaped a", "--output=" + canary, "HEAD"},
		{"option-shaped b", "HEAD", "--output=" + canary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := MergeBaseOf(dir, tc.a, tc.b); err == nil {
				t.Fatalf("MergeBaseOf(%q, %q) accepted an option-shaped ref", tc.a, tc.b)
			}
		})
	}

	got, err := os.ReadFile(canary)
	if err != nil || string(got) != "intact\n" {
		t.Fatalf("canary = %q (err %v), want it untouched", got, err)
	}
}

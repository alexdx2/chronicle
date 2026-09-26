package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGit runs a git subcommand against dir, failing the test on error. Named
// distinctly from the local "run" closures in gitdiff_test.go (harmless
// either way — those are function-scoped — but this avoids any confusion).
func runGit(t *testing.T, dir string, args ...string) {
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

// TestDiffHunks_AddRemoveModify covers all three edit shapes -U0 produces:
// a pure insertion, a pure deletion and a one-line modify. The edits are
// placed far apart in the file so git has no reason to merge them into one
// hunk, but the assertions are content-based (per the brief) rather than
// count-based, in case git's hunk-splitting heuristics merge or split them
// differently than expected.
func TestDiffHunks_AddRemoveModify(t *testing.T) {
	dir := initRepo(t)
	initial := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "c2")

	// Relative to c2: insert "inserted" after line2, delete line5, modify
	// line8 -> line8-modified.
	edited := "line1\nline2\ninserted\nline3\nline4\nline6\nline7\nline8-modified\nline9\nline10\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "c3")

	files, err := DiffHunks(dir, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("DiffHunks: %v", err)
	}
	if len(files) != 1 || files[0].Path != "a.txt" {
		t.Fatalf("files = %+v, want exactly one entry for a.txt", files)
	}
	fh := files[0]
	if fh.OldPath != "" {
		t.Errorf("OldPath = %q, want empty (not a rename)", fh.OldPath)
	}
	if fh.Binary {
		t.Errorf("Binary = true, want false")
	}
	if len(fh.Hunks) == 0 {
		t.Fatalf("no hunks parsed: %+v", fh)
	}

	var hasInsert, hasDelete, hasModify bool
	for _, h := range fh.Hunks {
		switch {
		case h.OldLines == 0 && h.OldStart == 2 && h.NewStart == 3 && h.NewLines == 1:
			hasInsert = true
		case h.NewLines == 0 && h.OldStart == 5 && h.OldLines == 1 && h.NewStart == 5:
			hasDelete = true
		case h.OldStart == 8 && h.OldLines == 1 && h.NewStart == 8 && h.NewLines == 1:
			hasModify = true
		}
	}
	if !hasInsert {
		t.Errorf("no insert-shaped hunk (OldLines==0 at OldStart=2, NewStart=3,NewLines=1) in %+v", fh.Hunks)
	}
	if !hasDelete {
		t.Errorf("no delete-shaped hunk (NewLines==0 at OldStart=5,OldLines=1, NewStart=5) in %+v", fh.Hunks)
	}
	if !hasModify {
		t.Errorf("no modify-shaped hunk (OldStart=8,OldLines=1, NewStart=8,NewLines=1) in %+v", fh.Hunks)
	}
}

// TestDiffHunks_Rename covers git mv + an accompanying edit: one FileHunks
// with OldPath and Path different, plus the hunk for the edit that rode
// along with the rename.
func TestDiffHunks_Rename(t *testing.T) {
	dir := initRepo(t)
	// Enough shared content that git's default 50% similarity threshold
	// still detects this as a rename after the one-line edit below.
	body := "export const x = 1\n// keep similarity index high padding padding\nline\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "c2")

	const after = "renamed.txt"
	runGit(t, dir, "mv", "a.txt", after)
	edited := "export const x = 2\n// keep similarity index high padding padding\nline\n"
	if err := os.WriteFile(filepath.Join(dir, after), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "c3")

	files, err := DiffHunks(dir, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("DiffHunks: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v, want exactly one renamed entry", files)
	}
	fh := files[0]
	if fh.Path != after {
		t.Errorf("Path = %q, want %q", fh.Path, after)
	}
	if fh.OldPath != "a.txt" {
		t.Errorf("OldPath = %q, want %q", fh.OldPath, "a.txt")
	}
	if len(fh.Hunks) != 1 {
		t.Fatalf("Hunks = %+v, want exactly one (the line-1 edit)", fh.Hunks)
	}
	if h := fh.Hunks[0]; h.OldStart != 1 || h.NewStart != 1 {
		t.Errorf("hunk = %+v, want the edit at line 1", h)
	}
}

// TestDiffHunks_Binary covers a binary file: Binary must be true, Hunks must
// be empty, and parsing must not panic on the "Binary files ... differ" line
// (which has no @@ headers at all).
func TestDiffHunks_Binary(t *testing.T) {
	dir := initRepo(t)
	// A NUL byte guarantees git's binary-content heuristic fires, rather than
	// relying on chance with random bytes.
	data1 := []byte("PNG\x00fake-image-data-1-------")
	if err := os.WriteFile(filepath.Join(dir, "img.bin"), data1, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "img.bin")
	runGit(t, dir, "commit", "-m", "c2")

	data2 := []byte("PNG\x00fake-image-data-2-------")
	if err := os.WriteFile(filepath.Join(dir, "img.bin"), data2, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "c3")

	files, err := DiffHunks(dir, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("DiffHunks: %v", err)
	}
	var bin *FileHunks
	for i := range files {
		if files[i].Path == "img.bin" {
			bin = &files[i]
		}
	}
	if bin == nil {
		t.Fatalf("img.bin not found in %+v", files)
	}
	if !bin.Binary {
		t.Errorf("Binary = false, want true: %+v", bin)
	}
	if len(bin.Hunks) != 0 {
		t.Errorf("Hunks = %+v, want empty for a binary file", bin.Hunks)
	}
}

// TestDiffHunks_UnicodeSpacePath covers a path with both a space and a
// non-ASCII byte: it must come back whole, not quoted/escaped, the same
// failure mode ChangedFiles already guards against via core.quotePath=false.
func TestDiffHunks_UnicodeSpacePath(t *testing.T) {
	dir := initRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	const rel = "src/tom café.ts"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("export const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "c2")

	if err := os.WriteFile(filepath.Join(dir, rel), []byte("export const x = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "c3")

	files, err := DiffHunks(dir, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("DiffHunks: %v", err)
	}
	var got *FileHunks
	for i := range files {
		if files[i].Path == rel {
			got = &files[i]
		}
	}
	if got == nil {
		t.Fatalf("path %q not found unquoted/unescaped in %+v", rel, files)
	}
	if len(got.Hunks) != 1 || got.Hunks[0].OldStart != 1 || got.Hunks[0].NewStart != 1 {
		t.Errorf("unexpected hunks for %q: %+v", rel, got.Hunks)
	}
}

// TestDiffHunks_RefusesOptionShapedRef mirrors
// TestShowAndMergeBaseRefuseAnOptionShapedRef: a canary file proves the
// option-shaped ref was rejected before doing anything destructive, not that
// the function merely happened to return an error for some unrelated reason.
func TestDiffHunks_RefusesOptionShapedRef(t *testing.T) {
	dir := initRepo(t)
	canary := filepath.Join(dir, "canary.txt")
	if err := os.WriteFile(canary, []byte("intact\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := DiffHunks(dir, "--output="+canary, "HEAD")
	if err == nil {
		t.Fatalf("an option-shaped base was accepted, returning %d files", len(files))
	}
	got, rerr := os.ReadFile(canary)
	if rerr != nil {
		t.Fatalf("canary unreadable after the call: %v", rerr)
	}
	if string(got) != "intact\n" {
		t.Fatalf("canary = %q, want it untouched", got)
	}
}

// TestDiffHunks_WorkingTree covers head=="" comparing base against the
// working tree, as ChangedFiles does: an uncommitted edit must show up.
func TestDiffHunks_WorkingTree(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := DiffHunks(dir, "HEAD", "")
	if err != nil {
		t.Fatalf("DiffHunks: %v", err)
	}
	var got *FileHunks
	for i := range files {
		if files[i].Path == "a.txt" {
			got = &files[i]
		}
	}
	if got == nil {
		t.Fatalf("a.txt (uncommitted edit) not in %+v", files)
	}
	if len(got.Hunks) != 1 {
		t.Fatalf("Hunks = %+v, want exactly one", got.Hunks)
	}
	if h := got.Hunks[0]; h.OldStart != 1 || h.OldLines != 1 || h.NewStart != 1 || h.NewLines != 1 {
		t.Errorf("unexpected hunk: %+v", h)
	}
}

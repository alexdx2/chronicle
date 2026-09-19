package gitutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	if _, err := Run(dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := Run(dir, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-q", "-m", "base"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	sha, err := Run(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return dir, sha
}

func TestRunTrimsAndRunsInDir(t *testing.T) {
	dir, sha := repo(t)
	if strings.ContainsAny(sha, " \n\t") {
		t.Errorf("Run must trim its output, got %q", sha)
	}
	if len(sha) != 40 {
		t.Errorf("want a full sha, got %q", sha)
	}
	// A sibling directory that is not a repo must fail, proving -C is applied.
	other := t.TempDir()
	if _, err := Run(other, "rev-parse", "HEAD"); err == nil {
		t.Error("rev-parse in a non-repo must fail")
	}
	_ = filepath.Join(dir, ".git")
}

func TestOKAnswersTheExitCode(t *testing.T) {
	dir, sha := repo(t)
	if !OK(dir, "cat-file", "-e", sha+"^{commit}") {
		t.Error("an existing commit must read as OK")
	}
	if OK(dir, "cat-file", "-e", "0000000000000000000000000000000000000000^{commit}") {
		t.Error("a missing commit must not read as OK")
	}
}

func TestExitCodeSeparatesAnswerFromFailure(t *testing.T) {
	dir, sha := repo(t)
	if _, err := Run(dir, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-q", "-m", "next"); err != nil {
		t.Fatal(err)
	}
	// "is HEAD an ancestor of <first commit>" is a real "no": exit 1.
	head, _ := Run(dir, "rev-parse", "HEAD")
	_, err := Run(dir, "merge-base", "--is-ancestor", head, sha)
	if got := ExitCode(err); got != 1 {
		t.Errorf("a git 'no' must surface as exit 1, got %d (err %v)", got, err)
	}
	if got := ExitCode(nil); got != 0 {
		t.Errorf("no error is exit 0, got %d", got)
	}
	if got := ExitCode(os.ErrNotExist); got != -1 {
		t.Errorf("a non-exec error must be -1 (git never ran), got %d", got)
	}
}

// A commit can sit on many branches or none, so the only commit whose branch
// is knowable is the one checked out. Labelling any other with whatever branch
// happens to be current would put "main" on a scan of a feature commit.
func TestBranchAtOnlyAnswersForHEAD(t *testing.T) {
	dir, sha := repo(t)
	if got := BranchAt(dir, sha); got == "" {
		t.Fatalf("BranchAt(HEAD) = %q, want the current branch", got)
	}
	if got := BranchAt(dir, "0123456789abcdef0123456789abcdef01234567"); got != "" {
		t.Fatalf("BranchAt(other commit) = %q, want empty", got)
	}
	if got := BranchAt(dir, ""); got != "" {
		t.Fatalf("BranchAt(\"\") = %q, want empty", got)
	}
}

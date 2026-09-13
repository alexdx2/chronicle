package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/store"
)

// refreshRepo builds a git repo with two commits on main and one commit on a
// branch nobody merged, plus an open store. The unmerged commit is the shape
// that broke this in the field: a product generates its surface extract on a
// feature branch, the import names that commit, and the refresh then diffs
// from a commit HEAD has never contained.
func refreshRepo(t *testing.T) (dir string, s *store.Store, c1, c2, unmerged string) {
	t.Helper()
	dir = t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	commit := func(msg string) string {
		gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t",
			"commit", "--allow-empty", "-q", "-m", msg)
		return strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD"))
	}
	c1 = commit("one")
	gitRun(t, dir, "checkout", "-q", "-b", "side")
	unmerged = commit("side")
	gitRun(t, dir, "checkout", "-q", "main")
	c2 = commit("two")

	var err error
	s, err = store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return dir, s, c1, c2, unmerged
}

// uiImport writes the revision a surface import creates: trigger_kind manual,
// metadata.layer "ui", named by the commit the extract was made at.
func uiImport(t *testing.T, s *store.Store, domain, sha string) {
	t.Helper()
	if _, err := s.CreateRevision(domain, "", sha, "manual", "incremental",
		`{"layer":"ui","source":"auto.surface.json"}`); err != nil {
		t.Fatal(err)
	}
}

// A ui import is knowledge about one layer at one commit — never the commit
// the code knowledge was taken from. Diffing from it re-verifies files nobody
// changed and stale-marks the graph wholesale (72 files in the live run that
// found this, where the true base gives 1).
func TestRefreshBaseIgnoresASurfaceImport(t *testing.T) {
	dir, s, c1, _, unmerged := refreshRepo(t)
	if _, err := s.CreateRevision("auto", "", c1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	uiImport(t, s, "auto", unmerged)

	base, err := refreshBase(s, dir)
	if err != nil {
		t.Fatalf("refreshBase: %v", err)
	}
	if base.GitAfterSHA != c1 {
		t.Fatalf("base = %s, want the scanned commit %s", base.GitAfterSHA, c1)
	}
}

// A refresh that landed after the scan IS the newest confirmation of the code
// layer, so it — not the older scan — is what the next refresh diffs from.
func TestRefreshBaseUsesTheNewerRefresh(t *testing.T) {
	dir, s, c1, c2, unmerged := refreshRepo(t)
	if _, err := s.CreateRevision("auto", "", c1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("auto", "", c2, "git_hook", "incremental", `{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}
	uiImport(t, s, "auto", unmerged)

	base, err := refreshBase(s, dir)
	if err != nil {
		t.Fatalf("refreshBase: %v", err)
	}
	if base.GitAfterSHA != c2 {
		t.Fatalf("base = %s, want the refreshed commit %s", base.GitAfterSHA, c2)
	}
}

// A refresh recorded on a branch this checkout does not contain cannot be
// diffed from; the scan still can, so it is the fallback rather than a refusal.
func TestRefreshBaseFallsBackToTheScanWhenTheRefreshIsNotAnAncestor(t *testing.T) {
	dir, s, c1, _, unmerged := refreshRepo(t)
	if _, err := s.CreateRevision("auto", "", c1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("auto", "", unmerged, "git_hook", "incremental", `{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}

	base, err := refreshBase(s, dir)
	if err != nil {
		t.Fatalf("refreshBase: %v", err)
	}
	if base.GitAfterSHA != c1 {
		t.Fatalf("base = %s, want the scanned commit %s", base.GitAfterSHA, c1)
	}
}

// Nothing on this branch to diff from: refuse, and say why. Silently diffing
// from an unrelated commit is how 146 rows of evidence got refuted at once.
func TestRefreshBaseRefusesWhenNothingIsAnAncestor(t *testing.T) {
	dir, s, _, _, unmerged := refreshRepo(t)
	if _, err := s.CreateRevision("auto", "", unmerged, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}

	_, err := refreshBase(s, dir)
	if err == nil {
		t.Fatal("want a refusal when the scanned commit is not on this branch")
	}
	if !strings.Contains(err.Error(), "not an ancestor") {
		t.Errorf("the refusal must say what is wrong, got %q", err)
	}
}

// A repo with nothing but a ui import has no code knowledge to refresh.
func TestRefreshBaseRefusesWithOnlyASurfaceImport(t *testing.T) {
	dir, s, _, _, _ := refreshRepo(t)
	uiImport(t, s, "auto", strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD")))

	if _, err := refreshBase(s, dir); err == nil {
		t.Fatal("want a refusal: a surface import is not a scan")
	}
}

// A commit that touched no refreshable file still moves the graph's verified
// point: a docs-only commit must leave the repo reading "verified", not slide
// it into "stale" for a change that cannot have invalidated anything.
func TestRefreshRecordsANoopRevisionForACommitWithNothingToVerify(t *testing.T) {
	dir, wt, _ := buildLinkedWorktree(t) // reused: a plain repo with a graph
	_ = wt
	cwd, _ := os.Getwd()
	defer resetWorktreeGlobals(t, cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	paths.SetGitDir("")
	defer paths.SetGitDir("")

	// A commit that changes nothing a refresh can verify.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "README.md")
	gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "docs")
	head := strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD"))

	cmd := newRefreshCmd()
	cmd.SetArgs([]string{"--quiet"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rev, err := s.LatestRefreshRevision("")
	if err != nil {
		t.Fatalf("no refresh revision was recorded: %v", err)
	}
	if rev.GitAfterSHA != head {
		t.Errorf("refresh revision at %s, want HEAD %s", rev.GitAfterSHA, head)
	}
	if !strings.Contains(rev.Metadata, `"noop":true`) {
		t.Errorf("a refresh that verified nothing must say so: metadata %s", rev.Metadata)
	}
}

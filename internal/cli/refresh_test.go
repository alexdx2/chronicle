package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
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

// runRefreshIn chdirs into dir, runs the refresh command with args, and
// returns everything it printed to stdout.
func runRefreshIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cwd, _ := os.Getwd()
	defer resetWorktreeGlobals(t, cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	paths.SetGitDir("")
	defer paths.SetGitDir("")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	realStdout := os.Stdout
	os.Stdout = w

	cmd := newRefreshCmd()
	cmd.SetArgs(args)
	runErr := cmd.Execute()

	w.Close()
	os.Stdout = realStdout
	out, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("refresh: %v", runErr)
	}
	return string(out)
}

func refreshRevisions(t *testing.T, dir string) []*store.Revision {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out []*store.Revision
	for id := int64(1); ; id++ {
		rev, err := s.GetRevision(id)
		if err != nil {
			break
		}
		if rev.TriggerKind == "git_hook" {
			out = append(out, rev)
		}
	}
	return out
}

// A commit that touched no file the graph knows about still moves the verified
// point: a docs-only commit must leave the repo reading "verified", not slide
// it into "stale" for a change that cannot have invalidated anything.
func TestRefreshRecordsANoopRevisionForACommitTheGraphKnowsNothingAbout(t *testing.T) {
	dir, _, _ := buildLinkedWorktree(t) // reused: a plain repo with a graph
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "README.md")
	gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "docs")
	head := strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD"))

	runRefreshIn(t, dir, "--quiet")

	revs := refreshRevisions(t, dir)
	if len(revs) != 1 {
		t.Fatalf("want exactly one refresh revision, got %d", len(revs))
	}
	if revs[0].GitAfterSHA != head {
		t.Errorf("refresh revision at %s, want HEAD %s", revs[0].GitAfterSHA, head)
	}
	if !strings.Contains(revs[0].Metadata, `"noop":true`) {
		t.Errorf("a refresh that verified nothing must say so: metadata %s", revs[0].Metadata)
	}
}

// "Verified" may only mean "no file the graph knows about changed". A repo
// written in a language the deterministic refresh cannot check — Ruby, PHP,
// Java — would otherwise have EVERY commit stamped verified@HEAD simply
// because no changed file matched refreshExtensions, which is the graph
// claiming to have checked code it has never read.
func TestRefreshDoesNotClaimVerifiedWhenAKnownFileChanged(t *testing.T) {
	dir, _, _ := buildLinkedWorktree(t)
	const known = "lib/thing.rb"
	seedEvidenceFor(t, dir, known)

	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, known), []byte("class Thing; end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", known)
	gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "ruby")

	out := runRefreshIn(t, dir)

	if revs := refreshRevisions(t, dir); len(revs) != 0 {
		t.Fatalf("a change to a file the graph has evidence for must not be stamped verified: %+v", revs[0])
	}
	if !strings.Contains(out, known) {
		t.Fatalf("the file the graph knows changed must be reported as pending, got:\n%s", out)
	}
}

// seedEvidenceFor gives the graph a node with evidence anchored at filePath, so
// the file counts as one the graph knows about.
func seedEvidenceFor(t *testing.T, dir, filePath string) {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	g := graph.New(s, reg)
	rev, err := s.LatestScanRevision("")
	if err != nil {
		t.Fatal(err)
	}
	const key = "code:symbol:d:thing"
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: key, Layer: "code", NodeType: "symbol", DomainKey: "d",
		Name: "Thing", FilePath: filePath,
	}, rev.RevisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddNodeEvidence(key, validate.EvidenceInput{
		TargetKind: "node", SourceKind: "file", FilePath: filePath, LineStart: 1,
		ExtractorID: "test", ExtractorVersion: "1",
		Assertion: `{}`, AssertionKind: "symbol_exists", AssertionVersion: "1",
		Confidence: 0.9, Polarity: "positive", RevisionID: rev.RevisionID,
	}); err != nil {
		t.Fatal(err)
	}
}

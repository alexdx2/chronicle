package cli

import (
	"errors"
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

// --- phase 2: deterministic structural re-extraction -------------------------

const srvSource = `import { Db } from './db';

@Injectable()
export class AService {
  list() { return []; }
}
`

const ctrlSource = `import { AService } from './a.service';

@Controller('a')
export class AController {
  constructor(private readonly a: AService) {}

  @Get('items')
  list() { return this.a.list(); }
}
`

const laterCtrlSource = `import { AService } from './a.service';

@Controller('later')
export class LaterController {
  constructor(private readonly a: AService) {}

  @Post('things')
  make() { return this.a.list(); }
}
`

const structuralManifest = `domains:
  d:
    name: D
    scan:
      include:
        - "src/**"
      exclude:
        - "**/node_modules/**"
tech:
  - nestjs
`

// writeCommit writes a file and commits it, returning the new HEAD.
func writeCommit(t *testing.T, dir, rel, content, msg string) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", rel)
	gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", msg)
	return strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD"))
}

// structuralRepo is a repo whose graph was fully scanned at its first commit:
// one service under src/, a manifest naming domain "d" and the nestjs packs.
func structuralRepo(t *testing.T) (dir, scanned string) {
	t.Helper()
	dir = t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, ".depbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".depbot", "chronicle.domain.yaml"),
		[]byte(structuralManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/.depbot/chronicle.db*\n/.depbot/events/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".gitignore", ".depbot/chronicle.domain.yaml")
	scanned = writeCommit(t, dir, "src/a.service.ts", srvSource, "scanned state")

	s := openRepoStore(t, dir)
	defer s.Close()
	if _, err := s.CreateRevision("d", "", scanned, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	return dir, scanned
}

func openRepoStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func structuralSHA(t *testing.T, dir string) string {
	t.Helper()
	s := openRepoStore(t, dir)
	defer s.Close()
	rev, err := s.LatestStructuralRevision("d")
	if err != nil {
		return ""
	}
	return rev.GitAfterSHA
}

func evidenceCount(t *testing.T, dir string) int {
	t.Helper()
	s := openRepoStore(t, dir)
	defer s.Close()
	var n int
	if err := s.QueryRowScan(`SELECT COUNT(*) FROM graph_evidence`, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A commit is structured by the same hook that verifies it: the files it
// touched become nodes and edges with no model in the loop, under a pointer of
// their own that the scan's pointer never moves with.
func TestRefreshStructuresTheCommit(t *testing.T) {
	dir, scanned := structuralRepo(t)
	head := writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "add a controller")

	runRefreshIn(t, dir, "--quiet")

	s := openRepoStore(t, dir)
	defer s.Close()
	if _, err := s.GetNodeByKey("contract:endpoint:d:get:/a/items"); err != nil {
		t.Fatalf("the route the commit added is not in the graph: %v", err)
	}
	rev, err := s.LatestStructuralRevision("d")
	if err != nil {
		t.Fatalf("LatestStructuralRevision: %v", err)
	}
	if rev.GitAfterSHA != head {
		t.Errorf("structured@%s, want HEAD %s", rev.GitAfterSHA, head)
	}
	scanRev, err := s.LatestScanRevision("d")
	if err != nil || scanRev.GitAfterSHA != scanned {
		t.Errorf("the scan pointer moved to %+v; only a scan moves it", scanRev)
	}
}

// Honesty test 1: a crash between verification and structure.
//
// The two phases have separate pointers because they succeed and fail
// separately. A structural phase that died must leave verified@HEAD and
// structured@<the last commit it finished>, and the NEXT run must diff from
// there — everything in between is re-extracted, nothing is lost.
func TestRefreshStructuralFailureLeavesVerificationStanding(t *testing.T) {
	dir, _ := structuralRepo(t)
	b := writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "B: a controller")
	runRefreshIn(t, dir, "--quiet")
	if got := structuralSHA(t, dir); got != b {
		t.Fatalf("structured@%s, want %s before the crash", got, b)
	}

	// C: a second controller, and a structural phase that dies.
	c := writeCommit(t, dir, "src/later.controller.ts", laterCtrlSource, "C: another controller")
	orig := structuralFn
	structuralFn = func(*graph.Graph, graph.StructuralInput) (*graph.StructuralResult, error) {
		return nil, errors.New("structural phase died")
	}
	runRefreshIn(t, dir, "--quiet")
	structuralFn = orig

	s := openRepoStore(t, dir)
	verified, err := s.LatestRefreshRevision("d")
	if err != nil {
		t.Fatalf("LatestRefreshRevision: %v", err)
	}
	if verified.GitAfterSHA != c {
		t.Errorf("verified@%s, want %s — the verification phase succeeded", verified.GitAfterSHA, c)
	}
	if _, err := s.GetNodeByKey("contract:endpoint:d:post:/later/things"); err == nil {
		t.Error("a failed structural phase must not have written the route")
	}
	s.Close()
	if got := structuralSHA(t, dir); got != b {
		t.Fatalf("structured@%s, want %s — a failed phase guarantees nothing", got, b)
	}

	// D: the next run diffs from B, so C's route arrives after all.
	d := writeCommit(t, dir, "src/d.service.ts", srvSource, "D: another service")
	runRefreshIn(t, dir, "--quiet")

	s2 := openRepoStore(t, dir)
	defer s2.Close()
	if _, err := s2.GetNodeByKey("contract:endpoint:d:post:/later/things"); err != nil {
		t.Fatalf("the commit the crash skipped was never re-extracted: %v", err)
	}
	if got := structuralSHA(t, dir); got != d {
		t.Errorf("structured@%s, want %s", got, d)
	}
}

// The phase is opt-out: a caller that only wants verification gets only
// verification, and no structural claim comes out of it.
func TestRefreshNoStructuralSkipsPhaseTwo(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "add a controller")

	runRefreshIn(t, dir, "--quiet", "--no-structural")

	if got := structuralSHA(t, dir); got != "" {
		t.Fatalf("structured@%s, want nothing — phase 2 was switched off", got)
	}
	s := openRepoStore(t, dir)
	defer s.Close()
	if _, err := s.GetNodeByKey("contract:endpoint:d:get:/a/items"); err == nil {
		t.Error("phase 2 ran anyway")
	}
}

// After an interrupted phase the pointer is behind, so the next run sees the
// whole repo again — and redoes none of the files whose content it already
// applied. Re-extracting them would be wasted work; re-writing their evidence
// would be a lie about when it was observed.
func TestRefreshRerunAfterAnInterruptedPhaseRedoesNothing(t *testing.T) {
	dir, scanned := structuralRepo(t)
	writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "add a controller")
	runRefreshIn(t, dir, "--quiet")
	before := evidenceCount(t, dir)

	// The state an interrupted phase leaves: the work applied, the claim not
	// made. The next run therefore has no pointer and sweeps every file.
	s := openRepoStore(t, dir)
	rev, err := s.LatestStructuralRevision("d")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRevisionMetadata(rev.RevisionID, map[string]any{
		"structural": map[string]any{"complete": false},
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	runRefreshIn(t, dir, "--quiet")

	if after := evidenceCount(t, dir); after != before {
		t.Errorf("evidence rows %d → %d; content already applied must not be re-extracted", before, after)
	}
	if got := structuralSHA(t, dir); got == "" || got == scanned {
		t.Errorf("structured@%q — the rerun must finish what the interrupted phase started", got)
	}
}

// The batch bounds a hook; a person draining a first pass or a rules-pack
// backlog needs to be able to lift it. --structural-batch=1 proves the knob is
// wired to the phase and not just parsed: two supported files, one parsed, the
// other left as backlog, and no pointer until it is drained.
func TestRefreshStructuralBatchIsWiredThrough(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "add a controller")

	runRefreshIn(t, dir, "--quiet", "--structural-batch=1")

	if got := structuralSHA(t, dir); got != "" {
		t.Fatalf("structured@%s — one file of two is not a complete phase", got)
	}
	runRefreshIn(t, dir, "--quiet", "--structural-batch=1")
	if got := structuralSHA(t, dir); got == "" {
		t.Fatal("the second batch must finish the sweep")
	}
	s := openRepoStore(t, dir)
	defer s.Close()
	if _, err := s.GetNodeByKey("contract:endpoint:d:get:/a/items"); err != nil {
		t.Fatalf("both files must be structured after two batches: %v", err)
	}
}

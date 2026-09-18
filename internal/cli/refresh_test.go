package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/extract/rules"
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

const ctrlSourceTwoRoutes = `import { AService } from './a.service';

@Controller('a')
export class AController {
  constructor(private readonly a: AService) {}

  @Get('items')
  list() { return this.a.list(); }

  @Post('items')
  make() { return this.a.list(); }
}
`

// The phase diffs commits, so it must read commits. A half-written working
// tree — a partially staged file, an editor buffer saved mid-thought — would
// otherwise put structure in the graph that no commit contains, attributed to
// the commit that does not contain it.
func TestRefreshStructuresTheCommitNotTheWorkingTree(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "one route")

	// Uncommitted: a second route nobody has committed.
	if err := os.WriteFile(filepath.Join(dir, "src/a.controller.ts"), []byte(ctrlSourceTwoRoutes), 0o644); err != nil {
		t.Fatal(err)
	}

	runRefreshIn(t, dir, "--quiet")

	s := openRepoStore(t, dir)
	defer s.Close()
	if _, err := s.GetNodeByKey("contract:endpoint:d:get:/a/items"); err != nil {
		t.Fatalf("the committed route is missing: %v", err)
	}
	if _, err := s.GetNodeByKey("contract:endpoint:d:post:/a/items"); err == nil {
		t.Fatal("the graph gained a route no commit contains")
	}
	// And the content record names the committed bytes, so the commit that
	// eventually lands the second route is not mistaken for a no-op.
	sum := sha256.Sum256([]byte(ctrlSource))
	hash, _, ok, err := s.GetStructuralHash("d", "src/a.controller.ts")
	if err != nil || !ok {
		t.Fatalf("no content record (ok=%v, err=%v)", ok, err)
	}
	if hash != hex.EncodeToString(sum[:]) {
		t.Error("the content record names the working tree, so the real commit will look unchanged")
	}
}

// A rules-pack bump makes files re-extractable without a commit. The phase has
// to run for that alone: gating it on "has the diff moved" would leave the
// backlog undrained until somebody happened to commit something.
func TestRefreshDrainsThePackBacklogWithNoNewCommit(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "one route")
	runRefreshIn(t, dir, "--quiet")

	// The state a pack bump leaves: the files are recorded, under an older
	// pack. HEAD has not moved.
	s := openRepoStore(t, dir)
	rev, err := s.LatestStructuralRevision("d")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/a.service.ts", "src/a.controller.ts"} {
		hash, _, ok, err := s.GetStructuralHash("d", f)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", f, ok, err)
		}
		if err := s.SetStructuralHash("d", f, hash, "0", rev.RevisionID); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := s.CountStructuralHashesNotOnPack("d", rules.PackVersion); n != 2 {
		t.Fatalf("fixture: backlog = %d, want 2", n)
	}
	s.Close()

	runRefreshIn(t, dir, "--quiet")

	s2 := openRepoStore(t, dir)
	defer s2.Close()
	if n, err := s2.CountStructuralHashesNotOnPack("d", rules.PackVersion); err != nil || n != 0 {
		t.Fatalf("backlog = %d (%v); a pack bump must drain without waiting for a commit", n, err)
	}
}

// A structural pointer this checkout cannot reach was earned on a branch
// nobody merged. Its content records say "already done" about files whose
// structure came from code this history does not contain, so they are
// forgotten along with the pointer — otherwise the new base's version of those
// files is never read.
func TestRefreshForgetsBranchOnlyContentRecordsAfterARebase(t *testing.T) {
	dir, scanned := structuralRepo(t)

	gitRun(t, dir, "checkout", "-q", "-b", "side")
	writeCommit(t, dir, "src/side.controller.ts", ctrlSource, "side: a controller")
	runRefreshIn(t, dir, "--quiet")
	if _, _, ok, _ := func() (string, string, bool, error) {
		s := openRepoStore(t, dir)
		defer s.Close()
		return s.GetStructuralHash("d", "src/side.controller.ts")
	}(); !ok {
		t.Fatal("fixture: the side branch's file was not recorded")
	}

	// Back on a history the side branch is not part of.
	gitRun(t, dir, "checkout", "-q", "main")
	if strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD")) != scanned {
		t.Fatal("fixture: main should still be at the scanned commit")
	}
	writeCommit(t, dir, "src/main.controller.ts", laterCtrlSource, "main: a controller")

	runRefreshIn(t, dir, "--quiet")

	s := openRepoStore(t, dir)
	defer s.Close()
	if _, _, ok, _ := s.GetStructuralHash("d", "src/side.controller.ts"); ok {
		t.Error("a branch-only content record survived the switch")
	}
	if _, err := s.GetNodeByKey("contract:endpoint:d:post:/later/things"); err != nil {
		t.Fatalf("the commit on this branch was not structured: %v", err)
	}
}

// What the hook says, and when it stays quiet. A commit that only deleted
// files parses nothing and still changed what the graph asserts — silence
// there would hide the one kind of change people most want confirmed.
func TestStructuralQuietLine(t *testing.T) {
	if got := structuralQuietLine(nil); got != "" {
		t.Errorf("no phase, no line: %q", got)
	}
	if got := structuralQuietLine(&graph.StructuralResult{Skipped: 12}); got != "" {
		t.Errorf("a run that did nothing must stay silent: %q", got)
	}
	if got := structuralQuietLine(&graph.StructuralResult{Superseded: 3}); got == "" {
		t.Error("a deletion-only run changed the graph and must say so")
	}
	// Both kinds of no-answer are named: they ask for different fixes, and a
	// single "failed" number would send the reader after the wrong one.
	got := structuralQuietLine(&graph.StructuralResult{
		Processed: 5, Failed: []string{"a.ts"}, Unread: []string{"b.ts", "c.ts"},
		Unresolved: 2, Backlog: 7,
	})
	want := "chronicle refresh: structure 5 files (1 failed, 2 not read, 2 unresolved, 7 remaining)"
	if got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// The sweep lists HEAD's tree, and a path git would quote — a space, a
// non-ASCII byte — must survive that listing. Losing one is silent: the file
// is never structured, and `complete: true` is stamped over its absence.
func TestRefreshSweepSeesQuotedAndSpacedPaths(t *testing.T) {
	dir, _ := structuralRepo(t)
	const spaced = "src/my service.ts"
	const accented = "src/café.controller.ts"
	writeCommit(t, dir, spaced, srvSource, "a spaced path")
	writeCommit(t, dir, accented, ctrlSource, "an accented path")

	runRefreshIn(t, dir, "--quiet")

	s := openRepoStore(t, dir)
	defer s.Close()
	for _, f := range []string{spaced, accented} {
		if _, _, ok, err := s.GetStructuralHash("d", f); err != nil || !ok {
			t.Errorf("%q was never looked at (ok=%v, err=%v)", f, ok, err)
		}
	}
	// The accented controller's route made it into the graph.
	if _, err := s.GetNodeByKey("contract:endpoint:d:get:/a/items"); err != nil {
		t.Errorf("the route in the accented file is missing: %v", err)
	}
	if rev, err := s.LatestStructuralRevision("d"); err != nil {
		t.Fatalf("the phase must still complete: %v", err)
	} else if rev.GitAfterSHA == "" {
		t.Fatal("no structural pointer")
	}
}

// nodeStatus reports a node's status, or "" when the graph has no such node.
func nodeStatus(t *testing.T, dir, key string) string {
	t.Helper()
	s := openRepoStore(t, dir)
	defer s.Close()
	n, err := s.GetNodeByKey(key)
	if err != nil || n == nil {
		return ""
	}
	return n.Status
}

// drainSweep runs the bounded sweep until the structural pointer reaches HEAD.
func drainSweep(t *testing.T, dir string, batch string) {
	t.Helper()
	for i := 0; i < 12; i++ {
		runRefreshIn(t, dir, "--quiet", "--structural-batch="+batch)
		if structuralSHA(t, dir) == strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD")) {
			return
		}
	}
	t.Fatal("the sweep never drained")
}

// A sweep lists what HEAD HAS, so nothing in it can report a deletion. Across
// the several runs a bounded drain takes, a file extracted by an earlier batch
// can be deleted before the last one finishes — and it simply stops appearing.
// Its evidence and edges outlived it under a pointer that then said complete,
// and its content record could never be re-read nor dropped, so the next pack
// bump wedged on a backlog that could not drain.
func TestRefreshSweepRetiresAFileDeletedMidDrain(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "a controller")
	writeCommit(t, dir, "src/later.controller.ts", laterCtrlSource, "another controller")

	// One file per run, so the drain spans commits.
	runRefreshIn(t, dir, "--quiet", "--structural-batch=1")
	runRefreshIn(t, dir, "--quiet", "--structural-batch=1")
	func() {
		s := openRepoStore(t, dir)
		defer s.Close()
		if _, _, ok, _ := s.GetStructuralHash("d", "src/a.controller.ts"); !ok {
			t.Skip("the sweep order did not reach a.controller.ts first; nothing to test here")
		}
	}()
	if got := nodeStatus(t, dir, "contract:endpoint:d:get:/a/items"); got != "active" {
		t.Fatalf("fixture: the endpoint is %q, want active before the deletion", got)
	}

	gitRun(t, dir, "rm", "-q", "src/a.controller.ts")
	gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "drop a controller")
	drainSweep(t, dir, "1")

	if got := nodeStatus(t, dir, "contract:endpoint:d:get:/a/items"); got == "active" {
		t.Error("an endpoint of a file deleted mid-drain is still active")
	}
	s := openRepoStore(t, dir)
	defer s.Close()
	if _, _, ok, _ := s.GetStructuralHash("d", "src/a.controller.ts"); ok {
		t.Error("a content record survived the file; a pack bump would wedge on it forever")
	}
}

// supportedFilesAtHead narrows the sweep to the domain's scan globs so it
// cannot claim files another domain owns. The incremental path has to agree,
// or a file the sweep deliberately skipped is claimed by this domain the
// moment a later commit happens to touch it.
func TestRefreshIncrementalHonoursTheDomainScanScope(t *testing.T) {
	dir, _ := structuralRepo(t)

	// Out of scope: the manifest's domain "d" includes only src/**.
	writeCommit(t, dir, "other/x.controller.ts", ctrlSource, "out of scope, before the pointer")
	drainSweep(t, dir, "0")
	if got := nodeStatus(t, dir, "contract:endpoint:d:get:/a/items"); got != "" {
		t.Fatalf("fixture: the sweep claimed an out-of-scope file (%q)", got)
	}

	// A later commit touches another out-of-scope file: the incremental path
	// sees it in the diff and must skip it for the same reason.
	writeCommit(t, dir, "other/later.controller.ts", laterCtrlSource, "out of scope, after the pointer")
	runRefreshIn(t, dir, "--quiet")

	if got := nodeStatus(t, dir, "contract:endpoint:d:post:/later/things"); got != "" {
		t.Errorf("the incremental path claimed an out-of-scope file: endpoint is %q", got)
	}
	s := openRepoStore(t, dir)
	defer s.Close()
	if _, _, ok, _ := s.GetStructuralHash("d", "other/later.controller.ts"); ok {
		t.Error("an out-of-scope file got a content record from the incremental path")
	}
}

// Forgetting a branch-only content record only means "look at this file
// again", and the look is driven by the diff — which, after the pointer fell
// back, does not mention those files at all. Their structure came from a branch
// nobody merged, so leaving them active is the graph asserting code this
// history does not contain, under a pointer that says complete.
func TestRefreshRebaseFallbackRetiresBranchOnlyStructure(t *testing.T) {
	dir, scanned := structuralRepo(t)

	gitRun(t, dir, "checkout", "-q", "-b", "side")
	writeCommit(t, dir, "src/side.controller.ts", ctrlSource, "side: a controller")
	runRefreshIn(t, dir, "--quiet")
	if got := nodeStatus(t, dir, "contract:endpoint:d:get:/a/items"); got != "active" {
		t.Fatalf("fixture: the side branch's endpoint is %q, want active", got)
	}

	gitRun(t, dir, "checkout", "-q", "main")
	if strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD")) != scanned {
		t.Fatal("fixture: main should still be at the scanned commit")
	}
	writeCommit(t, dir, "src/main.controller.ts", laterCtrlSource, "main: a controller")

	runRefreshIn(t, dir, "--quiet")

	if got := nodeStatus(t, dir, "contract:endpoint:d:get:/a/items"); got == "active" {
		t.Error("structure from an unreachable branch is still active under a complete pointer")
	}
	if got := nodeStatus(t, dir, "contract:endpoint:d:post:/later/things"); got != "active" {
		t.Errorf("this branch's own commit was not structured: endpoint is %q", got)
	}
}

// An endpoint is an address, not a file, so its node has no file_path of its
// own — but the controller that declares it does. Attributing the creation
// evidence to that controller is what puts the endpoint inside the replacement
// contract: drop one route and keep the file, and that route has to stop being
// asserted while everything else the file still says survives. Without it the
// row was in no file's supersede scope, so the endpoint stayed active with
// valid evidence and impact kept reporting a route the code no longer serves.
func TestRefreshRetiresARouteDroppedFromASurvivingFile(t *testing.T) {
	const twoRoutes = `import { AService } from './a.service';

@Controller('a')
export class AController {
  constructor(private readonly a: AService) {}

  @Get('items')
  list() { return this.a.list(); }

  @Post('things')
  make() { return this.a.list(); }
}
`
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/a.controller.ts", twoRoutes, "a controller with two routes")
	runRefreshIn(t, dir, "--quiet")
	for _, key := range []string{"contract:endpoint:d:get:/a/items", "contract:endpoint:d:post:/a/things"} {
		if got := nodeStatus(t, dir, key); got != "active" {
			t.Fatalf("fixture: %s is %q, want active", key, got)
		}
	}

	// Same file, same class, one route removed.
	writeCommit(t, dir, "src/a.controller.ts", ctrlSource, "drop the POST route")
	runRefreshIn(t, dir, "--quiet")

	if got := nodeStatus(t, dir, "contract:endpoint:d:post:/a/things"); got == "active" {
		t.Error("a route the file no longer declares is still active")
	}
	if got := nodeStatus(t, dir, "contract:endpoint:d:get:/a/items"); got != "active" {
		t.Errorf("the route the file still declares was retired too: %q", got)
	}
	// And the file itself survives its own edit. Creation evidence for a node
	// that HAS a file used to be written once, on the run that minted it, so
	// re-extracting a CHANGED file did not hand it back and the replacement
	// contract retired a controller that was still right there in the source.
	if got := nodeStatus(t, dir, "code:controller:d:src/a-controller"); got != "active" {
		t.Errorf("the edited controller is %q, want active — its file still declares it", got)
	}
}

// A model IS its name, and the structural extractor says so in a `name` field —
// the natural shape for a declaration. Fact has no name field, so until the
// resolver learned to read it every prisma model and enum in a repo resolved to
// ONE nameless node: `data:model:<domain>:` with an empty qualified name,
// collecting every model's evidence. `data:enum:<domain>:` likewise.
func TestRefreshNamesPrismaModelsAndEnums(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/schema.prisma", `model Order {
  id     String      @id @default(cuid())
  status OrderStatus @default(OPEN)
  total  Int
}

enum OrderStatus {
  OPEN
  PAID
  VOID
}
`, "a prisma schema")
	runRefreshIn(t, dir, "--quiet")

	for _, key := range []string{"data:model:d:order", "data:enum:d:order-status"} {
		if got := nodeStatus(t, dir, key); got != "active" {
			t.Errorf("%s is %q, want active", key, got)
		}
	}
	// The nameless collector must not exist in either shape.
	for _, key := range []string{"data:model:d:", "data:enum:d:"} {
		if got := nodeStatus(t, dir, key); got != "" {
			t.Errorf("a nameless node %q exists with status %q", key, got)
		}
	}
}

// A decorator whose argument is an imported constant — @Processor(MAIL_QUEUE)
// rather than @Processor('mail') — yields a fact with no target. Carrying on
// named the topic `contract:topic:<domain>:`, which is not a topic: it is one
// nameless node every such decorator in the repo collapses into, wired by hard
// 0.95 edges, so one unresolved constant made every producer look like it
// publishes to the same place.
func TestRefreshRefusesANamelessTopic(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/mail.processor.ts", `import { Processor } from '@nestjs/bull';
import { MAIL_QUEUE } from './queues';

@Processor(MAIL_QUEUE)
export class MailProcessor {
  handle() { return 1; }
}
`, "a processor keyed by an imported constant")
	runRefreshIn(t, dir, "--quiet")

	for _, key := range []string{"contract:topic:d:", "contract:topic:d"} {
		if got := nodeStatus(t, dir, key); got != "" {
			t.Errorf("a nameless topic %q exists with status %q", key, got)
		}
	}
	// Nothing may point at it either: an edge to a node that was refused is a
	// dangling hard claim.
	s := openRepoStore(t, dir)
	defer s.Close()
	edges, err := s.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if strings.HasSuffix(e.ToNodeKey, "contract:topic:d:") || e.ToNodeID == 0 {
			t.Errorf("edge %q points at a refused node (to_id=%d)", e.EdgeKey, e.ToNodeID)
		}
	}
}

// The file index decides what a class name means. A role-scoped writer keeps a
// STANDING row per file, so scoping the index to one revision showed it only
// the files THIS run looked at — and an import of a file structured in an
// earlier commit fell through to class-name inference and minted a second,
// mistyped node for a path that already had one.
func TestRefreshImportAcrossCommitsDoesNotMintATwin(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/app.module.ts", `import { Module } from '@nestjs/common';
import { AService } from './a.service';

@Module({ providers: [AService] })
export class AppModule {}
`, "a module")
	runRefreshIn(t, dir, "--quiet")
	if got := nodeStatus(t, dir, "code:module:d:src/app-module"); got != "active" {
		t.Fatalf("fixture: the module is %q, want active", got)
	}

	// A LATER commit imports it. The module's own row is not in this run's
	// revision; only a standing lookup finds it.
	writeCommit(t, dir, "src/boot.ts", `import { AppModule } from './app.module';

export function boot() { return AppModule; }
`, "something imports the module")
	runRefreshIn(t, dir, "--quiet")

	if got := nodeStatus(t, dir, "code:provider:d:src/app-module"); got != "" {
		t.Errorf("a mistyped twin code:provider:d:src/app-module exists (%q) beside the module", got)
	}
	if got := nodeStatus(t, dir, "code:module:d:src/app-module"); got != "active" {
		t.Errorf("the real module node is %q, want active", got)
	}

	// One path, one node.
	s := openRepoStore(t, dir)
	defer s.Close()
	nodes, err := s.ListNodes(store.NodeFilter{Domain: "d"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, n := range nodes {
		if n.FilePath == "" || n.Layer != "code" || n.Status != "active" {
			continue
		}
		if prev, dup := seen[n.FilePath]; dup {
			t.Errorf("two active code nodes for %s: %s and %s", n.FilePath, prev, n.NodeKey)
		}
		seen[n.FilePath] = n.NodeKey
	}
}

// A code node's key embeds the type the file turned out to be, so a file that
// gains a @Controller where it had @Injectable asks for a new key for a path
// that is already spoken for. Minting one left TWO active nodes for one file:
// inbound edges kept pointing at the old type while the new facts landed on the
// new one, and impact answered about whichever half the caller reached.
func TestRefreshMovesANodeWhenItsFileChangesType(t *testing.T) {
	dir, _ := structuralRepo(t)
	writeCommit(t, dir, "src/x.service.ts", `import { Injectable } from '@nestjs/common';

@Injectable()
export class XService {
  list() { return []; }
}
`, "a service")
	writeCommit(t, dir, "src/y.controller.ts", `import { XService } from './x.service';

@Controller('y')
export class YController {
  constructor(private readonly x: XService) {}

  @Get('items')
  list() { return this.x.list(); }
}
`, "something injects it")
	runRefreshIn(t, dir, "--quiet")
	if got := nodeStatus(t, dir, "code:provider:d:src/x-service"); got != "active" {
		t.Fatalf("fixture: the service is %q, want active", got)
	}

	// The same file becomes a controller.
	writeCommit(t, dir, "src/x.service.ts", `@Controller('x')
export class XService {
  @Get('items')
  list() { return []; }
}
`, "the service becomes a controller")
	runRefreshIn(t, dir, "--quiet")

	s := openRepoStore(t, dir)
	defer s.Close()
	nodes, err := s.ListNodes(store.NodeFilter{Domain: "d", Layer: "code", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	var forFile []string
	for _, n := range nodes {
		if n.FilePath == "src/x.service.ts" {
			forFile = append(forFile, n.NodeKey+" ("+n.NodeType+")")
		}
	}
	if len(forFile) != 1 {
		t.Errorf("one file, %d active code nodes: %v", len(forFile), forFile)
	}
	// The row must not contradict its own key.
	for _, n := range nodes {
		if n.FilePath != "src/x.service.ts" {
			continue
		}
		parts := strings.SplitN(n.NodeKey, ":", 4)
		if len(parts) == 4 && parts[1] != n.NodeType {
			t.Errorf("node %s says node_type=%q", n.NodeKey, n.NodeType)
		}
	}
}

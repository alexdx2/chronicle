package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
)

// seedGraphUpdateStore opens a fresh store at dbPath and gives it one active
// node — runGraphUpdate's own record mapping is exercised in depth by the
// snapshot package's own tests; this only needs enough content to tell
// "empty" from "has one more node" apart across calls.
func seedGraphUpdateStore(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:a", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "A", FilePath: "svc/a.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("seed UpsertNode: %v", err)
	}
	return s
}

// TestRunGraphUpdate_WriteThenCheck drives runGraphUpdate directly (no
// cobra, no process) through the exact sequence the brief's CLI test
// describes: update writes seq 1; a --check right after is clean (exit 0);
// mutating the store and checking again is not (exit 1); a second update
// picks up the change at seq 2; --check is clean again.
func TestRunGraphUpdate_WriteThenCheck(t *testing.T) {
	dir := t.TempDir()
	s := seedGraphUpdateStore(t, filepath.Join(dir, "chronicle.db"))
	defer s.Close()

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	graphPath := filepath.Join(dir, "graph.jsonl")

	// No graph.jsonl yet: --check must report a mismatch and write nothing.
	if _, mismatch, err := runGraphUpdate(s, reg, graphPath, true); err != nil {
		t.Fatalf("runGraphUpdate(check, no file): %v", err)
	} else if !mismatch {
		t.Fatal("check on a project with no graph.jsonl yet must report a mismatch")
	}
	if _, err := os.Stat(graphPath); err == nil {
		t.Fatal("--check must not create graph.jsonl")
	}

	// update: writes the file at seq 1, one node added relative to nothing.
	result, mismatch, err := runGraphUpdate(s, reg, graphPath, false)
	if err != nil {
		t.Fatalf("runGraphUpdate(update): %v", err)
	}
	if mismatch {
		t.Fatal("update itself must never report a mismatch")
	}
	if result.Seq != 1 {
		t.Fatalf("first update: Seq = %d, want 1", result.Seq)
	}
	if result.Added != 1 || result.Removed != 0 || result.Changed != 0 {
		t.Fatalf("first update summary = %+v, want Added=1 Removed=0 Changed=0", result)
	}
	// Pins internal/cli/graph.go's ordering: runGraphUpdate reads
	// next.Header.Counts only after calling Encode, which is what actually
	// fills it in (see snapshot.TestFromStore_EncodeFillsHeaderCounts for
	// the underlying invariant this depends on).
	if result.Counts.Node != 1 || result.Counts.Edge != 0 {
		t.Fatalf("first update Counts = %+v, want Node=1 Edge=0", result.Counts)
	}
	written, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatalf("read graph.jsonl: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("update wrote an empty graph.jsonl")
	}

	// check again, store unchanged: code 0 (no mismatch), file untouched.
	result2, mismatch2, err := runGraphUpdate(s, reg, graphPath, true)
	if err != nil {
		t.Fatalf("runGraphUpdate(check, unchanged): %v", err)
	}
	if mismatch2 {
		t.Fatal("check must report no mismatch when the store hasn't changed since update")
	}
	if result2.Seq != 1 {
		t.Fatalf("check on an unchanged store: Seq = %d, want 1 (must not bump seq)", result2.Seq)
	}
	if after, err := os.ReadFile(graphPath); err != nil {
		t.Fatalf("read graph.jsonl after check: %v", err)
	} else if !bytes.Equal(written, after) {
		t.Fatal("--check modified graph.jsonl")
	}

	// Mutate the store, then check again: code 1 (mismatch), file still untouched.
	if _, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:b", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "B", FilePath: "svc/b.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("UpsertNode b: %v", err)
	}
	_, mismatch3, err := runGraphUpdate(s, reg, graphPath, true)
	if err != nil {
		t.Fatalf("runGraphUpdate(check, after mutation): %v", err)
	}
	if !mismatch3 {
		t.Fatal("check must report a mismatch after the store changed")
	}
	if stillOld, err := os.ReadFile(graphPath); err != nil {
		t.Fatalf("read graph.jsonl after second check: %v", err)
	} else if !bytes.Equal(written, stillOld) {
		t.Fatal("--check wrote to graph.jsonl")
	}

	// A real update now bumps seq and picks up the new node.
	result3, _, err := runGraphUpdate(s, reg, graphPath, false)
	if err != nil {
		t.Fatalf("runGraphUpdate(second update): %v", err)
	}
	if result3.Seq != 2 {
		t.Fatalf("second update: Seq = %d, want 2", result3.Seq)
	}
	if result3.Added != 1 || result3.Removed != 0 || result3.Changed != 0 {
		t.Fatalf("second update summary = %+v, want Added=1 Removed=0 Changed=0", result3)
	}
	if result3.Counts.Node != 2 || result3.Counts.Edge != 0 {
		t.Fatalf("second update Counts = %+v, want Node=2 Edge=0", result3.Counts)
	}

	// Final check is clean again.
	if _, mismatch4, err := runGraphUpdate(s, reg, graphPath, true); err != nil {
		t.Fatalf("runGraphUpdate(final check): %v", err)
	} else if mismatch4 {
		t.Fatal("check must be clean immediately after an update")
	}
}

// TestNewGraphUpdateCmd_RegisteredUnderGraph is a thin wiring smoke test:
// `chronicle graph update` must resolve through the real root command and
// declare --check. It never calls Execute(), so it can't trip the
// mismatch-error exit path.
func TestNewGraphUpdateCmd_RegisteredUnderGraph(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"graph", "update"})
	if err != nil {
		t.Fatalf("chronicle graph update not registered: %v", err)
	}
	if f := cmd.Flags().Lookup("check"); f == nil {
		t.Fatal("graph update is missing its --check flag")
	}
}

// writeGraphFixture writes a valid, decodable .depbot/graph.jsonl (real
// Encode, not a hand-rolled JSON literal) into dir, for runGraphStatus/
// runGraphShow tests that need a real committed graph to resolve.
func writeGraphFixture(t *testing.T, dir string, seq int, domains []string, coverage []snapshot.Coverage) {
	t.Helper()
	snap := &snapshot.Snapshot{
		Header: snapshot.Header{
			Kind: "header", Format: snapshot.Format, Version: snapshot.Version,
			Seq: seq, FactSchema: snapshot.FactSchema, Domains: domains,
		},
		Coverage: coverage,
	}
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, snap); err != nil {
		t.Fatalf("snapshot.Encode: %v", err)
	}
	full := filepath.Join(dir, ".depbot", "graph.jsonl")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePlainFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitAllCLI stages everything (including deletions) and commits, the same
// inline "-c user.email=... -c user.name=..." shape refresh_test.go's own
// commit closure uses in this package, and returns the new commit's SHA.
//
// gitCapture (hook_test.go) does not trim its output — every one of its
// callers that wants a bare SHA trims it themselves (see refresh_test.go's
// own commit closure) — so this does too; skipping it would leave a trailing
// "\n" riding along inside the SHA, silently turning it into an invalid git
// revision the moment it is used as one.
func commitAllCLI(t *testing.T, dir, msg string) string {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", msg)
	return strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD"))
}

// blobAtHEAD returns the (trimmed) git blob id of path at HEAD — see
// commitAllCLI's comment on why gitCapture's output must be trimmed at every
// call site.
func blobAtHEAD(t *testing.T, dir, path string) string {
	t.Helper()
	return strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD:"+path))
}

// TestRunGraphStatus_AtRevision drives runGraphStatus directly (no cobra,
// no --working-tree) against a real temp git repo: a file covered at the
// graph commit changes afterward, so status at the later revision must
// report it Stale, resolved against the graph commit (not the later one).
func TestRunGraphStatus_AtRevision(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")

	writePlainFile(t, dir, "svc/a.ts", "v1")
	commitAllCLI(t, dir, "content")
	blob := blobAtHEAD(t, dir, "svc/a.ts")

	writeGraphFixture(t, dir, 1, []string{"d"}, []snapshot.Coverage{
		{Kind: "coverage", Path: "svc/a.ts", Blob: blob, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
	})
	graphSHA := commitAllCLI(t, dir, "graph seq1")

	writePlainFile(t, dir, "svc/a.ts", "v2")
	rev := commitAllCLI(t, dir, "change")

	man := &manifest.Manifest{Domains: []manifest.DomainEntry{
		{Key: "d", Name: "d", Scan: manifest.ScanConfig{Include: []string{"svc/**"}}},
	}}

	result, err := runGraphStatus(dir, man, rev, false)
	if err != nil {
		t.Fatalf("runGraphStatus: %v", err)
	}
	if result.Graph.Commit != graphSHA {
		t.Errorf("Graph.Commit = %s, want %s", result.Graph.Commit, graphSHA)
	}
	if result.Graph.Seq != 1 {
		t.Errorf("Graph.Seq = %d, want 1", result.Graph.Seq)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "svc/a.ts" || result.Files[0].State != snapshot.Stale {
		t.Fatalf("Files = %+v, want exactly one Stale svc/a.ts", result.Files)
	}
}

// TestRunGraphStatus_WorkingTree drives runGraphStatus with workingTree=true:
// an edit sitting on disk, never committed, must be seen immediately.
func TestRunGraphStatus_WorkingTree(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")

	writePlainFile(t, dir, "svc/a.ts", "v1")
	commitAllCLI(t, dir, "content")
	blob := blobAtHEAD(t, dir, "svc/a.ts")

	writeGraphFixture(t, dir, 1, []string{"d"}, []snapshot.Coverage{
		{Kind: "coverage", Path: "svc/a.ts", Blob: blob, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
	})
	graphSHA := commitAllCLI(t, dir, "graph seq1")

	// Uncommitted edit.
	writePlainFile(t, dir, "svc/a.ts", "v2-uncommitted")

	man := &manifest.Manifest{Domains: []manifest.DomainEntry{
		{Key: "d", Name: "d", Scan: manifest.ScanConfig{Include: []string{"svc/**"}}},
	}}

	result, err := runGraphStatus(dir, man, "", true)
	if err != nil {
		t.Fatalf("runGraphStatus: %v", err)
	}
	if result.Rev != "" {
		t.Errorf("Rev = %q, want \"\"", result.Rev)
	}
	if result.Graph.Commit != graphSHA {
		t.Errorf("Graph.Commit = %s, want %s (HEAD, unmoved)", result.Graph.Commit, graphSHA)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "svc/a.ts" || result.Files[0].State != snapshot.Stale {
		t.Fatalf("Files = %+v, want exactly one Stale svc/a.ts (uncommitted disk edit)", result.Files)
	}
}

// TestRunGraphShow_ResolvesVersion drives runGraphShow directly: it must
// resolve the same nearest-ancestor graph commit runGraphStatus does, and
// summarize its header (seq, counts, domains) plus its own commit/blob.
func TestRunGraphShow_ResolvesVersion(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")

	writePlainFile(t, dir, "svc/a.ts", "v1")
	commitAllCLI(t, dir, "content")
	blob := blobAtHEAD(t, dir, "svc/a.ts")

	writeGraphFixture(t, dir, 1, []string{"d"}, []snapshot.Coverage{
		{Kind: "coverage", Path: "svc/a.ts", Blob: blob, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
	})
	graphSHA := commitAllCLI(t, dir, "graph seq1")
	wantGraphBlob := strings.TrimSpace(gitCapture(t, dir, "rev-parse", graphSHA+":.depbot/graph.jsonl"))

	writePlainFile(t, dir, "code.txt", "v2")
	rev := commitAllCLI(t, dir, "code change")

	result, err := runGraphShow(dir, rev)
	if err != nil {
		t.Fatalf("runGraphShow: %v", err)
	}
	if result.Commit != graphSHA {
		t.Errorf("Commit = %s, want %s", result.Commit, graphSHA)
	}
	if result.Blob != wantGraphBlob {
		t.Errorf("Blob = %s, want %s", result.Blob, wantGraphBlob)
	}
	if result.Seq != 1 {
		t.Errorf("Seq = %d, want 1", result.Seq)
	}
	if result.Counts.Coverage != 1 {
		t.Errorf("Counts.Coverage = %d, want 1", result.Counts.Coverage)
	}
	if len(result.Domains) != 1 || result.Domains[0] != "d" {
		t.Errorf("Domains = %v, want [d]", result.Domains)
	}
}

// TestRunGraphShow_NoVersion_ErrNoVersion: a repo whose history never
// committed .depbot/graph.jsonl must surface snapshot.ErrNoVersion, so the
// CLI layer can turn it into "no graph version in history of <rev>" and
// exit 1 instead of a stack trace.
func TestRunGraphShow_NoVersion_ErrNoVersion(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writePlainFile(t, dir, "readme.txt", "hello")
	commitAllCLI(t, dir, "only commit")

	_, err := runGraphShow(dir, "HEAD")
	if !errors.Is(err, snapshot.ErrNoVersion) {
		t.Fatalf("runGraphShow: err = %v, want ErrNoVersion", err)
	}
}

// TestNewGraphStatusCmd_RegisteredUnderGraph and
// TestNewGraphShowCmd_RegisteredUnderGraph are thin wiring smoke tests, the
// same shape as TestNewGraphUpdateCmd_RegisteredUnderGraph: they never call
// Execute(), so they can't trip either command's error-exit paths.
func TestNewGraphStatusCmd_RegisteredUnderGraph(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"graph", "status"})
	if err != nil {
		t.Fatalf("chronicle graph status not registered: %v", err)
	}
	if f := cmd.Flags().Lookup("working-tree"); f == nil {
		t.Fatal("graph status is missing its --working-tree flag")
	}
	if f := cmd.Flags().Lookup("json"); f == nil {
		t.Fatal("graph status is missing its --json flag")
	}
}

func TestNewGraphShowCmd_RegisteredUnderGraph(t *testing.T) {
	root := NewRootCmd()
	if _, _, err := root.Find([]string{"graph", "show"}); err != nil {
		t.Fatalf("chronicle graph show not registered: %v", err)
	}
}

// runGraphStatusCmdIn chdirs into dir, runs the REAL `chronicle graph status`
// cobra command (not runGraphStatus directly) with args, and returns
// everything it printed to stdout. Same recipe as refresh_test.go's
// runRefreshIn: status is a leaf command with no store to open, so
// PersistentPreRun (which lives on the root command and normally resolves
// paths from --project/--chronicle-dir) never runs when the command is
// constructed and executed standalone — paths has to be primed by hand,
// exactly as it would be for a real invocation from cwd with no flags.
func runGraphStatusCmdIn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	resetWorktreeGlobals(t, cwd) // clean baseline, independent of prior tests' state
	defer resetWorktreeGlobals(t, cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	paths.SetGitDir("")
	defer paths.SetGitDir("")

	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatal(perr)
	}
	realStdout := os.Stdout
	os.Stdout = w

	cmd := newGraphStatusCmd()
	cmd.SetArgs(args)
	runErr := cmd.Execute()

	w.Close()
	os.Stdout = realStdout
	out, _ := io.ReadAll(r)
	return string(out), runErr
}

// TestGraphStatusCmd_ResolvesManifestPathEndToEnd exercises the actual cobra
// RunE, not runGraphStatus directly. This is a regression test: `chronicle
// graph status` opens no store, so — unlike `update`, which gets manifestPath
// populated as a side effect of openGraph — its RunE originally left
// manifestPath at its zero value ("") and failed every real invocation with
// "load manifest: reading manifest: open : no such file or directory" until
// newGraphStatusCmd's RunE was given its own resolveWorktreeGraph/
// resolveDefaults call (the same pair hookAdvisory uses in hook.go). Found by
// manually running the built binary end-to-end, not by the runGraphStatus
// unit tests above — they pass a *manifest.Manifest directly and so never
// touch manifestPath at all.
func TestGraphStatusCmd_ResolvesManifestPathEndToEnd(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")

	writePlainFile(t, dir, "svc/a.ts", "v1")
	commitAllCLI(t, dir, "content")
	blob := blobAtHEAD(t, dir, "svc/a.ts")

	writeGraphFixture(t, dir, 1, []string{"d"}, []snapshot.Coverage{
		{Kind: "coverage", Path: "svc/a.ts", Blob: blob, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
	})
	commitAllCLI(t, dir, "graph seq1")

	writePlainFile(t, dir, ".depbot/chronicle.domain.yaml", "domains:\n  - name: d\n    scan:\n      include:\n        - \"svc/**\"\n")

	out, err := runGraphStatusCmdIn(t, dir, "--json")
	if err != nil {
		t.Fatalf("graph status --json: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, `"fresh"`) {
		t.Fatalf("graph status --json output = %s, want it to report svc/a.ts fresh", out)
	}
}

package review

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// --- shared test git helpers -------------------------------------------
//
// These mirror snapshot/freshness_test.go's own initGitRepoT/runGitT
// pattern (same repo, closest sibling package exercising the exact same
// git-touching code this task builds on) — duplicated rather than shared
// because this task does not touch snapshot's test file, and the two
// packages must not depend on each other's _test.go files.

func initGitRepoT(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "init", "-q", "-b", "main")
	return dir
}

func runGitT(t *testing.T, dir string, args ...string) {
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

func captureGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFileT(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitAllT stages everything on disk (including deletions) and commits,
// returning the new commit's full SHA.
func commitAllT(t *testing.T, dir, msg string) string {
	t.Helper()
	runGitT(t, dir, "add", "-A")
	runGitT(t, dir, "commit", "-q", "-m", msg)
	return captureGitT(t, dir, "rev-parse", "HEAD")
}

// encodeSnapshotT runs snap through the real Encode, so a test fixture is
// exactly what production code would write (canonicalized, Counts filled
// in) rather than a hand-rolled JSON literal that could drift from the real
// format.
func encodeSnapshotT(t *testing.T, snap *snapshot.Snapshot) string {
	t.Helper()
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, snap); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.String()
}

// writeManifestT writes a minimal one-domain manifest ("d", matching
// everything) at the RepoRoot/.depbot/chronicle.domain.yaml convention
// SnapshotSource.Load resolves the manifest from — see graph.go's
// loadManifest.
func writeManifestT(t *testing.T, dir string) {
	t.Helper()
	writeFileT(t, dir, ".depbot/chronicle.domain.yaml", ""+
		"domains:\n"+
		"  - name: d\n"+
		"    scan:\n"+
		"      include:\n"+
		"        - \"**\"\n")
}

// --- SnapshotSource.Load --------------------------------------------------

// TestSnapshotSource_Load: a repo with a graph commit followed by a code
// commit — Load(head) must resolve the graph version, load it into a real
// *graph.Graph, and compute freshness anchored at head.
func TestSnapshotSource_Load(t *testing.T) {
	dir := initGitRepoT(t)
	writeManifestT(t, dir)

	writeFileT(t, dir, "file.ts", "v1")
	graphSnap := &snapshot.Snapshot{
		Header: snapshot.Header{
			Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema,
			Domains: []string{"d"},
		},
		Nodes: []snapshot.Node{
			{Kind: "node", ID: "contract:endpoint:d:foo", Layer: "contract", Type: "endpoint", Domain: "d", Name: "foo", Status: "active"},
		},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, graphSnap))
	commitAllT(t, dir, "graph seq1")

	writeFileT(t, dir, "file.ts", "v2")
	headSHA := commitAllT(t, dir, "code change")

	src := SnapshotSource{RepoRoot: dir, Rev: headSHA}
	vg, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if vg.Close == nil {
		t.Fatal("Close is nil, want a non-nil closer (point 7: must never be nil)")
	}
	defer vg.Close()

	if vg.Origin != "snapshot" {
		t.Errorf("Origin = %q, want %q", vg.Origin, "snapshot")
	}
	if vg.Graph == nil {
		t.Fatal("Graph is nil, want a loaded *graph.Graph")
	}
	if vg.Export == nil {
		t.Fatal("Export is nil, want the decoded snapshot")
	}
	if vg.Freshness.Rev != headSHA {
		t.Errorf("Freshness.Rev = %q, want %q (head)", vg.Freshness.Rev, headSHA)
	}
	if vg.Version.Seq != 1 {
		t.Errorf("Version.Seq = %d, want 1", vg.Version.Seq)
	}
}

// TestSnapshotSource_Load_NoVersion: a revision whose history never
// committed .depbot/graph.jsonl must report Origin:"missing" with a nil
// error — ErrNoVersion is an expected state, not a failure — and Close must
// still be safely callable (point 7).
func TestSnapshotSource_Load_NoVersion(t *testing.T) {
	dir := initGitRepoT(t)
	writeFileT(t, dir, "readme.txt", "hello\n")
	commitAllT(t, dir, "only commit")

	src := SnapshotSource{RepoRoot: dir, Rev: "HEAD"}
	vg, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: err = %v, want nil (ErrNoVersion is a state, not a failure)", err)
	}
	if vg.Origin != "missing" {
		t.Errorf("Origin = %q, want %q", vg.Origin, "missing")
	}
	if vg.Graph != nil {
		t.Errorf("Graph = %v, want nil", vg.Graph)
	}
	if vg.Export != nil {
		t.Errorf("Export = %v, want nil", vg.Export)
	}
	if vg.Close == nil {
		t.Fatal("Close is nil, want a non-nil no-op closer (point 7)")
	}
	if err := vg.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// TestSnapshotSource_Load_WorkingTree: Rev=="" reads .depbot/graph.jsonl
// straight off disk, not from HEAD's git tree — a graph file edited but not
// committed must be what Load sees, Version.Commit must be "" (no
// ResolveVersion call), and freshness must be computed against the actual
// files on disk (a source file edited but not committed must show up as
// Stale, exactly as it would be missed if freshness compared against HEAD's
// tree instead).
func TestSnapshotSource_Load_WorkingTree(t *testing.T) {
	dir := initGitRepoT(t)
	writeManifestT(t, dir)

	writeFileT(t, dir, "file.ts", "v1")
	commitAllT(t, dir, "seed file.ts")
	blob1 := captureGitT(t, dir, "rev-parse", "HEAD:file.ts")

	committedSnap := &snapshot.Snapshot{
		Header: snapshot.Header{
			Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema,
			Domains: []string{"d"},
		},
		Coverage: []snapshot.Coverage{
			{Kind: "coverage", Path: "file.ts", Blob: blob1, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
		},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, committedSnap))
	commitAllT(t, dir, "graph seq1")

	// Uncommitted edits: the graph file itself (seq bump + an extra node —
	// proves Load reads the DISK bytes, not the committed ones), and a
	// source file the committed graph's Coverage already claims (proves
	// freshness reads the disk blob, not HEAD's tree blob).
	diskSnap := &snapshot.Snapshot{
		Header: snapshot.Header{
			Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 2, FactSchema: snapshot.FactSchema,
			Domains: []string{"d"},
		},
		Nodes: []snapshot.Node{
			{Kind: "node", ID: "code:module:d:new", Layer: "code", Type: "module", Domain: "d", Name: "new", Status: "active"},
		},
		Coverage: committedSnap.Coverage,
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, diskSnap))
	writeFileT(t, dir, "file.ts", "v2-uncommitted")

	wantBlob, err := gitutil.FileBlobSHA(dir, snapshot.Path)
	if err != nil {
		t.Fatalf("FileBlobSHA: %v", err)
	}

	src := SnapshotSource{RepoRoot: dir, Rev: ""}
	vg, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer vg.Close()

	if vg.Origin != "snapshot" {
		t.Errorf("Origin = %q, want %q", vg.Origin, "snapshot")
	}
	if vg.Version.Commit != "" {
		t.Errorf("Version.Commit = %q, want \"\" (ResolveVersion must not be called for the working tree)", vg.Version.Commit)
	}
	if vg.Version.Blob != wantBlob {
		t.Errorf("Version.Blob = %q, want %q (the ON-DISK file's blob)", vg.Version.Blob, wantBlob)
	}
	if vg.Version.Seq != 2 {
		t.Errorf("Version.Seq = %d, want 2 (the ON-DISK snapshot's seq, not the committed seq 1)", vg.Version.Seq)
	}
	if vg.Export == nil || len(vg.Export.Nodes) != 1 || vg.Export.Nodes[0].ID != "code:module:d:new" {
		t.Fatalf("Export = %+v, want the ON-DISK snapshot's one node (code:module:d:new)", vg.Export)
	}

	var fileState snapshot.FileState
	found := false
	for _, ff := range vg.Freshness.Files {
		if ff.Path == "file.ts" {
			fileState = ff.State
			found = true
		}
	}
	if !found {
		t.Fatalf("Freshness.Files = %+v, want an entry for file.ts", vg.Freshness.Files)
	}
	if fileState != snapshot.Stale {
		t.Errorf("file.ts freshness state = %s, want %s (uncommitted edit must be visible: freshness reads disk, not HEAD's tree)", fileState, snapshot.Stale)
	}
}

// TestSnapshotSource_Load_WorkingTree_NoCommits: a repository with NO
// commits at all (only `git init`) still has a real, on-disk working-tree
// graph. Before fix-round-1, this path would have failed inside
// ComputeFreshness's internal ChangedSince computation, which unconditionally
// tried `git diff <empty-Commit>..<empty-rev>` — an argument error with
// nothing to do with whether HEAD exists. Now that ComputeFreshness itself
// recognizes Version.Commit=="" and skips that computation, Load must
// succeed even here — this is the "root cause fix" resolving the exact
// limitation task-9-report.md originally documented as unfixed.
func TestSnapshotSource_Load_WorkingTree_NoCommits(t *testing.T) {
	dir := initGitRepoT(t) // git init only — no commits anywhere
	writeManifestT(t, dir)

	snap := &snapshot.Snapshot{
		Header: snapshot.Header{
			Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema,
			Domains: []string{"d"},
		},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, snap))

	src := SnapshotSource{RepoRoot: dir, Rev: ""}
	vg, err := src.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v, want success (must not depend on any commit existing)", err)
	}
	defer vg.Close()

	if vg.Origin != "snapshot" {
		t.Errorf("Origin = %q, want %q", vg.Origin, "snapshot")
	}
	if len(vg.Freshness.ChangedSince) != 0 {
		t.Errorf("ChangedSince = %v, want empty (no commit anywhere to diff against)", vg.Freshness.ChangedSince)
	}
}

// TestSnapshotSource_Load_CanceledContext: an already-canceled context must
// short-circuit Load before it does any real work (point 6 — ctx.Err()
// checked once at the top).
func TestSnapshotSource_Load_CanceledContext(t *testing.T) {
	dir := initGitRepoT(t)
	writeFileT(t, dir, "readme.txt", "hello\n")
	commitAllT(t, dir, "only commit")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := SnapshotSource{RepoRoot: dir, Rev: "HEAD"}
	_, err := src.Load(ctx)
	if err == nil {
		t.Fatal("Load: err = nil, want the canceled context's error")
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("Load: err = %v, want it to wrap context.Canceled", err)
	}
}

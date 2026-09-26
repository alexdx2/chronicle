package snapshot

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
)

// --- shared test git helpers -----------------------------------------------
//
// These mirror gitdiff_test.go's own initRepo/run pattern (same repo, closest
// sibling package: small git-touching code, throwaway temp repos, explicit
// author/committer env so a commit succeeds with no git config present).
// They live here, not in gitdiff_test.go, because this task does not touch
// gitdiff.go or its tests.

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
func encodeSnapshotT(t *testing.T, snap *Snapshot) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, snap); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.String()
}

// singleDomainManifest builds a one-domain manifest directly (no YAML),
// enough for DomainForFile to attribute scan scope by glob.
func singleDomainManifest(key string, include ...string) *manifest.Manifest {
	return &manifest.Manifest{
		Domains: []manifest.DomainEntry{
			{Key: key, Name: key, Scan: manifest.ScanConfig{Include: include}},
		},
	}
}

// --- ResolveVersion ----------------------------------------------------

// TestResolveVersion_NearestAncestor: commits A(no graph)-B(graph seq1)-
// C(code)-D(code) — ResolveVersion(D) must resolve to B (the nearest
// ancestor, inclusive, that last touched .depbot/graph.jsonl), with Seq 1
// and the blob git itself recorded for the file at B.
func TestResolveVersion_NearestAncestor(t *testing.T) {
	dir := initGitRepoT(t)

	writeFileT(t, dir, "readme.txt", "hello\n")
	commitAllT(t, dir, "A: no graph")

	graphSnap := &Snapshot{Header: Header{Kind: "header", Format: Format, Version: Version, Seq: 1, FactSchema: FactSchema}}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, graphSnap))
	bSHA := commitAllT(t, dir, "B: graph seq1")

	writeFileT(t, dir, "code.txt", "v1\n")
	commitAllT(t, dir, "C: code")

	writeFileT(t, dir, "code.txt", "v2\n")
	dSHA := commitAllT(t, dir, "D: code")

	v, err := ResolveVersion(dir, dSHA)
	if err != nil {
		t.Fatalf("ResolveVersion: %v", err)
	}
	if v.Commit != bSHA {
		t.Errorf("Commit = %s, want %s (B)", v.Commit, bSHA)
	}
	if v.Seq != 1 {
		t.Errorf("Seq = %d, want 1", v.Seq)
	}
	wantBlob := captureGitT(t, dir, "rev-parse", bSHA+":.depbot/graph.jsonl")
	if v.Blob != wantBlob {
		t.Errorf("Blob = %s, want %s (git's own blob of the file at B)", v.Blob, wantBlob)
	}
}

// TestResolveVersion_NoGraph: a repository whose history never once
// committed .depbot/graph.jsonl must report ErrNoVersion, not a generic
// error and not a panic.
func TestResolveVersion_NoGraph(t *testing.T) {
	dir := initGitRepoT(t)
	writeFileT(t, dir, "readme.txt", "hello\n")
	commitAllT(t, dir, "only commit")

	_, err := ResolveVersion(dir, "HEAD")
	if !errors.Is(err, ErrNoVersion) {
		t.Fatalf("ResolveVersion: err = %v, want ErrNoVersion", err)
	}
}

// --- ComputeFreshness ----------------------------------------------------

// TestComputeFreshness_AllFive drives all five FileStates off one domain's
// five scope files, per the brief: file1 unscanned (added after the graph
// commit, never covered), file2 stale (covered, but its blob at Rev
// differs), file3 version_stale (same blob as covered, but the snapshot
// shows a newer version of the same extractor used on file5), file4 deleted
// (covered, but gone from Rev's tree), file5 fresh (matches, and already at
// the newest extractor version in this snapshot).
func TestComputeFreshness_AllFive(t *testing.T) {
	dir := initGitRepoT(t)

	writeFileT(t, dir, "file2.ts", "v1")
	writeFileT(t, dir, "file3.ts", "same")
	writeFileT(t, dir, "file4.ts", "gone")
	writeFileT(t, dir, "file5.ts", "stay")
	commitAllT(t, dir, "content")

	blob2 := captureGitT(t, dir, "rev-parse", "HEAD:file2.ts")
	blob3 := captureGitT(t, dir, "rev-parse", "HEAD:file3.ts")
	blob4 := captureGitT(t, dir, "rev-parse", "HEAD:file4.ts")
	blob5 := captureGitT(t, dir, "rev-parse", "HEAD:file5.ts")

	snap := &Snapshot{
		Header: Header{
			Kind: "header", Format: Format, Version: Version, Seq: 1, FactSchema: FactSchema,
			Domains: []string{"d"},
		},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "file2.ts", Blob: blob2, Extractors: []ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
			{Kind: "coverage", Path: "file3.ts", Blob: blob3, Extractors: []ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
			{Kind: "coverage", Path: "file4.ts", Blob: blob4, Extractors: []ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
			{Kind: "coverage", Path: "file5.ts", Blob: blob5, Extractors: []ExtractorRun{{ID: "fact-schema", Version: "fact-schema@2", Status: "ok"}}},
		},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, snap))
	commitAllT(t, dir, "graph seq1")

	// file1 added (never covered), file2 changed, file3 untouched, file4
	// removed, file5 untouched.
	writeFileT(t, dir, "file1.ts", "new")
	writeFileT(t, dir, "file2.ts", "v2")
	runGitT(t, dir, "rm", "-q", "file4.ts")
	rev := commitAllT(t, dir, "rev")

	man := singleDomainManifest("d", "file*.ts")

	graphVer, err := ResolveVersion(dir, rev)
	if err != nil {
		t.Fatalf("ResolveVersion: %v", err)
	}
	fr, err := ComputeFreshness(dir, snap, graphVer, man, rev)
	if err != nil {
		t.Fatalf("ComputeFreshness: %v", err)
	}

	want := map[string]FileState{
		"file1.ts": Unscanned,
		"file2.ts": Stale,
		"file3.ts": VersionStale,
		"file4.ts": Deleted,
		"file5.ts": Fresh,
	}
	if len(fr.Files) != len(want) {
		t.Fatalf("Files = %+v, want exactly %d entries matching %v", fr.Files, len(want), want)
	}
	for _, ff := range fr.Files {
		wantState, ok := want[ff.Path]
		if !ok {
			t.Errorf("unexpected file in report: %+v", ff)
			continue
		}
		if ff.State != wantState {
			t.Errorf("%s: state = %s, want %s", ff.Path, ff.State, wantState)
		}
	}
	for i := 1; i < len(fr.Files); i++ {
		if fr.Files[i-1].Path >= fr.Files[i].Path {
			t.Fatalf("Files not sorted by Path: %+v", fr.Files)
		}
	}

	if fr.Graph.Seq != 1 {
		t.Errorf("Graph.Seq = %d, want 1", fr.Graph.Seq)
	}

	changed := map[string]bool{}
	for _, p := range fr.ChangedSince {
		changed[p] = true
	}
	for _, want := range []string{"file1.ts", "file2.ts", "file4.ts"} {
		if !changed[want] {
			t.Errorf("ChangedSince = %v, want it to include %q", fr.ChangedSince, want)
		}
	}
	for _, notWant := range []string{"file3.ts", "file5.ts"} {
		if changed[notWant] {
			t.Errorf("ChangedSince = %v, want it to NOT include %q (unchanged since the graph commit)", fr.ChangedSince, notWant)
		}
	}
}

// TestComputeFreshness_MultiDomain: two domains in one snapshot; a file
// belonging to one domain but outside the OTHER's include pattern must
// still be scoped correctly (via its own domain), and a file outside every
// declared domain must be silently absent from the report.
func TestComputeFreshness_MultiDomain(t *testing.T) {
	dir := initGitRepoT(t)

	writeFileT(t, dir, "a/x.ts", "ax")
	writeFileT(t, dir, "b/y.ts", "by")
	writeFileT(t, dir, "c/z.ts", "cz") // outside every domain
	commitAllT(t, dir, "content")

	blobA := captureGitT(t, dir, "rev-parse", "HEAD:a/x.ts")
	blobB := captureGitT(t, dir, "rev-parse", "HEAD:b/y.ts")

	snap := &Snapshot{
		Header: Header{
			Kind: "header", Format: Format, Version: Version, Seq: 1, FactSchema: FactSchema,
			Domains: []string{"a", "b"},
		},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "a/x.ts", Blob: blobA, Extractors: []ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
			{Kind: "coverage", Path: "b/y.ts", Blob: blobB, Extractors: []ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
		},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, snap))
	rev := commitAllT(t, dir, "graph seq1")

	man := &manifest.Manifest{
		Domains: []manifest.DomainEntry{
			{Key: "a", Name: "a", Scan: manifest.ScanConfig{Include: []string{"a/**"}}},
			{Key: "b", Name: "b", Scan: manifest.ScanConfig{Include: []string{"b/**"}}},
		},
	}

	graphVer, err := ResolveVersion(dir, rev)
	if err != nil {
		t.Fatalf("ResolveVersion: %v", err)
	}
	fr, err := ComputeFreshness(dir, snap, graphVer, man, rev)
	if err != nil {
		t.Fatalf("ComputeFreshness: %v", err)
	}

	want := map[string]FileState{"a/x.ts": Fresh, "b/y.ts": Fresh}
	if len(fr.Files) != len(want) {
		t.Fatalf("Files = %+v, want exactly %v (c/z.ts must be excluded: outside every domain)", fr.Files, want)
	}
	for _, ff := range fr.Files {
		wantState, ok := want[ff.Path]
		if !ok || ff.State != wantState {
			t.Errorf("%s: state = %s (present=%v), want %s", ff.Path, ff.State, ok, wantState)
		}
	}
}

// TestComputeFreshness_UnicodeAndSpacesPaths: a path with a non-ASCII byte
// and an embedded space must survive both the git ls-tree scope listing and
// the git rev-parse blob lookup intact — losing either silently would read
// as "file not covered" or "file gone", the two most misleading possible
// wrong answers for a real path that is actually fine.
func TestComputeFreshness_UnicodeAndSpacesPaths(t *testing.T) {
	dir := initGitRepoT(t)

	const path = "src/café room.ts"
	writeFileT(t, dir, path, "x")
	commitAllT(t, dir, "content")

	blob := captureGitT(t, dir, "rev-parse", "HEAD:"+path)

	snap := &Snapshot{
		Header: Header{
			Kind: "header", Format: Format, Version: Version, Seq: 1, FactSchema: FactSchema,
			Domains: []string{"d"},
		},
		Coverage: []Coverage{
			{Kind: "coverage", Path: path, Blob: blob, Extractors: []ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
		},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, snap))
	rev := commitAllT(t, dir, "graph seq1")

	man := singleDomainManifest("d", "src/**")

	graphVer, err := ResolveVersion(dir, rev)
	if err != nil {
		t.Fatalf("ResolveVersion: %v", err)
	}
	fr, err := ComputeFreshness(dir, snap, graphVer, man, rev)
	if err != nil {
		t.Fatalf("ComputeFreshness: %v", err)
	}
	if len(fr.Files) != 1 || fr.Files[0].Path != path || fr.Files[0].State != Fresh {
		t.Fatalf("Files = %+v, want exactly one Fresh entry for %q", fr.Files, path)
	}
}

// TestComputeFreshness_WorkingTree: rev=="" must read scope and blobs from
// disk, not from the last commit — an uncommitted edit must show up as
// Stale immediately, not stay Fresh until it's committed.
func TestComputeFreshness_WorkingTree(t *testing.T) {
	dir := initGitRepoT(t)

	writeFileT(t, dir, "file.ts", "v1")
	commitAllT(t, dir, "content")
	blob1 := captureGitT(t, dir, "rev-parse", "HEAD:file.ts")

	snap := &Snapshot{
		Header: Header{
			Kind: "header", Format: Format, Version: Version, Seq: 1, FactSchema: FactSchema,
			Domains: []string{"d"},
		},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "file.ts", Blob: blob1, Extractors: []ExtractorRun{{ID: "fact-schema", Version: "fact-schema@1", Status: "ok"}}},
		},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, snap))
	graphSHA := commitAllT(t, dir, "graph seq1")

	// Edit on disk WITHOUT committing.
	writeFileT(t, dir, "file.ts", "v2-uncommitted")

	man := singleDomainManifest("d", "file*.ts")

	// The caller resolves the graph version at HEAD itself (mirroring what
	// `chronicle graph status --working-tree`, with no explicit <rev>, does)
	// — ComputeFreshness no longer re-derives this from rev=="" internally.
	graphVer, err := ResolveVersion(dir, "HEAD")
	if err != nil {
		t.Fatalf("ResolveVersion: %v", err)
	}

	fr, err := ComputeFreshness(dir, snap, graphVer, man, "")
	if err != nil {
		t.Fatalf("ComputeFreshness: %v", err)
	}
	if fr.Rev != "" {
		t.Errorf("Rev = %q, want \"\" (working tree)", fr.Rev)
	}
	if fr.Graph.Commit != graphSHA {
		t.Errorf("Graph.Commit = %s, want %s (HEAD, which hasn't moved)", fr.Graph.Commit, graphSHA)
	}
	if len(fr.Files) != 1 || fr.Files[0].Path != "file.ts" || fr.Files[0].State != Stale {
		t.Fatalf("Files = %+v, want exactly one Stale file.ts (disk content changed, nothing committed)", fr.Files)
	}
}

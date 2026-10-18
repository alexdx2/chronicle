package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/store"
)

const testManifest = "domains:\n  d:\n    scan:\n      include: [\"src/**\"]\ntech: [typescript]\n"

// scanEnv is a committed temp repo with an open store in its .depbot dir.
type scanEnv struct {
	root string
	g    *graph.Graph
	s    *store.Store
}

// newScanRepoT commits files (plus a one-domain manifest when withManifest) and
// opens a store at <root>/.depbot/chronicle.db with the project root set.
func newScanRepoT(t *testing.T, files map[string]string, withManifest bool) *scanEnv {
	t.Helper()
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "t@example.com")
	gitT(t, root, "config", "user.name", "t")
	gitT(t, root, "config", "commit.gpgsign", "false")
	for rel, content := range files {
		writeT(t, root, rel, content)
	}
	if withManifest {
		writeT(t, root, ".depbot/chronicle.domain.yaml", testManifest)
	}
	commitAllT(t, root, "base")
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })
	s, err := store.Open(filepath.Join(root, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return &scanEnv{root: root, g: newGraphT(t, s), s: s}
}

func fiveFiles() map[string]string {
	f := map[string]string{}
	for i := 1; i <= 5; i++ {
		f[fmt.Sprintf("src/f%d.ts", i)] = fmt.Sprintf("export const f%d = %d;\n", i, i)
	}
	return f
}

// countingBackend answers from the fake fixtures and counts model calls per file.
type countingBackend struct {
	mu          sync.Mutex
	calls       map[string]int  // file path -> Complete calls
	failPaths   map[string]bool // these files get a non-JSON reply
	hangPaths   map[string]bool // these files never answer until the context ends
	cancelAfter int             // >0: cancel() after this many answers were served
	cancel      context.CancelFunc
	served      int
}

func (b *countingBackend) Name() string  { return "fake" }
func (b *countingBackend) Model() string { return "fake" }

func (b *countingBackend) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	first, _, _ := strings.Cut(req.User, "\n")
	path := strings.TrimPrefix(first, "File: ")
	b.mu.Lock()
	if b.calls == nil {
		b.calls = map[string]int{}
	}
	b.calls[path]++
	hang, fail := b.hangPaths[path], b.failPaths[path]
	b.mu.Unlock()
	if hang {
		<-ctx.Done()
		return llm.Response{}, ctx.Err()
	}
	if fail {
		return llm.Response{Text: "this is not json"}, nil
	}
	resp, err := llm.NewFake("testdata/fake").Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	b.mu.Lock()
	b.served++
	trip := b.cancelAfter > 0 && b.served >= b.cancelAfter && b.cancel != nil
	b.mu.Unlock()
	if trip {
		b.cancel()
	}
	return resp, nil
}

func (b *countingBackend) total() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, c := range b.calls {
		n += c
	}
	return n
}

func (b *countingBackend) callsFor(path string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[path]
}

func fullOpts(e *scanEnv, b llm.Backend) FullOptions {
	return FullOptions{RepoRoot: e.root, ProjectPath: "proj", Backend: b, Concurrency: 1}
}

func TestFullScan_BootstrapsAndExtractsEverything(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), false)
	b := &countingBackend{}
	rep, err := FullScan(context.Background(), e.g, fullOpts(e, b))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.ManifestCreated || !rep.Complete || len(rep.Domains) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	d := rep.Domains[0]
	if d.Domain != "proj" || d.InScope != 5 || d.Sent != 5 || d.Extracted != 5 || d.AlreadyCovered != 0 {
		t.Fatalf("domain = %+v", d)
	}
	if d.Structural == nil || d.Structural.Processed < 1 {
		t.Fatalf("structural = %+v", d.Structural)
	}
	if b.total() != 5 {
		t.Fatalf("model calls = %d, want 5", b.total())
	}
	if rep.HeadSHA != gitT(t, e.root, "rev-parse", "HEAD") {
		t.Fatalf("HeadSHA = %q", rep.HeadSHA)
	}
	cov, err := e.s.ListCoverage("proj")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range cov {
		if c.ExtractorID == "chronicle-scan" {
			n++
			if c.BlobSHA == "" {
				t.Errorf("coverage of %s has no blob sha", c.FilePath)
			}
		}
	}
	if n != 5 {
		t.Fatalf("chronicle-scan coverage rows = %d, want 5 (%+v)", n, cov)
	}
}

func TestFullScan_ResumeSkipsCoveredFiles(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b1 := &countingBackend{cancelAfter: 3, cancel: cancel}
	rep, err := FullScan(ctx, e.g, fullOpts(e, b1))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rep == nil || rep.Complete {
		t.Fatalf("partial report = %+v", rep)
	}
	if b1.total() != 3 {
		t.Fatalf("first run calls = %d, want 3", b1.total())
	}

	b2 := &countingBackend{}
	rep, err = FullScan(context.Background(), e.g, fullOpts(e, b2))
	if err != nil {
		t.Fatal(err)
	}
	if b2.total() != 2 {
		t.Fatalf("second run calls = %d, want 2", b2.total())
	}
	d := rep.Domains[0]
	if d.AlreadyCovered != 3 || d.Sent != 2 || !rep.Complete {
		t.Fatalf("second run: domain = %+v complete = %v", d, rep.Complete)
	}

	b3 := &countingBackend{}
	rep, err = FullScan(context.Background(), e.g, fullOpts(e, b3))
	if err != nil {
		t.Fatal(err)
	}
	if b3.total() != 0 || rep.Domains[0].Sent != 0 || rep.Domains[0].AlreadyCovered != 5 || !rep.Complete {
		t.Fatalf("third run: calls = %d, domain = %+v", b3.total(), rep.Domains[0])
	}
	if !containsStr(rep.Limitations, "nothing to extract: every in-scope file already covered at its current blob") {
		t.Fatalf("limitations = %v", rep.Limitations)
	}
}

func TestFullScan_FailedFileRetriedNotSilentlyDone(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), true)
	b1 := &countingBackend{failPaths: map[string]bool{"src/f2.ts": true}}
	rep, err := FullScan(context.Background(), e.g, fullOpts(e, b1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failed) != 1 || rep.Failed[0].Path != "src/f2.ts" || rep.Failed[0].Error == "" {
		t.Fatalf("Failed = %+v", rep.Failed)
	}
	if rep.Complete {
		t.Fatal("a run with a failed file must not be complete")
	}
	if !containsStr(rep.Limitations, "1 files failed extraction and will be retried by the next run") {
		t.Fatalf("limitations = %v", rep.Limitations)
	}
	cov, _ := e.s.ListCoverage("d")
	for _, c := range cov {
		if c.FilePath == "src/f2.ts" && c.ExtractorID == "chronicle-scan" {
			t.Fatalf("failed file has coverage: %+v", c)
		}
	}

	b2 := &countingBackend{}
	rep, err = FullScan(context.Background(), e.g, fullOpts(e, b2))
	if err != nil {
		t.Fatal(err)
	}
	if b2.total() != 1 || b2.callsFor("src/f2.ts") != 1 {
		t.Fatalf("second run calls = %v, want exactly src/f2.ts", b2.calls)
	}
	if len(rep.Failed) != 0 || !rep.Complete {
		t.Fatalf("second run: failed = %+v complete = %v", rep.Failed, rep.Complete)
	}
}

func TestFullScan_ChangedFileIsReextracted(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), true)
	if _, err := FullScan(context.Background(), e.g, fullOpts(e, &countingBackend{})); err != nil {
		t.Fatal(err)
	}
	writeT(t, e.root, "src/f1.ts", "export const f1 = 100;\n")
	commitAllT(t, e.root, "change f1")
	b := &countingBackend{}
	rep, err := FullScan(context.Background(), e.g, fullOpts(e, b))
	if err != nil {
		t.Fatal(err)
	}
	if b.total() != 1 || b.callsFor("src/f1.ts") != 1 {
		t.Fatalf("calls = %v, want exactly src/f1.ts", b.calls)
	}
	if d := rep.Domains[0]; d.AlreadyCovered != 4 || d.Sent != 1 || !rep.Complete {
		t.Fatalf("domain = %+v complete = %v", d, rep.Complete)
	}
}

func TestFullScan_NeverOverwritesExistingManifest(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), false)
	const custom = "domains:\n  d:\n    scan:\n      include: [\"src/**\"]\ntech: [typescript]\npublishers:\n  allowed_signers: |\n    ci ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample\n"
	writeT(t, e.root, ".depbot/chronicle.domain.yaml", custom)
	commitAllT(t, e.root, "manifest")
	rep, err := FullScan(context.Background(), e.g, fullOpts(e, &countingBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(manifestPathOf(e.root))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Fatalf("manifest changed:\n%s", got)
	}
	if rep.ManifestCreated {
		t.Fatal("ManifestCreated for an existing manifest")
	}
}

func TestFullScan_NoExtractRunsStructuralOnly(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), true)
	b := &countingBackend{}
	o := fullOpts(e, b)
	o.NoExtract = true
	rep, err := FullScan(context.Background(), e.g, o)
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Domains[0]
	if b.total() != 0 || d.Sent != 0 || d.Structural == nil || d.Structural.Processed < 1 {
		t.Fatalf("calls = %d, domain = %+v", b.total(), d)
	}
	if !rep.Complete {
		t.Fatalf("structural-only run should be complete: %+v", rep)
	}
}

func TestFullScan_MetricsReported(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), true)
	var got []FileMetric
	o := fullOpts(e, &countingBackend{})
	o.OnFiles = func(ms []FileMetric) { got = append(got, ms...) }
	rep, err := FullScan(context.Background(), e.g, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("OnFiles metrics = %d, want 5", len(got))
	}
	for _, m := range got {
		if m.Bytes <= 0 || m.Parts != 1 || m.Status != "extracted" || m.Model != "fake:fake" || m.At.IsZero() {
			t.Fatalf("metric = %+v", m)
		}
	}
	if !reflect.DeepEqual(rep.Metrics, got) {
		t.Fatalf("report metrics differ from OnFiles:\n%+v\n%+v", rep.Metrics, got)
	}
}

func TestFullScan_FileTimeoutCountsAsFailed(t *testing.T) {
	files := fiveFiles()
	files["src/slow.ts"] = "export const slow = 1;\n"
	e := newScanRepoT(t, files, true)
	o := fullOpts(e, &countingBackend{hangPaths: map[string]bool{"src/slow.ts": true}})
	o.FileTimeout = 80 * time.Millisecond
	rep, err := FullScan(context.Background(), e.g, o)
	if err != nil {
		t.Fatalf("a per-file timeout must not fail the scan: %v", err)
	}
	if len(rep.Failed) != 1 || rep.Failed[0].Path != "src/slow.ts" || !strings.HasPrefix(rep.Failed[0].Error, "timeout:") {
		t.Fatalf("Failed = %+v", rep.Failed)
	}
	if d := rep.Domains[0]; d.Extracted != 5 || d.Sent != 6 {
		t.Fatalf("domain = %+v", d)
	}
	if rep.Complete {
		t.Fatal("must not be complete with a failed file")
	}
}

func TestFullScan_SkippedFilesAreNotFailures(t *testing.T) {
	files := fiveFiles()
	files["src/blob.ts"] = "export const x = 1;\x00\x00\n"
	e := newScanRepoT(t, files, true)
	rep, err := FullScan(context.Background(), e.g, fullOpts(e, &countingBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failed) != 0 || !rep.Complete {
		t.Fatalf("failed = %+v complete = %v", rep.Failed, rep.Complete)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Path != "src/blob.ts" || !strings.Contains(rep.Skipped[0].Error, "binary") {
		t.Fatalf("Skipped = %+v", rep.Skipped)
	}
}

func TestFullScan_SeveralDomainsEachOwnTheirFiles(t *testing.T) {
	e := newScanRepoT(t, map[string]string{
		"src/a.ts": "export const a = 1;\n",
		"lib/b.ts": "export const b = 1;\n",
	}, false)
	writeT(t, e.root, ".depbot/chronicle.domain.yaml",
		"domains:\n  d1:\n    scan:\n      include: [\"src/**\"]\n  d2:\n    scan:\n      include: [\"lib/**\"]\ntech: [typescript]\n")
	commitAllT(t, e.root, "manifest")
	b := &countingBackend{}
	rep, err := FullScan(context.Background(), e.g, fullOpts(e, b))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Domains) != 2 || b.total() != 2 || !rep.Complete {
		t.Fatalf("domains = %+v calls = %d", rep.Domains, b.total())
	}
	for _, d := range rep.Domains {
		if d.InScope != 1 || d.Sent != 1 {
			t.Fatalf("domain = %+v", d)
		}
	}

	b = &countingBackend{}
	o := fullOpts(e, b)
	o.Domain = "d2"
	rep, err = FullScan(context.Background(), e.g, o)
	if err != nil || len(rep.Domains) != 1 || rep.Domains[0].Domain != "d2" {
		t.Fatalf("one domain: %+v %v", rep, err)
	}
	o.Domain = "nope"
	if _, err := FullScan(context.Background(), e.g, o); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("unknown domain error = %v", err)
	}
}

func TestLimitationsFor(t *testing.T) {
	if got := limitationsFor(true, 0); !reflect.DeepEqual(got, []string{"trace_flow phase not run"}) {
		t.Fatalf("clean = %v", got)
	}
	var got []string
	got = limitationsFor(false, 2)
	if !containsStr(got, "structural pass did not complete") || !containsStr(got, "2 files failed extraction and will be retried by the next run") {
		t.Fatalf("failed = %v", got)
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// A tracked file that is not a regular file of the working copy (symlink,
// submodule, one that cannot be statted) is not sent, and not silent: it is in
// the report's Skipped with its reason.
func TestFullScan_IrregularFilesAreReportedSkipped(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), true)
	if err := os.Symlink("f1.ts", filepath.Join(e.root, "src", "link.ts")); err != nil {
		t.Fatal(err)
	}
	gitT(t, e.root, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("ab", 20)+",src/sub")
	if err := os.MkdirAll(filepath.Join(e.root, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, e.root, "add", "src/link.ts")
	gitT(t, e.root, "commit", "-q", "-m", "irregular")
	if err := os.Remove(filepath.Join(e.root, "src", "f5.ts")); err != nil { // tracked, gone from disk
		t.Fatal(err)
	}
	b := &countingBackend{}
	rep, err := FullScan(context.Background(), e.g, fullOpts(e, b))
	if err != nil {
		t.Fatal(err)
	}
	why := map[string]string{}
	for _, s := range rep.Skipped {
		why[s.Path] = s.Error
	}
	if len(rep.Skipped) != 3 || !strings.Contains(why["src/link.ts"], "symlink") ||
		!strings.Contains(why["src/sub"], "submodule") || !strings.Contains(why["src/f5.ts"], "cannot stat") {
		t.Fatalf("Skipped = %+v", rep.Skipped)
	}
	if b.total() != 4 || b.callsFor("src/link.ts") != 0 || b.callsFor("src/sub") != 0 {
		t.Fatalf("calls = %v", b.calls)
	}
	if len(rep.Failed) != 0 || !rep.Complete {
		t.Fatalf("a skip is not a failure: failed %+v complete %v", rep.Failed, rep.Complete)
	}
}

func TestFullScan_OnFileDoneReportsEveryFileBeforeItsWave(t *testing.T) {
	e := newScanRepoT(t, fiveFiles(), true)
	var mu sync.Mutex
	done := map[string]string{}
	atWave := -1
	o := fullOpts(e, &countingBackend{})
	o.OnFileDone = func(p, st string) { mu.Lock(); done[p] = st; mu.Unlock() }
	o.OnFiles = func(ms []FileMetric) {
		mu.Lock()
		defer mu.Unlock()
		if atWave < 0 {
			atWave = len(done)
		}
	}
	if _, err := FullScan(context.Background(), e.g, o); err != nil {
		t.Fatal(err)
	}
	if len(done) != 5 || atWave != 5 {
		t.Fatalf("OnFileDone files = %d, seen before first wave callback = %d; want 5/5", len(done), atWave)
	}
}

func TestFullScan_PlainRunWritesDeterministicDependencies(t *testing.T) {
	files := fiveFiles()
	files["package.json"] = `{"name":"acme-api","version":"1.0.0","dependencies":{"@nestjs/common":"^10.0.0","kafkajs":"^2.0.0"}}`
	e := newScanRepoT(t, files, true)
	o := fullOpts(e, &countingBackend{})
	o.NoExtract = true
	for i := 0; i < 2; i++ { // second run: idempotent
		if _, err := FullScan(context.Background(), e.g, o); err != nil {
			t.Fatal(err)
		}
	}
	nodes, _ := e.s.ListNodes(store.NodeFilter{})
	have := map[string]bool{}
	for _, n := range nodes {
		have[n.NodeKey] = true
	}
	for _, want := range []string{"@nestjs/common", "kafkajs"} {
		found := false
		for k := range have {
			if strings.HasPrefix(k, "code:package:") && strings.HasSuffix(k, ":npm:"+want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no code:package node for %s in %v", want, have)
		}
	}
	edges, _ := e.s.ListEdges(store.EdgeFilter{})
	dep := 0
	for _, ed := range edges {
		if ed.EdgeType == "DEPENDS_ON" && ed.Active && strings.HasPrefix(ed.ToNodeKey, "code:package:") {
			dep++
		}
	}
	if dep != 2 {
		t.Fatalf("DEPENDS_ON to code:package = %d, want 2", dep)
	}
}

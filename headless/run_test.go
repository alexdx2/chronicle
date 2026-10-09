package headless

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alexdx2/chronicle-core/extract/deterministic"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// runEnv is a committed temp repo with a real store under .depbot/.
type runEnv struct {
	root string
	g    *graph.Graph
	m    *manifest.Manifest
	head string
}

func newRunEnv(t *testing.T) *runEnv {
	t.Helper()
	root := newRepo(t)
	write(t, root, "src/bad.ts", "export const bad = 1;\n")
	write(t, root, "src/arr.ts", "export const arr = 1;\n")
	write(t, root, "src/app/[id]/page.tsx", "export default function P() { return null }\n")
	commitAll(t, root, "more sources")
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })

	s, err := store.Open(filepath.Join(root, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return &runEnv{
		root: root,
		g:    graph.New(s, reg),
		m: &manifest.Manifest{
			Tech:    []string{"typescript"},
			Domains: []manifest.DomainEntry{{Key: "d", Name: "d", Scan: manifest.ScanConfig{Include: []string{"src/**"}}}},
		},
		head: strings.TrimSpace(string(out)),
	}
}

func (e *runEnv) input(b llm.Backend, files ...string) RunInput {
	return RunInput{
		G: e.g, RepoRoot: e.root, ChronicleDir: filepath.Join(e.root, ".depbot"),
		Domain: "d", Manifest: e.m, HeadSHA: e.head, Files: files, Backend: b, Concurrency: 2,
	}
}

func TestRun_FakeBackend_EndToEnd(t *testing.T) {
	e := newRunEnv(t)
	res, err := Run(context.Background(), e.input(llm.NewFake("testdata/fake"), "src/a.ts", "src/app/[id]/page.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Extracted != 1 || res.Failed != 0 {
		t.Fatalf("extracted/failed = %d/%d, want 1/0 (%+v)", res.Extracted, res.Failed, res.Files)
	}
	if res.Commit == nil || res.Commit.Imported != 2 || res.Commit.Errors != 0 {
		t.Fatalf("commit = %+v", res.Commit)
	}
	if res.Resolve == nil || res.Finalize == nil {
		t.Fatalf("resolve/finalize missing: %+v / %+v", res.Resolve, res.Finalize)
	}
	if res.Totals.InputTokens <= 0 || res.Totals.OutputTokens <= 0 {
		t.Errorf("totals = %+v", res.Totals)
	}
	if res.CostKnown {
		t.Error("fake backend cost must be unknown")
	}
	if len(res.Limitations) != 2 || res.Model != "fake:fake" {
		t.Errorf("limitations/model = %v / %q", res.Limitations, res.Model)
	}
	rev, err := e.g.Store().GetRevision(res.RevisionID)
	if err != nil || rev == nil {
		t.Fatalf("GetRevision: %v %v", rev, err)
	}
	if rev.TriggerKind != "ci_pipeline" || !strings.Contains(rev.Metadata, `"kind":"headless"`) {
		t.Errorf("revision = %+v; want ci_pipeline with metadata kind headless", rev)
	}
	// Commit records coverage with the blob of the file on disk.
	cov, err := e.g.Store().ListCoverage("d")
	if err != nil || len(cov) != 2 || cov[0].FilePath != "src/a.ts" || cov[0].BlobSHA == "" || cov[1].BlobSHA == "" {
		t.Errorf("coverage = %+v, err %v; want rows for both files, each with a blob", cov, err)
	}
	// The outbox was drained.
	if left, _ := filepath.Glob(filepath.Join(e.root, ".depbot", "scan-outbox", "*", "*.json")); len(left) != 0 {
		t.Errorf("outbox not drained: %v", left)
	}
}

func TestRun_InvalidResponseBecomesFailed(t *testing.T) {
	e := newRunEnv(t)
	res, err := Run(context.Background(), e.input(llm.NewFake("testdata/fake"), "src/bad.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Extracted != 0 {
		t.Fatalf("extracted/failed = %d/%d, want 0/1", res.Extracted, res.Failed)
	}
	rows, err := e.g.Store().ListExtractions(res.RevisionID, "d")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "failed" || !strings.Contains(rows[0].ErrorMessage, "prose") {
		t.Fatalf("rows = %+v; want one failed row mentioning prose", rows)
	}
	if res.Files[0].Status != "failed" {
		t.Errorf("file status = %q", res.Files[0].Status)
	}
	// Two attempts: usage covers the retry.
	one, _ := llm.NewFake("testdata/fake").Complete(context.Background(), BuildPrompt("", "src/bad.ts", []byte("export const bad = 1;\n"), nil))
	if res.Files[0].Usage.InputTokens <= one.Usage.InputTokens {
		t.Errorf("usage %+v does not include the retry", res.Files[0].Usage)
	}
}

func TestRun_TopLevelArrayIsAccepted(t *testing.T) {
	e := newRunEnv(t)
	dir := t.TempDir()
	write(t, dir, "src__arr.ts.json", `[{"kind":"import","to":"./a","from_type":"provider"}]`)
	res, err := Run(context.Background(), e.input(llm.NewFake(dir), "src/arr.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Extracted != 1 || res.Failed != 0 || res.Files[0].Status != "extracted" {
		t.Fatalf("result = %+v", res)
	}
}

type errBackend struct{}

func (errBackend) Name() string  { return "err" }
func (errBackend) Model() string { return "err" }
func (errBackend) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("boom 503")
}

func TestRun_ProviderErrorAndNotDiscoverable(t *testing.T) {
	e := newRunEnv(t)
	res, err := Run(context.Background(), e.input(errBackend{}, "src/a.ts", "src/missing.ts"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]FileUsage{}
	for _, f := range res.Files {
		got[f.Path] = f
	}
	if got["src/a.ts"].Status != "provider_error" || !strings.Contains(got["src/a.ts"].Error, "boom") {
		t.Errorf("a.ts = %+v", got["src/a.ts"])
	}
	if got["src/missing.ts"].Status != "skipped" || got["src/missing.ts"].Error != "not discoverable" {
		t.Errorf("missing.ts = %+v", got["src/missing.ts"])
	}
	if res.Failed != 1 {
		t.Errorf("Failed = %d, want 1", res.Failed)
	}
}

func TestRun_CancelledContext(t *testing.T) {
	e := newRunEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, e.input(llm.NewFake("testdata/fake"), "src/a.ts")); err == nil {
		t.Fatal("cancelled context should return an error")
	}
}

func TestBuildPrompt_FirstLineIsFile(t *testing.T) {
	req := BuildPrompt("GUIDE-TEXT", "src/a.ts", []byte("export const a = 1;\n"),
		[]deterministic.Candidate{{Kind: "import", To: "./b"}})
	if !strings.HasPrefix(req.User, "File: src/a.ts\n") {
		t.Fatalf("User = %q", req.User)
	}
	for _, want := range []string{`"to":"./b"`, "<<<FILE\nexport const a = 1;\n\nFILE>>>"} {
		if !strings.Contains(req.User, want) {
			t.Errorf("User missing %q:\n%s", want, req.User)
		}
	}
	if !strings.HasPrefix(req.System, "GUIDE-TEXT") || !strings.Contains(req.System, "## Output contract") {
		t.Errorf("System = %q", req.System)
	}
	none := BuildPrompt("g", "src/a.ts", nil, nil)
	if !strings.Contains(none.User, "candidates (merge the confident ones, drop the wrong ones):\nnone\n") {
		t.Errorf("no-candidates prompt: %q", none.User)
	}
}

func TestRun_EmptyFilesDoesNothing(t *testing.T) {
	e := newRunEnv(t)
	res, err := Run(context.Background(), e.input(errBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	if res.RevisionID != 0 || len(res.Files) != 0 || res.Totals.InputTokens != 0 ||
		len(res.Limitations) != 1 || res.Limitations[0] != "no files to extract" {
		t.Fatalf("result = %+v", res)
	}
	if revs, _ := e.g.Store().GetRevisionBySHA("d", e.head); revs != nil {
		t.Errorf("a revision was claimed: %+v", revs)
	}
}

// blockingBackend holds every call until the context is cancelled.
type blockingBackend struct{}

func (blockingBackend) Name() string  { return "block" }
func (blockingBackend) Model() string { return "block" }
func (blockingBackend) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	<-ctx.Done()
	return llm.Response{}, ctx.Err()
}

// A cancelled run leaves claimed obligations behind, but re-discovery on the
// same head resets them, so a rerun extracts normally instead of silently
// doing nothing.
func TestRun_RerunAfterCancelledRunExtracts(t *testing.T) {
	e := newRunEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	if _, err := Run(ctx, e.input(blockingBackend{}, "src/a.ts")); err == nil {
		t.Fatal("first run should end with the cancellation")
	}
	res, err := Run(context.Background(), e.input(llm.NewFake("testdata/fake"), "src/a.ts"))
	if err != nil || res.Extracted != 1 || res.Commit.Imported != 1 {
		t.Fatalf("rerun: err %v res %+v", err, res)
	}
}

// cancelOnSecond answers the first call from the fake, then cancels the run.
type cancelOnSecond struct {
	inner  llm.Backend
	cancel context.CancelFunc
	calls  int
}

func (c *cancelOnSecond) Name() string  { return "fake" }
func (c *cancelOnSecond) Model() string { return "fake" }
func (c *cancelOnSecond) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	c.calls++
	if c.calls == 2 {
		c.cancel()
		return llm.Response{}, ctx.Err()
	}
	return c.inner.Complete(ctx, r)
}

func TestRun_CancelMidWaveKeepsCompletedWork(t *testing.T) {
	e := newRunEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &cancelOnSecond{inner: llm.NewFake("testdata/fake"), cancel: cancel}
	in := e.input(b, "src/a.ts", "src/arr.ts")
	in.Concurrency = 1
	res, err := Run(ctx, in)
	if !errors.Is(err, context.Canceled) || res == nil {
		t.Fatalf("res = %v, err = %v; want partial result and context.Canceled", res, err)
	}
	counts := map[string]int{}
	for _, f := range res.Files {
		counts[f.Status]++
	}
	if len(res.Files) != 2 || counts["cancelled"] != 1 || counts["failed"] != 0 || counts["extracted"]+counts["no_runtime_architecture"] != 1 {
		t.Fatalf("files = %+v", res.Files)
	}
	if res.Commit == nil || res.Commit.Imported != 1 {
		t.Fatalf("commit = %+v; want the completed file committed", res.Commit)
	}
	rows, _ := e.g.Store().ListExtractions(res.RevisionID, "d")
	if len(rows) != 1 {
		t.Errorf("extraction rows = %+v; want exactly one", rows)
	}
	if left, _ := filepath.Glob(filepath.Join(e.root, ".depbot", "scan-outbox", "*", "*")); len(left) != 0 {
		t.Errorf("outbox not drained: %v", left)
	}
}

type longErrBackend struct{}

func (longErrBackend) Name() string  { return "err" }
func (longErrBackend) Model() string { return "err" }
func (longErrBackend) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New(strings.Repeat("é", 3000))
}

func TestRun_ProviderErrorIsCappedOnRuneBoundary(t *testing.T) {
	e := newRunEnv(t)
	res, err := Run(context.Background(), e.input(longErrBackend{}, "src/a.ts"))
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Files[0].Error
	if len(msg) > 2000 || !utf8.ValidString(msg) || len(msg) < 1990 {
		t.Fatalf("error len %d valid=%v", len(msg), utf8.ValidString(msg))
	}
	if got := truncate("aé", 2); got != "a" {
		t.Errorf("truncate mid-rune = %q", got)
	}
}

func TestRun_SkipsLargeAndBinaryFiles(t *testing.T) {
	e := newRunEnv(t)
	write(t, e.root, "src/big.ts", strings.Repeat("x", 600))
	write(t, e.root, "src/bin.ts", "ab\x00cd")
	commitAll(t, e.root, "big+bin")
	in := e.input(errBackend{}, "src/big.ts", "src/bin.ts")
	in.MaxFileBytes = 500
	res, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]FileUsage{}
	for _, f := range res.Files {
		got[f.Path] = f
	}
	if f := got["src/big.ts"]; f.Status != "skipped" || !strings.Contains(f.Error, "file too large (600 bytes > cap 500)") {
		t.Errorf("big = %+v", f)
	}
	if f := got["src/bin.ts"]; f.Status != "skipped" || f.Error != "skipped: binary file" {
		t.Errorf("bin = %+v", f)
	}
	if res.Totals.InputTokens != 0 || res.Failed != 0 {
		t.Errorf("no LLM call expected: %+v", res)
	}
	rows, _ := e.g.Store().ListExtractions(res.RevisionID, "d")
	if len(rows) != 2 || rows[0].Status != "skipped" || rows[1].Status != "skipped" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestRun_DuplicateFilesRunOnce(t *testing.T) {
	e := newRunEnv(t)
	res, err := Run(context.Background(), e.input(llm.NewFake("testdata/fake"), "src/a.ts", "src/a.ts"))
	if err != nil || len(res.Files) != 1 {
		t.Fatalf("err %v files %+v", err, res.Files)
	}
}

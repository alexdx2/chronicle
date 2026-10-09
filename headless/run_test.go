package headless

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	res, err := Run(context.Background(), e.input(llm.NewFake("testdata/fake"), "src/a.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Extracted != 1 || res.Failed != 0 {
		t.Fatalf("extracted/failed = %d/%d, want 1/0 (%+v)", res.Extracted, res.Failed, res.Files)
	}
	if res.Commit == nil || res.Commit.Imported != 1 || res.Commit.Errors != 0 {
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
	if err != nil || len(cov) != 1 || cov[0].FilePath != "src/a.ts" || cov[0].BlobSHA == "" {
		t.Errorf("coverage = %+v, err %v; want one row for src/a.ts with a blob", cov, err)
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

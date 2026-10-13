package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/llm"
)

// factsBackend is a countingBackend whose answers carry one model fact named
// after the file, so the built graph has content to compare.
type factsBackend struct{ countingBackend }

func (b *factsBackend) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := b.countingBackend.Complete(ctx, req)
	if err != nil || !strings.HasPrefix(resp.Text, "{") { // a failing file keeps its bad reply
		return resp, err
	}
	first, _, _ := strings.Cut(req.User, "\n")
	name := strings.NewReplacer("File: ", "", "/", "_", ".", "_").Replace(first)
	resp.Text = fmt.Sprintf(`{"status":"extracted","facts":[{"kind":"model","name":"M_%s"}]}`, name)
	return resp, nil
}

func sixFiles() map[string]string {
	f := fiveFiles()
	f["src/f6.ts"] = "export const f6 = 6;\n"
	return f
}

// graphIDs are the sorted node and edge identifiers of a built snapshot.
func graphIDs(t *testing.T, res *BuildResult) (nodes, edges []string) {
	t.Helper()
	s := decodeT(t, res.Files)
	for _, n := range s.Nodes {
		nodes = append(nodes, n.ID)
	}
	for _, e := range s.Edges {
		edges = append(edges, e.ID)
	}
	sort.Strings(nodes)
	sort.Strings(edges)
	return nodes, edges
}

func sameStrings(a, b []string) bool {
	return strings.Join(a, "\n") == strings.Join(b, "\n")
}

type planRecorder struct{ plans []Plan }

func (p *planRecorder) on(pl Plan) { p.plans = append(p.plans, pl) }

// cancelledFirstRun runs the first attempt at c1 of repo, cancelled by the
// backend after n answers, with state kept in stateDir.
func cancelledFirstRun(t *testing.T, repo, c1, stateDir string, n int, mod func(*BuildInput)) (*factsBackend, *BuildResult) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &factsBackend{countingBackend{cancelAfter: n, cancel: cancel}}
	in := buildIn(repo, c1, t.TempDir(), b)
	in.StateDir = stateDir
	if mod != nil {
		mod(&in)
	}
	res, err := BuildSnapshot(ctx, in)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("first run err = %v, want context.Canceled", err)
	}
	return b, res
}

func TestBuildSnapshot_ResumeAfterCancelSkipsCoveredFiles(t *testing.T) {
	repo, c1 := newCodeRepoT(t, sixFiles())
	stateDir := filepath.Join(t.TempDir(), "state")

	b1, _ := cancelledFirstRun(t, repo, c1, stateDir, 3, nil)
	if b1.total() != 3 {
		t.Fatalf("first run made %d model calls, want 3", b1.total())
	}

	b2 := &factsBackend{}
	in := buildIn(repo, c1, t.TempDir(), b2)
	in.StateDir = stateDir
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if b1.total()+b2.total() != 6 {
		t.Fatalf("model calls %d + %d, want 6 in all", b1.total(), b2.total())
	}
	for p, n := range b2.calls {
		if b1.callsFor(p) != 0 || n != 1 {
			t.Fatalf("%s was sent %d times in the resumed run and %d in the first", p, n, b1.callsFor(p))
		}
	}

	whole, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), &factsBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	wn, we := graphIDs(t, whole)
	gn, ge := graphIDs(t, res)
	if len(wn) == 0 || !sameStrings(wn, gn) || !sameStrings(we, ge) {
		t.Fatalf("resumed graph differs from an uninterrupted one: nodes %d vs %d, edges %d vs %d", len(gn), len(wn), len(ge), len(we))
	}
	if res.Partial || len(res.Failed) != 0 {
		t.Fatalf("resumed result partial=%v failed=%v", res.Partial, res.Failed)
	}
}

func TestBuildSnapshot_ResumeIncrementalKeepsBaseFacts(t *testing.T) {
	repo, c1, c2, first := twoCommits(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	base := &Base{CodeSha: c1, Files: first.Files}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b1 := &factsBackend{countingBackend{cancelAfter: 1, cancel: cancel}}
	in := buildIn(repo, c2, t.TempDir(), b1)
	in.Base, in.StateDir = base, stateDir
	if _, err := BuildSnapshot(ctx, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("first run err = %v", err)
	}
	if b1.total() != 1 {
		t.Fatalf("first run made %d calls, want 1", b1.total())
	}

	b2 := &factsBackend{}
	in = buildIn(repo, c2, t.TempDir(), b2)
	in.Base, in.StateDir = base, stateDir
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	for _, p := range []string{"src/f2.ts", "src/f6.ts"} {
		if n := b1.callsFor(p) + b2.callsFor(p); n != 1 {
			t.Fatalf("%s sent %d times in all, want 1 (first %v, second %v)", p, n, b1.calls, b2.calls)
		}
	}
	if b2.total() != 1 || !res.Incremental || res.BaseSha != c1 {
		t.Fatalf("second run calls %d, incremental %v base %s", b2.total(), res.Incremental, res.BaseSha)
	}

	// f1 was never sent: its coverage is the base's, and so is the graph.
	was, now := decodeT(t, first.Files), decodeT(t, res.Files)
	if blobOf(was, "src/f1.ts") == "" || blobOf(now, "src/f1.ts") != blobOf(was, "src/f1.ts") {
		t.Fatalf("f1 coverage blob %q -> %q", blobOf(was, "src/f1.ts"), blobOf(now, "src/f1.ts"))
	}
	if blobOf(now, "src/f2.ts") == blobOf(was, "src/f2.ts") || blobOf(now, "src/f6.ts") == "" {
		t.Fatalf("f2 %q -> %q, f6 %q", blobOf(was, "src/f2.ts"), blobOf(now, "src/f2.ts"), blobOf(now, "src/f6.ts"))
	}
	if now.Header.Seq != was.Header.Seq+1 {
		t.Fatalf("seq %d -> %d, want +1 (the export must continue from the base's sequence)", was.Header.Seq, now.Header.Seq)
	}

	// The same graph as an uninterrupted incremental build.
	whole := buildIn(repo, c2, t.TempDir(), &factsBackend{})
	whole.Base = base
	wres, err := BuildSnapshot(context.Background(), whole)
	if err != nil {
		t.Fatal(err)
	}
	wn, we := graphIDs(t, wres)
	gn, ge := graphIDs(t, res)
	if len(wn) == 0 || !sameStrings(wn, gn) || !sameStrings(we, ge) {
		t.Fatalf("resumed graph differs from an uninterrupted one: nodes %d vs %d, edges %d vs %d", len(gn), len(wn), len(ge), len(we))
	}
}

func TestBuildSnapshot_StateForOtherShaIsDiscarded(t *testing.T) {
	repo, c1 := newCodeRepoT(t, sixFiles())
	stateDir := filepath.Join(t.TempDir(), "state")
	cancelledFirstRun(t, repo, c1, stateDir, 3, nil)

	writeT(t, repo, "src/f1.ts", "export const f1 = 100;\n")
	c2 := commitAllT(t, repo, "c2")
	b := &countingBackend{}
	in := buildIn(repo, c2, t.TempDir(), b)
	in.StateDir = stateDir
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if b.total() != 6 {
		t.Fatalf("calls = %d, want the full 6", b.total())
	}
	if !containsStr(res.Limitations, "discarded unusable build state") {
		t.Fatalf("limitations = %v", res.Limitations)
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil || !strings.Contains(string(raw), c2) || strings.Contains(string(raw), c1) {
		t.Fatalf("state.json = %q, %v; want it on %s", raw, err, c2)
	}
}

func TestBuildSnapshot_ManifestStableAcrossResume(t *testing.T) {
	repo, c1 := newCodeRepoT(t, sixFiles())
	stateDir := filepath.Join(t.TempDir(), "state")
	cancelledFirstRun(t, repo, c1, stateDir, 2, nil)
	saved, err := os.ReadFile(filepath.Join(stateDir, "chronicle.domain.yaml"))
	if err != nil || len(saved) == 0 {
		t.Fatalf("generated manifest not saved: %v", err)
	}

	in := buildIn(repo, c1, t.TempDir(), &countingBackend{})
	in.StateDir = stateDir
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Files.Domain) != string(saved) {
		t.Fatalf("manifest changed across the resume:\n%s\nvs\n%s", saved, res.Files.Domain)
	}
}

func TestBuildSnapshot_OnPlanReportsTotals(t *testing.T) {
	repo, c1 := newCodeRepoT(t, sixFiles())
	stateDir := filepath.Join(t.TempDir(), "state")

	var first planRecorder
	cancelledFirstRun(t, repo, c1, stateDir, 3, func(in *BuildInput) { in.OnPlan = first.on })
	if len(first.plans) != 1 || first.plans[0].InScope != 6 || first.plans[0].AlreadyCovered != 0 || first.plans[0].ToSend != 6 {
		t.Fatalf("first plans = %+v", first.plans)
	}

	var second planRecorder
	in := buildIn(repo, c1, t.TempDir(), &countingBackend{})
	in.StateDir, in.OnPlan = stateDir, second.on
	if _, err := BuildSnapshot(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(second.plans) != 1 || second.plans[0].InScope != 6 || second.plans[0].AlreadyCovered != 3 || second.plans[0].ToSend != 3 {
		t.Fatalf("resumed plans = %+v", second.plans)
	}
	if second.plans[0].Domain == "" {
		t.Fatal("plan has no domain")
	}
}

func TestBuildSnapshot_CorruptStateIsIgnored(t *testing.T) {
	repo, c1 := newCodeRepoT(t, sixFiles())
	stateDir := filepath.Join(t.TempDir(), "state")
	cancelledFirstRun(t, repo, c1, stateDir, 3, nil)
	if err := os.WriteFile(filepath.Join(stateDir, "chronicle.db"), []byte("this is not a database, not even close to one"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &countingBackend{}
	in := buildIn(repo, c1, t.TempDir(), b)
	in.StateDir = stateDir
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if b.total() != 6 {
		t.Fatalf("calls = %d, want the full 6", b.total())
	}
	if !containsStr(res.Limitations, "discarded unusable build state") {
		t.Fatalf("limitations = %v", res.Limitations)
	}
}

func TestBuildSnapshot_NoStateDirLeavesNothingBehind(t *testing.T) {
	repo, c1 := newCodeRepoT(t, sixFiles())
	work := t.TempDir()
	res, err := BuildSnapshot(context.Background(), buildIn(repo, c1, work, &countingBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range res.Limitations {
		if strings.Contains(l, "build state") {
			t.Fatalf("unexpected limitation %q", l)
		}
	}
	assertNoWorkingCopies(t, repo, work)
}

// partialBase builds C1 (five files) with f4 failing, then commits C2 that
// edits f2 only.
func partialBase(t *testing.T) (repo, c1, c2 string, base *Base) {
	t.Helper()
	files := fiveFiles()
	files["README.md"] = "# readme\n"
	repo, c1 = newCodeRepoT(t, files)
	first, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), &factsBackend{countingBackend{failPaths: map[string]bool{"src/f4.ts": true}}}))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Partial || len(first.Failed) != 1 || first.Failed[0].Path != "src/f4.ts" {
		t.Fatalf("setup: failed = %+v", first.Failed)
	}
	writeT(t, repo, "src/f2.ts", "export const f2 = 22;\n")
	c2 = commitAllT(t, repo, "c2")
	return repo, c1, c2, &Base{CodeSha: c1, Files: first.Files}
}

// Every in-scope file without a matching coverage row is a candidate, so a
// file that failed in the base is sent again even without RetryPaths.
func TestBuildSnapshot_WithoutRetryPathsAFailedBaseFileIsResent(t *testing.T) {
	repo, _, c2, base := partialBase(t)
	b := &countingBackend{}
	in := buildIn(repo, c2, t.TempDir(), b)
	in.Base = base
	if _, err := BuildSnapshot(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if b.total() != 2 || b.callsFor("src/f2.ts") != 1 || b.callsFor("src/f4.ts") != 1 {
		t.Fatalf("calls = %v", b.calls)
	}
}

// Plan numbers: the candidates are the whole scope (f1..f5), so InScope is 5;
// f3, f5 match their coverage (AlreadyCovered 2); f2 changed, f4 has no
// coverage row and f1 is covered but named in RetryPaths, which is an explicit
// order to extract again (ToSend 3).
func TestBuildSnapshot_RetryPathsAreSentAlthoughUnchanged(t *testing.T) {
	repo, _, c2, base := partialBase(t)
	b := &countingBackend{}
	var plans planRecorder
	in := buildIn(repo, c2, t.TempDir(), b)
	in.Base, in.OnPlan = base, plans.on
	in.RetryPaths = []string{
		"src/f4.ts",     // failed in the base: sent
		"src/f2.ts",     // changed too: once, not twice
		"src/f1.ts",     // covered at its blob: sent anyway (forced)
		"src/gone.ts",   // not in the tree
		"README.md",     // outside the scan scope
		"../etc/passwd", // not a path inside the tree
		"/etc/passwd",
	}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if b.total() != 3 || b.callsFor("src/f1.ts") != 1 || b.callsFor("src/f2.ts") != 1 || b.callsFor("src/f4.ts") != 1 {
		t.Fatalf("calls = %v", b.calls)
	}
	if res.Partial || len(res.Failed) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("partial %v failed %v skipped %v", res.Partial, res.Failed, res.Skipped)
	}
	if blobOf(decodeT(t, res.Files), "src/f4.ts") == "" {
		t.Fatal("f4 is still not covered in the result")
	}
	if len(plans.plans) != 1 || plans.plans[0].InScope != 5 || plans.plans[0].AlreadyCovered != 2 || plans.plans[0].ToSend != 3 {
		t.Fatalf("plans = %+v", plans.plans)
	}
}

func TestBuildSnapshot_RetryPathThroughSymlinkedDirIsSkipped(t *testing.T) {
	repo, _, _, base := partialBase(t)
	// Later commit turns src/link into a symlink to a directory outside the
	// clone, which holds a file of the retried name.
	outside := t.TempDir()
	writeT(t, outside, "x.ts", "export const secret = 1;\n")
	if err := os.Symlink(outside, filepath.Join(repo, "src", "link")); err != nil {
		t.Fatal(err)
	}
	c3 := commitAllT(t, repo, "c3")

	b := &countingBackend{}
	in := buildIn(repo, c3, t.TempDir(), b)
	in.Base = base
	in.RetryPaths = []string{"src/link/x.ts", "", ".", "src/link"}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if b.callsFor("src/link/x.ts") != 0 || b.callsFor("src/link") != 0 {
		t.Fatalf("a path outside the tree reached the model: %v", b.calls)
	}
	var found bool
	for _, s := range res.Skipped {
		if s.Path == "src/link/x.ts" && strings.Contains(s.Error, "retry") {
			found = true
		}
	}
	if !found {
		t.Fatalf("src/link/x.ts not listed as skipped: %+v", res.Skipped)
	}
}

// A scope change that newly admits a directory sends its files with no
// RetryPaths: they have no coverage row.
func TestBuildSnapshot_NewlyAdmittedDirectoryIsSentWithoutRetryPaths(t *testing.T) {
	man := func(include string) string {
		return "domains:\n  \"proj\":\n    name: \"proj\"\n    scan:\n      include:\n        - \"" + include + "\"\ntech: [typescript]\n"
	}
	repo, c1 := newCodeRepoT(t, map[string]string{
		"src/a.ts": "export const a = 1", "legacy/old.ts": "export const o = 1",
		".depbot/chronicle.domain.yaml": man("src/**"),
	})
	first, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), &countingBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	if blobOf(decodeT(t, first.Files), "legacy/old.ts") != "" {
		t.Fatal("setup: legacy/old.ts is covered in the base")
	}
	writeT(t, repo, ".depbot/chronicle.domain.yaml", man("**/*.ts"))
	c2 := commitAllT(t, repo, "admit legacy")
	b := &countingBackend{}
	var plans planRecorder
	in := buildIn(repo, c2, t.TempDir(), b)
	in.Base, in.OnPlan = &Base{CodeSha: c1, Files: first.Files}, plans.on
	if _, err := BuildSnapshot(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(plans.plans) != 1 || plans.plans[0].ToSend != 1 || b.callsFor("legacy/old.ts") != 1 || b.total() != 1 {
		t.Fatalf("plans %+v calls %v", plans.plans, b.calls)
	}
}

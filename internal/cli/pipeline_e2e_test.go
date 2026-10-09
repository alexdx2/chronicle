package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// The headline CI flow, chained in one test:
//
//	graph restore --at <merge base> -> refresh -> extract -> graph update -> review
//
// for a branch that edits one file of a committed two-file graph. It is the
// regression net for the seams between the steps: restore must leave a
// structural pointer (refresh must not sweep the repository), the plan must
// start at the merge base, and the coverage the steps leave behind must read
// as fresh to the review — so a one-file change reviews as exactly that.
func TestPipelineE2E_RestoreRefreshExtractUpdateReview(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "t@example.com")
	gitRun(t, dir, "config", "user.name", "t")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := func() string { return strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD")) }

	// --- main: sources, manifest, and a committed graph for both files -----
	write(".depbot/chronicle.domain.yaml", structuralManifest)
	write(".gitignore", "/.depbot/chronicle.db*\n/.depbot/events/\n/.depbot/scan-outbox/\n")
	write("src/a.ts", srvSource)
	write("src/b.ts", ctrlSource)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "sources")
	scanned := head()

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, ".depbot", "chronicle.db")
	graphPath := filepath.Join(dir, ".depbot", "graph.jsonl")
	func() {
		s := openRepoStore(t, dir)
		defer s.Close()
		rev, err := s.CreateRevision("d", "", scanned, "manual", "full", "{}")
		if err != nil {
			t.Fatal(err)
		}
		g := graph.New(s, reg)
		for _, f := range []struct{ key, name, file string }{
			{"code:symbol:d:a", "A", "src/a.ts"},
			{"code:symbol:d:b", "B", "src/b.ts"},
		} {
			if _, err := g.UpsertNode(validate.NodeInput{
				NodeKey: f.key, Layer: "code", NodeType: "symbol", DomainKey: "d", Name: f.name, FilePath: f.file,
			}, rev); err != nil {
				t.Fatal(err)
			}
			if _, err := g.AddNodeEvidence(f.key, validate.EvidenceInput{
				TargetKind: "node", SourceKind: "file", FilePath: f.file, LineStart: 1,
				ExtractorID: "chronicle-scan", ExtractorVersion: "1",
				Assertion: `{}`, AssertionKind: "symbol_exists", AssertionVersion: "1",
				Confidence: 0.9, Polarity: "positive", RevisionID: rev,
			}); err != nil {
				t.Fatal(err)
			}
			blob, err := gitutil.FileBlobSHA(dir, f.file)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertCoverage(store.CoverageRow{
				DomainKey: "d", FilePath: f.file, ExtractorID: "chronicle-scan",
				ExtractorVersion: "fact-schema@1", Status: "extracted", BlobSHA: blob,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := runGraphUpdate(s, reg, graphPath, false); err != nil {
			t.Fatal(err)
		}
	}()
	gitRun(t, dir, "add", ".depbot/graph.jsonl")
	gitRun(t, dir, "commit", "-q", "-m", "graph")
	mainTip := head()
	// CI starts without a database.
	for _, suf := range []string{"", "-wal", "-shm"} {
		os.Remove(dbPath + suf)
	}
	os.RemoveAll(filepath.Join(dir, ".depbot", "events"))

	// --- feature: a one-line comment change in src/a.ts ---------------------
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	write("src/a.ts", "// reworded\n"+srvSource)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "touch a")
	// main moves on after the fork; the branch must not be charged for it.
	gitRun(t, dir, "checkout", "-q", "main")
	write("src/b.ts", "// main only\n"+ctrlSource)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "main moves on")
	gitRun(t, dir, "checkout", "-q", "feature")

	// --- 1. graph restore --at main --merge-base-with HEAD ------------------
	res, err := runGraphRestore(restoreInput{
		RepoRoot: dir, DBPath: dbPath, GraphPath: graphPath,
		At: "main", MergeBaseWith: "HEAD", Force: true, Registry: reg,
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if res.Commit != mainTip {
		t.Fatalf("restored at %s, want the merge base %s", res.Commit, mainTip)
	}

	// --- 2. refresh --structural-batch -1 -----------------------------------
	var refreshed refreshOutput
	out := runRefreshIn(t, dir, "--structural-batch", "-1")
	if err := json.Unmarshal([]byte(out), &refreshed); err != nil {
		t.Fatalf("refresh output is not JSON: %v\n%s", err, out)
	}
	if refreshed.StructuralError != "" || refreshed.Structural == nil {
		t.Fatalf("structural phase did not run: %s", out)
	}
	if refreshed.Structural.Processed > 1 {
		t.Fatalf("structural phase processed %d files after a one-file change: it swept the repository (%s)", refreshed.Structural.Processed, out)
	}

	// --- 3. extract --base main (plan from the merge base) ------------------
	paths.SetProjectRoot(dir)
	t.Cleanup(func() { paths.SetProjectRoot("") })
	s := openRepoStore(t, dir)
	defer s.Close()
	g := graph.New(s, reg)
	var extractOut strings.Builder
	code, err := runExtract(context.Background(), &extractOut, g, dir, filepath.Join(dir, ".depbot"), extractCLIOptions{
		Base: "main", Head: "HEAD", Concurrency: 1,
		Model: "fake:" + filepath.Join("testdata", "extract-fake"),
	}, noEnv)
	if code != 0 || err != nil {
		t.Fatalf("extract: code=%d err=%v\n%s", code, err, extractOut.String())
	}
	if !strings.HasPrefix(extractOut.String(), "chronicle extract: 1 files, 1 extracted") {
		t.Fatalf("extract must read exactly the branch's file (not main's later edit of src/b.ts): %q", extractOut.String())
	}

	// --- 4. graph update ----------------------------------------------------
	if _, _, err := runGraphUpdate(s, reg, graphPath, false); err != nil {
		t.Fatalf("graph update: %v", err)
	}

	// --- 5. review main...HEAD --head-snapshot .depbot/graph.jsonl ----------
	headSrc := review.FileSource{RepoRoot: dir, Path: graphPath, Rev: "HEAD"}
	vg, err := headSrc.Load(context.Background())
	if err != nil {
		t.Fatalf("load head snapshot: %v", err)
	}
	defer vg.Close()
	state := map[string]snapshot.FileState{}
	for _, f := range vg.Freshness.Files {
		state[f.Path] = f.State
	}
	for _, p := range []string{"src/a.ts", "src/b.ts"} {
		if state[p] != snapshot.Fresh {
			t.Errorf("%s is %q in the head graph, want fresh (all: %v)", p, state[p], state)
		}
	}

	result, err := review.Review(context.Background(), review.ReviewInput{
		// `review main...HEAD` compares against the merge base, not main's tip.
		BaseSource: review.SnapshotSource{RepoRoot: dir, Rev: mainTip},
		HeadSource: headSrc,
		RepoRoot:   dir,
		Depth:      4,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	for _, f := range result.Findings {
		if f.Severity == review.SeverityUnknown {
			t.Errorf("unexpected unknown finding %+v: the files the pipeline re-read must not read as unscanned or stale", f)
		}
	}
}

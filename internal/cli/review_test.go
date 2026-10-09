package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// --- fixture helpers --------------------------------------------------

// writeReviewManifestCLI writes a one-domain manifest whose scan scope
// matches nothing this file's fixtures create (mirroring review/result_test.
// go's own scopedManifestT choice): every commit these tests make touches
// .depbot/graph.jsonl itself, and a scope of "**" would put that file in its
// own scan scope, producing a spurious Unknown finding on every test. Not
// shared with the review package's own _test.go helpers of the same shape —
// see review/graph_test.go's own comment: the two packages must not depend
// on each other's test files.
func writeReviewManifestCLI(t *testing.T, dir string) {
	t.Helper()
	writePlainFile(t, dir, ".depbot/chronicle.domain.yaml", ""+
		"domains:\n"+
		"  - name: tomandjerry\n"+
		"    scan:\n"+
		"      include:\n"+
		"        - \"zzz-nomatch/**\"\n")
}

// writeReviewSnapshotCLI encodes snap via the real snapshot.Encode (so the
// fixture is exactly what production code would write, not a hand-rolled
// JSON literal) and writes it at the .depbot/graph.jsonl convention
// review.SnapshotSource resolves.
func writeReviewSnapshotCLI(t *testing.T, dir string, snap *snapshot.Snapshot) {
	t.Helper()
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, snap); err != nil {
		t.Fatalf("snapshot.Encode: %v", err)
	}
	writePlainFile(t, dir, ".depbot/graph.jsonl", buf.String())
}

// buildReviewFixtureRepo builds a 2-commit repo with a REAL committed graph
// snapshot on each commit, in the same shape review/graph_test.go's
// TestReview_E2E_Fixture uses (duplicated rather than imported — internal/cli
// and review must not depend on each other's _test.go files): arena-api
// stops publishing to battle-results (an IntegrationRemoved whose Downstream
// carries an Affected finding for scoreboard-api, still subscribed on base)
// and spectators-api starts calling scoreboard-api directly (an
// IntegrationAdded). No MakeQuerier override anywhere in this file: every
// test below drives the exact real CoreImpactQuerier engine `chronicle
// review` itself uses.
func buildReviewFixtureRepo(t *testing.T) (dir, baseSHA, headSHA string) {
	t.Helper()
	dir = t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writeReviewManifestCLI(t, dir)

	svcArena := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:arena-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "arena-api", Status: "active"}
	svcScoreboard := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:scoreboard-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "scoreboard-api", Status: "active"}
	svcSpectators := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:spectators-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "spectators-api", Status: "active"}
	producer := snapshot.Node{Kind: "node", ID: "code:provider:tomandjerry:arena-api/producer", Layer: "code", Type: "provider", Domain: "tomandjerry", Name: "BattleResultProducer", File: "arena-api/producer.ts", Status: "active"}
	consumer := snapshot.Node{Kind: "node", ID: "code:provider:tomandjerry:scoreboard-api/consumer", Layer: "code", Type: "provider", Domain: "tomandjerry", Name: "BattleResultConsumer", File: "scoreboard-api/consumer.ts", Status: "active"}
	caller := snapshot.Node{Kind: "node", ID: "code:provider:tomandjerry:spectators-api/caller", Layer: "code", Type: "provider", Domain: "tomandjerry", Name: "ScoreCaller", File: "spectators-api/caller.ts", Status: "active"}
	topic := snapshot.Node{Kind: "node", ID: "contract:topic:tomandjerry:battle-results", Layer: "contract", Type: "topic", Domain: "tomandjerry", Name: "battle-results", Status: "active"}
	nodes := []snapshot.Node{svcArena, svcScoreboard, svcSpectators, producer, consumer, caller, topic}

	publishEdge := snapshot.Edge{Kind: "edge", ID: producer.ID + "->" + topic.ID + ":PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: producer.ID, To: topic.ID, Active: true, Derivation: "hard"}
	consumeEdge := snapshot.Edge{Kind: "edge", ID: consumer.ID + "->" + topic.ID + ":CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: consumer.ID, To: topic.ID, Active: true, Derivation: "hard"}
	newCallEdge := snapshot.Edge{Kind: "edge", ID: caller.ID + "->" + svcScoreboard.ID + ":CALLS_SERVICE", Type: "CALLS_SERVICE", From: caller.ID, To: svcScoreboard.ID, Active: true, Derivation: "hard"}

	baseSnap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema, Domains: []string{"tomandjerry"}},
		Nodes:  nodes,
		Edges:  []snapshot.Edge{publishEdge, consumeEdge},
	}
	writeReviewSnapshotCLI(t, dir, baseSnap)
	baseSHA = commitAllCLI(t, dir, "base")

	headSnap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 2, FactSchema: snapshot.FactSchema, Domains: []string{"tomandjerry"}},
		Nodes:  nodes,
		Edges:  []snapshot.Edge{consumeEdge, newCallEdge}, // publishEdge removed, newCallEdge added
	}
	writeReviewSnapshotCLI(t, dir, headSnap)
	headSHA = commitAllCLI(t, dir, "head")

	return dir, baseSHA, headSHA
}

// --- parseRange -------------------------------------------------------

// TestParseRange_Empty covers both halves of the brief's empty-argument
// bullet: base falls back to the nearest resolvable of
// reviewDefaultBaseCandidates and head defaults to "HEAD", or to "" when
// --working-tree is set.
func TestParseRange_Empty(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writePlainFile(t, dir, "a.txt", "v1")
	commitAllCLI(t, dir, "c1")
	// No origin/main, no origin/master, no "master" — only "main" resolves.

	t.Run("head defaults to HEAD", func(t *testing.T) {
		base, head, err := parseRange(dir, "", "", "", false)
		if err != nil {
			t.Fatalf("parseRange: %v", err)
		}
		if base != "main" {
			t.Errorf("base = %q, want %q (the only resolvable candidate)", base, "main")
		}
		if head != "HEAD" {
			t.Errorf("head = %q, want %q", head, "HEAD")
		}
	})

	t.Run("working-tree substitutes empty head", func(t *testing.T) {
		base, head, err := parseRange(dir, "", "", "", true)
		if err != nil {
			t.Fatalf("parseRange: %v", err)
		}
		if base != "main" {
			t.Errorf("base = %q, want %q", base, "main")
		}
		if head != "" {
			t.Errorf("head = %q, want \"\" (working tree)", head)
		}
	})

	t.Run("no candidate resolves is an error", func(t *testing.T) {
		empty := t.TempDir()
		gitRun(t, empty, "init", "-q", "-b", "totally-unrelated-branch-name")
		writePlainFile(t, empty, "a.txt", "v1")
		commitAllCLI(t, empty, "c1")
		if _, _, err := parseRange(empty, "", "", "", false); err == nil {
			t.Fatal("parseRange: err = nil, want an error (no candidate base resolves)")
		}
	})
}

// TestParseRange_DotDot: "A..B" is used exactly as given — no git call at
// all, so a bogus repoRoot is fine here.
func TestParseRange_DotDot(t *testing.T) {
	base, head, err := parseRange("/does/not/exist", "main..feature", "", "", false)
	if err != nil {
		t.Fatalf("parseRange: %v", err)
	}
	if base != "main" || head != "feature" {
		t.Errorf("base,head = %q,%q, want main,feature", base, head)
	}
}

// TestParseRange_DotDotDot: "A...B" resolves base via gitdiff.MergeBaseOf.
// The fixture repo is a straight line (base commit, then head commit, same
// branch), so the merge base of the two is exactly the base commit itself.
func TestParseRange_DotDotDot(t *testing.T) {
	dir, baseSHA, headSHA := buildReviewFixtureRepo(t)

	base, head, err := parseRange(dir, baseSHA+"..."+headSHA, "", "", false)
	if err != nil {
		t.Fatalf("parseRange: %v", err)
	}
	if base != baseSHA {
		t.Errorf("base = %q, want the merge base %q", base, baseSHA)
	}
	if head != headSHA {
		t.Errorf("head = %q, want %q", head, headSHA)
	}
}

// TestParseRange_ExplicitFlagsOverride: --base/--head win regardless of what
// the positional argument (or its absence) would otherwise produce.
func TestParseRange_ExplicitFlagsOverride(t *testing.T) {
	t.Run("overrides an explicit range", func(t *testing.T) {
		base, head, err := parseRange("/does/not/exist", "main..feature", "explicit-base", "explicit-head", false)
		if err != nil {
			t.Fatalf("parseRange: %v", err)
		}
		if base != "explicit-base" || head != "explicit-head" {
			t.Errorf("base,head = %q,%q, want explicit-base,explicit-head", base, head)
		}
	})

	t.Run("overrides the empty-argument default", func(t *testing.T) {
		dir := t.TempDir()
		gitRun(t, dir, "init", "-q", "-b", "main")
		writePlainFile(t, dir, "a.txt", "v1")
		commitAllCLI(t, dir, "c1")

		base, head, err := parseRange(dir, "", "explicit-base", "explicit-head", false)
		if err != nil {
			t.Fatalf("parseRange: %v", err)
		}
		if base != "explicit-base" || head != "explicit-head" {
			t.Errorf("base,head = %q,%q, want explicit-base,explicit-head", base, head)
		}
	})
}

// --- chronicle review CLI (runReview) ----------------------------------

// TestReviewCLI_MarkdownToStdout: the normal Markdown report goes to w when
// --out is not given.
func TestReviewCLI_MarkdownToStdout(t *testing.T) {
	dir, baseSHA, headSHA := buildReviewFixtureRepo(t)

	var out bytes.Buffer
	code, stderrMsg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (no --fail-on); stderr=%q", code, stderrMsg)
	}
	if !strings.Contains(out.String(), "### Chronicle review") {
		t.Errorf("stdout = %q, want it to contain the normal report heading", out.String())
	}
}

// TestReviewCLI_JSONToFile: --out writes the JSON report to a file, leaving w
// untouched.
func TestReviewCLI_JSONToFile(t *testing.T) {
	dir, baseSHA, headSHA := buildReviewFixtureRepo(t)
	outPath := filepath.Join(t.TempDir(), "review.json")

	var out bytes.Buffer
	code, stderrMsg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "json", OutPath: outPath, Depth: 4,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderrMsg)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty (--out was given)", out.String())
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read --out file: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("--out file is not valid JSON: %v\n%s", err, data)
	}
	if _, ok := decoded["Verdict"]; !ok {
		t.Errorf("--out JSON = %s, want a Verdict field", data)
	}
}

// TestReviewCLI_FailOnTriggers: the fixture has a real Affected finding
// (scoreboard-api, downstream of the removed publish) — --fail-on affected
// must exit 1 and name it on stderr.
func TestReviewCLI_FailOnTriggers(t *testing.T) {
	dir, baseSHA, headSHA := buildReviewFixtureRepo(t)

	var out bytes.Buffer
	code, stderrMsg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4,
		FailOn: []string{"affected"},
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (the fixture has a real Affected finding); stderr=%q", code, stderrMsg)
	}
	if !strings.Contains(stderrMsg, "affected") {
		t.Errorf("stderr = %q, want it to name the triggering severity", stderrMsg)
	}
}

// TestReviewCLI_FailOnClean: the SAME fixture (real findings and all) exits 0
// when --fail-on is not given at all — task-14-brief.md's own explicit case.
func TestReviewCLI_FailOnClean(t *testing.T) {
	dir, baseSHA, headSHA := buildReviewFixtureRepo(t)

	var out bytes.Buffer
	code, stderrMsg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (--fail-on not set, even though real findings exist); stderr=%q", code, stderrMsg)
	}
	if stderrMsg != "" {
		t.Errorf("stderrMsg = %q, want empty", stderrMsg)
	}
}

// TestReviewCLI_BothOriginsMissing: a range entirely before Chronicle
// adoption (neither side ever committed .depbot/graph.jsonl) must exit 2, not
// render a misleadingly clean "this change introduces the graph" report.
func TestReviewCLI_BothOriginsMissing(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writePlainFile(t, dir, "a.txt", "v1")
	baseSHA := commitAllCLI(t, dir, "c1")
	writePlainFile(t, dir, "a.txt", "v2")
	headSHA := commitAllCLI(t, dir, "c2")

	var out bytes.Buffer
	code, stderrMsg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4,
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q, stdout=%q", code, stderrMsg, out.String())
	}
	if stderrMsg == "" {
		t.Error("stderrMsg = \"\", want an explanation")
	}
}

// --- wiring -------------------------------------------------------------

// TestNewReviewCmd_Registered is a thin wiring smoke test, mirroring
// TestNewGraphUpdateCmd_RegisteredUnderGraph's own style: `chronicle review`
// must resolve through the real root command and declare every flag the
// brief names.
func TestNewReviewCmd_Registered(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"review"})
	if err != nil {
		t.Fatalf("chronicle review not registered: %v", err)
	}
	for _, name := range []string{"base", "head", "working-tree", "format", "out", "depth", "fail-on"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("chronicle review is missing its --%s flag", name)
		}
	}
}

// TestRunReview_WorkingTree_NoProjectFlag: without --project, paths.Root()
// is "" and used to reach ComputeFreshness("") ("walk working tree: open :
// no such file"). The command's root now comes from the absolute git dir.
func TestRunReview_WorkingTree_NoProjectFlag(t *testing.T) {
	dir, baseSHA, _ := buildReviewFixtureRepo(t)

	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	paths.SetProjectRoot("")
	paths.SetGitDir("")
	t.Cleanup(func() {
		_ = os.Chdir(prev)
		paths.SetProjectRoot("")
		paths.SetGitDir("")
	})

	root, err := reviewRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, stderrMsg := runReview(context.Background(), &out, root, reviewCLIOptions{
		Base: baseSHA, Head: "", Format: "markdown", Depth: 4,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderrMsg)
	}
	if strings.Contains(stderrMsg, "walk working tree") {
		t.Errorf("stderr = %q, must not contain the working-tree walk error", stderrMsg)
	}
	if !strings.Contains(out.String(), "### Chronicle review") {
		t.Errorf("stdout = %q, want the normal report heading", out.String())
	}
}

// TestRunReview_HeadSnapshotFlag: --head-snapshot replaces the head graph
// with the given file; its seq shows in the provenance line.
func TestRunReview_HeadSnapshotFlag(t *testing.T) {
	dir, baseSHA, headSHA := buildReviewFixtureRepo(t)

	data, err := os.ReadFile(filepath.Join(dir, ".depbot", "graph.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	snap, _, err := snapshot.Decode(bytes.NewReader(data), snapshot.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	snap.Header.Seq = 9
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, snap); err != nil {
		t.Fatal(err)
	}
	snapPath := filepath.Join(t.TempDir(), "built.jsonl")
	if err := os.WriteFile(snapPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code, stderrMsg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4, HeadSnapshot: snapPath,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderrMsg)
	}
	if !strings.Contains(out.String(), "Graph v9") {
		t.Errorf("stdout = %q, want the provenance line to carry head seq 9", out.String())
	}
}

// TestRunReview_FooterReproducesHeadSnapshot: the footer's reproduce command
// carries --head-snapshot and the path as given, or it would not reproduce
// the report's bytes.
func TestRunReview_FooterReproducesHeadSnapshot(t *testing.T) {
	dir, baseSHA, headSHA := buildReviewFixtureRepo(t)
	snapPath := filepath.Join(dir, ".depbot", "graph.jsonl")

	cmd := reviewReproCommand("", baseSHA, headSHA, false, snapPath)
	var out bytes.Buffer
	code, stderrMsg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4,
		HeadSnapshot: snapPath, Command: cmd,
	})
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%q", code, stderrMsg)
	}
	if want := "--head-snapshot " + snapPath; !strings.Contains(out.String(), want) {
		t.Errorf("footer lacks %q; output=%q", want, out.String())
	}
	if got := reviewReproCommand("", "", "", false, ""); strings.Contains(got, "--head-snapshot") {
		t.Errorf("repro command without the flag = %q, must not mention it", got)
	}
}

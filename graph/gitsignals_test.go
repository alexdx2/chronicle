package graph

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// gitLogFixture mimics `git log --pretty=format:%H|%ct --name-only` output.
const gitLogFixture = `aaa1|1700000100
src/a.ts
src/b.ts

bbb2|1700000200
src/a.ts
src/b.ts

ccc3|1700000300
src/a.ts
src/b.ts

ddd4|1700000400
src/a.ts

eee5|1700000500
src/c.ts
`

func TestParseGitLog(t *testing.T) {
	commits := parseGitLog(gitLogFixture)
	if len(commits) != 5 {
		t.Fatalf("want 5 commits, got %d", len(commits))
	}
	if len(commits[0].Files) != 2 || commits[0].Files[0] != "src/a.ts" {
		t.Fatalf("commit 0 files = %v", commits[0].Files)
	}
	if commits[0].Timestamp != 1700000100 {
		t.Fatalf("commit 0 ts = %d", commits[0].Timestamp)
	}
}

func TestComputeChurn(t *testing.T) {
	churn := computeChurn(parseGitLog(gitLogFixture))
	if churn["src/a.ts"].Commits != 4 {
		t.Errorf("a.ts commits = %d, want 4", churn["src/a.ts"].Commits)
	}
	if churn["src/a.ts"].LastCommitUnix != 1700000400 {
		t.Errorf("a.ts last = %d, want 1700000400", churn["src/a.ts"].LastCommitUnix)
	}
	if churn["src/c.ts"].Commits != 1 {
		t.Errorf("c.ts commits = %d, want 1", churn["src/c.ts"].Commits)
	}
}

// TestComputeCoupling pins the competitor-derived formula:
// score = co_changes / min(total_a, total_b), thresholds: >=3 co-changes,
// score >= 0.3, commits touching >20 files skipped as refactor noise.
func TestComputeCoupling(t *testing.T) {
	pairs := computeCoupling(parseGitLog(gitLogFixture))
	if len(pairs) != 1 {
		t.Fatalf("want exactly 1 coupled pair, got %d: %+v", len(pairs), pairs)
	}
	p := pairs[0]
	if p.FileA != "src/a.ts" || p.FileB != "src/b.ts" {
		t.Fatalf("pair = %s <-> %s", p.FileA, p.FileB)
	}
	if p.CoChanges != 3 {
		t.Errorf("co_changes = %d, want 3", p.CoChanges)
	}
	// a: 4 commits, b: 3 commits -> score = 3/min(4,3) = 1.0
	if math.Abs(p.Score-1.0) > 1e-9 {
		t.Errorf("score = %v, want 1.0", p.Score)
	}
}

// TestComputeCouplingThresholds proves the noise gates: <3 co-changes never
// couples, and a mega-commit (>20 files) is ignored entirely.
func TestComputeCouplingThresholds(t *testing.T) {
	// Two co-changes only -> below GH_MIN_COMMITS analog.
	two := "a|1\nx.ts\ny.ts\n\nb|2\nx.ts\ny.ts\n"
	if pairs := computeCoupling(parseGitLog(two)); len(pairs) != 0 {
		t.Fatalf("2 co-changes must not couple, got %+v", pairs)
	}

	// A >20-file commit must be skipped: build one commit with 21 files three
	// times; no coupling may emerge from it.
	var mega string
	for c := 0; c < 3; c++ {
		mega += "c" + string(rune('a'+c)) + "|1\n"
		for i := 0; i < 21; i++ {
			mega += "f" + string(rune('a'+i)) + ".ts\n"
		}
		mega += "\n"
	}
	if pairs := computeCoupling(parseGitLog(mega)); len(pairs) != 0 {
		t.Fatalf("mega-commits are refactor noise, must not couple, got %d pairs", len(pairs))
	}
}

// TestComputeGitSignalsEndToEnd builds a real temp git repo with co-changing
// files and proves the pass writes churn Metadata + exact git evidence per node
// and a CHANGES_WITH edge (score-as-confidence) with heuristic evidence.
func TestComputeGitSignalsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	run("init", "-q")
	// 3 commits touching both files together, 1 touching only a.ts.
	for i := 0; i < 3; i++ {
		write("a.ts", fmt.Sprintf("export const a = %d;\n", i))
		write("b.ts", fmt.Sprintf("export const b = %d;\n", i))
		run("add", ".")
		run("commit", "-q", "-m", fmt.Sprintf("c%d", i))
	}
	write("a.ts", "export const a = 99;\n")
	run("add", ".")
	run("commit", "-q", "-m", "solo")

	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	for _, n := range []validate.NodeInput{
		{NodeKey: "code:provider:orders:a", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "A", FilePath: filepath.Join(dir, "a.ts")},
		{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B", FilePath: filepath.Join(dir, "b.ts")},
	} {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}

	if err := g.ComputeGitSignals(revID); err != nil {
		t.Fatalf("ComputeGitSignals: %v", err)
	}

	na, err := g.store.GetNodeByKey("code:provider:orders:a")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if !strings.Contains(na.Metadata, `"commits":4`) {
		t.Errorf("a.ts churn should be 4 commits, metadata: %q", na.Metadata)
	}

	aID, _ := g.store.GetNodeIDByKey("code:provider:orders:a")
	evs, _ := g.store.ListEvidenceByNode(aID)
	churnRows := 0
	for _, e := range evs {
		if e.ExtractorID == "churn-git" && e.SourceKind == "git" {
			churnRows++
			if e.Confidence != 1.0 {
				t.Errorf("churn is an exact git-log count, confidence = %v, want 1.0", e.Confidence)
			}
		}
	}
	if churnRows != 1 {
		t.Fatalf("want 1 churn-git evidence row, got %d", churnRows)
	}

	active := true
	edges, err := g.store.ListEdges(store.EdgeFilter{EdgeType: "CHANGES_WITH", Active: &active})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 CHANGES_WITH edge, got %d", len(edges))
	}
	// a: 4 commits, b: 3 -> score 3/3 = 1.0. Edge confidence is DERIVED from
	// evidence tiers (inferred cap), so the exact score lives in edge Metadata.
	if !strings.Contains(edges[0].Metadata, `"coupling_score":1.0000`) {
		t.Errorf("edge metadata should carry the exact score, got %q", edges[0].Metadata)
	}
	if !strings.Contains(edges[0].Metadata, `"co_changes":3`) {
		t.Errorf("edge metadata should carry co_changes, got %q", edges[0].Metadata)
	}

	// Idempotent: second run must not duplicate evidence or edges.
	if err := g.ComputeGitSignals(revID); err != nil {
		t.Fatalf("ComputeGitSignals (2nd): %v", err)
	}
	edges2, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CHANGES_WITH", Active: &active})
	if len(edges2) != 1 {
		t.Fatalf("re-run duplicated CHANGES_WITH edges: %d", len(edges2))
	}
}

// TestChangesWithExcludedFromImpact proves the analytical CHANGES_WITH edge does
// not inflate the default impact blast radius (structural-policy exclusion).
func TestChangesWithExcludedFromImpact(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	for _, n := range []validate.NodeInput{
		{NodeKey: "code:provider:orders:x", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "X"},
		{NodeKey: "code:provider:orders:y", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Y"},
	} {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:provider:orders:x", ToNodeKey: "code:provider:orders:y",
		EdgeType: "CHANGES_WITH", DerivationKind: "inferred", FromLayer: "code", ToLayer: "code",
		Confidence: 0.9,
	}, revID); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	res, err := g.QueryImpact("code:provider:orders:y", ImpactOptions{})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	for _, n := range res.Impacts {
		if n.NodeKey == "code:provider:orders:x" {
			t.Fatalf("CHANGES_WITH must not drive default impact traversal")
		}
	}
}

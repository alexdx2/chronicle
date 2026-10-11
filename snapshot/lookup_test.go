package snapshot_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/snapshot/storetest"
)

// historyT is a throwaway git repository whose commits are addressed by name.
type historyT struct {
	t    *testing.T
	dir  string
	shas map[string]string
}

func newHistoryT(t *testing.T) *historyT {
	t.Helper()
	h := &historyT{t: t, dir: t.TempDir(), shas: map[string]string{}}
	h.git("init", "-q", "-b", "main")
	return h
}

func (h *historyT) git(args ...string) string {
	h.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", h.dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.Output()
	if err != nil {
		h.t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// commit adds a commit called name on branch, which must already exist (the
// first commit goes on the initial main).
func (h *historyT) commit(branch, name string) string {
	h.t.Helper()
	if len(h.shas) > 0 && h.git("rev-parse", "--abbrev-ref", "HEAD") != branch {
		h.git("checkout", "-q", branch)
	}
	if err := os.WriteFile(filepath.Join(h.dir, name+".txt"), []byte(name), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.git("add", name+".txt")
	h.git("commit", "-q", "-m", name)
	h.shas[name] = h.git("rev-parse", "HEAD")
	return h.shas[name]
}

// checkout creates (or resets) branch at the commit called from and switches to it.
func (h *historyT) checkout(branch, from string) {
	h.t.Helper()
	h.git("checkout", "-q", "-B", branch, h.shas[from])
}

func (h *historyT) orphan() string {
	h.t.Helper()
	tree := h.git("mktree")
	return h.git("commit-tree", "-m", "orphan", tree)
}

// graph builds main: A--B--C, feature: C--D--E, release: B--R.
func graph(t *testing.T) *historyT {
	h := newHistoryT(t)
	h.commit("main", "A")
	h.commit("main", "B")
	h.commit("main", "C")
	h.checkout("feature", "C")
	h.commit("feature", "D")
	h.commit("feature", "E")
	h.checkout("release", "B")
	h.commit("release", "R")
	return h
}

func putAt(t *testing.T, st snapshot.Store, sha, name string, labels ...string) snapshot.Entry {
	t.Helper()
	r, err := st.Put(context.Background(), "r", sha, storetest.Sample(name), snapshot.PutOptions{Labels: labels})
	if err != nil {
		t.Fatal(err)
	}
	return r.Entry
}

func lookup(t *testing.T, st snapshot.Store, h *historyT, q snapshot.LookupQuery, cfg snapshot.LookupConfig) (snapshot.LookupAnswer, error) {
	t.Helper()
	q.Repo = "r"
	dir := ""
	if h != nil {
		dir = h.dir
	}
	return snapshot.Lookup(context.Background(), st, dir, q, cfg)
}

func wantAnswer(t *testing.T, a snapshot.LookupAnswer, err error, sha, match string, dist int) {
	t.Helper()
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if a.CodeSha != sha || a.Match != match || a.Distance != dist {
		t.Fatalf("answer %+v, want sha %s match %s distance %d", a, sha, match, dist)
	}
}

func TestLookup_Exact(t *testing.T) {
	h := graph(t)
	st := snapshot.NewMemStore()
	e := putAt(t, st, h.shas["E"], "e")
	a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"]}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, h.shas["E"], snapshot.MatchExact, 0)
	if a.SnapshotID != e.SnapshotID {
		t.Fatalf("snapshot id %s, want %s", a.SnapshotID, e.SnapshotID)
	}
}

func TestLookup_AncestorNearestWins(t *testing.T) {
	h := graph(t)
	st := snapshot.NewMemStore()
	putAt(t, st, h.shas["B"], "b")
	putAt(t, st, h.shas["D"], "d")
	a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"]}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, h.shas["D"], snapshot.MatchAncestor, 1)

	st = snapshot.NewMemStore()
	putAt(t, st, h.shas["B"], "b")
	a, err = lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"]}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, h.shas["B"], snapshot.MatchAncestor, 3)
}

func TestLookup_AncestorIgnoresSnapshotsOfNonAncestors(t *testing.T) {
	h := graph(t)
	st := snapshot.NewMemStore()
	// Labels do not matter to the ancestor walk: R is on the release chain,
	// but it is not an ancestor of E.
	putAt(t, st, h.shas["R"], "r", snapshot.ChainLabel("release"))
	_, err := lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"]}, snapshot.LookupConfig{Order: []string{"exact", "ancestor"}})
	if !errors.Is(err, snapshot.ErrNoSnapshot) {
		t.Fatalf("E must not find R, got %v", err)
	}
	h.commit("release", "R2")
	a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["R2"]}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, h.shas["R"], snapshot.MatchAncestor, 1)
}

func TestLookup_RebaseSameTree(t *testing.T) {
	h := graph(t)
	h.commit("main", "C2")
	h.checkout("rebased", "E")
	h.git("rebase", "-q", "--onto", h.shas["C2"], h.shas["C"], "rebased")
	eNew := h.git("rev-parse", "HEAD")
	if eNew == h.shas["E"] {
		t.Fatal("rebase did not rewrite E")
	}
	st := snapshot.NewMemStore()
	putAt(t, st, h.shas["C2"], "c2")
	a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: eNew}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, h.shas["C2"], snapshot.MatchAncestor, 2)
}

func TestLookup_RewrittenHistoryFallsToTargetThenDefault(t *testing.T) {
	h := graph(t)
	x := h.orphan()
	st := snapshot.NewMemStore()
	putAt(t, st, h.shas["C"], "c")

	a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: x, TargetBranch: "main"}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, h.shas["C"], snapshot.MatchTarget, -1)
	if !a.Far(50) {
		t.Fatal("a target answer must be far")
	}

	a, err = lookup(t, st, h, snapshot.LookupQuery{Sha: x, TargetSha: h.shas["C"]}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, h.shas["C"], snapshot.MatchTarget, -1)

	a, err = lookup(t, st, h, snapshot.LookupQuery{Sha: x}, snapshot.LookupConfig{DefaultBranch: "main"})
	wantAnswer(t, a, err, h.shas["C"], snapshot.MatchDefault, -1)
}

func TestLookup_DefaultFallsBackToChainTip(t *testing.T) {
	h := graph(t)
	x := h.orphan()
	st := snapshot.NewMemStore()
	putAt(t, st, h.shas["B"], "b", snapshot.ChainLabel("main"))
	a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: x}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, h.shas["B"], snapshot.MatchDefault, -1)
}

func TestLookup_MaxAncestorsCutsTheWalk(t *testing.T) {
	h := graph(t)
	st := snapshot.NewMemStore()
	putAt(t, st, h.shas["B"], "b")
	_, err := lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"]}, snapshot.LookupConfig{MaxAncestors: 2})
	if !errors.Is(err, snapshot.ErrNoSnapshot) {
		t.Fatalf("the walk must stop before B, got %v", err)
	}
	// with a chain label the default step still answers
	st = snapshot.NewMemStore()
	putAt(t, st, h.shas["B"], "b", snapshot.ChainLabel("main"))
	a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"]}, snapshot.LookupConfig{MaxAncestors: 2})
	wantAnswer(t, a, err, h.shas["B"], snapshot.MatchDefault, -1)
}

func TestLookup_OrderIsRespected(t *testing.T) {
	h := graph(t)
	st := snapshot.NewMemStore()
	putAt(t, st, h.shas["E"], "e")
	putAt(t, st, h.shas["C"], "c")
	a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"]}, snapshot.LookupConfig{Order: []string{"default"}})
	wantAnswer(t, a, err, h.shas["C"], snapshot.MatchDefault, -1)

	_, err = lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"]}, snapshot.LookupConfig{Order: []string{"exact", "x"}})
	if err == nil || err.Error() != `lookup: unknown step "x"` {
		t.Fatalf("got %v", err)
	}
}

func TestLookup_UnknownShaWithoutCloneStillExact(t *testing.T) {
	st := snapshot.NewMemStore()
	sha := storetest.Sha(7)
	putAt(t, st, sha, "a")
	a, err := lookup(t, st, nil, snapshot.LookupQuery{Sha: sha}, snapshot.LookupConfig{})
	wantAnswer(t, a, err, sha, snapshot.MatchExact, 0)

	a, err = lookup(t, st, nil, snapshot.LookupQuery{Sha: storetest.Sha(8)}, snapshot.LookupConfig{})
	if !errors.Is(err, snapshot.ErrNoSnapshot) {
		t.Fatalf("got %v", err)
	}
	if !hasWarning(a.Warnings, "ancestor step skipped") {
		t.Fatalf("warnings %v must say why ancestor was skipped", a.Warnings)
	}
}

func TestLookup_BadInputs(t *testing.T) {
	h := graph(t)
	st := snapshot.NewMemStore()
	if _, err := lookup(t, st, h, snapshot.LookupQuery{Sha: "--x"}, snapshot.LookupConfig{}); !errors.Is(err, snapshot.ErrBadSha) {
		t.Fatalf("got %v", err)
	}
	for _, b := range []string{"-x", "a..b"} {
		a, err := lookup(t, st, h, snapshot.LookupQuery{Sha: h.shas["E"], TargetBranch: b}, snapshot.LookupConfig{})
		if !errors.Is(err, snapshot.ErrNoSnapshot) {
			t.Fatalf("branch %q: got %v", b, err)
		}
		if !hasWarning(a.Warnings, "invalid branch") {
			t.Fatalf("branch %q: warnings %v", b, a.Warnings)
		}
	}
}

func TestAnswer_Far(t *testing.T) {
	if !(snapshot.LookupAnswer{Distance: -1}).Far(1000) {
		t.Fatal("distance -1 is always far")
	}
	if !(snapshot.LookupAnswer{Distance: 51}).Far(50) {
		t.Fatal("51 > 50 is far")
	}
	if (snapshot.LookupAnswer{Distance: 50}).Far(50) {
		t.Fatal("50 is not far")
	}
}

func hasWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

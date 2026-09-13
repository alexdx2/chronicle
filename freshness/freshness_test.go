package freshness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func commit(t *testing.T, dir, file string) string {
	os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0644)
	git(t, dir, "add", file)
	git(t, dir, "commit", "-q", "-m", file)
	out := git(t, dir, "rev-parse", "HEAD")
	return out[:len(out)-1]
}

func newRepo(t *testing.T) (string, *store.Store) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return dir, s
}

func TestEmpty(t *testing.T) {
	dir, s := newRepo(t)
	commit(t, dir, "a.ts")
	r, err := Compute(dir, "r", "d", s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "empty" || r.Scanned != nil {
		t.Fatalf("want empty, got %+v", r)
	}
	if r.Head == nil || r.Head.SHA == "" {
		t.Fatalf("empty report must still carry HEAD: %+v", r.Head)
	}
}

func TestFreshThenStaleThenVerified(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	r, _ := Compute(dir, "r", "d", s)
	if r.Status != "fresh" || r.Unscanned.Commits != 0 {
		t.Fatalf("want fresh, got %s %+v", r.Status, r.Unscanned)
	}
	sha2 := commit(t, dir, "b.ts")
	r, _ = Compute(dir, "r", "d", s)
	if r.Status != "stale" || r.Unscanned.Commits != 1 || r.Unscanned.Files != 1 {
		t.Fatalf("want stale +1/1, got %s %+v", r.Status, r.Unscanned)
	}
	if _, err := s.CreateRevision("d", "", sha2, "git_hook", "incremental", `{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}
	r, _ = Compute(dir, "r", "d", s)
	if r.Status != "verified" || r.Scanned.SHA != sha1 || r.Verified.SHA != sha2 || r.Unscanned.Commits != 1 {
		t.Fatalf("want verified scanned=%s verified=%s, got %+v", sha1[:7], sha2[:7], r)
	}
}

func TestDiverged(t *testing.T) {
	dir, s := newRepo(t)
	base := commit(t, dir, "a.ts")
	git(t, dir, "checkout", "-q", "-b", "feat")
	featSHA := commit(t, dir, "f.ts")
	git(t, dir, "checkout", "-q", "main")
	commit(t, dir, "m.ts")
	if _, err := s.CreateRevision("d", "", featSHA, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	r, _ := Compute(dir, "r", "d", s)
	if r.Status != "diverged" || r.Unscanned.MergeBase != base || r.Unscanned.HeadAhead != 1 || r.Unscanned.KnowledgeAhead != 1 {
		t.Fatalf("want diverged at %s, got %s %+v", base[:7], r.Status, r.Unscanned)
	}
}

func TestLayersAndLine(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	s.CreateRevision("d", "", sha1, "manual", "full", "{}")
	sha2 := commit(t, dir, "b.ts")
	s.CreateRevision("d", "", sha2, "manual", "incremental", `{"layer":"ui","source":"docs/surface/d.surface.json"}`)
	r, _ := Compute(dir, "r", "d", s)
	if r.Scanned.SHA != sha1 {
		t.Fatalf("a ui import must not move the code layer: %+v", r.Scanned)
	}
	if r.Layers["ui"] == nil || r.Layers["ui"].SHA != sha2 || r.Layers["ui"].Source == "" {
		t.Fatalf("ui layer missing: %+v", r.Layers)
	}
	if r.Layers["code"] == nil || r.Layers["code"].SHA != sha1 {
		t.Fatalf("code layer must mirror scanned: %+v", r.Layers)
	}
	if got := r.Line(); got == "" || got[:len("knowledge: r ")] != "knowledge: r " {
		t.Fatalf("line: %q", got)
	}
}

func TestNoDomainPicksNewestRevisionDomain(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	r, err := Compute(dir, "r", "", s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Domain != "d" || r.Status != "fresh" {
		t.Fatalf("want domain d / fresh, got %q %q", r.Domain, r.Status)
	}
}

func TestNoGitDegradesWithoutError(t *testing.T) {
	dir := t.TempDir() // not a git repo
	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.CreateRevision("d", "", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	r, err := Compute(dir, "r", "d", s)
	if err != nil {
		t.Fatalf("git failure must not fail Compute: %v", err)
	}
	if r.Head != nil {
		t.Fatalf("head must be empty without git: %+v", r.Head)
	}
	if r.Line() == "" {
		t.Fatal("line must still render")
	}
}

func TestLineShapes(t *testing.T) {
	fresh := &Report{Repo: "auto", Status: "fresh", Scanned: &Point{SHA: "8df170b1111111"}}
	if got, want := fresh.Line(), "knowledge: auto scanned@8df170b · current"; got != want {
		t.Errorf("fresh line = %q, want %q", got, want)
	}
	none := &Report{Repo: "auto", Status: "empty"}
	if got, want := none.Line(), "knowledge: auto · no scan yet"; got != want {
		t.Errorf("empty line = %q, want %q", got, want)
	}
	stale := &Report{
		Repo: "auto", Status: "stale",
		Scanned:   &Point{SHA: "22f9f92aaaa", At: "2026-08-07T10:00:00Z"},
		Verified:  &Point{SHA: "dd193adbbbb"},
		Unscanned: Distance{Commits: 796},
		Touched:   Touched{NodesStale: 118},
	}
	want := "knowledge: auto scanned@22f9f92 (07.08) · verified@dd193ad · 796 unscanned commits · 118 nodes touched"
	if got := stale.Line(); got != want {
		t.Errorf("stale line = %q, want %q", got, want)
	}
}

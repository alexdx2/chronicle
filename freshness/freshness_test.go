package freshness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/extract/rules"
	"github.com/alexdx2/chronicle-core/store"
)

// structuralStamp records a structural phase the way the phase itself does:
// as metadata merged onto whatever revision already sits at that commit, and
// as a revision of its own only when nothing had claimed the commit yet.
func structuralStamp(t *testing.T, s *store.Store, domain, sha string, complete bool) {
	t.Helper()
	rev, err := s.GetRevisionBySHA(domain, sha)
	var id int64
	switch {
	case err == nil:
		id = rev.RevisionID
	default:
		id, err = s.CreateRevision(domain, "", sha, "git_hook", "incremental", `{"kind":"structural"}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateRevisionMetadata(id, map[string]any{
		"structural": map[string]any{"complete": complete, "pack": rules.PackVersion},
	}); err != nil {
		t.Fatal(err)
	}
}

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

	// A revision that pinned no commit is not a scan point: "empty" must not
	// come back with a scanned object attached.
	if _, err := s.CreateRevision("d", "", "", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	r, err = Compute(dir, "r", "d", s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "empty" || r.Scanned != nil || r.Layers["code"] != nil {
		t.Fatalf("revision without a sha must leave scanned nil: %s scanned=%+v layers=%+v",
			r.Status, r.Scanned, r.Layers)
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
		Scanned:   &Point{SHA: "22f9f92aaaa", At: "2026-08-07T10:00:00Z", RevisionID: 1},
		Verified:  &Point{SHA: "dd193adbbbb", RevisionID: 2},
		Unscanned: Distance{Commits: 796},
		Touched:   Touched{NodesStale: 118},
	}
	want := "knowledge: auto scanned@22f9f92 (07.08) · verified@dd193ad · 796 unscanned commits · 118 nodes touched"
	if got := stale.Line(); got != want {
		t.Errorf("stale line = %q, want %q", got, want)
	}
}

// A refresh that predates the scan is older knowledge about an older commit.
// It must neither earn the verified status nor appear on the knowledge line.
func TestRefreshBeforeScanIsNotVerification(t *testing.T) {
	dir, s := newRepo(t)
	commit(t, dir, "a.ts")
	sha2 := commit(t, dir, "b.ts")

	// Hook refreshed at HEAD first...
	if _, err := s.CreateRevision("d", "", sha2, "git_hook", "incremental", `{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}
	// ...then an older checkout was scanned.
	sha1 := git(t, dir, "rev-parse", "HEAD~1")
	sha1 = sha1[:len(sha1)-1]
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}

	r, err := Compute(dir, "r", "d", s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "stale" {
		t.Fatalf("a refresh older than the scan must not read as verified: %s", r.Status)
	}
	if strings.Contains(r.Line(), "verified@") {
		t.Fatalf("line claims a verification that predates the scan: %q", r.Line())
	}
	if r.Verified == nil || r.Verified.SHA != sha2 {
		t.Fatalf("the refresh itself must still be reported: %+v", r.Verified)
	}
}

// The ordering guard must not suppress a real verification.
func TestRefreshAfterScanStillVerifies(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	sha2 := commit(t, dir, "b.ts")
	if _, err := s.CreateRevision("d", "", sha2, "git_hook", "incremental", `{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}
	r, _ := Compute(dir, "r", "d", s)
	if r.Status != "verified" {
		t.Fatalf("want verified, got %s", r.Status)
	}
	if !strings.Contains(r.Line(), "verified@"+sha2[:7]) {
		t.Fatalf("line must name the refresh: %q", r.Line())
	}
}

// --- structured -------------------------------------------------------------

// `structured` is the SHA up to which deterministic structure is guaranteed.
// It moves on its own: a scan at an old commit plus a completed structural
// phase at HEAD is a real, nameable state — the graph knows today's imports,
// routes and models, and yesterday's meanings.
func TestStructuredPointAndStatus(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	sha2 := commit(t, dir, "b.ts")
	structuralStamp(t, s, "d", sha2, true)

	r, err := Compute(dir, "r", "d", s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Structured == nil || r.Structured.SHA != sha2 {
		t.Fatalf("structured point: %+v", r.Structured)
	}
	if r.Status != StatusStructured {
		t.Fatalf("status = %q, want structured", r.Status)
	}
	if r.Scanned.SHA != sha1 {
		t.Fatalf("a structural phase must not move the scanned point: %+v", r.Scanned)
	}
	line := r.Line()
	if !strings.Contains(line, "structured@"+sha2[:7]) {
		t.Fatalf("line must name the structural commit: %q", line)
	}
	if !strings.Contains(line, "scanned@"+sha1[:7]) {
		t.Fatalf("line must still name the scan: %q", line)
	}
}

// An interrupted structural phase guarantees nothing. Whatever it managed to
// write, no structural claim may come out of it — while the verification phase
// that ran before it in the same hook keeps the claim it did earn.
func TestIncompleteStructuralPhaseIsNotAPoint(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	sha2 := commit(t, dir, "b.ts")
	structuralStamp(t, s, "d", sha2, false)

	r, _ := Compute(dir, "r", "d", s)
	if r.Structured != nil {
		t.Fatalf("an incomplete phase must leave no structured point: %+v", r.Structured)
	}
	if strings.Contains(r.Line(), "structured@") {
		t.Fatalf("line claims structure it does not have: %q", r.Line())
	}
}

// Structure at HEAD outranks a verification at HEAD: both are true, and the
// stronger claim is the one the reader needs first.
func TestStructuredWinsOverVerified(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	sha2 := commit(t, dir, "b.ts")
	if _, err := s.CreateRevision("d", "", sha2, "git_hook", "incremental",
		`{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}
	// Both phases of the SAME hook run land on the SAME revision: phase 1
	// creates the row at HEAD, phase 2 stamps metadata.structural onto it
	// (UNIQUE(domain_key, git_after_sha) allows nothing else).
	sha3 := commit(t, dir, "c.ts")
	if _, err := s.CreateRevision("d", "", sha3, "git_hook", "incremental",
		`{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}
	structuralStamp(t, s, "d", sha3, true)

	r, _ := Compute(dir, "r", "d", s)
	if r.Status != StatusStructured {
		t.Fatalf("status = %q, want structured", r.Status)
	}
	if r.Verified == nil || r.Verified.SHA != sha3 {
		t.Fatalf("the same revision is still the refresh pointer: %+v", r.Verified)
	}
	if r.Structured == nil || r.Structured.SHA != sha3 {
		t.Fatalf("structured point: %+v", r.Structured)
	}
}

// A structural pointer behind HEAD is reported but does not claim the status:
// the commits between it and HEAD have no guaranteed structure.
func TestStructuredBehindHeadDoesNotClaimTheStatus(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	sha2 := commit(t, dir, "b.ts")
	structuralStamp(t, s, "d", sha2, true)
	commit(t, dir, "c.ts") // HEAD moved past the structural phase

	r, _ := Compute(dir, "r", "d", s)
	if r.Status != StatusStale {
		t.Fatalf("status = %q, want stale", r.Status)
	}
	if r.Structured == nil || r.Structured.SHA != sha2 {
		t.Fatalf("the structural point is still a fact worth reporting: %+v", r.Structured)
	}
	if !strings.Contains(r.Line(), "structured@"+sha2[:7]) {
		t.Fatalf("line: %q", r.Line())
	}
}

// The rules-pack backlog rides on the structured point: files whose last
// structural look used an older pack are re-extraction work even though
// nothing in them changed. The count comes from the phase's own per-file
// records (project_settings), not from evidence rows — a file that yields
// zero facts has no evidence to hang a version on and would otherwise be
// invisible backlog forever.
func TestStructuredCarriesTheOldRulesBacklog(t *testing.T) {
	dir, s := newRepo(t)
	sha1 := commit(t, dir, "a.ts")
	if _, err := s.CreateRevision("d", "", sha1, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"old-a.ts", "old-b.ts"} {
		if err := s.SetStructuralHash("d", f, "deadbeef", "0"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetStructuralHash("d", "current.ts", "cafe", rules.PackVersion); err != nil {
		t.Fatal(err)
	}
	sha2 := commit(t, dir, "b.ts")
	structuralStamp(t, s, "d", sha2, true)

	r, _ := Compute(dir, "r", "d", s)
	if r.Structured == nil || r.Structured.OldRules != 2 {
		t.Fatalf("old-rules backlog: %+v", r.Structured)
	}
	if !strings.Contains(r.Line(), "2 files on old rules") {
		t.Fatalf("line must carry the backlog: %q", r.Line())
	}
}

// The whole line, in the shape the knowledge block prints it.
func TestStructuredLineShape(t *testing.T) {
	r := &Report{
		Repo: "auto", Status: StatusStructured,
		Scanned:    &Point{SHA: "22f9f92aaaa", At: "2026-08-07T10:00:00Z", RevisionID: 1},
		Structured: &Point{SHA: "4cd10d1cccc", RevisionID: 3, OldRules: 12},
		Verified:   &Point{SHA: "dd193adbbbb", RevisionID: 2},
		Unscanned:  Distance{Commits: 798},
	}
	want := "knowledge: auto scanned@22f9f92 (07.08) structured@4cd10d1 · verified@dd193ad · 798 unscanned commits · 12 files on old rules"
	if got := r.Line(); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// The closed set the dashboard and the pro lane switch on.
func TestStatusStructuredIsInTheClosedSet(t *testing.T) {
	if StatusStructured != "structured" {
		t.Fatalf("StatusStructured = %q", StatusStructured)
	}
}

// A structural phase that finished at the same commit the scan was taken from
// has nothing of its own to say: "scanned@abc structured@abc" is the same fact
// twice, and the line's whole job is to be read at a glance.
func TestStructuredIsNotRepeatedWhenItEqualsTheScan(t *testing.T) {
	r := &Report{
		Repo: "auto", Status: StatusFresh,
		Scanned:    &Point{SHA: "22f9f92aaaa", At: "2026-08-07T10:00:00Z", RevisionID: 1},
		Structured: &Point{SHA: "22f9f92aaaa", RevisionID: 1},
	}
	want := "knowledge: auto scanned@22f9f92 · current"
	if got := r.Line(); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	// The backlog is still the structural point's to report, even then.
	r.Structured.OldRules = 3
	if got := r.Line(); !strings.Contains(got, "3 files on old rules") {
		t.Errorf("line dropped the backlog with the repeated point: %q", got)
	}
}

// VerifiedAfterScan is the one rule for "is there a re-verification worth
// naming" — exported because the federated line has to answer it the same way
// core's does, and a second copy of the rule is a second chance to get it wrong.
func TestVerifiedAfterScanIsTheExportedRule(t *testing.T) {
	scanned := &Point{SHA: "aaa", RevisionID: 5}
	if (&Report{Scanned: scanned, Verified: &Point{SHA: "bbb", RevisionID: 6}}).VerifiedAfterScan() != true {
		t.Error("a refresh newer than the scan is a verification worth naming")
	}
	if (&Report{Scanned: scanned, Verified: &Point{SHA: "bbb", RevisionID: 4}}).VerifiedAfterScan() != false {
		t.Error("a refresh older than the scan is older knowledge, not newer")
	}
	if (&Report{Scanned: scanned}).VerifiedAfterScan() != false {
		t.Error("no refresh at all is not a verification")
	}
}

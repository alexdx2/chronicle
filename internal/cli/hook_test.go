package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/freshness"
	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/store"
)

// gitRun runs a git command in dir, failing the test on error. Shared by any
// test in this package that needs a real git repo.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitCapture runs a git command in dir and returns its stdout, failing the
// test on error.
func gitCapture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func TestMergeHookIntoSettings_Empty(t *testing.T) {
	out, changed, err := wiring.MergeHookIntoSettings(nil, "Grep|Glob|Read", "chronicle hook fire")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on empty settings")
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	hooks, ok := parsed["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key missing: %v", parsed)
	}
	pre, ok := hooks["PreToolUse"].([]any)
	if !ok || len(pre) != 1 {
		t.Fatalf("expected one PreToolUse entry, got %v", hooks["PreToolUse"])
	}
	entry := pre[0].(map[string]any)
	if entry["matcher"] != "Grep|Glob|Read" {
		t.Errorf("matcher mismatch: %v", entry["matcher"])
	}
}

func TestMergeHookIntoSettings_Idempotent(t *testing.T) {
	out1, _, err := wiring.MergeHookIntoSettings(nil, "Grep|Glob|Read", "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	out2, changed, err := wiring.MergeHookIntoSettings(out1, "Grep|Glob|Read", "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second install must be a no-op (changed=false)")
	}
	if string(out1) != string(out2) {
		t.Errorf("idempotent merge changed bytes:\n%s\nvs\n%s", out1, out2)
	}
}

func TestMergeHookIntoSettings_PreservesOtherKeys(t *testing.T) {
	existing := []byte(`{"model":"opus","permissions":{"allow":["Bash"]},"hooks":{"PostToolUse":[{"matcher":"Edit","hooks":[]}]}}`)
	out, changed, err := wiring.MergeHookIntoSettings(existing, "Grep|Glob|Read", "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "opus" {
		t.Error("model key lost")
	}
	if parsed["permissions"] == nil {
		t.Error("permissions key lost")
	}
	hooks := parsed["hooks"].(map[string]any)
	if hooks["PostToolUse"] == nil {
		t.Error("existing PostToolUse hook lost")
	}
	if hooks["PreToolUse"] == nil {
		t.Error("PreToolUse not added")
	}
}

func TestRemoveHookFromSettings(t *testing.T) {
	installed, _, _ := wiring.MergeHookIntoSettings(
		[]byte(`{"model":"opus"}`), "Grep|Glob|Read", "chronicle hook fire")
	out, changed, err := removeHookFromSettings(installed, "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected removal to change settings")
	}
	if strings.Contains(string(out), "chronicle hook fire") {
		t.Errorf("chronicle hook not removed: %s", out)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "opus" {
		t.Error("unrelated key lost during removal")
	}
}

func TestRemoveHookFromSettings_PreservesForeignPreToolUse(t *testing.T) {
	existing := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other-tool"}]}]}}`)
	installed, _, _ := wiring.MergeHookIntoSettings(existing, "Grep|Glob|Read", "chronicle hook fire")
	out, _, err := removeHookFromSettings(installed, "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "other-tool") {
		t.Errorf("foreign PreToolUse hook must survive removal: %s", out)
	}
	if strings.Contains(string(out), "chronicle hook fire") {
		t.Errorf("chronicle hook should be gone: %s", out)
	}
}

// ─── hookAdvisoryFor: ghost DB silence ─────────────────────────────────────

func TestHookAdvisorySilentOnEmptyGraph(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "chronicle.db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got := hookAdvisoryFor(db, dir); got != "" {
		t.Fatalf("empty graph must stay silent, got %q", got)
	}
}

func TestHookAdvisorySpeaksWithRevision(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "chronicle.db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("d", "", "deadbeef", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got := hookAdvisoryFor(db, dir); got == "" {
		t.Fatalf("graph with a revision must nudge")
	}
}

// TestHookAdvisoryMeasuresWorktreeHEADNotMainHEAD guards against staleness
// math running "git -C <main>" after graph resolution: the graph lives in
// main's .depbot, but a linked worktree can be on a branch main's checked-out
// ref knows nothing about. commitsBehindIn must be told the worktree's own
// dir so "N commits behind HEAD" reflects the worktree's actual HEAD, not
// main's (which never moves when you commit inside the worktree).
func TestHookAdvisoryMeasuresWorktreeHEADNotMainHEAD(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.MkdirAll(main, 0755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, main, "init", "-q", "-b", "main")
	gitRun(t, main, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "base")
	baseSHA := strings.TrimSpace(gitCapture(t, main, "rev-parse", "HEAD"))

	wt := filepath.Join(root, "wt")
	gitRun(t, main, "worktree", "add", "-q", wt, "-b", "feat")
	// The worktree moves one commit ahead of main's checked-out ref. Main's
	// own HEAD never changes — this is exactly the case that "git -C main"
	// would get wrong (0 commits behind instead of 1).
	gitRun(t, wt, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "ahead")

	db := filepath.Join(main, ".depbot", "chronicle.db")
	if err := os.MkdirAll(filepath.Dir(db), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("d", "", baseSHA, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	got := hookAdvisoryFor(db, wt)
	if !strings.Contains(got, "1 unscanned commit") {
		t.Fatalf("expected 1 unscanned commit measured against the worktree's HEAD, got %q", got)
	}
}

// The nudge and chronicle_freshness must never disagree: the hook used to
// hand-roll its own "last scanned at X, N commits behind" from the newest
// revision of ANY kind, which in the live okeep run named the surface
// extract's commit — a commit the code layer was never scanned at.
func TestHookAdvisoryReportsTheSameFreshnessTheToolDoes(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "one")
	scanned := strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD"))
	gitRun(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "two")
	uiCommit := strings.TrimSpace(gitCapture(t, dir, "rev-parse", "HEAD"))

	db := filepath.Join(dir, ".depbot", "chronicle.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("auto", "", scanned, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	// A surface import at a LATER commit: the newest revision, and not the
	// commit the code knowledge came from.
	if _, err := s.CreateRevision("auto", "", uiCommit, "manual", "incremental", `{"layer":"ui"}`); err != nil {
		t.Fatal(err)
	}
	rep, err := freshness.Compute(dir, filepath.Base(dir), "", s)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	got := hookAdvisoryFor(db, dir)
	if !strings.Contains(got, rep.Message) {
		t.Fatalf("the nudge must carry the freshness report's own line.\n got: %q\nwant it to contain: %q", got, rep.Message)
	}
	if strings.Contains(got, shortSHA(uiCommit)) {
		t.Errorf("the nudge named the surface extract's commit as the scan point: %q", got)
	}
}

// TestHookAdvisoryGhostDBDoesNotBurnRateLimit guards the ordering fix: a ghost
// DB (no revision yet) must not touch the 10-minute rate-limit marker, or a
// scan that lands moments later could still be suppressed for the rest of
// that window.
func TestHookAdvisoryGhostDBDoesNotBurnRateLimit(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer func() {
		os.Chdir(cwd)
		projectPath = ""
		chronicleDir = ".depbot"
		chronicleDirExplicit = false
		paths.SetProjectRoot("")
		paths.SetChronicleDir(".depbot")
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	projectPath = ""
	chronicleDir = ".depbot"
	chronicleDirExplicit = false
	paths.SetProjectRoot("")
	paths.SetChronicleDir(".depbot")

	depbot := filepath.Join(dir, ".depbot")
	if err := os.MkdirAll(depbot, 0755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(depbot, "chronicle.db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	if got := hookAdvisory(); got != "" {
		t.Fatalf("ghost DB must stay silent, got %q", got)
	}

	// A scan just landed — the DB now has a revision. Because the ghost check
	// above must not have touched the rate limiter, this must fire right away
	// instead of waiting out the 10-minute window.
	s2, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.CreateRevision("d", "", "", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	s2.Close()

	if got := hookAdvisory(); got == "" {
		t.Fatalf("advisory must fire immediately once the ghost DB gains a revision — the rate limiter must not have been burned by the earlier ghost check")
	}
}

// TestHookAdvisoryChecksRateWindowBeforeReadingAnything guards the other half
// of the ordering: inside the 10-minute window the answer is "" whatever the
// graph holds, so the store must never be opened and git must never run. This
// fires before every Grep/Glob/Read — the cost of computing a line nobody will
// see lands directly on the agent's hot path.
func TestHookAdvisoryChecksRateWindowBeforeReadingAnything(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	realCompute := hookCompute
	defer func() {
		os.Chdir(cwd)
		hookCompute = realCompute
		projectPath = ""
		chronicleDir = ".depbot"
		chronicleDirExplicit = false
		paths.SetProjectRoot("")
		paths.SetChronicleDir(".depbot")
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	projectPath = ""
	chronicleDir = ".depbot"
	chronicleDirExplicit = false
	paths.SetProjectRoot("")
	paths.SetChronicleDir(".depbot")

	depbot := filepath.Join(dir, ".depbot")
	if err := os.MkdirAll(depbot, 0755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(depbot, "chronicle.db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("d", "", "", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	computed := 0
	hookCompute = func(dbPath, repoDir string) string {
		computed++
		return "advisory"
	}

	if got := hookAdvisory(); got != "advisory" {
		t.Fatalf("first call must speak, got %q", got)
	}
	if computed != 1 {
		t.Fatalf("first call must compute once, computed=%d", computed)
	}

	// Second call is inside the window: silent, and nothing read.
	if got := hookAdvisory(); got != "" {
		t.Fatalf("second call inside the window must be silent, got %q", got)
	}
	if computed != 1 {
		t.Fatalf("inside the rate window the store must not be opened and git must not run; computed=%d", computed)
	}

	// Age the marker past the window: the advisory computes again.
	old := time.Now().Add(-11 * time.Minute)
	if err := os.Chtimes(hookMarkerPath(), old, old); err != nil {
		t.Fatal(err)
	}
	if got := hookAdvisory(); got != "advisory" {
		t.Fatalf("past the window the advisory must fire again, got %q", got)
	}
	if computed != 2 {
		t.Fatalf("computed=%d, want 2", computed)
	}
}

// A silent case must leave the marker untouched — not merely un-updated, but
// never created, so the next real advisory fires immediately.
func TestHookAdvisoryGhostDBNeverCreatesMarker(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer func() {
		os.Chdir(cwd)
		projectPath = ""
		chronicleDir = ".depbot"
		chronicleDirExplicit = false
		paths.SetProjectRoot("")
		paths.SetChronicleDir(".depbot")
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	projectPath = ""
	chronicleDir = ".depbot"
	chronicleDirExplicit = false
	paths.SetProjectRoot("")
	paths.SetChronicleDir(".depbot")

	depbot := filepath.Join(dir, ".depbot")
	if err := os.MkdirAll(depbot, 0755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(depbot, "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	if got := hookAdvisory(); got != "" {
		t.Fatalf("ghost DB must stay silent, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(depbot, "hook-last-fire")); err == nil {
		t.Fatal("a silent advisory must not create the rate-limit marker")
	}
}

// --git-path honours core.hooksPath, so in a repo using husky or lefthook the
// path Chronicle writes to is the project's own TRACKED hook script. Replacing
// it would delete the user's lint-and-test run and show up as a modified file
// in git status.
func TestHookInstallGitRefusesToOverwriteSomebodyElsesHook(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "core.hooksPath", ".husky")
	if err := os.MkdirAll(filepath.Join(dir, ".husky"), 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := "#!/bin/sh\nnpm run lint && npm test\n"
	hook := filepath.Join(dir, ".husky", "post-commit")
	if err := os.WriteFile(hook, []byte(theirs), 0o755); err != nil {
		t.Fatal(err)
	}

	restore := chdir(t, dir)
	defer restore()

	err := installGitPostCommit()
	if err == nil {
		t.Fatal("Chronicle overwrote a hook it did not write")
	}
	got, rerr := os.ReadFile(hook)
	if rerr != nil || string(got) != theirs {
		t.Fatalf("their hook = %q (err %v), want it untouched", got, rerr)
	}

	// Our own hook is still ours to rewrite: that is how the baked-in binary
	// path gets refreshed after a move or an upgrade.
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"+freshness.CommitHookMarker+" old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installGitPostCommit(); err != nil {
		t.Fatalf("re-installing over our own hook: %v", err)
	}
	after, _ := os.ReadFile(hook)
	if !strings.Contains(string(after), "refresh --quiet") {
		t.Errorf("re-install did not write the refresh line: %q", after)
	}
}

// chdir moves into dir for the duration of a test; installGitPostCommit reads
// the repo from the process's working directory.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { os.Chdir(prev) }
}

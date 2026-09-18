package freshness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/store"
)

func upkeepRepo(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return dir, s
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := gitutil.Run(dir, args...); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestUpkeepReportsBothHooksMissing(t *testing.T) {
	dir, s := upkeepRepo(t)
	u, err := ComputeUpkeep(dir, "", "d", s, UpkeepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if u.CommitHook.Installed || u.AgentHook.Installed {
		t.Fatalf("nothing is installed, yet commit=%v agent=%v", u.CommitHook.Installed, u.AgentHook.Installed)
	}
	if u.Status != UpkeepUnattended {
		t.Errorf("status = %q, want %q — without a commit hook nothing moves this graph", u.Status, UpkeepUnattended)
	}
	// Naming a gap without naming the fix sends the reader looking.
	if u.CommitHook.Fix == "" || u.AgentHook.Fix == "" {
		t.Error("a missing hook must carry the command that installs it")
	}
}

func TestUpkeepFindsTheAgentHookInSettings(t *testing.T) {
	dir, s := upkeepRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The absolute binary path is baked in at install time, so the marker has
	// to match whatever that path turned out to be.
	settings := `{"hooks":{"PreToolUse":[{"hooks":[{"command":"\"/opt/some/where/chronicle\" hook fire"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := ComputeUpkeep(dir, "", "d", s, UpkeepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !u.AgentHook.Installed {
		t.Error("an installed agent hook was not found")
	}
}

// The commit hook is looked up through `git rev-parse --git-path hooks`, which
// honours core.hooksPath — so a repo using husky is checked where git actually
// looks rather than where .git/hooks would be.
func TestUpkeepFindsTheCommitHookThroughHooksPath(t *testing.T) {
	dir, s := upkeepRepo(t)
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "core.hooksPath", ".husky")
	if err := os.MkdirAll(filepath.Join(dir, ".husky"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".husky", "post-commit"),
		[]byte("#!/bin/sh\n"+CommitHookMarker+" refresh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	u, err := ComputeUpkeep(dir, "", "d", s, UpkeepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !u.CommitHook.Installed {
		t.Errorf("the hook under core.hooksPath was not found; looked at %q", u.CommitHook.Path)
	}
}

// A hand-written post-commit is not ours. Reporting it as installed would tell
// the reader the graph is being refreshed while nothing refreshes it.
func TestUpkeepDoesNotClaimSomebodyElsesCommitHook(t *testing.T) {
	dir, s := upkeepRepo(t)
	mustGit(t, dir, "init", "-q", "-b", "main")
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "post-commit"), []byte("#!/bin/sh\nnpm test\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	u, err := ComputeUpkeep(dir, "", "d", s, UpkeepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if u.CommitHook.Installed {
		t.Error("somebody else's post-commit was reported as Chronicle's")
	}
}

// Coverage is the gap a freshness report cannot show: the graph can be
// perfectly current about the half of the repo it happens to know.
func TestUpkeepCoverageCountsWhatTheGraphNeverRead(t *testing.T) {
	dir, s := upkeepRepo(t)
	mustGit(t, dir, "init", "-q", "-b", "main")
	for _, f := range []string{"a.ts", "b.ts", "c.ts", "notes.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("export const x = 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "one")

	revID, err := s.CreateRevision("d", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:module:d:a", Layer: "code", NodeType: "module", DomainKey: "d",
		Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEvidence(store.EvidenceRow{
		TargetKind: "node", NodeID: id, SourceKind: "file", FilePath: "a.ts",
		LineStart: 1, ExtractorID: "x", ExtractorVersion: "1.0",
		Confidence: 1, AssertionKind: "import", Metadata: "{}", ValidFromRevisionID: revID,
	}); err != nil {
		t.Fatal(err)
	}

	u, err := ComputeUpkeep(dir, "", "d", s, UpkeepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// notes.md is not a gap: it is not something Chronicle extracts from.
	if u.Coverage.Supported != 3 {
		t.Errorf("supported = %d, want the 3 .ts files", u.Coverage.Supported)
	}
	if u.Coverage.Known != 1 || u.Coverage.Missing != 2 {
		t.Errorf("known/missing = %d/%d, want 1/2", u.Coverage.Known, u.Coverage.Missing)
	}
	if u.Coverage.Percent != 33 {
		t.Errorf("percent = %d, want 33", u.Coverage.Percent)
	}
}

// InScope is what keeps one domain from reporting another domain's files as
// its own gap.
func TestUpkeepCoverageHonoursScope(t *testing.T) {
	dir, s := upkeepRepo(t)
	mustGit(t, dir, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.ts", "other/b.ts"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("export const x = 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "one")

	u, err := ComputeUpkeep(dir, "", "d", s, UpkeepOptions{
		InScope: func(p string) bool { return !filepath.IsAbs(p) && len(p) > 0 && p[0] != 'o' },
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.Coverage.Supported != 1 {
		t.Errorf("supported = %d, want only the in-scope file", u.Coverage.Supported)
	}
}

func TestUpkeepSurvivesARepoWithoutGit(t *testing.T) {
	dir, s := upkeepRepo(t)
	u, err := ComputeUpkeep(dir, "", "d", s, UpkeepOptions{})
	if err != nil {
		t.Fatalf("a repo without git must still report its hooks: %v", err)
	}
	if u.Coverage.Supported != 0 {
		t.Errorf("coverage = %d supported files with no git to ask", u.Coverage.Supported)
	}
	if u.Repo == "" {
		t.Error("the report has no repo name")
	}
}

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

// The PreToolUse hook fires before Grep/Glob/Read and injects a non-blocking
// reminder that a Chronicle graph exists. It NEVER blocks — exit 0 always,
// permissionDecision "allow". The reminder reports staleness honestly so the
// agent is never pushed toward a stale graph.

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Manage Chronicle git/agent hooks",
	}
	cmd.AddCommand(newHookInstallCmd(), newHookUninstallCmd(), newHookStatusCmd(), newHookFireCmd())
	return cmd
}

// hookCommand returns the command string written into settings.json. It embeds
// the absolute binary path resolved at install time so the hook fires even when
// ~/.local/bin is not on PATH (GUI git clients, restricted shells).
func hookCommand() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "chronicle hook fire"
	}
	return fmt.Sprintf("%q hook fire", exe)
}

func settingsPath() string {
	base := "."
	if projectPath != "" {
		base = projectPath
	}
	return filepath.Join(base, ".claude", "settings.json")
}

func newHookInstallCmd() *cobra.Command {
	var withGit bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the PreToolUse nudge into .claude/settings.json",
		Run: func(cmd *cobra.Command, args []string) {
			path := settingsPath()
			existing, _ := os.ReadFile(path)
			out, changed, err := wiring.MergeHookIntoSettings(existing, wiring.HookMatcher, hookCommand())
			if err != nil {
				outputError(err)
			}
			if !changed {
				fmt.Println("PreToolUse hook already installed —", path)
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					outputError(err)
				}
				if err := os.WriteFile(path, out, 0644); err != nil {
					outputError(err)
				}
				fmt.Println("Installed PreToolUse nudge →", path)
			}
			if withGit {
				if err := installGitPostCommit(); err != nil {
					outputError(err)
				}
				fmt.Println("Installed git post-commit refresh hook")
			}
		},
	}
	cmd.Flags().BoolVar(&withGit, "git", false, "Also install a git post-commit hook that runs 'chronicle refresh'")
	return cmd
}

func newHookUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Chronicle PreToolUse hook from .claude/settings.json",
		Run: func(cmd *cobra.Command, args []string) {
			path := settingsPath()
			existing, err := os.ReadFile(path)
			if err != nil {
				fmt.Println("No settings file —", path)
				return
			}
			out, changed, err := removeHookFromSettings(existing, hookFireMarker())
			if err != nil {
				outputError(err)
			}
			if !changed {
				fmt.Println("No Chronicle hook found in", path)
				return
			}
			if err := os.WriteFile(path, out, 0644); err != nil {
				outputError(err)
			}
			fmt.Println("Removed Chronicle PreToolUse hook from", path)
		},
	}
}

func newHookStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the Chronicle PreToolUse hook is installed",
		Run: func(cmd *cobra.Command, args []string) {
			path := settingsPath()
			existing, err := os.ReadFile(path)
			if err != nil {
				fmt.Println("not installed (no settings file)")
				return
			}
			if strings.Contains(string(existing), hookFireMarker()) {
				fmt.Println("installed →", path)
			} else {
				fmt.Println("not installed →", path)
			}
		},
	}
}

func newHookFireCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "fire",
		Short:  "Emit the PreToolUse advisory (called by the hook; reads stdin, never blocks)",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			io.Copy(io.Discard, os.Stdin) // drain hook payload; content is irrelevant
			ctx := hookAdvisory()
			if ctx != "" {
				payload := map[string]any{
					"hookSpecificOutput": map[string]any{
						"hookEventName":      "PreToolUse",
						"permissionDecision": "allow",
						"additionalContext":  ctx,
					},
				}
				b, _ := json.Marshal(payload)
				fmt.Println(string(b))
			}
			os.Exit(0) // never block, regardless of advisory state
		},
	}
}

// hookFireMarker is the stable substring identifying our hook command across
// the absolute-path variations baked in at install time.
func hookFireMarker() string { return "hook fire" }

// --- pure settings transforms (unit-tested) --------------------------------

func removeHookFromSettings(existing []byte, marker string) ([]byte, bool, error) {
	root := map[string]any{}
	if err := json.Unmarshal(existing, &root); err != nil {
		return nil, false, fmt.Errorf("settings.json is not valid JSON: %w", err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		return existing, false, nil
	}
	pre, _ := hooks["PreToolUse"].([]any)
	if pre == nil {
		return existing, false, nil
	}
	var kept []any
	for _, e := range pre {
		if wiring.EntryHasChronicleHook(e) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == len(pre) {
		return existing, false, nil
	}
	if len(kept) == 0 {
		delete(hooks, "PreToolUse")
	} else {
		hooks["PreToolUse"] = kept
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// --- advisory text ----------------------------------------------------------

// hookAdvisory builds the reminder, or "" when there is no graph or the graph
// has never been scanned (a DB with zero revisions is a ghost, not knowledge).
//
// Order matters twice. The rate window is checked FIRST, and read-only: this
// runs before every Grep/Glob/Read, and inside the window the answer is "" no
// matter what the graph says — so opening the store and shelling out to git to
// compute a line nobody will see is pure latency on the agent's hot path. The
// marker is touched LAST, only once there is something to say: a ghost DB (or
// any other silent case) must not burn the window, or a real advisory could be
// suppressed for 10 minutes by an empty-graph check that had nothing to report.
func hookAdvisory() string {
	resolveWorktreeGraph()
	resolveDefaults()
	if _, err := os.Stat(dbPath); err != nil {
		return "" // no graph in this project
	}
	if withinRateWindow() {
		return "" // already nudged recently — say nothing, and read nothing
	}
	advisory := hookCompute(dbPath, repoDirForGit())
	if advisory == "" {
		return ""
	}
	touchRateMarker()
	return advisory
}

// hookCompute is the one step that opens the store and runs git. It is a
// variable so a test can prove it is never reached inside the rate window.
var hookCompute = hookAdvisoryFor

// hookAdvisoryFor is the testable body: it opens dbPath, and speaks only when a
// revision exists. repoDir is where git runs for the commits-behind count.
func hookAdvisoryFor(dbPath, repoDir string) string {
	s, err := store.Open(dbPath)
	if err != nil {
		return ""
	}
	defer s.Close()

	rev, err := s.LatestRevisionAnyDomain()
	if err != nil || rev == nil {
		return "" // ghost DB: no scan has ever happened here
	}

	staleness := ""
	if rev.GitAfterSHA != "" {
		if behind := commitsBehindIn(repoDir, rev.GitAfterSHA); behind > 0 {
			staleness = fmt.Sprintf(" (graph last scanned at %s, %d commit(s) behind HEAD — run 'chronicle refresh' or rescan if results look incomplete)", shortSHA(rev.GitAfterSHA), behind)
		}
	}
	return "A Chronicle knowledge graph exists for this project" + staleness +
		". Prefer chronicle_node_search(q=...) to resolve a name, then chronicle_query_deps / chronicle_impact / chronicle_subgraph, over grepping files for architecture questions."
}

// hookWindow is how long one advisory stands: repeated Grep/Read calls in one
// burst get exactly one reminder.
const hookWindow = 10 * time.Minute

func hookMarkerPath() string {
	return filepath.Join(filepath.Dir(dbPath), "hook-last-fire")
}

// withinRateWindow reports whether an advisory already fired recently. Pure
// read: it never creates or touches the marker, so asking the question costs
// nothing and cannot suppress a later advisory.
func withinRateWindow() bool {
	info, err := os.Stat(hookMarkerPath())
	return err == nil && time.Since(info.ModTime()) < hookWindow
}

// touchRateMarker starts a new window. Called only after an advisory that will
// actually be delivered.
func touchRateMarker() {
	marker := hookMarkerPath()
	now := time.Now()
	os.Chtimes(marker, now, now)
	if _, err := os.Stat(marker); err != nil {
		os.WriteFile(marker, []byte("chronicle hook last-fire timestamp\n"), 0644)
	}
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func commitsBehindIn(repoDir, sha string) int {
	out, err := exec.Command("git", "-C", repoDir, "rev-list", "--count", sha+"..HEAD").Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func installGitPostCommit() error {
	base := "."
	if projectPath != "" {
		base = projectPath
	}
	out, err := exec.Command("git", "-C", base, "rev-parse", "--git-dir").Output()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(base, gitDir)
	}
	exe, _ := os.Executable()
	hookPath := filepath.Join(gitDir, "hooks", "post-commit")
	script := fmt.Sprintf("#!/bin/sh\n# Chronicle: zero-token structural refresh after each commit\n%q refresh --quiet >/dev/null 2>&1 &\n", exe)
	if err := os.MkdirAll(filepath.Dir(hookPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(hookPath, []byte(script), 0755)
}

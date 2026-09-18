package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/version"
	"github.com/spf13/cobra"
)

var (
	projectPath  string
	chronicleDir string
	dbPath       string
	registryPath string
	manifestPath string

	// chronicleDirExplicit records whether --chronicle-dir was passed on the
	// command line (vs. left at its default) — resolveWorktreeGraph must not
	// second-guess an explicitly configured artifacts directory.
	chronicleDirExplicit bool
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "chronicle",
		Short: "Chronicle MCP — knowledge graph for your codebase",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			chronicleDirExplicit = cmd.Flags().Changed("chronicle-dir")
			paths.SetProjectRoot(projectPath)
			// Recorded BEFORE any worktree resolution can move the graph root:
			// this is the directory git is measured in, and it stays the
			// worktree the user is actually standing in.
			paths.SetGitDir(projectPath)
			paths.SetChronicleDir(chronicleDir)
		},
	}

	root.PersistentFlags().StringVar(&projectPath, "project", "", "Path to project root (default: current directory)")
	root.PersistentFlags().StringVar(&chronicleDir, "chronicle-dir", ".depbot", "Artifacts directory: relative to project root, or absolute")

	root.AddCommand(
		newVersionCmd(),
		newInitCmd(),
		newRevisionCmd(),
		newNodeCmd(),
		newEdgeCmd(),
		newEvidenceCmd(),
		newSnapshotCmd(),
		newImportCmd(),
		newQueryCmd(),
		newSearchCmd(),
		newSubgraphCmd(),
		newHookCmd(),
		newRefreshCmd(),
		newSurfaceCmd(),
		newValidateCmd(),
		newMCPCmd(),
		newImpactCmd(),
		newAdminCmd(),
		newAliasCmd(),
		newJournalCmd(),
		newSetupCmd(),
		newAttachCmd(),
		newDetachCmd(),
		newDoctorCmd(),
	)

	return root
}

func newVersionCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:   "version",
		Short: "Print MCP identity (codename + fingerprint, not just semver)",
		Run: func(cmd *cobra.Command, args []string) {
			version.StampBuildTime()
			id := version.Identity()
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(version.IdentityMap())
				return
			}
			fmt.Println(id.Banner)
			fmt.Printf("release_codename: %s\n", id.ReleaseCodename)
			fmt.Printf("fingerprint: %s\n", id.Fingerprint)
			fmt.Printf("schema_generation: %d\n", id.SchemaGeneration)
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit full identity JSON (same as chronicle_mcp_identity)")
	return c
}

// resolveDefaults derives artifact file paths from the resolved chronicle dir.
func resolveDefaults() {
	base := paths.Dir()
	dbPath = filepath.Join(base, "chronicle.db")
	registryPath = filepath.Join(base, "chronicle.types.yaml")
	manifestPath = filepath.Join(base, "chronicle.domain.yaml")
}

// resolveWorktreeGraph points the graph location (paths' project root) at the
// main checkout when the current directory is a linked worktree whose own
// artifacts dir has no graph but the main checkout's does. It only applies in
// cwd mode with the default artifacts dir — an explicit --project already
// says exactly where the graph lives, and an explicit --chronicle-dir means
// the user is deliberately scoping artifacts, which this must not second-guess
// (redirecting to main under an assumed name could make ensureDepbotDir create
// a stray "<main>/<custom>/" that was never asked for). Never creates files
// (paths.ResolveProjectDir only stats).
//
// Deliberately does NOT change projectPath: that stays the repo dir for git
// commands (repoDirForGit) — a linked worktree's HEAD differs from main's, so
// commit-count math (hook staleness, refresh diffs) must keep running against
// the actual worktree, not the main checkout the graph was resolved to.
func resolveWorktreeGraph() {
	if projectPath != "" || chronicleDirExplicit {
		return
	}
	if r := paths.ResolveProjectDir(".", chronicleDir); r != "." {
		paths.SetProjectRoot(r)
		fmt.Fprintf(os.Stderr, "chronicle: linked worktree — using graph of %s\n", r)
	}
}

// repoDirForGit returns the directory git commands should run in for
// commit-count math (hook staleness, refresh diffs). It is paths.GitDir() —
// the one process-wide answer, shared with the MCP tools, the importer and the
// dashboard — and never the graph location resolveWorktreeGraph may have
// redirected to main: HEAD must be the worktree's own branch tip, not main's.
func repoDirForGit() string { return paths.GitDir() }

// linkedWorktreeRefusal reports whether a WRITE command is standing in a
// linked worktree whose graph resolves to the main checkout — the one place a
// write is never what the user meant.
//
// The hazard is not hypothetical: git runs the MAIN checkout's post-commit
// hook for a commit made in any worktree, so `chronicle refresh` fires there
// on every feature-branch commit and writes a revision — at the FEATURE
// BRANCH's tip — into main's graph. The same goes for a surface import.
//
// Read-only commands are unaffected: answering a query from main's graph is
// exactly what a worktree with no graph of its own should do.
//
// Three cases are deliberately NOT refusals: an explicit --project or
// --chronicle-dir (the caller said where the graph is), and a worktree that
// owns a graph of its own (a deliberate setup, writing its own knowledge).
// They are the same three conditions resolveWorktreeGraph declines to
// redirect under, so the guard and the redirect can never disagree.
func linkedWorktreeRefusal() (string, bool) {
	if projectPath != "" || chronicleDirExplicit {
		return "", false
	}
	dir := paths.GitDir()
	main, ok := paths.MainWorktreeDir(dir)
	if !ok {
		return "", false
	}
	if paths.ResolveProjectDir(dir, chronicleDir) == dir {
		return "", false
	}
	return "linked worktree — run this in " + main, true
}

// refuseWrite ends a write command that must not run here. quiet callers are
// git hooks: they exit 0 and stay off stdout, because a nudge that fails a
// commit is a worse bug than the one this guard prevents.
func refuseWrite(notice string, quiet bool) {
	if quiet {
		fmt.Fprintln(os.Stderr, "chronicle: "+notice)
		return
	}
	outputError(errors.New(notice))
}

func openGraph() *graph.Graph {
	resolveWorktreeGraph()
	resolveDefaults()
	ensureDepbotDir()

	s, err := store.Open(dbPath)
	if err != nil {
		if strings.Contains(err.Error(), "no such column") || strings.Contains(err.Error(), "SQL logic error") {
			fmt.Fprintf(os.Stderr, "Database schema is outdated: %v\n", err)
			fmt.Fprintf(os.Stderr, "The database needs to be reset to apply new schema changes.\n")
			fmt.Fprintf(os.Stderr, "This will delete all existing graph data. Reset database? [y/N] ")
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(answer)) == "y" {
				os.Remove(dbPath)
				s, err = store.Open(dbPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error opening database after reset: %v\n", err)
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "Database reset successfully. Run 'chronicle scan' to rebuild the graph.\n")
			} else {
				fmt.Fprintf(os.Stderr, "Aborted. Database not modified.\n")
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "error opening database %q: %v\n", dbPath, err)
			os.Exit(1)
		}
	}

	s.SetJournalActor(journalActor())

	var reg *registry.Registry
	if _, statErr := os.Stat(registryPath); statErr == nil {
		reg, err = registry.LoadFile(registryPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading registry: %v\n", err)
			os.Exit(1)
		}
	} else {
		reg, err = registry.LoadDefaults()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading defaults: %v\n", err)
			os.Exit(1)
		}
	}

	g := graph.New(s, reg)

	// Sync-on-open replays merged journal events with placeholder trust —
	// recompute derived trust/confidence so statuses match a verified rebuild.
	if err := g.RecalculateTrustAfterJournalSync(); err != nil {
		fmt.Fprintf(os.Stderr, "chronicle: trust recalculation after journal sync failed: %v\n", err)
	}
	return g
}

// journalActor returns the configured git identity, or hostname.
// Resolved once at startup and stamped on every journal event.
func journalActor() string {
	out, err := exec.Command("git", "config", "user.email").Output()
	if err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	host, _ := os.Hostname()
	if host == "" {
		return "unknown"
	}
	return host
}

// ensureDepbotDir creates .depbot/ and skeleton manifest if they don't exist.
func ensureDepbotDir() {
	os.MkdirAll(filepath.Dir(dbPath), 0755)

	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		os.WriteFile(manifestPath, []byte(manifestSkeleton), 0644)
	}

	// Write boundary: ordinary commands never modify wiring files
	// (AGENTS.md / CLAUDE.md). setup/attach own those; here we only
	// notice staleness. (Spec: agent-setup-distribution rev 3.)
	base := "."
	if projectPath != "" {
		base = projectPath
	}
	maybePrintWiringNotice(base, os.Stderr)
}

// maybePrintWiringNotice prints a single stderr hint when the project HAS
// chronicle wiring (marker present) and the managed section is outdated.
// Unwired projects stay silent — attach is opt-in.
func maybePrintWiringNotice(dir string, w io.Writer) {
	data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil || !strings.Contains(string(data), wiring.AgentsMarkerStart) {
		return
	}
	action, _ := wiring.PlanMarkedSection(data, true, wiring.ProjectAgentsSection())
	if action != wiring.ActionUnchanged {
		fmt.Fprintln(w, "chronicle: project wiring is outdated — run 'chronicle attach --upgrade'")
	}
}

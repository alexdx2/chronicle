package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
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
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "chronicle",
		Short: "Chronicle MCP — knowledge graph for your codebase",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			paths.SetProjectRoot(projectPath)
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
		newValidateCmd(),
		newMCPCmd(),
		newImpactCmd(),
		newAdminCmd(),
		newAliasCmd(),
		newJournalCmd(),
		newSetupCmd(),
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

func openGraph() *graph.Graph {
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
	if s.JournalSyncApplied() > 0 {
		if err := g.RecalculateAllTrust(); err != nil {
			fmt.Fprintf(os.Stderr, "chronicle: trust recalculation after journal sync failed: %v\n", err)
		}
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
		skeleton := "# Chronicle Manifest — edit this file\ndomain: my-domain\ndescription: \"\"\nrepositories:\n  - name: my-repo\n    path: .\n    tags: []\nowner: my-team\n"
		os.WriteFile(manifestPath, []byte(skeleton), 0644)
	}

	// Create CLAUDE.md if not exists — enables slash commands
	claudeMD := "CLAUDE.md"
	if _, err := os.Stat(claudeMD); os.IsNotExist(err) {
		content := `# Chronicle Knowledge Graph

This project uses Chronicle for code analysis and knowledge management. Chronicle MCP tools are available.

## Quick Commands

When the user says any of these, call chronicle_command with the command name and execute the instructions:

| User says | Command | What it does |
|---|---|---|
| "chronicle scan" or "scan this project" | chronicle_command(command='scan') | Full project scan |
| "chronicle data" or "analyze data models" | chronicle_command(command='data') | Prisma/data model analysis |
| "chronicle language" or "define domain language" | chronicle_command(command='language') | Domain glossary + violations |
| "chronicle impact X" or "what breaks if I change X" | chronicle_command(command='impact') | Blast radius analysis |
| "chronicle deps X" or "what depends on X" | chronicle_command(command='deps') | Dependency analysis |
| "chronicle path A B" or "how does A connect to B" | chronicle_command(command='path') | Path between nodes |
| "chronicle services" or "show service architecture" | chronicle_command(command='services') | Service dependency map |
| "chronicle topology" or "show domain topology" | chronicle_command(command='topology') | Federation domain map |
| "chronicle connections" or "show cross-repo edges" | chronicle_command(command='connections') | Cross-repo edge inventory |
| "chronicle status" or "chronicle dashboard" | chronicle_command(command='status') | Graph state + dashboard URL |
| "chronicle version" or "which MCP" | chronicle_command(command='version') or chronicle_mcp_identity | MCP codename + fingerprint |
| "chronicle help" | chronicle_command(command='help') | Show all commands |

## How it works

Chronicle builds a knowledge graph of your codebase: data models, services, endpoints, dependencies.
Call chronicle_command to get step-by-step instructions for any analysis task.
The admin dashboard shows the graph visually — get the URL via chronicle_command(command='status').
`
		os.WriteFile(claudeMD, []byte(content), 0644)
	}

	// AGENTS.md — same guidance for agents that don't read CLAUDE.md
	// (Codex, OpenCode, Gemini CLI, ...). Marker-wrapped upsert: creates the
	// file if missing, refreshes only the chronicle section otherwise.
	upsertMarkedSection("AGENTS.md", projectAgentsSection())
}

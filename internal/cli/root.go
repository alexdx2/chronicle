package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

var (
	projectPath  string
	dbPath       string
	registryPath string
	manifestPath string
)

const depbotDir = ".depbot"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "chronicle",
		Short: "Chronicle MCP — knowledge graph for your codebase",
	}

	root.PersistentFlags().StringVar(&projectPath, "project", "", "Path to project root containing .depbot/ (default: current directory)")
	root.PersistentFlags().StringVar(&dbPath, "db", "", "Path to SQLite database (overrides --project)")
	root.PersistentFlags().StringVar(&registryPath, "registry", "", "Path to type registry (overrides --project)")
	root.PersistentFlags().StringVar(&manifestPath, "manifest", "", "Path to domain manifest (overrides --project)")

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
		newValidateCmd(),
		newMCPCmd(),
		newImpactCmd(),
		newAdminCmd(),
		newAliasCmd(),
	)

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("chronicle v0.4.0")
		},
	}
}

// resolveDefaults sets default paths under .depbot/ if not explicitly provided.
// If --project is set, all paths are relative to that directory.
func resolveDefaults() {
	base := depbotDir
	if projectPath != "" {
		base = filepath.Join(projectPath, depbotDir)
	}
	if dbPath == "" {
		dbPath = filepath.Join(base, "chronicle.db")
	}
	if registryPath == "" {
		registryPath = filepath.Join(base, "chronicle.types.yaml")
	}
	if manifestPath == "" {
		manifestPath = filepath.Join(base, "chronicle.domain.yaml")
	}
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

	return graph.New(s, reg)
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
| "chronicle status" or "chronicle dashboard" | chronicle_command(command='status') | Graph state + dashboard URL |
| "chronicle help" | chronicle_command(command='help') | Show all commands |

## How it works

Chronicle builds a knowledge graph of your codebase: data models, services, endpoints, dependencies.
Call chronicle_command to get step-by-step instructions for any analysis task.
The admin dashboard shows the graph visually — get the URL via chronicle_command(command='status').
`
		os.WriteFile(claudeMD, []byte(content), 0644)
	}
}

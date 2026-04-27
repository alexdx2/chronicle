package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/spf13/cobra"
)

var (
	dbPath       string
	registryPath string
	manifestPath string
)

const depbotDir = ".depbot"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "oracle",
		Short: "Domain Oracle — graph storage and query API",
	}

	root.PersistentFlags().StringVar(&dbPath, "db", "", "Path to SQLite database (default: .depbot/oracle.db)")
	root.PersistentFlags().StringVar(&registryPath, "registry", "", "Path to type registry (default: .depbot/oracle.types.yaml)")
	root.PersistentFlags().StringVar(&manifestPath, "manifest", "", "Path to domain manifest (default: .depbot/oracle.domain.yaml)")

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
	)

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("oracle v0.1.0")
		},
	}
}

// resolveDefaults sets default paths under .depbot/ if not explicitly provided.
func resolveDefaults() {
	if dbPath == "" {
		dbPath = filepath.Join(depbotDir, "oracle.db")
	}
	if registryPath == "" {
		registryPath = filepath.Join(depbotDir, "oracle.types.yaml")
	}
	if manifestPath == "" {
		manifestPath = filepath.Join(depbotDir, "oracle.domain.yaml")
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
				fmt.Fprintf(os.Stderr, "Database reset successfully. Run 'oracle scan' to rebuild the graph.\n")
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
		skeleton := "# Domain Oracle Manifest — edit this file\ndomain: my-domain\ndescription: \"\"\nrepositories:\n  - name: my-repo\n    path: .\n    tags: []\nowner: my-team\n"
		os.WriteFile(manifestPath, []byte(skeleton), 0644)
	}

	// Create CLAUDE.md if not exists — enables slash commands
	claudeMD := "CLAUDE.md"
	if _, err := os.Stat(claudeMD); os.IsNotExist(err) {
		content := `# Oracle Knowledge Graph

This project uses Oracle for code analysis and knowledge management. Oracle MCP tools are available.

## Quick Commands

When the user says any of these, call oracle_command with the command name and execute the instructions:

| User says | Command | What it does |
|---|---|---|
| "oracle scan" or "scan this project" | oracle_command(command='scan') | Full project scan |
| "oracle data" or "analyze data models" | oracle_command(command='data') | Prisma/data model analysis |
| "oracle language" or "define domain language" | oracle_command(command='language') | Domain glossary + violations |
| "oracle impact X" or "what breaks if I change X" | oracle_command(command='impact') | Blast radius analysis |
| "oracle deps X" or "what depends on X" | oracle_command(command='deps') | Dependency analysis |
| "oracle path A B" or "how does A connect to B" | oracle_command(command='path') | Path between nodes |
| "oracle services" or "show service architecture" | oracle_command(command='services') | Service dependency map |
| "oracle status" or "oracle dashboard" | oracle_command(command='status') | Graph state + dashboard URL |
| "oracle help" | oracle_command(command='help') | Show all commands |

## How it works

Oracle builds a knowledge graph of your codebase: data models, services, endpoints, dependencies.
Call oracle_command to get step-by-step instructions for any analysis task.
The admin dashboard shows the graph visually — get the URL via oracle_command(command='status').
`
		os.WriteFile(claudeMD, []byte(content), 0644)
	}
}

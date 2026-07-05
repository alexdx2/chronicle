package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize the chronicle dir with manifest, types, and database",
		Run: func(cmd *cobra.Command, args []string) {
			resolveDefaults()

			// Create the chronicle dir
			if err := os.MkdirAll(paths.Dir(), 0755); err != nil {
				fmt.Fprintf(os.Stderr, "error creating %s: %v\n", paths.Dir(), err)
				os.Exit(1)
			}

			// Create types file
			if _, err := os.Stat(registryPath); os.IsNotExist(err) {
				if err := os.WriteFile(registryPath, registry.DefaultRegistryYAML, 0644); err != nil {
					fmt.Fprintf(os.Stderr, "error writing registry: %v\n", err)
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "created %s\n", registryPath)
			}

			// Create manifest skeleton
			if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
				skeleton := `# Chronicle Manifest — edit this file
domain: my-domain
description: ""
repositories:
  - name: my-repo
    path: .
    tags: []
owner: my-team
`
				if err := os.WriteFile(manifestPath, []byte(skeleton), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "error writing manifest: %v\n", err)
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "created %s (edit this file)\n", manifestPath)
			}

			// Init database
			s, err := store.Open(dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error creating database: %v\n", err)
				os.Exit(1)
			}
			s.Close()
			fmt.Fprintf(os.Stderr, "database ready at %s\n", dbPath)

			// Add .depbot to .gitignore if not already there
			addToGitignore()

			outputJSON(map[string]string{
				"directory": paths.Dir(),
				"manifest":  manifestPath,
				"registry":  registryPath,
				"database":  dbPath,
				"status":    "initialized",
			})
		},
	}
}

func addToGitignore() {
	entry := paths.ConfiguredDir()
	if filepath.IsAbs(entry) {
		return // artifacts live outside the repo — nothing to ignore
	}
	line := entry + "/"
	gitignorePath := ".gitignore"
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		os.WriteFile(gitignorePath, []byte(line+"\n"), 0644)
		return
	}
	if strings.Contains(string(content), entry) {
		return
	}
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString("\n" + line + "\n")
}

package cli

import (
	"fmt"
	"os"

	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize .depbot/ with manifest, types, and database",
		Run: func(cmd *cobra.Command, args []string) {
			resolveDefaults()

			// Create .depbot/ directory
			if err := os.MkdirAll(depbotDir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "error creating %s: %v\n", depbotDir, err)
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
				"directory": depbotDir,
				"manifest":  manifestPath,
				"registry":  registryPath,
				"database":  dbPath,
				"status":    "initialized",
			})
		},
	}
}

func addToGitignore() {
	gitignorePath := ".gitignore"
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		// No .gitignore — create one
		os.WriteFile(gitignorePath, []byte(".depbot/\n"), 0644)
		return
	}
	// Check if already present
	if contains := func(s, sub string) bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}; contains(string(content), ".depbot") {
		return
	}
	// Append
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString("\n.depbot/\n")
}

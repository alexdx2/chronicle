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

// manifestSkeleton is the starter chronicle.domain.yaml content written by
// both `chronicle init` (this file) and ensureDepbotDir (root.go) when no
// manifest exists yet. Single source of truth — the two call sites used to
// carry independent copies of a "domain:"/"repositories:" shape that
// manifest.Load (manifest/manifest.go) has never recognized: the parser only
// understands a "domains:" key (map or list). That skeleton parsed into a
// Manifest with zero domains, so a fresh project's manifest failed to load
// with "domains is required" the moment anything tried to use it — and
// saveManifestHandler used to swallow that error, reporting
// {"status":"saved"} for a manifest that could never drive a scan.
const manifestSkeleton = `# Chronicle Manifest — edit this file
domains:
  my-domain:
    name: My Domain
    description: What this domain covers
    scan:
      include:
        - "src/**"
      exclude:
        - "**/node_modules/**"
        - "**/__tests__/**"
tech: []
infrastructure: []
instruction_packs: []
`

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
				if err := os.WriteFile(manifestPath, []byte(manifestSkeleton), 0644); err != nil {
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

			// Add the chronicle dir to .gitignore if not already there
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
	for _, l := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(l)
		if trimmed == entry || trimmed == line {
			return
		}
	}
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString("\n" + line + "\n")
}

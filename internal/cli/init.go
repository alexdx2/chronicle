package cli

import (
	"fmt"
	"os"
	"os/exec"
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

			// Bring .gitignore/.gitattributes/the merge driver up to the
			// graph-snapshot design spec's §3 shape (cwd is the repo root
			// for `init`, same as every other file it writes here).
			ensureGitWiring(".")

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

// ensureGitWiring brings a project's git configuration up to the graph-
// snapshot design spec's §3 shape, so graph.jsonl is tracked instead of
// ignored and mergeable across branches: .gitignore ignores the configured
// chronicle dir except for the three files meant to be committed,
// .gitattributes routes graph.jsonl through the chronicle-graph merge
// driver, and that driver is registered in .git/config. `init`, `attach` and
// (a later task) `graph migrate` are the three places a project's git wiring
// needs to reach this same shape — the rules live here once so none of them
// duplicate it.
//
// root is the directory containing .gitignore/.gitattributes and the git
// repository itself: `init` operates on the process cwd (root == "."), the
// same as every other file it writes; `attach` passes the project root it
// already resolved, since it can target a directory other than cwd.
//
// Mirrors the old addToGitignore's early return: when the configured
// chronicle dir is an absolute path, its contents (including graph.jsonl)
// live outside the repo entirely, so there is nothing to ignore, attribute
// or register a merge driver for.
func ensureGitWiring(root string) {
	if filepath.IsAbs(paths.ConfiguredDir()) {
		return
	}
	rewriteGitignore(root)
	ensureGitattributesEntry(root)
	// Best-effort, silent on failure — same convention as this file's other
	// git subprocess call (gitRemote in attach.go): root may legitimately
	// not be a git repository yet (e.g. `chronicle init` before `git init`),
	// and that is not a reason to fail init/attach or warn on every run.
	_ = registerGraphMergeDriver(root)
}

// rewriteGitignore replaces (not appends to) any old-style whole-directory
// ignore line for the configured chronicle dir with the §3 block: a
// wildcard ignore plus three explicit exceptions. This is a REPLACE, not an
// addition, because git will not descend into an already-ignored directory
// to evaluate per-file `!` negation patterns inside it — leaving the old
// `<dir>/` line in place alongside the new exceptions would make the
// exceptions silently do nothing and graph.jsonl would stay ignored.
//
// Idempotent: re-running it strips any prior copy of the wildcard/exception
// lines (wherever they ended up) and re-appends a single canonical copy, so
// repeated calls converge instead of accumulating duplicates.
func rewriteGitignore(root string) {
	dir := paths.ConfiguredDir()
	oldWholeDir := []string{dir, dir + "/"}
	wildcard := dir + "/*"
	exceptions := []string{
		"!" + dir + "/graph.jsonl",
		"!" + dir + "/chronicle.domain.yaml",
		"!" + dir + "/chronicle.types.yaml",
	}

	gitignorePath := filepath.Join(root, ".gitignore")
	content, _ := os.ReadFile(gitignorePath) // missing file: content == nil, handled like an empty one

	var kept []string
	for _, l := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(l)
		if trimmed == wildcard {
			continue // re-added below, in canonical position
		}
		if contains(exceptions, trimmed) {
			continue // re-added below, in canonical position
		}
		if contains(oldWholeDir, trimmed) {
			continue // the whole-directory ignore this block replaces
		}
		kept = append(kept, l)
	}
	// Drop trailing blank lines so re-running this doesn't accumulate blank
	// separators before the block on every call.
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}

	if len(kept) > 0 {
		kept = append(kept, "") // one blank separator before the block
	}
	kept = append(kept, wildcard)
	kept = append(kept, exceptions...)

	os.WriteFile(gitignorePath, []byte(strings.Join(kept, "\n")+"\n"), 0644)
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// ensureGitattributesEntry appends the `graph.jsonl merge=chronicle-graph`
// line to .gitattributes if it isn't already there. Unlike .gitignore,
// nothing here ever needs to be replaced — attach never wrote a
// .gitattributes line before this task, so there is no stale predecessor to
// remove, only the ordinary "don't duplicate on a repeat call" check.
func ensureGitattributesEntry(root string) {
	dir := paths.ConfiguredDir()
	line := dir + "/graph.jsonl merge=chronicle-graph"
	path := filepath.Join(root, ".gitattributes")

	content, err := os.ReadFile(path)
	if err != nil {
		os.WriteFile(path, []byte(line+"\n"), 0644)
		return
	}
	for _, l := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(l) == line {
			return
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	sep := "\n"
	if len(content) == 0 || content[len(content)-1] == '\n' {
		sep = ""
	}
	f.WriteString(sep + line + "\n")
}

// registerGraphMergeDriver registers the chronicle-graph merge driver in
// root's .git/config, the same way `git config` itself would from the
// command line — a fixed, hardcoded key and value, never data-derived, so
// none of the argument-injection concerns that apply to git plumbing fed
// external revisions or paths apply here.
func registerGraphMergeDriver(root string) error {
	cmd := exec.Command("git", "-C", root, "config", "merge.chronicle-graph.driver", "chronicle graph merge %O %A %B")
	_, err := cmd.Output()
	return err
}

// Package paths resolves where Chronicle stores its artifacts (database,
// registry, manifest, journal, scan work dirs). Two process-wide values are
// set once at CLI startup from --project and --chronicle-dir; everything
// else derives from them. Setters are not goroutine-safe — call them before
// serving.
package paths

import "path/filepath"

const defaultDir = ".depbot"

var (
	projectRoot  string
	chronicleDir = defaultDir
	gitDir       string
)

// SetProjectRoot records the project root from --project. Empty = unset
// (paths stay relative to the process working directory, as before).
func SetProjectRoot(dir string) {
	projectRoot = dir
}

// SetGitDir records the directory git is measured in, from --project. Set
// once at startup, alongside SetProjectRoot and BEFORE any worktree
// resolution: the two answers separate there, and only there.
func SetGitDir(dir string) {
	gitDir = dir
}

// GitDir is the directory every git question in this process is asked in:
// "how far behind HEAD is the graph", "is this commit an ancestor", "which
// files changed". It is NOT the graph location — inside a linked worktree the
// graph resolves to the main checkout (see ResolveProjectDir) while HEAD stays
// the worktree's own branch tip, and measuring one against the other is how a
// feature branch ends up reported as the state of main.
func GitDir() string {
	if gitDir == "" {
		return "."
	}
	return gitDir
}

// SetChronicleDir records the artifacts directory from --chronicle-dir.
// Empty resets to the default ".depbot".
func SetChronicleDir(dir string) {
	if dir == "" {
		dir = defaultDir
	}
	chronicleDir = dir
}

// Root returns the configured project root, or "" when unset.
func Root() string {
	return projectRoot
}

// ConfiguredDir returns the raw configured chronicle dir (default ".depbot").
func ConfiguredDir() string {
	return chronicleDir
}

// DirAt resolves the configured chronicle dir against an explicit root:
// absolute config wins; empty root keeps the config as-is (cwd-relative).
func DirAt(root string) string {
	if filepath.IsAbs(chronicleDir) || root == "" {
		return chronicleDir
	}
	return filepath.Join(root, chronicleDir)
}

// Dir resolves the chronicle dir against the configured project root.
func Dir() string {
	return DirAt(projectRoot)
}

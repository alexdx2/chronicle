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
)

// SetProjectRoot records the project root from --project. Empty = unset
// (paths stay relative to the process working directory, as before).
func SetProjectRoot(dir string) {
	projectRoot = dir
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

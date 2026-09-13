package paths

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/gitutil"
)

// MainWorktreeDir reports the main worktree of the repo containing dir when dir
// is inside a linked worktree (git worktree add). ("", false) for the main
// checkout, non-git dirs, or any git error.
func MainWorktreeDir(dir string) (string, bool) {
	common, err := gitutil.Run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", false
	}
	gitDir, err := gitutil.Run(dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", false
	}
	if absPath(dir, common) == absPath(dir, gitDir) {
		return "", false // main checkout
	}
	out, err := gitutil.Run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			return strings.TrimPrefix(line, "worktree "), true // first entry is the main worktree
		}
	}
	return "", false
}

// ResolveProjectDir is dir when it owns a graph at <dir>/<chronicleDir>/chronicle.db;
// otherwise the main worktree's dir when dir is a linked worktree and the main
// checkout owns a graph there (same chronicleDir name); otherwise dir
// unchanged. It never creates files.
//
// chronicleDir is the configured artifacts directory name (relative to the
// project root; "" defaults to ".depbot", matching ConfiguredDir's default).
// An absolute chronicleDir names the same location regardless of which
// worktree you're in, so it is left out of this resolution entirely — dir is
// returned unchanged.
func ResolveProjectDir(dir, chronicleDir string) string {
	rel := chronicleDir
	if rel == "" {
		rel = defaultDir
	}
	if filepath.IsAbs(rel) {
		return dir
	}
	if _, err := os.Stat(filepath.Join(dir, rel, "chronicle.db")); err == nil {
		return dir
	}
	if main, ok := MainWorktreeDir(dir); ok {
		if _, err := os.Stat(filepath.Join(main, rel, "chronicle.db")); err == nil {
			return main
		}
	}
	return dir
}

func absPath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

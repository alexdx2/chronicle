package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MainWorktreeDir reports the main worktree of the repo containing dir when dir
// is inside a linked worktree (git worktree add). ("", false) for the main
// checkout, non-git dirs, or any git error.
func MainWorktreeDir(dir string) (string, bool) {
	common, err := gitOut(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", false
	}
	gitDir, err := gitOut(dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", false
	}
	if absPath(dir, common) == absPath(dir, gitDir) {
		return "", false // main checkout
	}
	out, err := gitOut(dir, "worktree", "list", "--porcelain")
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

// ResolveProjectDir is dir when it owns a graph; otherwise the main worktree
// when dir is a linked worktree; otherwise dir unchanged. It never creates files.
func ResolveProjectDir(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".depbot", "chronicle.db")); err == nil {
		return dir
	}
	if main, ok := MainWorktreeDir(dir); ok {
		if _, err := os.Stat(filepath.Join(main, ".depbot", "chronicle.db")); err == nil {
			return main
		}
	}
	return dir
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func absPath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

package graph

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/internal/manifest"
)

// DiscoverResult holds files found during project discovery.
type DiscoverResult struct {
	Files      []string       `json:"files"`
	TotalFiles int            `json:"total_files"`
	ScanConfig map[string]any `json:"scan_config"`
}

// DiscoverFiles finds all scannable files using git ls-files + manifest include/exclude rules.
// Only git-tracked files are considered — no temp files, no untracked junk.
// If manifest has no scan config, returns ALL git-tracked files (Claude decides per-file).
func (g *Graph) DiscoverFiles(rootDir, domainKey string, revisionID int64, scanCfg *manifest.ScanConfig) (*DiscoverResult, error) {
	// Get git-tracked files
	gitFiles, err := gitTrackedFiles(rootDir)
	if err != nil {
		return nil, err
	}

	// Apply include/exclude filters from manifest
	var filtered []string
	for _, f := range gitFiles {
		if shouldInclude(f, scanCfg) {
			filtered = append(filtered, f)
		}
	}

	result := &DiscoverResult{
		Files:      filtered,
		TotalFiles: len(filtered),
	}

	if scanCfg != nil {
		result.ScanConfig = map[string]any{
			"include": scanCfg.Include,
			"exclude": scanCfg.Exclude,
		}
	}

	// Create scan_file obligation for each discovered file
	for _, f := range filtered {
		if revisionID > 0 {
			g.store.CreateObligation(revisionID, domainKey, "scan_file", f, "git-tracked, matches scan config")
		}
	}

	return result, nil
}

// gitTrackedFiles returns all files tracked by git in the given directory.
func gitTrackedFiles(rootDir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// shouldInclude checks if a file matches the manifest scan config.
// If no config → include everything (Claude decides per-file).
// If include patterns exist → file must match at least one.
// If exclude patterns exist → file must NOT match any.
func shouldInclude(filePath string, cfg *manifest.ScanConfig) bool {
	if cfg == nil || (len(cfg.Include) == 0 && len(cfg.Exclude) == 0) {
		return true // no config = include all
	}

	// Check excludes first
	for _, pattern := range cfg.Exclude {
		if matchGlob(filePath, pattern) {
			return false
		}
	}

	// If no includes specified, everything passes (after excludes)
	if len(cfg.Include) == 0 {
		return true
	}

	// Must match at least one include
	for _, pattern := range cfg.Include {
		if matchGlob(filePath, pattern) {
			return true
		}
	}
	return false
}

// matchGlob does simple glob matching supporting ** and *.
func matchGlob(filePath, pattern string) bool {
	// Handle ** (any directory depth)
	if strings.Contains(pattern, "**") {
		parts := strings.SplitN(pattern, "**", 2)
		prefix := parts[0]
		suffix := ""
		if len(parts) > 1 {
			suffix = strings.TrimPrefix(parts[1], "/")
		}

		// Prefix must match start of path
		if prefix != "" && !strings.HasPrefix(filePath, prefix) {
			return false
		}

		// Suffix must match end of path (as glob)
		if suffix == "" {
			return true
		}

		// Check if any part of the remaining path matches suffix
		remaining := filePath
		if prefix != "" {
			remaining = strings.TrimPrefix(filePath, prefix)
		}
		// Simple: check if the filename matches the suffix pattern
		matched, _ := filepath.Match(suffix, filepath.Base(remaining))
		if matched {
			return true
		}
		// Also try matching the full remaining path
		matched, _ = filepath.Match(suffix, remaining)
		return matched
	}

	// Simple glob
	matched, _ := filepath.Match(pattern, filePath)
	if matched {
		return true
	}
	// Also try matching just the filename
	matched, _ = filepath.Match(pattern, filepath.Base(filePath))
	return matched
}

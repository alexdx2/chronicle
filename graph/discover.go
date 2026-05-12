package graph

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/internal/manifest"
)

// DiscoverResult holds files found during project discovery.
type DiscoverResult struct {
	Files        []string          `json:"files"`
	TotalFiles   int               `json:"total_files"`
	TotalGit     int               `json:"total_git_files"`     // all git-tracked files before filtering
	Excluded     int               `json:"excluded"`            // files excluded by patterns
	ByDirectory  map[string]int    `json:"by_directory"`        // file count per top-level directory
	ByExtension  map[string]int    `json:"by_extension"`        // file count per extension
	ScanConfig   map[string]any    `json:"scan_config"`
}

// DiscoverFiles finds all scannable files using git ls-files + manifest include/exclude rules.
// Only git-tracked files are considered — no temp files, no untracked junk.
// If manifest is provided, uses MergedScanConfig for filtering and DomainForFile for per-file domain.
// Falls back to domainKey for all files when manifest is nil.
func (g *Graph) DiscoverFiles(rootDir, domainKey string, revisionID int64, m *manifest.Manifest) (*DiscoverResult, error) {
	// Get git-tracked files
	gitFiles, err := gitTrackedFiles(rootDir)
	if err != nil {
		return nil, err
	}

	// Build scan config from manifest (or nil for no filtering)
	var scanCfg *manifest.ScanConfig
	if m != nil {
		merged := m.MergedScanConfig()
		scanCfg = &merged
	}

	// Apply include/exclude filters from manifest
	var filtered []string
	for _, f := range gitFiles {
		if shouldInclude(f, scanCfg) {
			filtered = append(filtered, f)
		}
	}

	// Build summary stats
	byDir := map[string]int{}
	byExt := map[string]int{}
	for _, f := range filtered {
		// Top-level directory (first path component)
		dir := f
		if i := strings.Index(f, "/"); i >= 0 {
			dir = f[:i]
		}
		byDir[dir]++

		ext := filepath.Ext(f)
		if ext == "" {
			ext = "(no ext)"
		}
		byExt[ext]++
	}

	result := &DiscoverResult{
		Files:       filtered,
		TotalFiles:  len(filtered),
		TotalGit:    len(gitFiles),
		Excluded:    len(gitFiles) - len(filtered),
		ByDirectory: byDir,
		ByExtension: byExt,
	}

	if scanCfg != nil {
		result.ScanConfig = map[string]any{
			"include": scanCfg.Include,
			"exclude": scanCfg.Exclude,
		}
	}

	// Create scan_file obligation for each discovered file — per-file domain from manifest
	for _, f := range filtered {
		if revisionID > 0 {
			domain := domainKey
			if m != nil {
				domain = m.DomainForFile(f)
			}
			g.store.CreateObligation(revisionID, domain, "scan_file", f, "git-tracked, matches scan config")
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

// alwaysExclude are patterns that are ALWAYS excluded regardless of manifest.
// These are directories that never contain scannable architecture.
var alwaysExclude = []string{
	"**/node_modules/**",
	"**/.history/**",
	"**/.git/**",
	"**/dist/**",
	"**/build/**",
	"**/@generated/**",
	"**/generated/**",
	"**/__pycache__/**",
	"**/.venv/**",
	"**/vendor/**",
	"**/*.min.js",
	"**/*.map",
	"**/*.lock",
	"**/migrations/**",
}

// shouldInclude checks if a file matches the manifest scan config.
// If no config → include everything (Claude decides per-file).
// If include patterns exist → file must match at least one.
// If exclude patterns exist → file must NOT match any.
// alwaysExclude patterns are applied regardless of config.
func shouldInclude(filePath string, cfg *manifest.ScanConfig) bool {
	// Always-exclude patterns (safety net)
	for _, pattern := range alwaysExclude {
		if matchGlob(filePath, pattern) {
			return false
		}
	}

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

// matchGlob matches a file path against a glob pattern with ** support.
// "api/src/**/*.ts" matches "api/src/services/order.service.ts"
// "**/package.json" matches "api/package.json" and "packages/x/package.json"
// "docker-compose*.yml" matches "docker-compose.yml" and "docker-compose.dev.yml"
func matchGlob(filePath, pattern string) bool {
	// Split pattern on ** to get prefix and suffix
	if strings.Contains(pattern, "**") {
		parts := strings.SplitN(pattern, "**", 2)
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := ""
		if len(parts) > 1 {
			suffix = strings.TrimPrefix(parts[1], "/")
		}

		// ** at start means "anywhere in path"
		if prefix == "" {
			if suffix == "" {
				return true
			}
			// Match suffix against filename
			matched, _ := filepath.Match(suffix, filepath.Base(filePath))
			return matched
		}

		// Prefix must match start of path
		if !strings.HasPrefix(filePath, prefix+"/") && filePath != prefix {
			return false
		}

		// No suffix means "everything under prefix"
		if suffix == "" {
			return true
		}

		// Suffix: match against filename (most common: *.ts, *.json)
		matched, _ := filepath.Match(suffix, filepath.Base(filePath))
		return matched
	}

	// No ** — try exact filepath.Match
	matched, _ := filepath.Match(pattern, filePath)
	if matched {
		return true
	}
	// Also try against just the filename for patterns like "Dockerfile"
	matched, _ = filepath.Match(pattern, filepath.Base(filePath))
	return matched
}

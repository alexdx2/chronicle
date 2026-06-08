package graph

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type DirectoryGroup struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// FileTreeEntry represents a directory with all its filenames.
type FileTreeEntry struct {
	Path  string   `json:"path"`
	Files []string `json:"files"`
}

// GroupFilesByDirectory runs git ls-files and groups results by top-level directory (2-3 levels deep).
func GroupFilesByDirectory(rootDir string) ([]DirectoryGroup, int, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return nil, 0, err
	}

	dirCounts := make(map[string]int)
	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		total++
		dir := filepath.Dir(line)
		parts := strings.Split(dir, "/")
		if len(parts) > 3 {
			parts = parts[:3]
		}
		key := strings.Join(parts, "/")
		dirCounts[key]++
	}

	var groups []DirectoryGroup
	for path, count := range dirCounts {
		groups = append(groups, DirectoryGroup{Path: path, Count: count})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Count > groups[j].Count
	})

	return groups, total, nil
}

// BuildFileTree runs git ls-files and returns every directory with all its filenames.
// The LLM reads the full tree and decides what's relevant.
func BuildFileTree(rootDir string) ([]FileTreeEntry, int, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return nil, 0, err
	}

	dirMap := make(map[string][]string)
	total := 0

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		total++
		dir := filepath.Dir(line)
		name := filepath.Base(line)
		dirMap[dir] = append(dirMap[dir], name)
	}

	var entries []FileTreeEntry
	for dir, files := range dirMap {
		entries = append(entries, FileTreeEntry{Path: dir, Files: files})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	return entries, total, nil
}

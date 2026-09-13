// Package gitdiff provides small git helpers for diff-driven features
// (review report, refresh). Public so chronicle-pro can reuse them.
package gitdiff

import (
	"fmt"
	"os/exec"
	"strings"
)

// ChangedFile is one entry of a diff: repo-relative path + git status letter
// (A added, M modified, D deleted, R renamed — renames report the NEW path).
type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	// OldPath is the path a rename came FROM (empty for every other status).
	// A consumer that replaces per-file knowledge needs it: after a rename the
	// new path is re-read, and the old one still carries everything the file
	// used to assert under a name that no longer exists.
	OldPath string `json:"old_path,omitempty"`
}

// ChangedFiles lists files changed between base and head. head == "" compares
// base against the working tree and includes untracked files (status A) —
// that is what a reviewer means by "my current changes".
func ChangedFiles(repoRoot, base, head string) ([]ChangedFile, error) {
	args := []string{"diff", "--name-status", "-M"}
	if head == "" {
		args = append(args, base)
	} else {
		args = append(args, base+".."+head)
	}
	out, err := git(repoRoot, args...)
	if err != nil {
		return nil, fmt.Errorf("git diff (base %s): %w", base, err)
	}

	var files []ChangedFile
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := parts[0][:1] // R100 → R
		path := parts[1]
		oldPath := ""
		if status == "R" && len(parts) >= 3 {
			oldPath = parts[1]
			path = parts[2] // renamed: old new — report the new path
		}
		files = append(files, ChangedFile{Path: path, Status: status, OldPath: oldPath})
	}

	if head == "" {
		untracked, err := git(repoRoot, "ls-files", "--others", "--exclude-standard")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(untracked), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					files = append(files, ChangedFile{Path: line, Status: "A"})
				}
			}
		}
	}
	return files, nil
}

// Show returns the content of path at ref (git show ref:path).
func Show(repoRoot, ref, path string) ([]byte, error) {
	out, err := gitBytes(repoRoot, "show", ref+":"+path)
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", ref, path, err)
	}
	return out, nil
}

// MergeBase returns the merge base of ref and HEAD.
func MergeBase(repoRoot, ref string) (string, error) {
	out, err := git(repoRoot, "merge-base", ref, "HEAD")
	if err != nil {
		return "", fmt.Errorf("git merge-base %s HEAD: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}

func git(repoRoot string, args ...string) (string, error) {
	out, err := gitBytes(repoRoot, args...)
	return string(out), err
}

func gitBytes(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	return cmd.Output()
}

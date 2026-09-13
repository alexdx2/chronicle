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
//
// NUL-separated, with quoting off. Both defaults silently lose files rather
// than failing: core.quotePath renders "src/café.ts" as "src/caf\303\251.ts"
// WITH the quotation marks, and any whitespace split loses everything after
// the first space in a path. A consumer that replaces per-file knowledge reads
// a missing file as "nothing changed here", which is the worst possible
// failure — it is indistinguishable from the truth.
func ChangedFiles(repoRoot, base, head string) ([]ChangedFile, error) {
	args := []string{"diff", "--name-status", "-M", "-z"}
	if head == "" {
		args = append(args, base)
	} else {
		args = append(args, base+".."+head)
	}
	out, err := gitVerbatim(repoRoot, args...)
	if err != nil {
		return nil, fmt.Errorf("git diff (base %s): %w", base, err)
	}

	// With -z the stream is NUL-terminated fields, not lines: a status field,
	// then its path — and for a rename or copy, TWO paths (old, then new).
	fields := splitNUL(out)
	var files []ChangedFile
	for i := 0; i < len(fields); {
		status := fields[i][:1] // R100 → R
		i++
		if i >= len(fields) {
			break
		}
		path := fields[i]
		i++
		oldPath := ""
		if status == "R" || status == "C" {
			if i >= len(fields) {
				break
			}
			oldPath = path
			path = fields[i] // renamed: old then new — report the new path
			i++
		}
		files = append(files, ChangedFile{Path: path, Status: status, OldPath: oldPath})
	}

	if head == "" {
		untracked, err := gitVerbatim(repoRoot, "ls-files", "--others", "--exclude-standard", "-z")
		if err == nil {
			for _, path := range splitNUL(untracked) {
				files = append(files, ChangedFile{Path: path, Status: "A"})
			}
		}
	}
	return files, nil
}

// splitNUL splits a git -z stream into its fields, dropping the empty tail the
// final terminator leaves behind.
func splitNUL(out string) []string {
	var fields []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			fields = append(fields, f)
		}
	}
	return fields
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

// gitVerbatim runs git with path quoting off, so a path with a non-ASCII byte
// arrives as its own bytes instead of as a quoted C string. Set per invocation
// (-c) rather than relied on from the repo's config: the repo belongs to the
// user, and this is our parsing requirement, not their preference.
func gitVerbatim(repoRoot string, args ...string) (string, error) {
	out, err := gitBytes(repoRoot, append([]string{"-c", "core.quotePath=false"}, args...)...)
	return string(out), err
}

func gitBytes(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	return cmd.Output()
}

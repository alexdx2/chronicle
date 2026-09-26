// Package gitdiff provides small git helpers for diff-driven features
// (review report, refresh). Public so chronicle-pro can reuse them.
package gitdiff

import (
	"fmt"
	"os/exec"
	"strconv"
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
//
// --end-of-options for the same reason, one level up: base and head are data,
// not flags. They reach here from a review_report tool argument an agent chose,
// so without it a base of "--output=<path>" is read by git as an option — it
// truncates that file, writes an empty diff, and exits 0, which this function
// then reports as "no files changed". Verified: the file goes to zero bytes
// with exit 0 unflagged, and git refuses with exit 128 flagged.
func ChangedFiles(repoRoot, base, head string) ([]ChangedFile, error) {
	args := []string{"diff", "--name-status", "-M", "-z", "--end-of-options"}
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
	out, err := gitBytes(repoRoot, "show", "--end-of-options", ref+":"+path)
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", ref, path, err)
	}
	return out, nil
}

// MergeBase returns the merge base of ref and HEAD.
func MergeBase(repoRoot, ref string) (string, error) {
	out, err := git(repoRoot, "merge-base", "--end-of-options", ref, "HEAD")
	if err != nil {
		return "", fmt.Errorf("git merge-base %s HEAD: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}

// Hunk is one unified-diff range: the header "@@ -OldStart,OldLines
// +NewStart,NewLines @@" from a -U0 diff. OldLines==0 is a pure insertion
// after OldStart in base (there is no base-side line to anchor to); NewLines==0
// is a pure deletion (nothing survives on the head side).
type Hunk struct {
	OldStart, OldLines, NewStart, NewLines int
}

// FileHunks is one changed file's line ranges from a diff.
type FileHunks struct {
	Path string // head-side path (post-rename)
	// OldPath is the base-side path when this file was renamed, else "".
	OldPath string
	// Binary is true for "Binary files a/x and b/x differ" — Hunks is empty,
	// there is no line-range content to report.
	Binary bool
	Hunks  []Hunk
}

// DiffHunks returns, per changed file, the line ranges that differ between
// base and head (head == "" compares base against the working tree, as in
// ChangedFiles — that is what a reviewer means by "my current changes").
//
// `git -c core.quotePath=false diff -U0 -M --no-color --end-of-options <range>`:
//   - core.quotePath=false: same reasoning as ChangedFiles — a path with a
//     non-ASCII byte must come back as real UTF-8, not a quoted C string.
//   - -U0: zero context lines. A hunk header's own OldStart/OldLines and
//     NewStart/NewLines are then exactly the changed range, and nothing but
//     hunk headers and the changed content lines (+/-) appear between one
//     file's diff and the next — which is what makes the header-only,
//     line-oriented parse below sufficient (content lines are never parsed).
//   - -M: detect renames, so a moved-and-lightly-edited file is reported as
//     one FileHunks with OldPath/Path set, rather than a delete of the old
//     path plus an unrelated add of the new one.
//   - --no-color: guards against a user's color.ui=always turning the header
//     lines this function matches on into escape-coded text.
//   - --end-of-options: base and head are external data, not flags, for the
//     same reason as ChangedFiles/Show/MergeBase — a base of "--output=<path>"
//     read as an option would let git act on that path instead of failing.
//     Verified empirically against this exact command line: unflagged, git
//     would take "--output=..." as a real option; flagged, git exits 128
//     ("option ... must come before non-option arguments") and touches
//     nothing on disk.
func DiffHunks(repoRoot, base, head string) ([]FileHunks, error) {
	args := []string{"diff", "-U0", "-M", "--no-color", "--end-of-options"}
	if head == "" {
		args = append(args, base)
	} else {
		args = append(args, base+".."+head)
	}
	out, err := gitVerbatim(repoRoot, args...)
	if err != nil {
		return nil, fmt.Errorf("git diff -U0 (base %s): %w", base, err)
	}

	// -U0 output is line-oriented text, not NUL-separated like ChangedFiles's
	// -z output, so it's parsed by newline here.
	var files []FileHunks
	idx := -1 // index of the FileHunks currently being filled in, -1 = none yet
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git a/"):
			files = append(files, FileHunks{Path: diffGitHeaderPath(line)})
			idx = len(files) - 1
		case idx < 0:
			// A line before any "diff --git" (shouldn't happen) — ignore.
		case strings.HasPrefix(line, "rename from "):
			files[idx].OldPath = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			files[idx].Path = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ"):
			files[idx].Binary = true
		case strings.HasPrefix(line, "@@ "):
			if h, ok := parseHunkHeader(line); ok {
				files[idx].Hunks = append(files[idx].Hunks, h)
			}
		}
	}
	return files, nil
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

// diffGitHeaderPath recovers the path from a "diff --git a/<old> b/<new>"
// line. The unrenamed case is the common one and the one this must get
// exactly right even with a space or non-ASCII byte in the name: the path is
// repeated on both sides, so the correct split is the " b/" occurrence where
// the text before it equals the text after.
//
// A rename's old != new defeats that equality check. DiffHunks doesn't
// depend on getting the split right there, though: with -M, a renamed
// "diff --git" line is always followed by unambiguous "rename from"/
// "rename to" lines that overwrite OldPath/Path right after — so on that
// path this only has to return SOME path (the last "b/..." candidate), to be
// corrected immediately by the caller's loop.
func diffGitHeaderPath(line string) string {
	const prefix = "diff --git a/"
	rest := strings.TrimPrefix(line, prefix)
	if rest == line {
		return ""
	}
	const sep = " b/"
	last := ""
	for i := 0; i < len(rest); {
		j := strings.Index(rest[i:], sep)
		if j < 0 {
			break
		}
		pos := i + j
		left, right := rest[:pos], rest[pos+len(sep):]
		if left == right {
			return right
		}
		last = right
		i = pos + 1
	}
	return last
}

// parseHunkHeader parses a "@@ -OldStart,OldLines +NewStart,NewLines @@"
// line, optionally followed by trailing context git adds (e.g. the enclosing
// function name) after the closing "@@" — ignored.
//
// Either count is optional in real git output: a single-line range omits its
// ",count" entirely (git writes "@@ -5 +7 @@" for a one-line range, not
// "@@ -5,1 +7,1 @@"), so its absence means count == 1, not count == 0 — a
// count of exactly 0 (pure insertion or deletion on that side) is always
// written explicitly as ",0".
func parseHunkHeader(line string) (Hunk, bool) {
	if !strings.HasPrefix(line, "@@ -") {
		return Hunk{}, false
	}
	rest := line[len("@@ "):] // "-a,b +c,d @@ ..." (or "-a +c @@ ...")
	end := strings.Index(rest, " @@")
	if end < 0 {
		return Hunk{}, false
	}
	fields := strings.Fields(rest[:end])
	if len(fields) != 2 {
		return Hunk{}, false
	}
	oldStart, oldLines, ok := parseHunkRange(fields[0], '-')
	if !ok {
		return Hunk{}, false
	}
	newStart, newLines, ok := parseHunkRange(fields[1], '+')
	if !ok {
		return Hunk{}, false
	}
	return Hunk{OldStart: oldStart, OldLines: oldLines, NewStart: newStart, NewLines: newLines}, true
}

// parseHunkRange parses one side of a hunk header ("-a,b" or "-a" / "+c,d" or
// "+c") after checking its leading sign byte.
func parseHunkRange(field string, sign byte) (start, count int, ok bool) {
	if field == "" || field[0] != sign {
		return 0, 0, false
	}
	field = field[1:]
	startStr, countStr, hasComma := strings.Cut(field, ",")
	start, err := strconv.Atoi(startStr)
	if err != nil {
		return 0, 0, false
	}
	if !hasComma {
		return start, 1, true
	}
	count, err = strconv.Atoi(countStr)
	if err != nil {
		return 0, 0, false
	}
	return start, count, true
}

// Package gitutil runs git for the rest of Chronicle.
//
// Five packages used to carry their own three-line exec wrapper, each with a
// slightly different answer to the same two questions: where does git run, and
// what does a non-zero exit mean. One helper here means one answer — and one
// place to look when git behaves differently than a caller expected.
package gitutil

import (
	"errors"
	"os/exec"
	"strings"
)

// Run executes git in dir and returns its trimmed stdout. An empty dir runs in
// the process working directory (git's own default), which is what a caller
// that never configured a project root means.
func Run(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", withDir(dir, args)...).Output()
	return strings.TrimSpace(string(out)), err
}

// RunRaw is Run without the trim: git's stdout byte for byte.
//
// Every NUL-separated form (`-z`) needs it, and so does anything whose output
// can legally begin or end with whitespace — a path may contain, start with or
// end in a space, and Run's TrimSpace would quietly shorten it. Trimming is
// the right default for the questions git answers with one token; it is the
// wrong one for a stream of paths.
func RunRaw(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", withDir(dir, args)...).Output()
	return string(out), err
}

// OK reports whether git exited zero. It is the form for the questions whose
// answer IS the exit code ("is this commit an ancestor", "does this object
// exist"), where stdout carries nothing worth reading.
func OK(dir string, args ...string) bool {
	return exec.Command("git", withDir(dir, args)...).Run() == nil
}

// Output runs git and returns stdout AND stderr on failure, so an error can
// quote what git actually said instead of only that it failed.
func Output(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", withDir(dir, args)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ExitCode is git's exit status, or -1 when the command never ran (git
// missing, dir gone). The difference matters wherever git uses exit 1 as an
// answer — `merge-base --is-ancestor` says "no" that way — because reading a
// failure to launch as "no" would silently invent an answer.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func withDir(dir string, args []string) []string {
	if dir == "" {
		return args
	}
	return append([]string{"-C", dir}, args...)
}

// Branch is the branch HEAD points at in dir, or "" when there is none to
// name: a detached HEAD, an unborn branch, or no git at all. Callers record it
// as a label — "this was scanned on main" — so a value that is not a branch
// name is worse than no value, and "HEAD" (what rev-parse prints when
// detached) is exactly that.
func Branch(dir string) string {
	out, err := Output(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || out == "HEAD" {
		return ""
	}
	return out
}

// BranchAt is the branch a commit was reached on, which is knowable only when
// that commit is the one currently checked out. Asking "which branch is sha
// on" has no single answer — a commit can sit on many branches or none — so
// anything but HEAD returns "": a label that might be wrong is worse than a
// missing one, and the caller that records it (a scan naming a commit it did
// not check out, a backfill) has nothing true to say about a branch.
func BranchAt(dir, sha string) string {
	if sha == "" {
		return ""
	}
	head, err := Output(dir, "rev-parse", "HEAD")
	if err != nil || head != sha {
		return ""
	}
	return Branch(dir)
}

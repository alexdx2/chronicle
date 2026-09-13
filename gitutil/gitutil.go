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

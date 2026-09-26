package snapshot

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
)

// GraphVersion identifies one point in a repository's history at which
// .depbot/graph.jsonl carried a particular snapshot: the commit that last
// changed the file (in the history of some revision), that commit's git blob
// of the file, and the snapshot's own Header.Seq as of that commit.
//
// Named GraphVersion, not Version: this package already exports Version as
// the snapshot FORMAT's own major version constant (format.go) — the same
// name for this, a wholly different concept (a resolved point in a
// repository's commit history), would not even compile (they collide as the
// same package-level identifier), let alone read clearly side by side with
// Header.Version.
type GraphVersion struct {
	Commit string // commit that last changed .depbot/graph.jsonl in the history of Rev (git log -1 --format=%H rev -- path)
	Blob   string // git blob of the file at Commit
	Seq    int    // Header.Seq at Commit
}

// ErrNoVersion reports that no commit in the requested revision's history
// ever touched .depbot/graph.jsonl — there is no graph version to resolve.
// This is the ordinary, expected answer for a revision that predates the
// graph being adopted at all, not a failure of the git plumbing itself.
var ErrNoVersion = errors.New("no graph version in history of this revision")

// ResolveVersion finds the nearest ancestor of rev (rev itself included) that
// last changed .depbot/graph.jsonl, and reads that commit's Header.Seq via a
// cheap header-only read (ReadHeader) rather than a full Decode.
//
// Every git invocation here follows gitdiff.go's own established convention:
// exec.Command("git", "-C", repoRoot, ...), with rev passed behind
// --end-of-options so a revision string that looks like a flag (or is
// exactly "-") is never parsed as one — see lastCommitTouching and blobAt.
func ResolveVersion(repoRoot, rev string) (GraphVersion, error) {
	commit, err := lastCommitTouching(repoRoot, rev, Path)
	if err != nil {
		return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: %w", err)
	}
	if commit == "" {
		return GraphVersion{}, ErrNoVersion
	}

	blob, err := blobAt(repoRoot, commit, Path)
	if err != nil {
		return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: blob of %s at %s: %w", Path, commit, err)
	}

	data, err := gitdiff.Show(repoRoot, commit, Path)
	if err != nil {
		return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: read %s at %s: %w", Path, commit, err)
	}
	h, err := ReadHeader(bytes.NewReader(data))
	if err != nil {
		return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: header at %s: %w", commit, err)
	}

	return GraphVersion{Commit: commit, Blob: blob, Seq: h.Seq}, nil
}

// AtCommit loads the full graph snapshot recorded at v.Commit: `git show
// v.Commit:.depbot/graph.jsonl` (via gitdiff.Show, not reimplemented here),
// then Decode — so a malformed or hostile historical graph.jsonl degrades to
// Warnings exactly like decoding one from disk does, rather than panicking or
// aborting.
func AtCommit(repoRoot string, v GraphVersion) (*Snapshot, []Warning, error) {
	data, err := gitdiff.Show(repoRoot, v.Commit, Path)
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot: AtCommit: %w", err)
	}
	return Decode(bytes.NewReader(data), DefaultLimits())
}

// lastCommitTouching returns the full SHA of the most recent commit at or
// before rev (in rev's own history) that changed path, or "" if no commit
// reachable from rev ever touched it (git log exits 0 with empty stdout in
// that case — that is a real, negative answer, not a git failure).
func lastCommitTouching(repoRoot, rev, path string) (string, error) {
	out, err := runGit(repoRoot, "log", "-1", "--format=%H", "--end-of-options", rev, "--", path)
	if err != nil {
		return "", fmt.Errorf("git log -1 %s -- %s: %w", rev, path, err)
	}
	return strings.TrimSpace(out), nil
}

// blobAt returns the git blob object id of path at rev, via `git rev-parse
// --verify rev:path`.
//
// --verify is not optional decoration here: plain `git rev-parse
// --end-of-options <rev>:<path>` does NOT behave like `git show`/`git log`'s
// --end-of-options — rev-parse does not route it through the same option
// parser, so an option-shaped rev slips straight past it (confirmed
// empirically: --end-of-options alone let a "--upload-pack=..."-shaped rev
// through, and even echoed the literal string "--end-of-options" as a bogus
// output line). Adding --verify puts rev-parse into single-revision mode,
// under which --end-of-options actually shields the argument the same way it
// does for show/log/diff (confirmed: the same option-shaped input then fails
// cleanly with "fatal: Needed a single revision", no echo, nothing executed).
func blobAt(repoRoot, rev, path string) (string, error) {
	out, err := runGit(repoRoot, "rev-parse", "--verify", "--end-of-options", rev+":"+path)
	if err != nil {
		return "", fmt.Errorf("git rev-parse --verify %s:%s: %w", rev, path, err)
	}
	return strings.TrimSpace(out), nil
}

// runGit runs git in repoRoot and returns its stdout, the same minimal shape
// gitdiff.go's own git()/gitBytes() use.
func runGit(repoRoot string, args ...string) (string, error) {
	out, err := runGitBytes(repoRoot, args...)
	return string(out), err
}

// runGitVerbatim runs git with path quoting off (-c core.quotePath=false),
// for any call whose OUTPUT includes a path git would otherwise render as a
// quoted C string for non-ASCII bytes (e.g. "src/caf\303\251.ts" WITH the
// quote marks) — see gitdiff.go's gitVerbatim, which this mirrors. Only
// output-producing, path-listing calls need this; a call that merely
// receives a path as an argument (blobAt, lastCommitTouching) is unaffected
// by core.quotePath, which governs git's own printing, not its argument
// parsing.
func runGitVerbatim(repoRoot string, args ...string) (string, error) {
	out, err := runGitBytes(repoRoot, append([]string{"-c", "core.quotePath=false"}, args...)...)
	return string(out), err
}

func runGitBytes(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	return cmd.Output()
}

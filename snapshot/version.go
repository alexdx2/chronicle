package snapshot

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
)

// GraphVersion identifies one graph version: the commit whose tree holds
// .depbot/graph.jsonl (a PUBLICATION commit on chronicle/graphs/* or
// chronicle/heads/*, or — in the fallback — an in-tree commit of the code
// branch), that commit's blob of the file, and the snapshot's Header.Seq.
// CodeSha, SnapshotID, Ref, Signature and Source are filled only when the
// version came from a publication (spec §3.2, §7); they are zero in the
// fallback, where Commit itself is the code commit.
type GraphVersion struct {
	Commit string
	Blob   string
	Seq    int

	CodeSha    string
	SnapshotID string
	Ref        string
	Signature  SignatureStatus
	Source     string // "chain" | "head" | "tree"
}

// ResolveOptions tunes ResolveVersionWith.
type ResolveOptions struct {
	// IncludeHeads lists code shas whose chronicle/heads/<sha> publication
	// may be used. Heads carry no signature (spec §4.2), so a local core
	// uses only its OWN head, and only when asked.
	IncludeHeads []string
	// Verifier overrides LoadVerifier(repoRoot). nil = load and close one.
	Verifier *Verifier
}

// ErrNoVersion: no publication in the history of rev and no commit in that
// history ever touched .depbot/graph.jsonl.
var ErrNoVersion = errors.New("no graph version in history of this revision")

// ResolveVersion is ResolveVersionWith with default options.
func ResolveVersion(repoRoot, rev string) (GraphVersion, error) {
	return ResolveVersionWith(repoRoot, rev, ResolveOptions{})
}

// ResolveVersionWith finds the graph version for rev (spec §7):
//  1. the publication map (code sha → trusted publication) over every
//     chronicle/graphs/* ref (remote twins first) plus the requested heads;
//  2. the first commit of `git rev-list rev` that has an entry;
//  3. fallback: the in-tree history (`git log -1 rev -- .depbot/graph.jsonl`),
//     for repositories that still commit snapshots to the code branch.
func ResolveVersionWith(repoRoot, rev string, opt ResolveOptions) (GraphVersion, error) {
	pubs, _, err := PublicationMap(repoRoot, opt)
	if err != nil {
		return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: %w", err)
	}
	if len(pubs) > 0 {
		out, err := runGit(repoRoot, "rev-list", "--end-of-options", rev)
		if err != nil {
			return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: rev-list %s: %w", rev, err)
		}
		for _, sha := range strings.Split(strings.TrimSpace(out), "\n") {
			if p, ok := pubs[sha]; ok {
				return versionFromPublication(repoRoot, p)
			}
		}
	}
	return resolveInTree(repoRoot, rev)
}

// PublicationMap builds code sha → winning publication. Within one ref the
// publication closest to the tip wins; across refs, a requested head beats
// any chain (it describes exactly that sha); among chains the first ref in
// ChainRefs order wins (remote twins before local). Warnings list skipped
// commits: malformed trailers, and — in VerifySigned mode — unsigned or
// foreign-signed commits.
func PublicationMap(repoRoot string, opt ResolveOptions) (map[string]Publication, []string, error) {
	v := opt.Verifier
	if v == nil {
		loaded, err := LoadVerifier(repoRoot)
		if err != nil {
			return nil, nil, err
		}
		defer loaded.Close()
		v = loaded
	}
	var warns []string
	warns = append(warns, v.Warnings...)
	verify := v.Mode == VerifySigned
	result := map[string]Publication{}

	refs, err := ChainRefs(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	for _, ref := range refs {
		pubs, w, err := listPublications(repoRoot, ref, v.GitArgs(), verify)
		if err != nil {
			return nil, nil, err
		}
		warns = append(warns, w...)
		for _, p := range pubs { // tip first: the first seen per code sha is the closest to the tip
			if !v.Trusted(p) {
				warns = append(warns, fmt.Sprintf("%s: %s: signature %s — not a trusted publication", ref, p.Commit, p.Signature))
				continue
			}
			if _, seen := result[p.CodeSha]; !seen {
				result[p.CodeSha] = p
			}
		}
	}
	for _, sha := range opt.IncludeHeads {
		local, remote := HeadRef(sha)
		for _, ref := range []string{remote, local} {
			if _, err := runGit(repoRoot, "rev-parse", "--verify", "-q", "--end-of-options", ref); err != nil {
				continue
			}
			pubs, w, err := listPublications(repoRoot, ref, nil, false) // heads are never signed
			if err != nil {
				return nil, nil, err
			}
			warns = append(warns, w...)
			if len(pubs) > 0 && pubs[0].CodeSha == sha {
				p := pubs[0]
				p.Signature = SigUnchecked
				result[sha] = p // own head beats the chain
				break
			}
		}
	}
	return result, warns, nil
}

func versionFromPublication(repoRoot string, p Publication) (GraphVersion, error) {
	blob, err := blobAt(repoRoot, p.Commit, Path)
	if err != nil {
		return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: blob of %s at publication %s: %w", Path, p.Commit, err)
	}
	data, err := gitdiff.Show(repoRoot, p.Commit, Path)
	if err != nil {
		return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: read %s at publication %s: %w", Path, p.Commit, err)
	}
	h, err := ReadHeader(bytes.NewReader(data))
	if err != nil {
		return GraphVersion{}, fmt.Errorf("snapshot: ResolveVersion: header at %s: %w", p.Commit, err)
	}
	source := "chain"
	if strings.HasPrefix(p.Ref, HeadRefPrefix) || strings.HasPrefix(p.Ref, RemoteHeadRefPrefix) {
		source = "head"
	}
	return GraphVersion{
		Commit: p.Commit, Blob: blob, Seq: h.Seq,
		CodeSha: p.CodeSha, SnapshotID: p.SnapshotID, Ref: p.Ref, Signature: p.Signature, Source: source,
	}, nil
}

// resolveInTree is the pre-publication behaviour, kept verbatim as the
// fallback for repositories whose snapshots live in the code branch.
func resolveInTree(repoRoot, rev string) (GraphVersion, error) {
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
	return GraphVersion{Commit: commit, Blob: blob, Seq: h.Seq, Source: "tree"}, nil
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

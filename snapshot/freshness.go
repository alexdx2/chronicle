package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/manifest"
)

// FileState is the trust level a scanned file has right now, relative to what
// a graph snapshot's Coverage says about it.
type FileState string

const (
	// Fresh means the file's current blob matches the Coverage that recorded
	// it, at an extractor version no older than the newest use of that same
	// extractor anywhere else in the snapshot.
	Fresh FileState = "fresh"
	// Stale means the file's current blob does not match any Coverage
	// recorded for it — its content moved since it was last scanned.
	Stale FileState = "stale"
	// VersionStale means the file's content has NOT changed (its blob still
	// matches), but the snapshot shows a newer version of the same extractor
	// having run elsewhere — this file's facts were produced by extractor
	// logic that has since been superseded.
	VersionStale FileState = "version_stale"
	// Unscanned means the file is in scan scope (claimed by one of the
	// snapshot's domains) but no Coverage record mentions it at all.
	Unscanned FileState = "unscanned"
	// Deleted means a Coverage record names a file that no longer exists at
	// Rev (or on disk, for the working tree) — the graph still holds facts
	// sourced from a file that is gone.
	Deleted FileState = "deleted"
)

// FileFreshness is one file's freshness verdict.
type FileFreshness struct {
	Path  string
	State FileState
}

// Freshness is ComputeFreshness's report: which graph version it compared
// against, every non-fresh-or-notable file's state, and what changed in the
// tree since that graph version was written.
type Freshness struct {
	Rev   string
	Graph GraphVersion
	// Files is sorted by Path. It holds every scan-scope file (Fresh,
	// Stale, VersionStale or Unscanned) plus every Deleted Coverage path —
	// filtering it down to non-Fresh only (Compact) is the calling code's
	// job, not this package's: ComputeFreshness's own tests need to see
	// Fresh entries too.
	Files []FileFreshness
	// ChangedSince is `git diff --name-only Graph.Commit..Rev` (or, for
	// Rev=="", Graph.Commit against the working tree, untracked files
	// included), sorted.
	ChangedSince []string
}

// ComputeFreshness reports, for every file Rev's manifest-scanned domains
// claim, whether the graph snapshot snap's Coverage of it is still
// trustworthy.
//
// Scan scope is `git ls-tree -r --name-only rev`, filtered through man:
// DomainForFile must return one of snap.Header.Domains for the file to be in
// scope — a file outside every domain (DomainForFile's "_unassigned"
// sentinel, or a domain absent from this snapshot) is silently skipped, the
// same way a domain-less manifest silently admits nothing.
//
// Per scope file:
//   - no Coverage record at all for it → Unscanned.
//   - its current blob matches no Coverage.Blob recorded for it → Stale.
//   - its current blob DOES match, but the snapshot has a Coverage entry
//     for the SAME ExtractorID, at a newer ExtractorVersion, for ANY file
//     (not just this one) → VersionStale.
//   - otherwise → Fresh.
//
// Independent of scan scope: any Coverage.Path with no file at Rev at all
// is reported Deleted — the file may have moved out of every domain's
// include/exclude, but the graph still carries facts sourced from it.
//
// rev == "" means the working tree rather than a git commit (used by
// commands with no commit to point at, and by Task 9's SnapshotSource): scan
// scope and every file's blob are read from disk (os.ReadDir, gitutil.
// FileBlobSHA) instead of from git. The graph version this compares against
// is still resolved from git history — a graph is always a committed
// artifact, and "how fresh is my uncommitted working tree against the last
// committed graph" is exactly the question this mode answers — via
// ResolveVersion(repoRoot, "HEAD"). Freshness.Rev is left "" either way, and
// the rest of the algorithm (Coverage comparison, every FileState, and
// ChangedSince) is identical to the git-history case.
func ComputeFreshness(repoRoot string, snap *Snapshot, man *manifest.Manifest, rev string) (Freshness, error) {
	if snap == nil {
		return Freshness{}, fmt.Errorf("snapshot: ComputeFreshness: snapshot is nil")
	}
	if man == nil {
		return Freshness{}, fmt.Errorf("snapshot: ComputeFreshness: manifest is nil")
	}

	graphRev := rev
	if graphRev == "" {
		graphRev = "HEAD"
	}
	version, err := ResolveVersion(repoRoot, graphRev)
	if err != nil {
		return Freshness{}, fmt.Errorf("snapshot: ComputeFreshness: %w", err)
	}

	exists, blobOf, err := freshnessSource(repoRoot, rev)
	if err != nil {
		return Freshness{}, fmt.Errorf("snapshot: ComputeFreshness: %w", err)
	}

	domains := make(map[string]bool, len(snap.Header.Domains))
	for _, d := range snap.Header.Domains {
		domains[d] = true
	}

	var scope []string
	for path := range exists {
		if domains[man.DomainForFile(path)] {
			scope = append(scope, path)
		}
	}
	sort.Strings(scope)

	// A path may legitimately appear on more than one raw Coverage line —
	// Decode does not dedupe/group them the way FromStore's groupCoverage
	// does at export time, and this reads whatever a historical commit
	// actually wrote.
	byPath := make(map[string][]Coverage, len(snap.Coverage))
	for _, c := range snap.Coverage {
		byPath[c.Path] = append(byPath[c.Path], c)
	}

	seen := make(map[string]bool, len(scope))
	files := make([]FileFreshness, 0, len(scope))
	for _, path := range scope {
		seen[path] = true
		state, err := freshnessState(path, byPath[path], snap.Coverage, blobOf)
		if err != nil {
			return Freshness{}, fmt.Errorf("snapshot: ComputeFreshness: %s: %w", path, err)
		}
		files = append(files, FileFreshness{Path: path, State: state})
	}

	// Deleted: independent of scan scope entirely — a Coverage path outside
	// every domain, or outside include/exclude entirely, still gets reported
	// if the graph holds facts sourced from a file that is simply gone.
	for path := range byPath {
		if seen[path] {
			continue
		}
		if !exists[path] {
			files = append(files, FileFreshness{Path: path, State: Deleted})
			seen[path] = true
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	changedFiles, err := gitdiff.ChangedFiles(repoRoot, version.Commit, rev)
	if err != nil {
		return Freshness{}, fmt.Errorf("snapshot: ComputeFreshness: changes since %s: %w", version.Commit, err)
	}
	changedSince := make([]string, 0, len(changedFiles))
	for _, c := range changedFiles {
		changedSince = append(changedSince, c.Path)
	}
	sort.Strings(changedSince)

	return Freshness{
		Rev:          rev,
		Graph:        version,
		Files:        files,
		ChangedSince: changedSince,
	}, nil
}

// freshnessSource returns, for rev, the set of paths that currently exist
// and a function to fetch any one of their current blobs — from git (rev !=
// "") or from disk (rev == ""), per ComputeFreshness's doc comment.
func freshnessSource(repoRoot, rev string) (exists map[string]bool, blobOf func(string) (string, error), err error) {
	if rev == "" {
		disk, err := diskTree(repoRoot)
		if err != nil {
			return nil, nil, fmt.Errorf("walk working tree: %w", err)
		}
		return disk, func(path string) (string, error) {
			return gitutil.FileBlobSHA(repoRoot, path)
		}, nil
	}

	paths, err := lsTree(repoRoot, rev)
	if err != nil {
		return nil, nil, fmt.Errorf("git ls-tree %s: %w", rev, err)
	}
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set, func(path string) (string, error) {
		return blobAt(repoRoot, rev, path)
	}, nil
}

// freshnessState decides one scope file's FileState: matches is every raw
// Coverage record for exactly this path (possibly several); all is every
// Coverage record in the whole snapshot, for the VersionStale cross-file
// extractor-version check.
func freshnessState(path string, matches []Coverage, all []Coverage, blobOf func(string) (string, error)) (FileState, error) {
	if len(matches) == 0 {
		return Unscanned, nil
	}

	blob, err := blobOf(path)
	if err != nil {
		return "", fmt.Errorf("blob: %w", err)
	}

	var matchedExtractors []ExtractorRun
	blobMatched := false
	for _, c := range matches {
		if c.Blob == blob {
			blobMatched = true
			matchedExtractors = append(matchedExtractors, c.Extractors...)
		}
	}
	if !blobMatched {
		return Stale, nil
	}

	for _, mine := range matchedExtractors {
		for _, c := range all {
			for _, other := range c.Extractors {
				if other.ID == mine.ID && extractorVersionNewer(other.Version, mine.Version) {
					return VersionStale, nil
				}
			}
		}
	}
	return Fresh, nil
}

// extractorVersionNewer reports whether candidate is a newer extractor
// version than base. Comparison is lexicographic by string
// ("fact-schema@2" > "fact-schema@1"), and only ever applied when BOTH
// strings parse as "name@N" (an "@" followed by one or more decimal digits) —
// when either does not, the two versions are simply unequal-or-not with no
// ordering between them, so this returns false ("no basis for comparison")
// rather than guess a direction from an arbitrary string compare.
func extractorVersionNewer(candidate, base string) bool {
	if candidate == base {
		return false
	}
	if !looksLikeNameAtN(candidate) || !looksLikeNameAtN(base) {
		return false
	}
	return candidate > base
}

// looksLikeNameAtN reports whether v ends in "@" followed by one or more
// decimal digits.
func looksLikeNameAtN(v string) bool {
	i := strings.LastIndexByte(v, '@')
	if i < 0 || i == len(v)-1 {
		return false
	}
	for _, r := range v[i+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// lsTree lists every file path in rev's tree, via `git ls-tree -r --name-only
// -z rev`. -c core.quotePath=false and -z together are what gitdiff.go's own
// gitVerbatim relies on for the same reason here: quotePath would otherwise
// render a non-ASCII path as a quoted C string (WITH the quote characters),
// and anything but -z would risk losing a path at the first embedded
// whitespace.
func lsTree(repoRoot, rev string) ([]string, error) {
	out, err := runGitVerbatim(repoRoot, "ls-tree", "-r", "--name-only", "-z", "--end-of-options", rev)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// splitNUL splits a git -z stream into its fields, dropping the empty tail
// the final terminator leaves behind. Same shape as gitdiff.go's own
// (unexported, package-private) splitNUL — duplicated rather than shared
// because this task does not touch gitdiff.go.
func splitNUL(out string) []string {
	var fields []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

// diskTree lists every regular file under repoRoot as a slash-separated path
// relative to it, via os.ReadDir recursion — the working-tree equivalent of
// lsTree. ".git" is skipped: it is never part of a git tree either, and
// walking into it would be pure waste (and, for a manifest domain whose
// include pattern is broad, a correctness hazard — every internal object
// file would otherwise be a candidate scan-scope "file").
func diskTree(repoRoot string) (map[string]bool, error) {
	exists := make(map[string]bool)
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if e.IsDir() {
				if e.Name() == ".git" {
					continue
				}
				if err := walk(full); err != nil {
					return err
				}
				continue
			}
			rel, err := filepath.Rel(repoRoot, full)
			if err != nil {
				return err
			}
			exists[filepath.ToSlash(rel)] = true
		}
		return nil
	}
	if err := walk(repoRoot); err != nil {
		return nil, err
	}
	return exists, nil
}

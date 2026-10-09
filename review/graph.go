package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// Origin values a VersionGraph can report. "conflicted" is reserved for a
// raw git merge that left literal conflict-marker text in graph.jsonl (Task
// 6's server-side-merge fallback path) — no implementation in this package
// produces it; SnapshotSource.Load only ever returns "snapshot" or
// "missing".
const (
	OriginSnapshot   = "snapshot"
	OriginMissing    = "missing"
	OriginConflicted = "conflicted"
)

// GraphSource resolves one point in a repository's history (or its working
// tree) to the graph that was effective there.
type GraphSource interface {
	Load(ctx context.Context) (*VersionGraph, error)
}

// VersionGraph is one GraphSource's answer: the graph loaded at a revision
// (or the working tree), plus enough identity and freshness information for
// a caller to reason about how trustworthy and how current it is.
type VersionGraph struct {
	// Ref is the revision as the caller asked for it (a SHA, branch name,
	// "HEAD", ...); Commit is that same revision's resolved identity. Both
	// are "" for the working tree (a future report renderer prints that as
	// "working tree" — see SnapshotSource's doc comment). SnapshotSource
	// itself never resolves Ref to a different Commit (see its own doc
	// comment for why); a future non-git GraphSource (e.g. a remote/
	// federation source) is where the two would actually diverge.
	Ref, Commit string

	// Graph is the loaded graph, ready for queries — nil when Origin ==
	// "missing".
	Graph *graph.Graph
	// Export is the canonical snapshot this VersionGraph was built from —
	// what Diff compares. Nil when Origin == "missing".
	Export *snapshot.Snapshot

	// Version identifies exactly which graph.jsonl content this is: the
	// commit that last touched it (empty for the working tree), that
	// content's git blob, and its own Header.Seq.
	Version snapshot.GraphVersion
	// Freshness reports, file by file, whether the graph's Coverage of the
	// current tree (or working tree) is still trustworthy.
	Freshness snapshot.Freshness

	// Origin is one of the values above.
	Origin string

	// Close releases any resources backing Graph (its temp SQLite store).
	// Never nil, even when Origin == "missing" and Graph/Export are nil, so
	// callers can unconditionally `defer vg.Close()`.
	Close func() error
}

// noopClose is the closer for a VersionGraph with nothing to release
// (Origin == "missing").
func noopClose() error { return nil }

// SnapshotSource loads the graph effective at Rev in the repository rooted
// at RepoRoot.
//
// Rev == "" means the working tree, the same convention
// gitdiff.ChangedFiles/DiffHunks and snapshot.ComputeFreshness already use:
// .depbot/graph.jsonl is read directly off disk (os.ReadFile, not `git
// show`) — an uncommitted edit to the graph file itself is what Load sees.
// No file there is Origin: "missing", not an error. Commit and Version.Commit
// are both "" (ResolveVersion is never called: there is no commit to
// resolve a working-tree graph version FROM), Version.Blob is the on-disk
// file's blob (gitutil.FileBlobSHA), and Version.Seq comes from decoding
// that same content's own Header. Freshness is computed against the actual
// files on disk (ComputeFreshness's own rev=="" convention), not HEAD's
// tree; ChangedSince comes back empty (ComputeFreshness's own
// graphVer.Commit=="" convention — there is no commit to diff a
// never-committed graph's freshness FROM) rather than guessing at one — see
// loadWorkingTree's own comment.
//
// Rev != "" is the ordinary path: ResolveVersion finds the nearest ancestor
// of Rev that last touched the graph file, AtCommit loads that content,
// snapshot.Load builds a real graph from it, and ComputeFreshness compares
// Rev's own tree against that graph's Coverage. ErrNoVersion (no commit in
// Rev's history ever touched the graph file) becomes Origin: "missing" with
// Graph/Export nil and a nil error — this is an expected state (a revision
// that predates graph adoption), not a failure.
type SnapshotSource struct {
	RepoRoot, Rev string
}

var _ GraphSource = SnapshotSource{}

// Load implements GraphSource. ctx is honored minimally: Load checks
// ctx.Err() once, up front, as a cheap courtesy so an already-canceled
// context is never acted on — but nothing it calls (ResolveVersion,
// AtCommit, snapshot.Load, ComputeFreshness; all synchronous git-subprocess
// or SQLite calls) accepts a context to cancel deeper down, and inventing
// cancellation plumbing for calls that don't support it would be
// speculative work this task doesn't need. A future remote/federation
// source is where ctx would actually have somewhere to go.
func (s SnapshotSource) Load(ctx context.Context) (*VersionGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("review: SnapshotSource.Load: %w", err)
	}
	if s.Rev == "" {
		return s.loadWorkingTree()
	}
	return s.loadAtRev()
}

func (s SnapshotSource) loadAtRev() (*VersionGraph, error) {
	version, err := snapshot.ResolveVersion(s.RepoRoot, s.Rev)
	if err != nil {
		if errors.Is(err, snapshot.ErrNoVersion) {
			return &VersionGraph{
				Ref: s.Rev, Commit: s.Rev,
				Origin: OriginMissing,
				Close:  noopClose,
			}, nil
		}
		return nil, fmt.Errorf("review: SnapshotSource.Load: resolve version: %w", err)
	}

	snap, _, err := snapshot.AtCommit(s.RepoRoot, version)
	if err != nil {
		return nil, fmt.Errorf("review: SnapshotSource.Load: load graph at %s: %w", version.Commit, err)
	}

	reg, err := loadRegistryFor(s.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("review: SnapshotSource.Load: registry: %w", err)
	}
	g, closer, err := snapshot.Load(snap, reg, snapshot.LoadOptions{})
	if err != nil {
		return nil, fmt.Errorf("review: SnapshotSource.Load: build graph: %w", err)
	}

	man, err := loadManifestFor(s.RepoRoot)
	if err != nil {
		closer()
		return nil, fmt.Errorf("review: SnapshotSource.Load: manifest: %w", err)
	}
	fr, err := snapshot.ComputeFreshness(s.RepoRoot, snap, version, man, s.Rev)
	if err != nil {
		closer()
		return nil, fmt.Errorf("review: SnapshotSource.Load: freshness: %w", err)
	}

	return &VersionGraph{
		Ref: s.Rev, Commit: s.Rev,
		Graph: g, Export: snap,
		Version: version, Freshness: fr,
		Origin: OriginSnapshot,
		Close:  closer,
	}, nil
}

// loadWorkingTree implements the Rev=="" path described in SnapshotSource's
// doc comment.
//
// Version.Commit is "" here (no ResolveVersion call — see above), which
// snapshot.ComputeFreshness treats as "no commit to diff ChangedSince from":
// it leaves Freshness.ChangedSince empty rather than guessing at a fallback
// anchor (fix-round-1: this used to be worked around locally here by
// substituting "HEAD" for that one internal call and restoring the real
// version afterward, but ComputeFreshness itself now handles Commit=="" — a
// shared, already-public function crashing on a valid input state is that
// function's bug to fix, not every caller's workaround to invent; see
// task-9-report.md's fix-round-1 section). An empty ChangedSince is the
// honest answer for a graph that was never committed at all: there's no
// coherent "since" to measure from. A caller that wants a best-effort
// "changed since HEAD" convenience value for this case can compute one
// itself (gitdiff.ChangedFiles(s.RepoRoot, "HEAD", "")) — deliberately not
// done here, since it would reintroduce a dependency on HEAD existing at
// all (see TestSnapshotSource_Load_WorkingTree_NoCommits) for a value nothing
// in this package currently consumes.
func (s SnapshotSource) loadWorkingTree() (*VersionGraph, error) {
	graphPath := filepath.Join(s.RepoRoot, snapshot.Path)
	data, err := os.ReadFile(graphPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &VersionGraph{Origin: OriginMissing, Close: noopClose}, nil
		}
		return nil, fmt.Errorf("review: SnapshotSource.Load: read %s: %w", graphPath, err)
	}

	snap, _, err := snapshot.Decode(bytes.NewReader(data), snapshot.DefaultLimits())
	if err != nil {
		return nil, fmt.Errorf("review: SnapshotSource.Load: decode %s: %w", graphPath, err)
	}

	blob, err := gitutil.FileBlobSHA(s.RepoRoot, snapshot.Path)
	if err != nil {
		return nil, fmt.Errorf("review: SnapshotSource.Load: blob of %s: %w", snapshot.Path, err)
	}
	version := snapshot.GraphVersion{Commit: "", Blob: blob, Seq: snap.Header.Seq}

	reg, err := loadRegistryFor(s.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("review: SnapshotSource.Load: registry: %w", err)
	}
	g, closer, err := snapshot.Load(snap, reg, snapshot.LoadOptions{})
	if err != nil {
		return nil, fmt.Errorf("review: SnapshotSource.Load: build graph: %w", err)
	}

	man, err := loadManifestFor(s.RepoRoot)
	if err != nil {
		closer()
		return nil, fmt.Errorf("review: SnapshotSource.Load: manifest: %w", err)
	}

	fr, err := snapshot.ComputeFreshness(s.RepoRoot, snap, version, man, "")
	if err != nil {
		closer()
		return nil, fmt.Errorf("review: SnapshotSource.Load: freshness: %w", err)
	}

	return &VersionGraph{
		Graph: g, Export: snap,
		Version: version, Freshness: fr,
		Origin: OriginSnapshot,
		Close:  closer,
	}, nil
}

// loadRegistryFor resolves the type registry for repoRoot: a project-local
// .depbot/chronicle.types.yaml wins if present, otherwise the built-in
// defaults — the same convention graph/viewmodel's saliencePolicyFor already
// uses for the equivalent choice. A missing override file is not an error
// (LoadDefaults is always available); a PRESENT but malformed override file
// propagates its parse error rather than silently falling back, since a
// registry that fails to load is a real configuration problem for a
// review's fact classification, not an expected absence.
func loadRegistryFor(repoRoot string) (*registry.Registry, error) {
	typesPath := filepath.Join(repoRoot, ".depbot", "chronicle.types.yaml")
	if _, err := os.Stat(typesPath); err == nil {
		return registry.LoadFile(typesPath)
	}
	return registry.LoadDefaults()
}

// loadManifestFor loads repoRoot's domain manifest from the same
// .depbot/chronicle.domain.yaml convention every other Chronicle entry point
// uses (internal/cli/root.go's manifestPath).
func loadManifestFor(repoRoot string) (*manifest.Manifest, error) {
	return manifest.LoadFile(filepath.Join(repoRoot, ".depbot", "chronicle.domain.yaml"))
}

// FileSource loads a graph version from a snapshot file on disk instead of git history.
// Rev names the revision whose tree the freshness is computed against ("" = working tree);
// Commit is set to the resolved Rev so gitdiff can diff base..head.
//
// In CI this lets the head be the snapshot the pipeline just built while
// freshness is judged against the revision's own tree, so files the pipeline
// re-extracted read as fresh. A missing file at Path is Origin "missing", not
// an error. Commit is "" when Rev is "" or Rev does not resolve (a repository
// with no commits).
type FileSource struct{ RepoRoot, Path, Rev string }

var _ GraphSource = FileSource{}

// Load implements GraphSource.
func (s FileSource) Load(ctx context.Context) (*VersionGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return &VersionGraph{Ref: s.Rev, Origin: OriginMissing, Close: noopClose}, nil
		}
		return nil, fmt.Errorf("review: FileSource.Load: read %s: %w", s.Path, err)
	}

	snap, _, err := snapshot.Decode(bytes.NewReader(data), snapshot.DefaultLimits())
	if err != nil {
		return nil, fmt.Errorf("review: FileSource.Load: decode %s: %w", s.Path, err)
	}

	commit := ""
	if s.Rev != "" {
		if out, rpErr := gitutil.Output(s.RepoRoot, "rev-parse", "--verify", "--end-of-options", s.Rev+"^{commit}"); rpErr == nil {
			commit = out
		}
	}
	blob, err := gitutil.BlobSHA(s.RepoRoot, data)
	if err != nil {
		return nil, fmt.Errorf("review: FileSource.Load: blob of %s: %w", s.Path, err)
	}
	version := snapshot.GraphVersion{Commit: commit, Blob: blob, Seq: snap.Header.Seq}

	reg, err := loadRegistryFor(s.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("review: FileSource.Load: registry: %w", err)
	}
	g, closer, err := snapshot.Load(snap, reg, snapshot.LoadOptions{})
	if err != nil {
		return nil, fmt.Errorf("review: FileSource.Load: build graph: %w", err)
	}

	man, err := loadManifestFor(s.RepoRoot)
	if err != nil {
		closer()
		return nil, fmt.Errorf("review: FileSource.Load: manifest: %w", err)
	}
	fr, err := snapshot.ComputeFreshness(s.RepoRoot, snap, version, man, s.Rev)
	if err != nil {
		closer()
		return nil, fmt.Errorf("review: FileSource.Load: freshness: %w", err)
	}

	return &VersionGraph{
		Ref: s.Rev, Commit: commit,
		Graph: g, Export: snap,
		Version: version, Freshness: fr,
		Origin: OriginSnapshot,
		Close:  closer,
	}, nil
}

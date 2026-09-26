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
// tree — see loadWorkingTree's own comment for the one adjustment this
// requires beyond simply passing rev="" through.
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
// One adjustment beyond a plain rev="" pass-through: ComputeFreshness's
// ChangedSince computes `git diff graphVer.Commit..rev` internally
// (snapshot/freshness.go), and an empty string on BOTH sides of that diff
// (graphVer.Commit=="" here, rev=="" for the working tree) is not "diff
// against nothing" — it is a git argument error ("fatal: ambiguous argument
// ”"), confirmed empirically. There is no commit to anchor ChangedSince to
// when the graph itself has never been committed, so "HEAD" is substituted
// for that one internal call only — the same default this codebase already
// falls back to whenever no revision is given (internal/cli/graph.go's
// runGraphStatus: graphRev defaults to "HEAD"). The real (commit-less)
// version is restored onto the result's Freshness.Graph afterward, so
// Freshness.Graph and VersionGraph.Version never disagree about the graph's
// own identity — only ChangedSince's internal anchor borrows HEAD.
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

	freshnessAnchor := version
	freshnessAnchor.Commit = "HEAD" // see doc comment above
	fr, err := snapshot.ComputeFreshness(s.RepoRoot, snap, freshnessAnchor, man, "")
	if err != nil {
		closer()
		return nil, fmt.Errorf("review: SnapshotSource.Load: freshness: %w", err)
	}
	fr.Graph = version

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

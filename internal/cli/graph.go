package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

func newGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Manage the graph.jsonl snapshot — the git-committed source of truth",
	}
	cmd.AddCommand(newGraphUpdateCmd())
	cmd.AddCommand(newGraphStatusCmd())
	cmd.AddCommand(newGraphShowCmd())
	cmd.AddCommand(newGraphMergeCmd())
	cmd.AddCommand(newGraphMigrateCmd())
	cmd.AddCommand(newGraphRestoreCmd())
	return cmd
}

// noVersionMessage rewrites snapshot.ErrNoVersion into a message naming the
// revision the caller actually asked about — ErrNoVersion's own text ("no
// graph version in history of this revision") is deliberately generic since
// package snapshot has no revision label to put in it.
func noVersionMessage(rev string) error {
	return fmt.Errorf("no graph version in history of %s", rev)
}

// graphUpdateResult is the JSON body `chronicle graph update` (and
// `--check`) print: the file it wrote (or, under --check, would write), its
// new seq, the record counts, and how that compares to what was on disk
// before.
type graphUpdateResult struct {
	Path    string          `json:"path"`
	Seq     int             `json:"seq"`
	Counts  snapshot.Counts `json:"counts"`
	Added   int             `json:"added"`
	Removed int             `json:"removed"`
	Changed int             `json:"changed"`
}

func newGraphUpdateCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Export the live graph to .depbot/graph.jsonl",
		Long: `Export the current state of the database into the canonical, git-committed
graph.jsonl snapshot (atomic write: temp file + rename). Prints a JSON
summary: path, new seq, record counts, and how many records were
added/removed/changed relative to the file it replaced.

--check makes no changes: it reports whether graph.jsonl on disk already
matches what this command would write, and exits 1 if it does not — for a
pre-commit hook or CI gate.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			g := openGraph()
			defer g.Store().Close()

			graphPath := filepath.Join(paths.Dir(), filepath.Base(snapshot.Path))
			result, mismatch, err := runGraphUpdate(g.Store(), g.Registry(), graphPath, check)
			if err != nil {
				return err
			}
			outputJSON(result)
			if check && mismatch {
				return fmt.Errorf("%s is out of date", graphPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Report whether graph.jsonl is up to date instead of writing it (exit 1 if not)")
	return cmd
}

// runGraphUpdate is the testable core of `chronicle graph update`: no
// cobra, no process exit, so it can be called directly against a real store
// and a temp graphPath. mismatch is only meaningful when checkOnly is true.
//
// checkOnly deliberately reconstructs the snapshot using the ON-DISK file's
// OWN seq rather than the next one: a real update always advances seq by
// one (seq is a display-only counter, not a change signal — the version's
// real identity is graph.jsonl's own commit and blob, per the design spec),
// so "what would the NEXT update write" would never byte-match the current
// file even when nothing at all changed. --check instead asks "does
// graph.jsonl still match the store right now", which means holding seq
// steady and comparing everything else.
func runGraphUpdate(st *store.Store, reg *registry.Registry, graphPath string, checkOnly bool) (graphUpdateResult, bool, error) {
	existing, existingErr := os.ReadFile(graphPath)

	existingSeq := 0
	if existingErr == nil {
		if h, err := snapshot.ReadHeader(bytes.NewReader(existing)); err == nil {
			existingSeq = h.Seq
		}
	}
	prevSeq := existingSeq
	if checkOnly && existingSeq > 0 {
		prevSeq = existingSeq - 1
	}

	next, err := snapshot.FromStore(st, snapshot.ExportOptions{
		PrevSeq:  prevSeq,
		Registry: snapshot.RegistryFingerprint(reg),
	})
	if err != nil {
		return graphUpdateResult{}, false, fmt.Errorf("export graph: %w", err)
	}

	prevSnap := &snapshot.Snapshot{}
	if existingErr == nil {
		if decoded, _, derr := snapshot.Decode(bytes.NewReader(existing), snapshot.DefaultLimits()); derr == nil {
			prevSnap = decoded
		}
	}
	changes := snapshot.Summarize(prevSnap, next)

	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, next); err != nil {
		return graphUpdateResult{}, false, fmt.Errorf("encode graph: %w", err)
	}

	result := graphUpdateResult{
		Path:    graphPath,
		Seq:     next.Header.Seq,
		Counts:  next.Header.Counts,
		Added:   changes.Added,
		Removed: changes.Removed,
		Changed: changes.Changed,
	}

	if checkOnly {
		mismatch := existingErr != nil || !bytes.Equal(existing, buf.Bytes())
		return result, mismatch, nil
	}

	if err := wiring.AtomicWrite(graphPath, buf.Bytes(), 0644); err != nil {
		return graphUpdateResult{}, false, fmt.Errorf("write %s: %w", graphPath, err)
	}
	return result, false, nil
}

func newGraphStatusCmd() *cobra.Command {
	var workingTree, jsonOut bool
	cmd := &cobra.Command{
		Use:   "status [<rev>]",
		Short: "Report per-file graph freshness as of a revision (default HEAD)",
		Long: `Resolve the nearest graph.jsonl version in <rev>'s history (default HEAD),
load it, and compare it against the actual scan-scope files at <rev>: which
ones are fresh, stale, version_stale, unscanned or deleted.

--working-tree compares against the working tree on disk instead of the
git-committed content at <rev> — the graph version itself still comes from
<rev>'s history (a graph is always a committed artifact), but every file's
current blob is read straight off disk, so an uncommitted edit shows up
immediately.

Prints the non-fresh files as text by default, or the full report with
--json.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			// manifestPath is only populated once resolveDefaults runs — every
			// other command that reads it gets that for free as a side effect
			// of openGraph (update) or calls the same pair hookAdvisory does
			// (hook.go). status opens no store, so it must call them itself.
			resolveWorktreeGraph()
			resolveDefaults()

			rev := ""
			if len(args) > 0 {
				rev = args[0]
			}

			man, err := manifest.LoadFile(manifestPath)
			if err != nil {
				return fmt.Errorf("load manifest: %w", err)
			}

			result, warnings, err := runGraphStatus(paths.GitDir(), man, rev, workingTree)
			if err != nil {
				if errors.Is(err, snapshot.ErrNoVersion) {
					label := rev
					if label == "" {
						label = "HEAD"
					}
					return noVersionMessage(label)
				}
				return err
			}
			printWarnings(warnings)

			if jsonOut {
				outputJSON(result)
				return nil
			}
			printFreshnessText(result)
			return nil
		},
	}
	cmd.Flags().BoolVar(&workingTree, "working-tree", false, "Compare the working tree on disk instead of a git commit")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Machine-readable full freshness report")
	return cmd
}

// runGraphStatus is the testable core of `chronicle graph status`: no cobra,
// no process exit. rev == "" means "no revision given on the command line"
// and resolves the graph version at HEAD; workingTree, independently, picks
// whether the FILE SIDE of the comparison reads from rev's git tree or from
// disk (snapshot.ComputeFreshness's own rev=="" convention) — so `chronicle
// graph status abc123 --working-tree` is "how fresh is my working tree
// against the graph as it stood at abc123", a meaningful combination the two
// separate defaults below preserve.
//
// version is resolved ONCE, against graphRev, and passed straight through to
// ComputeFreshness — never re-derived from scanRev. ComputeFreshness has no
// way to tell "no revision was given" apart from "an explicit non-HEAD
// revision was given, but read files from disk" if it is handed scanRev=""
// and left to guess the graph version itself: guessing "HEAD" got this
// exactly backwards for `chronicle graph status <rev> --working-tree` with
// <rev> != HEAD, reporting the wrong Graph and an incomplete ChangedSince
// (fixed 2026-09-26 — see task-5-report.md's fix-round-1 section).
func runGraphStatus(repoRoot string, man *manifest.Manifest, rev string, workingTree bool) (snapshot.Freshness, []snapshot.Warning, error) {
	graphRev := rev
	if graphRev == "" {
		graphRev = "HEAD"
	}
	scanRev := graphRev
	if workingTree {
		scanRev = ""
	}

	version, err := snapshot.ResolveVersion(repoRoot, graphRev)
	if err != nil {
		return snapshot.Freshness{}, nil, err
	}
	snap, warnings, err := snapshot.AtCommit(repoRoot, version)
	if err != nil {
		return snapshot.Freshness{}, nil, fmt.Errorf("load graph at %s: %w", version.Commit, err)
	}
	fr, err := snapshot.ComputeFreshness(repoRoot, snap, version, man, scanRev)
	return fr, warnings, err
}

// printFreshnessText prints one line per non-fresh file: "<path>: <state>".
func printFreshnessText(f snapshot.Freshness) {
	nonFresh := 0
	for _, ff := range f.Files {
		if ff.State == snapshot.Fresh {
			continue
		}
		nonFresh++
		fmt.Printf("%s: %s\n", ff.Path, ff.State)
	}
	if nonFresh == 0 {
		fmt.Println("graph is fresh: no stale, unscanned, version-stale or deleted files")
	}
}

// printWarnings prints every snapshot decode warning to stderr, one per
// line. AtCommit's whole point (Task 1's design) is that a malformed or
// hostile line in a historical graph.jsonl is reported, not silently
// skipped — discarding this with "_" at the call site would defeat that.
func printWarnings(warnings []snapshot.Warning) {
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: graph.jsonl:%d: %s: %s\n", w.Line, w.Code, w.Msg)
	}
}

// graphShowResult is the JSON body `chronicle graph show` prints: the
// resolved graph version's identity (commit, blob, seq) plus a summary of
// what it contains (record counts, domains) — enough to answer "what graph
// would `chronicle graph status` be comparing against here" without
// printing the whole snapshot.
type graphShowResult struct {
	Commit  string          `json:"commit"`
	Blob    string          `json:"blob"`
	Seq     int             `json:"seq"`
	Counts  snapshot.Counts `json:"counts"`
	Domains []string        `json:"domains"`
}

func newGraphShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <rev>",
		Short: "Show the graph version resolved at a revision",
		Long: `Resolve the nearest graph.jsonl version in <rev>'s history and print its
identity and contents summary: {commit, blob, seq, counts, domains}.

Exits 1 with "no graph version in history of <rev>" (not a panic, not a Go
stack trace) when no commit reachable from <rev> ever committed
.depbot/graph.jsonl.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			rev := args[0]

			result, warnings, err := runGraphShow(paths.GitDir(), rev)
			if err != nil {
				if errors.Is(err, snapshot.ErrNoVersion) {
					return noVersionMessage(rev)
				}
				return err
			}
			printWarnings(warnings)
			outputJSON(result)
			return nil
		},
	}
	return cmd
}

// runGraphShow is the testable core of `chronicle graph show`.
func runGraphShow(repoRoot, rev string) (graphShowResult, []snapshot.Warning, error) {
	version, err := snapshot.ResolveVersion(repoRoot, rev)
	if err != nil {
		return graphShowResult{}, nil, err
	}
	snap, warnings, err := snapshot.AtCommit(repoRoot, version)
	if err != nil {
		return graphShowResult{}, nil, fmt.Errorf("load graph at %s: %w", version.Commit, err)
	}
	result := graphShowResult{
		Commit:  version.Commit,
		Blob:    version.Blob,
		Seq:     version.Seq,
		Counts:  snap.Header.Counts,
		Domains: snap.Header.Domains,
	}
	return result, warnings, nil
}

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/review"
	"github.com/spf13/cobra"
)

// reviewDefaultBaseCandidates is the ordered fallback list parseRange tries
// when no positional range argument or explicit --base is given: the first
// ref among these `git rev-parse --verify` resolves is the default base —
// task-14-brief.md's own literal list.
var reviewDefaultBaseCandidates = []string{"origin/main", "origin/master", "main", "master"}

// parseRange turns chronicle review's own positional argument plus its
// --base/--head/--working-tree flags into concrete base/head revisions —
// verbatim per task-14-brief.md:
//
//	""        (no argument) -> base = the nearest resolvable of
//	          reviewDefaultBaseCandidates (the first for which
//	          `git rev-parse --verify <ref>` succeeds), head = "HEAD"
//	          (or "" when workingTree is set)
//	"A..B"    -> base=A, head=B, as is (no git call — a two-dot range names
//	          both ends literally, the same convention `git diff A..B` uses)
//	"A...B"   -> base=gitdiff.MergeBaseOf(repoRoot, A, B), head=B (the
//	          three-dot, "since we forked" convention `git diff A...B` uses)
//
// Explicit --base/--head (flagBase/flagHead) override whatever the
// positional argument would have produced, applied uniformly AFTER the
// switch above — including overriding the empty-argument default and
// workingTree's own head="" substitution, per the brief's own "перекрывают"
// (override) wording, which names no exception for either.
//
// workingTree itself is deliberately consulted ONLY inside the empty-argument
// branch, exactly where the brief places its parenthetical ("или "" если
// задан --working-tree") — an explicit "A..B"/"A...B" range already has a
// way to name the working tree as its head (a trailing "A.." with an empty
// B), so --working-tree does not additionally reinterpret an explicit range.
func parseRange(repoRoot, arg, flagBase, flagHead string, workingTree bool) (base, head string, err error) {
	switch {
	case arg == "":
		base, err = defaultBaseRef(repoRoot)
		if err != nil {
			return "", "", err
		}
		head = "HEAD"
		if workingTree {
			head = ""
		}
	default:
		if a, b, ok := strings.Cut(arg, "..."); ok {
			mergeBase, mbErr := gitdiff.MergeBaseOf(repoRoot, a, b)
			if mbErr != nil {
				return "", "", fmt.Errorf("chronicle review: resolve merge base of %q and %q: %w", a, b, mbErr)
			}
			base, head = mergeBase, b
		} else if a, b, ok := strings.Cut(arg, ".."); ok {
			base, head = a, b
		} else {
			return "", "", fmt.Errorf("chronicle review: invalid range %q, want A..B or A...B", arg)
		}
	}

	if flagBase != "" {
		base = flagBase
	}
	if flagHead != "" {
		head = flagHead
	}
	return base, head, nil
}

// defaultBaseRef returns the first of reviewDefaultBaseCandidates that
// `git rev-parse --verify` resolves in repoRoot, or an error if none do —
// there is no further fallback for a human-facing CLI: an explicit --base (or
// a range argument) is the documented way out.
func defaultBaseRef(repoRoot string) (string, error) {
	for _, ref := range reviewDefaultBaseCandidates {
		if refResolves(repoRoot, ref) {
			return ref, nil
		}
	}
	return "", fmt.Errorf("chronicle review: no default base found (tried %s) — pass a range or --base",
		strings.Join(reviewDefaultBaseCandidates, ", "))
}

// refResolves reports whether `git rev-parse --verify <ref>` succeeds in
// repoRoot. --end-of-options is the same defensive convention every other
// ref-accepting call in this codebase uses (gitdiff.go, snapshot/version.go);
// every candidate here is a fixed literal, never user input, but there is no
// reason to special-case that away.
func refResolves(repoRoot, ref string) bool {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "--end-of-options", ref)
	return cmd.Run() == nil
}

// reviewCLIOptions bundles chronicle review's own flags, already resolved —
// parseRange has already turned the positional argument plus
// --base/--head/--working-tree into concrete Base/Head revisions — into
// exactly what runReview needs to run one review and decide this process's
// exit code.
type reviewCLIOptions struct {
	Base, Head string
	// Format is "json" or "markdown" (any other value is treated as
	// "markdown", matching the flag's own default).
	Format string
	// OutPath is the --out file to write to; "" means stdout.
	OutPath string
	// Depth is the impact BFS depth; 0 means review.Review's own default (4).
	Depth int
	// FailOn is the --fail-on list: any of "unknown", "affected", "changed".
	FailOn []string
	// Command is the reproduction command shown in a Markdown report's
	// footer (MarkdownOptions.Command) — e.g. "chronicle review main...HEAD".
	Command string
	// HeadSnapshot is the --head-snapshot file: when set, the head graph is
	// read from it (freshness judged against Head's tree) instead of from
	// Head's git history. "" means the ordinary SnapshotSource.
	HeadSnapshot string
}

// reviewRepoRoot is the absolute directory git questions are asked in.
// paths.Root() is "" unless --project is passed, which would reach
// ComputeFreshness("") and fail with "walk working tree: open"; GitDir()
// falls back to ".", which Abs pins to the current directory.
func reviewRepoRoot() (string, error) {
	return filepath.Abs(paths.GitDir())
}

// headSource is the GraphSource for the head side of a review.
func headSource(repoRoot string, opts reviewCLIOptions) review.GraphSource {
	if opts.HeadSnapshot != "" {
		return review.FileSource{RepoRoot: repoRoot, Path: opts.HeadSnapshot, Rev: opts.Head}
	}
	return review.SnapshotSource{RepoRoot: repoRoot, Rev: opts.Head}
}

// runReview is chronicle review's testable core: no cobra, no os.Exit. It
// runs the review, renders it per opts.Format, writes the rendered report to
// opts.OutPath (or to w when opts.OutPath is ""), and returns the process
// exit code the command should use, plus a human-readable line for stderr
// (empty when there is nothing to add) — see task-14-brief.md's exit-code
// table: 0 clean; 1 a --fail-on match; 2 the review itself could not be
// produced (a real failure — git unavailable, or neither revision has a
// Chronicle graph in its history at all, which is not the same as the
// "this change introduces the graph" case review.Review itself reports for
// ONE missing side — see the check below and task-14-report.md).
func runReview(ctx context.Context, w io.Writer, repoRoot string, opts reviewCLIOptions) (exitCode int, stderrMsg string) {
	in := review.ReviewInput{
		BaseSource: review.SnapshotSource{RepoRoot: repoRoot, Rev: opts.Base},
		HeadSource: headSource(repoRoot, opts),
		RepoRoot:   repoRoot,
		Depth:      opts.Depth,
	}
	result, err := review.Review(ctx, in)
	if err != nil {
		return 2, "chronicle review: " + err.Error()
	}

	// review.Review checks head's Origin first and short-circuits with the
	// "this change introduces the graph" message regardless of base's own
	// Origin (see result.go's own doc comment) — correct for Review's own
	// contract, but a range where NEITHER side ever had a graph has not
	// "introduced" anything; that is a real problem (wrong range, wrong
	// repo, or a review requested entirely before Chronicle adoption), which
	// this command surfaces as a failure rather than a misleadingly upbeat
	// clean report.
	if result.Base.Origin == review.OriginMissing && result.Head.Origin == review.OriginMissing {
		return 2, "chronicle review: neither revision has a Chronicle graph in its history"
	}

	rendered, err := renderReview(ctx, result, repoRoot, opts)
	if err != nil {
		return 2, "chronicle review: " + err.Error()
	}
	if err := writeReviewOutput(w, opts.OutPath, rendered); err != nil {
		return 2, "chronicle review: " + err.Error()
	}

	return reviewExitCode(result, opts.FailOn)
}

// renderReview renders result per opts.Format. The JSON path is a pure
// function of result (review.RenderJSON) — it never touches diagrams. The
// Markdown path needs review.CompactDiagram/FullDiagram (Task 12) for
// MarkdownOptions.Compact/.Full, and those need a LIVE, open head
// *VersionGraph — but review.Review already closed the one it loaded
// internally by the time it returns (result.go's own
// `defer head.Close()`/`defer base.Close()`, right after each Load): the
// head this function was given no store to query through anymore. The fix,
// deliberately NOT reopening Review's own already-shipped resource-lifetime
// design: load head a SECOND, separate time here, close THIS instance
// ourselves, and use it only for the diagram calls. This is a real, known
// inefficiency for the markdown format (head's snapshot is loaded twice) —
// accepted rather than fixed, since reopening Review's own Close-on-return
// design is a bigger, riskier change to already-reviewed code than one extra
// snapshot load; see task-14-report.md.
func renderReview(ctx context.Context, result *review.ReviewResult, repoRoot string, opts reviewCLIOptions) (string, error) {
	if opts.Format == "json" {
		data, err := review.RenderJSON(result)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	mdOpts := review.MarkdownOptions{Command: opts.Command}

	// Nothing to diagram when either side has no graph at all (RenderMarkdown
	// renders its own missing-graph special case then, and never reads
	// Compact/Full) — skip the second load entirely rather than load it only
	// to immediately discard it.
	if result.Base.Origin != review.OriginMissing && result.Head.Origin != review.OriginMissing {
		headVG, err := headSource(repoRoot, opts).Load(ctx)
		if err != nil {
			return "", fmt.Errorf("reload head for diagrams: %w", err)
		}
		defer headVG.Close()

		if headVG.Origin != review.OriginMissing && headVG.Graph != nil {
			// CompactDiagram/FullDiagram error when r.ChangedRelationships()
			// is empty (the NoSystemChange/impact-unknown special cases,
			// among others) — exactly the states RenderMarkdown itself never
			// reads Compact/Full for, so that error (and any other one from
			// these two calls) is swallowed here: a diagram that can't be
			// built is not a reason to fail the whole review, and leaving
			// Compact/Full at their nil zero value is explicitly safe per
			// MarkdownOptions' own doc comment.
			if compact, derr := review.CompactDiagram(result, headVG, review.DiagramOptions{}); derr == nil {
				mdOpts.Compact = compact
			}
			if full, derr := review.FullDiagram(result, headVG); derr == nil {
				mdOpts.Full = full
			}
		}
	}

	return review.RenderMarkdown(result, mdOpts)
}

// writeReviewOutput writes rendered (newline-terminated, if it isn't
// already) to outPath, or to w when outPath is "".
func writeReviewOutput(w io.Writer, outPath, rendered string) error {
	if !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	if outPath == "" {
		_, err := io.WriteString(w, rendered)
		return err
	}
	return os.WriteFile(outPath, []byte(rendered), 0o644)
}

// reviewExitCode decides chronicle review's --fail-on exit code (1) and its
// stderr line from the SAME bucket methods/fields a rendered report is
// itself built from — ChangedRelationships/WhoMayBeAffected/Verdict.
// UnknownFiles, never a separate counting pass — so `--fail-on` can never
// disagree with what the report body shows. 0 whenever failOn is empty,
// regardless of what the review found (task-14-brief.md's own
// "_FailOnClean" case), and 0 whenever none of the requested severities has
// a non-zero count.
func reviewExitCode(result *review.ReviewResult, failOn []string) (exitCode int, stderrMsg string) {
	if len(failOn) == 0 {
		return 0, ""
	}
	want := make(map[string]bool, len(failOn))
	for _, s := range failOn {
		want[strings.TrimSpace(s)] = true
	}

	counts := map[string]int{
		"unknown":  result.Verdict.UnknownFiles,
		"affected": result.Verdict.SystemsAffected,
		"changed":  len(result.ChangedRelationships()),
	}

	var parts []string
	total := 0
	for _, sev := range []string{"unknown", "affected", "changed"} {
		if !want[sev] {
			continue
		}
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
			total += n
		}
	}
	if total == 0 {
		return 0, ""
	}
	return 1, "review found: " + strings.Join(parts, ", ")
}

// reviewReproCommand builds the reproduction command shown in a Markdown
// report's footer, reflecting what the user actually typed rather than the
// resolved base/head (a resolved SHA is not something a human would retype).
func reviewReproCommand(rangeArg, flagBase, flagHead string, workingTree bool) string {
	parts := []string{"chronicle", "review"}
	if rangeArg != "" {
		parts = append(parts, rangeArg)
	}
	if flagBase != "" {
		parts = append(parts, "--base", flagBase)
	}
	if flagHead != "" {
		parts = append(parts, "--head", flagHead)
	}
	if workingTree {
		parts = append(parts, "--working-tree")
	}
	return strings.Join(parts, " ")
}

func newReviewCmd() *cobra.Command {
	var flagBase, flagHead, format, out, headSnapshot string
	var workingTree bool
	var depth int
	var failOn []string

	cmd := &cobra.Command{
		Use:   "review [<range>]",
		Short: "Render a Chronicle review report for a git range",
		Long: `Compares two committed graph snapshots (Tasks 9-13's diff, classify, impact
analysis, diagrams and rendering) and prints what changed, who may be
affected, and which changed files the graph cannot currently vouch for.

<range> is one of:

  (nothing)   base = the nearest of origin/main, origin/master, main, master
              that resolves; head = HEAD (or the working tree, with
              --working-tree)
  A..B        base=A, head=B, exactly as given
  A...B       base=the merge base of A and B, head=B

Explicit --base/--head override whatever <range> would otherwise give.

Exit code 0: the review ran cleanly, or --fail-on was not given.
Exit code 1: --fail-on named a severity (unknown, affected, changed) that the
review actually found — the stderr line says which.
Exit code 2: the review itself could not be produced (git unavailable, or
neither revision has a Chronicle graph in its history at all).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

			var rangeArg string
			if len(args) == 1 {
				rangeArg = args[0]
			}
			repoRoot, err := reviewRepoRoot()
			if err != nil {
				fmt.Fprintln(os.Stderr, "chronicle review: resolve repository root: "+err.Error())
				os.Exit(2)
			}

			base, head, err := parseRange(repoRoot, rangeArg, flagBase, flagHead, workingTree)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(2)
			}

			opts := reviewCLIOptions{
				Base: base, Head: head,
				Format: format, OutPath: out,
				Depth: depth, FailOn: failOn,
				Command:      reviewReproCommand(rangeArg, flagBase, flagHead, workingTree),
				HeadSnapshot: headSnapshot,
			}

			code, msg := runReview(context.Background(), os.Stdout, repoRoot, opts)
			if msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagBase, "base", "", "Base git ref (overrides the range's base)")
	cmd.Flags().StringVar(&flagHead, "head", "", "Head git ref (overrides the range's head)")
	cmd.Flags().BoolVar(&workingTree, "working-tree", false, "Compare against the working tree instead of HEAD (only affects the default, no-argument range)")
	cmd.Flags().StringVar(&headSnapshot, "head-snapshot", "", "Read the head graph from this snapshot file instead of the head revision's committed graph (freshness is still judged against the head revision's tree)")
	cmd.Flags().StringVar(&format, "format", "markdown", "Output format: markdown or json")
	cmd.Flags().StringVar(&out, "out", "", "Write the report to this file instead of stdout")
	cmd.Flags().IntVar(&depth, "depth", 4, "Impact traversal depth")
	cmd.Flags().StringSliceVar(&failOn, "fail-on", nil, "Exit 1 if the review finds a match: unknown, affected, changed")

	return cmd
}

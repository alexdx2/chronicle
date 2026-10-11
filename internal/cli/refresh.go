package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/scan"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

// refreshOutput is what one `chronicle refresh` did, in both phases. The
// verification result is embedded so its JSON shape is unchanged for anything
// already reading it; the structural phase adds one key.
type refreshOutput struct {
	*graph.RefreshResult
	Structural      *graph.StructuralResult `json:"structural,omitempty"`
	StructuralError string                  `json:"structural_error,omitempty"`
	// Sweep is set when --sweep widened both phases to every in-scope file.
	Sweep bool `json:"sweep,omitempty"`
}

// structuralFn is phase 2 as a package variable so a test can make it fail
// where the real thing would crash. The whole two-pointer design exists for
// that one moment — verification recorded, structure not — and an untested
// recovery path is a recovery path that silently stops working.
var structuralFn = func(g *graph.Graph, in graph.StructuralInput) (*graph.StructuralResult, error) {
	return g.StructuralRefresh(in)
}

func newRefreshCmd() *cobra.Command {
	var quiet bool
	var noStructural bool
	var sweep bool
	var structuralBatch int
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Zero-token structural freshness — re-verify evidence and re-extract structure for git-changed files",
		Long: `Runs two phases against the working tree, neither of which calls a model.

Phase 1 (verification) diffs against the last scan or refresh, re-verifies
structural evidence on changed files mechanically, stale-marks evidence on
deleted files, and reports which files still need an agent rescan.

Phase 2 (structure) diffs against its OWN pointer — the last commit whose
structural extraction completed — re-extracts imports, routes, models and calls
for the files that changed since, and replaces what those files no longer say.
The two pointers move independently, so a phase that fails costs only its own
claim. Use --no-structural to run phase 1 alone.

Safe to run from a git post-commit hook (see 'chronicle hook install --git').`,
		Run: func(cmd *cobra.Command, args []string) {
			runRefresh(quiet, noStructural, structuralBatch, sweep)
		},
	}
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (for git hooks)")
	cmd.Flags().BoolVar(&noStructural, "no-structural", false, "Skip phase 2 (deterministic structural re-extraction)")
	cmd.Flags().BoolVar(&sweep, "sweep", false,
		"Re-verify every in-scope file against HEAD and re-extract structure for all supported files; use after `graph restore` of a snapshot whose coverage may come from another checkout. No model is called. Pair with --structural-batch -1 to do it in one run; without it phase 2 drains in batches as usual.")
	cmd.Flags().IntVar(&structuralBatch, "structural-batch", 0,
		fmt.Sprintf("Files phase 2 parses per run (default %d; -1 = no bound). Raise it to drain a first pass or a rules-pack backlog in one go, away from a hook.", graph.DefaultBacklogBatch))
	return cmd
}

// runRefresh is the command body, extracted so tests can drive both phases
// without a process.
func runRefresh(quiet, noStructural bool, structuralBatch int, sweep bool) {
	if notice, linked := linkedWorktreeRefusal(); linked {
		// A commit in any worktree runs the MAIN checkout's post-commit hook,
		// so without this a feature branch writes refresh revisions — at its
		// own tip — into main's graph. Both phases are refused: phase 2 writes
		// more than phase 1 does, not less.
		refuseWrite(notice, quiet)
		return
	}

	g := openGraph()
	defer g.Store().Close()

	rev, err := refreshBase(g.Store(), repoDirForGit())
	if err != nil {
		outputError(err)
	}
	head, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		outputError(fmt.Errorf("not a git repository or no HEAD: %w", err))
	}
	head = strings.TrimSpace(head)

	out := &refreshOutput{Sweep: sweep}
	var quietLine, humanLine string
	out.RefreshResult, quietLine, humanLine, err = verifyPhase(g, rev.DomainKey, rev.GitAfterSHA, head, sweep)
	if err != nil {
		outputError(err)
	}
	if out.RefreshResult == nil {
		// Embedding a nil pointer is a marshalling panic, and a hook that
		// panics looks exactly like a broken install.
		out.RefreshResult = &graph.RefreshResult{HeadSHA: head}
	}

	// Phase 2 never undoes phase 1: whatever it does or fails to do, the
	// verification above is already recorded.
	var structuralErr error
	if !noStructural {
		out.Structural, structuralErr = structuralPhase(g, rev.DomainKey, head, structuralBatch, sweep)
		if structuralErr != nil {
			out.StructuralError = structuralErr.Error()
		}
	}

	switch {
	case quiet:
		// A hook must not talk unless it has something to say, and must not
		// fail the commit it is attached to.
		if quietLine != "" {
			fmt.Println(quietLine)
		}
		if line := structuralQuietLine(out.Structural); line != "" {
			fmt.Println(line)
		}
	case humanLine != "":
		fmt.Println(humanLine)
		if line := structuralQuietLine(out.Structural); line != "" {
			fmt.Println(line)
		}
		if structuralErr != nil {
			// This path prints a sentence, not the JSON that would have
			// carried structural_error — so the failure has to be said out
			// loud, or phase 2 fails invisibly behind "Graph is current".
			fmt.Fprintln(os.Stderr, "chronicle refresh: structural phase failed: "+structuralErr.Error())
		}
	default:
		outputJSON(out)
	}
	if structuralErr != nil && !quiet {
		os.Exit(1)
	}
}

// cliEnv is the refresh environment the command line runs in: the checkout git
// is measured in, the manifest the CLI resolved, and the phase-2 hook tests use.
func cliEnv() scan.Env {
	return scan.Env{RepoDir: repoDirForGit(), ManifestPath: manifestPath, StructuralFn: structuralFn}
}

// verifyPhase is phase 1 of refresh; see scan.Env.VerifyPhase.
func verifyPhase(g *graph.Graph, domainKey, base, head string, sweep bool) (*graph.RefreshResult, string, string, error) {
	return cliEnv().VerifyPhase(g, domainKey, base, head, sweep)
}

// structuralPhase is phase 2 of refresh; see scan.Env.StructuralPhase.
func structuralPhase(g *graph.Graph, domainKey, head string, batch int, forceSweep bool) (*graph.StructuralResult, error) {
	return cliEnv().StructuralPhase(g, domainKey, head, batch, forceSweep)
}

// structuralBase picks the commit phase 2 diffs from; see scan.Env.StructuralBase.
func structuralBase(s *store.Store, repoDir, domainKey string) (string, bool, int64) {
	e := cliEnv()
	e.RepoDir = repoDir
	return e.StructuralBase(s, domainKey)
}

// refreshBase picks the commit phase 1 diffs from; see scan.Env.RefreshBase.
func refreshBase(s *store.Store, repoDir string) (*store.Revision, error) {
	e := cliEnv()
	e.RepoDir = repoDir
	return e.RefreshBase(s)
}

func filterRefreshable(files []string) []string { return scan.FilterRefreshable(files) }

// structuralQuietLine is the one line a hook prints — only when the phase
// actually did something, so an ordinary commit stays silent.
func structuralQuietLine(res *graph.StructuralResult) string {
	// Superseded counts too: a commit that only DELETED files parses nothing
	// and still changed what the graph asserts, which is exactly the change a
	// reader would want to hear about.
	if res == nil || (res.Processed == 0 && res.Superseded == 0) {
		return ""
	}
	return fmt.Sprintf("chronicle refresh: structure %d files (%d failed, %d not read, %d unresolved, %d remaining)",
		res.Processed, len(res.Failed), len(res.Unread), res.Unresolved, res.Backlog)
}

// latestRevisionAnyDomain returns the most recent revision across all domains,
// since GetLatestRevision is domain-scoped and some callers have none.
func latestRevisionAnyDomain(g *graph.Graph) *store.Revision {
	rev, err := g.Store().LatestRevisionAnyDomain()
	if err != nil {
		return nil
	}
	return rev
}

func gitOutput(args ...string) (string, error) {
	return gitutil.Run(repoDirForGit(), args...)
}

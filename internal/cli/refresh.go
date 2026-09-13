package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

// refreshExtensions are the file types whose evidence the deterministic refresh
// can re-verify. Anything outside this set is left to a full scan.
var refreshExtensions = []string{".ts", ".tsx", ".js", ".jsx", ".cs", ".prisma", ".graphql", ".proto", ".go", ".py"}

func newRefreshCmd() *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Zero-token structural freshness — re-verify evidence on git-changed files since the last scan",
		Long: `Diffs the working tree against the last scanned revision, re-verifies
structural evidence on changed files mechanically (no LLM), stale-marks evidence
on deleted files, and reports which files still need an agent rescan. Safe to run
from a git post-commit hook (see 'chronicle hook install --git').`,
		Run: func(cmd *cobra.Command, args []string) {
			if notice, linked := linkedWorktreeRefusal(); linked {
				// A commit in any worktree runs the MAIN checkout's
				// post-commit hook, so without this a feature branch writes
				// refresh revisions — at its own tip — into main's graph.
				refuseWrite(notice, quiet)
				return
			}

			g := openGraph()
			defer g.Store().Close()

			rev, err := refreshBase(g.Store(), repoDirForGit())
			if err != nil {
				outputError(err)
			}
			base := rev.GitAfterSHA

			head, err := gitOutput("rev-parse", "HEAD")
			if err != nil {
				outputError(fmt.Errorf("not a git repository or no HEAD: %w", err))
			}
			head = strings.TrimSpace(head)

			changed, err := gitDiffFiles(base, "d") // added/copied/modified/renamed/type-changed
			if err != nil {
				outputError(err)
			}
			deleted, err := gitDiffFiles(base, "D") // deleted only
			if err != nil {
				outputError(err)
			}
			changed = filterRefreshable(changed)
			deleted = filterRefreshable(deleted)

			if len(changed) == 0 && len(deleted) == 0 {
				// Nothing to re-verify, but the graph IS still good at HEAD,
				// and only a revision recorded there says so. Without this a
				// docs-only commit leaves the repo reading "stale" against a
				// commit that could not have invalidated anything.
				if head != base {
					if _, err := g.RecordRefreshNoop(rev.DomainKey, head); err != nil {
						outputError(err)
					}
				}
				if !quiet {
					fmt.Println("Graph is current — no refreshable changes since last scan.")
				}
				return
			}

			res, err := g.RefreshFromDiff(rev.DomainKey, head, changed, deleted)
			if err != nil {
				outputError(err)
			}
			if quiet {
				if len(res.PendingSemantic) > 0 {
					fmt.Printf("chronicle refresh: %d files re-verified, %d need rescan\n",
						res.ChangedFiles, len(res.PendingSemantic))
				}
				return
			}
			outputJSON(res)
		},
	}
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (for git hooks)")
	return cmd
}

// refreshBase picks the commit a refresh diffs from: the newer of the last
// scan and the last refresh, and never a layer import.
//
// The two rules it enforces are the two ways the naive "newest revision of any
// kind" answer goes wrong.
//
// A surface import is named by the commit the EXTRACT was made at — a product
// generates it whenever it likes, on whatever branch it likes — so it says
// nothing about which commit the code knowledge came from. Diffing from it
// re-reads files nobody touched: measured live in okeep/auto, 72 files
// stale-marked where the true base gives 1.
//
// And a base must actually be on this branch. A commit that is not an ancestor
// of HEAD produces a diff of the symmetric difference — every file either side
// touched — which reads as "the world changed" rather than as "I cannot answer
// that". The scan is the fallback, and when even that is off-branch the answer
// is a refusal, not a guess.
func refreshBase(s *store.Store, repoDir string) (*store.Revision, error) {
	scanRev, err := revOrNil(s.LatestScanRevision(""))
	if err != nil {
		return nil, err
	}
	refreshRev, err := revOrNil(s.LatestRefreshRevision(""))
	if err != nil {
		return nil, err
	}
	if scanRev == nil {
		return nil, errors.New("no prior scan found — run a full scan before refresh")
	}

	base := scanRev
	if refreshRev != nil && refreshRev.RevisionID > base.RevisionID && refreshRev.GitAfterSHA != "" {
		base = refreshRev
	}
	if base.GitAfterSHA == "" {
		return nil, errors.New("last revision has no git SHA — cannot diff; run a full scan")
	}
	if isAncestorOfHEAD(repoDir, base.GitAfterSHA) {
		return base, nil
	}
	if base != scanRev && scanRev.GitAfterSHA != "" && isAncestorOfHEAD(repoDir, scanRev.GitAfterSHA) {
		return scanRev, nil
	}
	return nil, fmt.Errorf(
		"the graph's commit %s is not an ancestor of HEAD — it was built on a branch this checkout does not contain; "+
			"check out that branch or rescan before refresh", shortSHA(scanRev.GitAfterSHA))
}

// isAncestorOfHEAD is false both for "no" and for "that commit is not in this
// repo at all" — a base this checkout cannot reach is unusable either way.
func isAncestorOfHEAD(repoDir, sha string) bool {
	if !gitutil.OK(repoDir, "cat-file", "-e", sha+"^{commit}") {
		return false
	}
	return gitutil.OK(repoDir, "merge-base", "--is-ancestor", sha, "HEAD")
}

// revOrNil maps ErrNotFound to (nil, nil): "there is no such revision" is an
// answer this caller acts on, not a failure.
func revOrNil(rev *store.Revision, err error) (*store.Revision, error) {
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rev, nil
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

// gitDiffFiles returns files matching a diff-filter between base..HEAD.
func gitDiffFiles(base, filter string) ([]string, error) {
	out, err := gitOutput("diff", "--name-only", "--diff-filter="+filter, base+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff failed (base %s): %w", base, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func gitOutput(args ...string) (string, error) {
	return gitutil.Run(repoDirForGit(), args...)
}

func filterRefreshable(files []string) []string {
	var out []string
	for _, f := range files {
		for _, ext := range refreshExtensions {
			if strings.HasSuffix(f, ext) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

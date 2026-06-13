package cli

import (
	"fmt"
	"os/exec"
	"strings"

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
			g := openGraph()
			defer g.Store().Close()

			rev := latestRevisionAnyDomain(g)
			if rev == nil {
				outputError(fmt.Errorf("no prior scan found — run a full scan before refresh"))
			}
			base := rev.GitAfterSHA
			if base == "" {
				outputError(fmt.Errorf("last revision has no git SHA — cannot diff; run a full scan"))
			}

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

// latestRevisionAnyDomain returns the most recent revision across all domains,
// since GetLatestRevision is domain-scoped and refresh is invoked without one.
func latestRevisionAnyDomain(g *graph.Graph) *store.Revision {
	domains, err := g.Store().GetDomains()
	if err != nil {
		return nil
	}
	var best *store.Revision
	for _, d := range domains {
		rev, err := g.Store().GetLatestRevision(d)
		if err != nil || rev == nil {
			continue
		}
		if best == nil || rev.RevisionID > best.RevisionID {
			best = rev
		}
	}
	return best
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
	gitArgs := args
	if projectPath != "" {
		gitArgs = append([]string{"-C", projectPath}, args...)
	}
	out, err := exec.Command("git", gitArgs...).Output()
	return string(out), err
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

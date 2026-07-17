package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
)

// BuildReviewReportFromGit performs the git legwork (diff, prisma blobs, base
// resolution) and then builds the review report. Shared by the core MCP
// handler and chronicle-pro's federated handler.
//
// opts.Base == "" resolves to the merge-base with main/master, falling back to
// the domain's last scanned revision SHA. opts.Changed, opts.OldPrisma and
// opts.NewPrisma are filled from git when nil.
func (g *Graph) BuildReviewReportFromGit(q GraphQuerier, repoRoot, domainKey string, opts ReviewReportOptions) (*ReviewReport, error) {
	if opts.Base == "" {
		opts.Base = g.defaultReviewBase(repoRoot, domainKey)
	}
	if opts.Base == "" {
		return nil, fmt.Errorf("cannot determine base ref: no main/master merge-base and no prior scan SHA — pass base explicitly")
	}

	if opts.Changed == nil {
		changed, err := gitdiff.ChangedFiles(repoRoot, opts.Base, opts.Head)
		if err != nil {
			return nil, fmt.Errorf("git diff failed: %w", err)
		}
		opts.Changed = changed
	}

	if opts.OldPrisma == nil && opts.NewPrisma == nil {
		opts.OldPrisma = map[string][]byte{}
		opts.NewPrisma = map[string][]byte{}
		for _, cf := range opts.Changed {
			if !strings.HasSuffix(cf.Path, ".prisma") {
				continue
			}
			if blob, err := gitdiff.Show(repoRoot, opts.Base, cf.Path); err == nil {
				opts.OldPrisma[cf.Path] = blob
			}
			if opts.Head == "" {
				if blob, err := os.ReadFile(filepath.Join(repoRoot, cf.Path)); err == nil {
					opts.NewPrisma[cf.Path] = blob
				}
			} else if blob, err := gitdiff.Show(repoRoot, opts.Head, cf.Path); err == nil {
				opts.NewPrisma[cf.Path] = blob
			}
		}
	}

	return g.BuildReviewReport(q, domainKey, opts)
}

// defaultReviewBase picks the merge-base with main/master, falling back to the
// last scanned revision SHA (same fallback family as chronicle refresh).
func (g *Graph) defaultReviewBase(repoRoot, domainKey string) string {
	for _, ref := range []string{"main", "master"} {
		if sha, err := gitdiff.MergeBase(repoRoot, ref); err == nil && sha != "" {
			return sha
		}
	}
	if rev, err := g.store.GetLatestRevision(domainKey); err == nil && rev != nil && rev.GitAfterSHA != "" {
		return rev.GitAfterSHA
	}
	return ""
}

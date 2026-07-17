package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ---------------------------------------------------------------------------
// chronicle_review_report — deterministic MR/PR review report from a git diff
// ---------------------------------------------------------------------------

func reviewReportTool() mcp.Tool {
	return mcp.NewTool("chronicle_review_report",
		mcp.WithDescription("Deterministic merge-request review report from a git diff: changed entities mapped to the graph, per-entity blast radius (field-level for changed schema fields, with precision labels), external services affected, and files with no graph coverage (impact UNKNOWN). Renders markdown ready to post on the MR. base defaults to the merge-base with main/master, falling back to the last scanned revision; head defaults to the working tree."),
		mcp.WithString("base", mcp.Description("Base git ref (default: merge-base with main/master, else last-scan SHA)")),
		mcp.WithString("head", mcp.Description("Head git ref (default: working tree including uncommitted changes)")),
		mcp.WithString("domain", mcp.Description("Domain key (default: the single scanned domain)")),
		mcp.WithNumber("depth", mcp.Description("Impact traversal depth (default 4)")),
		mcp.WithString("format", mcp.Description("markdown (default) or json")),
	)
}

func reviewReportHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		repoRoot := graph.ProjectRoot()

		domain := strParam(args, "domain")
		if domain == "" {
			domains, err := g.Store().GetDomains()
			if err != nil || len(domains) == 0 {
				return errorResult(fmt.Errorf("no scanned domain found — run a scan first")), nil
			}
			if len(domains) > 1 {
				return errorResult(fmt.Errorf("multiple domains (%v) — pass domain explicitly", domains)), nil
			}
			domain = domains[0]
		}

		base := strParam(args, "base")
		if base == "" {
			base = defaultReviewBase(g, repoRoot, domain)
		}
		if base == "" {
			return errorResult(fmt.Errorf("cannot determine base ref: no main/master merge-base and no prior scan SHA — pass base explicitly")), nil
		}
		head := strParam(args, "head")

		changed, err := gitdiff.ChangedFiles(repoRoot, base, head)
		if err != nil {
			return errorResult(fmt.Errorf("git diff failed: %w", err)), nil
		}

		// Collect prisma blobs on both sides for field-level rows.
		oldPrisma := map[string][]byte{}
		newPrisma := map[string][]byte{}
		for _, cf := range changed {
			if !strings.HasSuffix(cf.Path, ".prisma") {
				continue
			}
			if blob, err := gitdiff.Show(repoRoot, base, cf.Path); err == nil {
				oldPrisma[cf.Path] = blob
			}
			if head == "" {
				if blob, err := os.ReadFile(filepath.Join(repoRoot, cf.Path)); err == nil {
					newPrisma[cf.Path] = blob
				}
			} else if blob, err := gitdiff.Show(repoRoot, head, cf.Path); err == nil {
				newPrisma[cf.Path] = blob
			}
		}

		report, err := g.BuildReviewReport(nil, domain, graph.ReviewReportOptions{
			Base:      base,
			Head:      head,
			Depth:     intParam(args, "depth"),
			Changed:   changed,
			OldPrisma: oldPrisma,
			NewPrisma: newPrisma,
		})
		if err != nil {
			return errorResult(err), nil
		}

		if strParam(args, "format") == "json" {
			return jsonResult(report), nil
		}
		return mcp.NewToolResultText(report.Markdown()), nil
	}
}

// defaultReviewBase picks the merge-base with main/master, falling back to the
// last scanned revision SHA (same fallback family as chronicle refresh).
func defaultReviewBase(g *graph.Graph, repoRoot, domain string) string {
	for _, ref := range []string{"main", "master"} {
		if sha, err := gitdiff.MergeBase(repoRoot, ref); err == nil && sha != "" {
			return sha
		}
	}
	if rev, err := g.Store().GetLatestRevision(domain); err == nil && rev != nil && rev.GitAfterSHA != "" {
		return rev.GitAfterSHA
	}
	return ""
}

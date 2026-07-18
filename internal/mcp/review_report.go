package mcp

import (
	"context"
	"fmt"

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

		report, err := g.BuildReviewReportFromGit(nil, graph.ProjectRoot(), domain, graph.ReviewReportOptions{
			Base:  strParam(args, "base"),
			Head:  strParam(args, "head"),
			Depth: intParam(args, "depth"),
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

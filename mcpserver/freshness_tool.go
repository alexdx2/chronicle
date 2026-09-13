package mcpserver

import (
	"context"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/freshness"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// serverRepoDir is the directory freshness compares the graph against: the
// --project root when the CLI set one, otherwise the process working
// directory — the same place scan_status has always run git.
func serverRepoDir() string {
	if root := paths.Root(); root != "" {
		return root
	}
	return "."
}

// repoLabel names a repo the way a human would: the directory's own name.
func repoLabel(repoDir string) string {
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		abs = repoDir
	}
	return filepath.Base(abs)
}

// FreshnessReport computes the freshness report for one project directory,
// letting the store pick the domain. Exported for the admin dashboard
// (/api/freshness) and chronicle-pro, which need the same numbers the tool
// returns without going through MCP.
func FreshnessReport(g *graph.Graph, repoDir string) (*freshness.Report, error) {
	return FreshnessReportForDomain(g, repoDir, "")
}

// FreshnessReportForDomain is FreshnessReport for a caller-known domain. A
// caller that already resolved which domain it is showing — scan_status, the
// dashboard's ?domain= selector — must report on that one, not on whichever
// domain happens to hold the newest revision.
func FreshnessReportForDomain(g *graph.Graph, repoDir, domain string) (*freshness.Report, error) {
	if repoDir == "" {
		repoDir = serverRepoDir()
	}
	if abs, err := filepath.Abs(repoDir); err == nil {
		repoDir = abs
	}
	return freshness.Compute(repoDir, repoLabel(repoDir), domain, g.Store())
}

func freshnessTool() mcp.Tool {
	return mcp.NewTool("chronicle_freshness",
		mcp.WithDescription("How old is this graph? Returns the commit the knowledge was scanned at, the commit it was last verified at, how many commits and files have landed since, whether the scanned commit is still on this branch (diverged), and how much of the graph is already marked stale. Call it before trusting a query answer, or when deciding whether a rescan is needed."),
		mcp.WithString("repo", mcp.Description("Label for this repo in the report (default: the project directory name)")),
	)
}

func freshnessHandler(g *graph.Graph, repoDir string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		rep, err := FreshnessReportForDomain(g, repoDir, "")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if label := strParam(args, "repo"); label != "" {
			rep.SetRepo(label)
		}
		return jsonResult(rep), nil
	}
}

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ---------------------------------------------------------------------------
// chronicle_review_report — deterministic MR/PR review report from a git diff
//
// Two engines back this one tool. When the project has a committed
// .depbot/graph.jsonl anywhere in HEAD's history, the report is built by the
// new review engine (Tasks 9-13: SnapshotSource, Diff, ClassifyDelta,
// Analyze, CompactDiagram/FullDiagram, RenderJSON/RenderMarkdown) — the same
// engine `chronicle review` itself drives (internal/cli/review.go),
// deterministic across two committed graph snapshots. Otherwise this falls
// back to the pre-existing graph.BuildReviewReportFromGit path (a live query
// against today's chronicle.db), unchanged, with a "note" added so a caller
// knows which engine actually answered.
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

		// snapshot.ResolveVersion(repoRoot, "HEAD") is the brief's own
		// literal branching condition: does HEAD's history contain a commit
		// that ever touched .depbot/graph.jsonl? ErrNoVersion is the one
		// case that keeps today's behavior (the old, live-database path);
		// any other outcome (a real version, or some other unexpected error
		// review.Review's own SnapshotSource.Load will hit again and report
		// properly) takes the new engine.
		if _, err := snapshot.ResolveVersion(repoRoot, "HEAD"); !errors.Is(err, snapshot.ErrNoVersion) {
			return reviewReportViaEngine(ctx, repoRoot, args)
		}
		return reviewReportFallback(g, args)
	}
}

// reviewDefaultBaseCandidatesMCP mirrors internal/cli's own
// reviewDefaultBaseCandidates list (duplicated, not imported: mcpserver
// cannot import the internal/cli package) — the one piece of this tool's
// pre-existing, documented contract ("base defaults to the merge-base with
// main/master...") worth preserving for the new engine path: an omitted base
// arg falls back to the nearest of these that resolves, the same default
// `chronicle review` itself uses with no positional range. head keeps its
// existing documented default ("the working tree") for free — Rev==""
// already means exactly that to review.SnapshotSource.
var reviewDefaultBaseCandidatesMCP = []string{"origin/main", "origin/master", "main", "master"}

// resolveReviewDefaultBaseMCP returns the first candidate that
// `git rev-parse --verify` resolves, or "HEAD" if none do. Unlike
// internal/cli's parseRange (a human-facing CLI, where erroring out and
// naming --base explicitly is the right nudge), this tool has always been
// resilient to a missing base rather than hard-failing on the very first
// call with no arguments — "HEAD" is a reasonable, always-available last
// resort that keeps that spirit for the new engine path too.
func resolveReviewDefaultBaseMCP(repoRoot string) string {
	for _, ref := range reviewDefaultBaseCandidatesMCP {
		cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "--end-of-options", ref)
		if cmd.Run() == nil {
			return ref
		}
	}
	return "HEAD"
}

// reviewReportViaEngine builds the response through the new review engine:
// review.Review, then RenderJSON or RenderMarkdown per the tool's own
// "format" argument, exactly as before. domain is deliberately never read
// here — a committed graph.jsonl snapshot is multi-domain, per the brief.
func reviewReportViaEngine(ctx context.Context, repoRoot string, args map[string]any) (*mcp.CallToolResult, error) {
	base := strParam(args, "base")
	if base == "" {
		base = resolveReviewDefaultBaseMCP(repoRoot)
	}
	head := strParam(args, "head") // "" already means "the working tree"

	result, err := review.Review(ctx, review.ReviewInput{
		BaseSource: review.SnapshotSource{RepoRoot: repoRoot, Rev: base},
		HeadSource: review.SnapshotSource{RepoRoot: repoRoot, Rev: head},
		RepoRoot:   repoRoot,
		Depth:      intParam(args, "depth"),
	})
	if err != nil {
		return errorResult(err), nil
	}

	if strParam(args, "format") == "json" {
		data, err := review.RenderJSON(result)
		if err != nil {
			return errorResult(err), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}

	md, err := reviewMarkdownForResult(ctx, result, repoRoot, head)
	if err != nil {
		return errorResult(err), nil
	}
	return mcp.NewToolResultText(md), nil
}

// reviewMarkdownForResult renders result as Markdown, best-effort-attaching
// the compact/full blast-radius diagrams — duplicated from internal/cli's
// own renderReview (mcpserver cannot import internal/cli), same reasoning:
// review.Review already closed the head *VersionGraph it loaded internally
// by the time it returns a *ReviewResult (review/result.go's own
// `defer head.Close()`), so CompactDiagram/FullDiagram (Task 12), which need
// a LIVE head store, require their own separate reload of the exact same
// head revision, closed here once done with it. A real, known inefficiency
// for the markdown format (head's snapshot loaded twice) — accepted rather
// than fixed; see task-14-report.md.
func reviewMarkdownForResult(ctx context.Context, result *review.ReviewResult, repoRoot, headRev string) (string, error) {
	opts := review.MarkdownOptions{}

	if result.Base.Origin != review.OriginMissing && result.Head.Origin != review.OriginMissing {
		headVG, err := (review.SnapshotSource{RepoRoot: repoRoot, Rev: headRev}).Load(ctx)
		if err != nil {
			return "", fmt.Errorf("reload head for diagrams: %w", err)
		}
		defer headVG.Close()

		if headVG.Origin != review.OriginMissing && headVG.Graph != nil {
			// CompactDiagram/FullDiagram error when r.ChangedRelationships()
			// is empty (the NoSystemChange/impact-unknown special cases) —
			// exactly the states RenderMarkdown never reads Compact/Full
			// for, so that error (and any other from these two calls) is
			// swallowed: a diagram that can't be built is not a reason to
			// fail the whole review.
			if compact, derr := review.CompactDiagram(result, headVG, review.DiagramOptions{}); derr == nil {
				opts.Compact = compact
			}
			if full, derr := review.FullDiagram(result, headVG); derr == nil {
				opts.Full = full
			}
		}
	}

	return review.RenderMarkdown(result, opts)
}

// reviewReportFallbackNote is appended to the tool's response whenever it
// takes the old, live-database path — the brief's own exact wording.
const reviewReportFallbackNote = "no committed graph.jsonl — using live database (run chronicle graph update to enable deterministic review)"

// reviewReportFallback is today's pre-existing behavior, unmodified except
// for the note added to both the JSON and Markdown responses so a caller
// knows which engine actually answered.
func reviewReportFallback(g *graph.Graph, args map[string]any) (*mcp.CallToolResult, error) {
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
		return jsonResult(struct {
			*graph.ReviewReport
			Note string `json:"note"`
		}{report, reviewReportFallbackNote}), nil
	}
	return mcp.NewToolResultText("_Note: " + reviewReportFallbackNote + "_\n\n" + report.Markdown()), nil
}

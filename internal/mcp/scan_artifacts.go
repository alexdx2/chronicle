package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ---------------------------------------------------------------------------
// chronicle_scan_checkout_batch — orchestrator claims work for artifact-pool wave
// ---------------------------------------------------------------------------

func scanCheckoutBatchTool() mcp.Tool {
	return mcp.NewTool("chronicle_scan_checkout_batch",
		mcp.WithDescription("Claim up to limit scan_file obligations and return a slim checkout for artifact-pool extractors. Orchestrator-only — subagents must NOT call this."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("limit", mcp.Description("Max obligations to claim (default 8)")),
		mcp.WithString("obligation_type", mcp.Description("Obligation type (default scan_file)")),
		mcp.WithBoolean("include_fact_schema", mcp.Description("If true, inline fact_schema in response (default false — use fact_schema_path to save context)")),
		mcp.WithBoolean("include_items", mcp.Description("If true, inline items[] in response (default false — read items_path to save context)")),
	)
}

func scanCheckoutBatchHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}
		limit := intParam(args, "limit")
		if limit <= 0 {
			limit = graph.DefaultBatchSize
		}
		effectiveLimit := graph.EffectiveCheckoutLimit(limit)
		obligationType := strParam(args, "obligation_type")
		if obligationType == "" {
			obligationType = "scan_file"
		}

		run, err := g.Store().GetActiveScanRun(domain)
		if err != nil || run == nil {
			if last, lerr := g.Store().GetLatestScanRun(domain); lerr == nil && last != nil && last.Status == "completed" {
				return errorResult(fmt.Errorf("no active scan run for domain %s — the last scan completed and the graph is ready to query; do not check out extraction work", domain)), nil
			}
			return errorResult(fmt.Errorf("no active scan run for domain %s — start one via chronicle_command(command=\"scan\")", domain)), nil
		}

		batches, err := g.ScanCheckoutBatches(run.RevisionID, obligationType, limit)
		if err != nil {
			return errorResult(err), nil
		}
		if len(batches) == 0 {
			return jsonResult(map[string]any{"item_count": 0, "items": []any{}}), nil
		}
		batch := batches[0]

		factSchema := prompts.FactSchema
		schemaName := "fact_schema.md"
		if obligationType == "trace_flow" {
			factSchema = prompts.FlowTracing
			schemaName = "flow_schema.md"
		}
		schemaPath := filepath.Join(batch.WorkDir, schemaName)
		if err := os.WriteFile(schemaPath, []byte(factSchema), 0o644); err != nil {
			return errorResult(fmt.Errorf("write schema: %w", err)), nil
		}

		resp := map[string]any{
			"revision_id":              run.RevisionID,
			"domain":                   domain,
			"scan_mode":                "artifact-pool",
			"outbox_dir":               batch.OutboxDir,
			"work_dir":                 batch.WorkDir,
			"fact_schema_path":         schemaPath,
			"items_path":               batch.ItemsPath,
			"item_count":               batch.ItemCount,
			"requested_limit":          limit,
			"effective_limit":          effectiveLimit,
			"truncated":                batch.Truncated || limit > effectiveLimit,
			"remaining_after_checkout": batch.RemainingAfter,
			"artifact_filename":        "path/with/slashes → path__with__slashes.json",
			"artifact_facts_fmt":       "facts must be a JSON array of fact objects (same as fact_schema output), not a string",
			"deterministic_hint":       "each item may include deterministic_candidates_path — read and merge with LLM facts",
		}
		if boolParam(args, "include_items") {
			resp["items"] = batch.Items
		}
		if boolParam(args, "include_fact_schema") {
			resp["fact_schema"] = factSchema
		}
		return jsonResult(resp), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_commit_scan_outbox — orchestrator commits extractor artifacts to DB
// ---------------------------------------------------------------------------

func commitScanOutboxTool() mcp.Tool {
	return mcp.NewTool("chronicle_commit_scan_outbox",
		mcp.WithDescription("Import extraction JSON artifacts from .depbot/scan-outbox/{revision_id}/ into the database and satisfy obligations. Orchestrator-only single-writer commit step after a wave finishes."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Current revision ID")),
		mcp.WithBoolean("remove_after", mcp.Description("Remove imported artifact files after success (default true)")),
	)
}

func commitScanOutboxHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		revisionID := int64Param(args, "revision_id")
		removeAfter := true
		if v, ok := args["remove_after"].(bool); ok {
			removeAfter = v
		}
		if domain == "" || revisionID == 0 {
			return errorResult(fmt.Errorf("domain and revision_id are required")), nil
		}

		outboxDir := filepath.Join(paths.DirAt(graph.ProjectRoot()), "scan-outbox", fmt.Sprintf("%d", revisionID))
		entries, err := os.ReadDir(outboxDir)
		if err != nil {
			return errorResult(fmt.Errorf("read outbox %s: %w", outboxDir, err)), nil
		}

		// Load manifest tech list for AST rule selection (best-effort; empty → defaults).
		var manifestTech []string
		if m, err := manifest.LoadFile(filepath.Join(paths.Dir(), "chronicle.domain.yaml")); err == nil {
			manifestTech = m.Tech
		}

		imported := 0
		rowsWritten := 0
		deduped := 0
		errors := 0
		var errMsgs []string
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(outboxDir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				errors++
				continue
			}

			ext, err := parseOutboxArtifact(data)
			if err != nil {
				errors++
				errMsgs = append(errMsgs, entry.Name()+": invalid json: "+err.Error())
				continue
			}
			if ext.FilePath == "" || ext.Status == "" {
				errors++
				errMsgs = append(errMsgs, entry.Name()+": missing file_path or status")
				continue
			}
			factsJSON, err := factsJSONString(ext.Facts)
			if err != nil {
				errors++
				errMsgs = append(errMsgs, ext.FilePath+": "+err.Error())
				continue
			}

			// Phase-2 flow artifacts are saved under role="flow" and skip the
			// AST merge: prepended AST import facts used to flip the artifact's
			// primary kind to "import", making the dedup match the phase-1 row
			// and silently discard the flow facts (otopoint field finding).
			isFlow := strings.Contains(factsJSON, `"kind":"flow"`)

			// Merge server-side AST facts with LLM facts for TypeScript and Prisma files.
			// This ensures deterministic AST imports/from_type are present regardless of
			// extractor model quality (evidence-first principle).
			if ext.Status == "extracted" && !isFlow {
				factsJSON, ext.FromType = graph.MergeASTFactsJSON(ext.FilePath, factsJSON, ext.FromType, manifestTech)
			}

			errMsg := ext.ErrorMessage
			if errMsg == "" {
				errMsg = ext.Error
			}
			dom := domain
			if ext.Domain != "" {
				dom = ext.Domain
			}

			role := "single"
			switch {
			case isFlow:
				role = "flow"
			case ext.VoteGroup != "":
				role = "llm_vote"
			}
			_, written, err := g.Store().SaveExtractionWithOutcome(revisionID, dom, ext.FilePath, ext.Status, ext.FromType, factsJSON, errMsg, role, ext.VoteGroup, ext.VoteIndex)
			if err != nil {
				errors++
				if len(errMsgs) < 3 {
					errMsgs = append(errMsgs, ext.FilePath+": "+err.Error())
				}
				continue
			}
			if written {
				rowsWritten++
			} else {
				deduped++
			}

			_ = g.SatisfyFileObligation(revisionID, ext.FilePath)
			_ = g.Store().SatisfyObligation(revisionID, "scan_file", ext.FilePath)
			_ = g.Store().SatisfyObligation(revisionID, "trace_flow", ext.FilePath)
			if ext.ObligationID > 0 {
				_ = g.Store().SatisfyObligationByID(ext.ObligationID)
			}

			if removeAfter {
				_ = os.Remove(path)
			}
			imported++
		}

		if run, _ := g.Store().GetActiveScanRun(domain); run != nil {
			for i := 0; i < imported; i++ {
				g.Store().IncrementScanRunExtracted(run.RunID)
			}
		}

		return jsonResult(map[string]any{
			"imported":     imported,
			"rows_written": rowsWritten,
			"deduped":      deduped,
			"errors":       errors,
			"dir":          outboxDir,
			"error_msgs":   errMsgs,
		}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_file_extracted_batch — orchestrator commits multiple files in one call
// ---------------------------------------------------------------------------

func fileExtractedBatchTool() mcp.Tool {
	return mcp.NewTool("chronicle_file_extracted_batch",
		mcp.WithDescription("Report extraction results for multiple files in one call. Orchestrator-only in artifact-pool mode."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Current revision ID")),
		mcp.WithString("items", mcp.Required(), mcp.Description("JSON array of file_extracted payloads")),
	)
}

func fileExtractedBatchHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		revisionID := int64Param(args, "revision_id")
		itemsJSON := strParam(args, "items")
		if domain == "" || revisionID == 0 || itemsJSON == "" {
			return errorResult(fmt.Errorf("domain, revision_id, and items are required")), nil
		}

		var items []map[string]any
		if err := json.Unmarshal([]byte(itemsJSON), &items); err != nil {
			return errorResult(fmt.Errorf("invalid items JSON: %w", err)), nil
		}

		committed := 0
		var failures []string
		for _, item := range items {
			item["revision_id"] = float64(revisionID)
			item["domain"] = domain
			res, err := commitOneFileExtracted(g, ctx, item)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%v: %v", item["file_path"], err))
				continue
			}
			if res != nil && res.IsError {
				failures = append(failures, fmt.Sprintf("%v: tool error", item["file_path"]))
				continue
			}
			committed++
		}

		return jsonResult(map[string]any{
			"committed": committed,
			"total":     len(items),
			"failures":  failures,
		}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_scan_mark_failed — orchestrator marks stuck obligations failed
// ---------------------------------------------------------------------------

func scanMarkFailedTool() mcp.Tool {
	return mcp.NewTool("chronicle_scan_mark_failed",
		mcp.WithDescription("Mark a scan obligation as failed/skipped and clear its claim. Orchestrator-only salvage action."),
		mcp.WithNumber("obligation_id", mcp.Required(), mcp.Description("Obligation ID to mark failed")),
		mcp.WithString("reason", mcp.Description("Failure reason")),
	)
}

func scanMarkFailedHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		obligationID := int64Param(args, "obligation_id")
		reason := strParam(args, "reason")
		if obligationID == 0 {
			return errorResult(fmt.Errorf("obligation_id is required")), nil
		}
		if reason == "" {
			reason = "orchestrator_mark_failed"
		}
		if err := g.Store().MarkObligationFailed(obligationID, reason); err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"obligation_id": obligationID, "status": "skipped"}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_scan_review_candidates — post-resolve review pass file list
// ---------------------------------------------------------------------------

func scanReviewCandidatesTool() mcp.Tool {
	return mcp.NewTool("chronicle_scan_review_candidates",
		mcp.WithDescription("List files that may need a second extraction pass after resolve (low confidence or unresolved refs)."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("revision_id", mcp.Description("Revision ID (defaults to active scan run)")),
	)
}

func commitOneFileExtracted(g *graph.Graph, ctx context.Context, item map[string]any) (*mcp.CallToolResult, error) {
	filePath, _ := item["file_path"].(string)
	status, _ := item["status"].(string)
	revisionID := int64Param(item, "revision_id")
	domain, _ := item["domain"].(string)
	if filePath == "" || status == "" || revisionID == 0 || domain == "" {
		return errorResult(fmt.Errorf("each item needs file_path, status, revision_id, domain")), nil
	}

	fromType, _ := item["from_type"].(string)
	factsJSON, _ := item["facts"].(string)
	errorMsg, _ := item["error_message"].(string)
	if errorMsg == "" {
		errorMsg, _ = item["error"].(string)
	}
	voteGroup, _ := item["vote_group"].(string)
	voteIndex := int(int64Param(item, "vote_index"))
	obligationID := int64Param(item, "obligation_id")

	var id int64
	var err error
	if voteGroup != "" {
		id, err = g.SaveFileExtractionWithVote(revisionID, domain, filePath, status, fromType, factsJSON, errorMsg, "llm_vote", voteGroup, voteIndex)
	} else {
		id, err = g.SaveFileExtraction(revisionID, domain, filePath, status, fromType, factsJSON, errorMsg)
	}
	if err != nil {
		return errorResult(err), nil
	}

	_ = g.SatisfyFileObligation(revisionID, filePath)
	if voteGroup != "" {
		if obligationID > 0 {
			_ = g.Store().SatisfyObligationByID(obligationID)
		}
	} else {
		_ = g.Store().SatisfyObligation(revisionID, "scan_file", filePath)
		_ = g.Store().SatisfyObligation(revisionID, "trace_flow", filePath)
		if obligationID > 0 {
			_ = g.Store().SatisfyObligationByID(obligationID)
		}
	}
	if run, _ := g.Store().GetActiveScanRun(domain); run != nil {
		g.Store().IncrementScanRunExtracted(run.RunID)
	}

	return jsonResult(map[string]any{
		"extraction_id": id,
		"file_path":     filePath,
		"status":        status,
	}), nil
}

func scanReviewCandidatesHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}
		revisionID := int64Param(args, "revision_id")
		if revisionID == 0 {
			run, err := g.Store().GetActiveScanRun(domain)
			if err != nil || run == nil {
				return errorResult(fmt.Errorf("no active scan run for domain %s", domain)), nil
			}
			revisionID = run.RevisionID
		}

		candidates, err := g.ScanReviewCandidates(domain, revisionID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{
			"revision_id": revisionID,
			"domain":      domain,
			"candidates":  candidates,
			"count":       len(candidates),
		}), nil
	}
}

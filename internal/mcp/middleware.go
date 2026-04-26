package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/store"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func loggingWrap(logStore *store.Store, toolName string, next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		start := time.Now()
		result, err := next(ctx, req)
		durationMs := int(time.Since(start).Milliseconds())

		entry := store.RequestLogEntry{
			ToolName:   toolName,
			DurationMs: durationMs,
		}

		paramsBytes, _ := json.Marshal(req.GetArguments())
		entry.ParamsJSON = string(paramsBytes)

		if err != nil {
			entry.ErrorMessage = err.Error()
			entry.Summary = truncate(err.Error(), 80)
		} else if result != nil && result.IsError {
			for _, c := range result.Content {
				if tc, ok := c.(mcplib.TextContent); ok {
					entry.ErrorMessage = tc.Text
					entry.Summary = truncate(tc.Text, 80)
					break
				}
			}
		} else if result != nil {
			for _, c := range result.Content {
				if tc, ok := c.(mcplib.TextContent); ok {
					entry.ResultJSON = tc.Text
					entry.Summary = makeSummary(toolName, tc.Text)
					break
				}
			}
		}

		logStore.LogRequest(entry) //nolint:errcheck

		// Auto-discovery: after import_all, analyze for low-confidence edges and gaps
		if toolName == "oracle_import_all" && err == nil && entry.ErrorMessage == "" {
			go autoDiscover(logStore, entry.ResultJSON)
		}

		return result, err
	}
}

// autoDiscover creates discoveries for low-confidence edges and structural gaps after an import.
func autoDiscover(s *store.Store, resultJSON string) {
	var result map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return
	}

	// Get domain from nodes
	allNodes, err := s.ListNodes(store.NodeFilter{})
	if err != nil || len(allNodes) == 0 {
		return
	}
	domain := allNodes[0].DomainKey

	// Check for low-confidence edges
	edges, err := s.ListEdges(store.EdgeFilter{})
	if err != nil {
		return
	}
	lowConfCount := 0
	for _, e := range edges {
		if e.Confidence > 0 && e.Confidence < 0.7 && e.Active {
			lowConfCount++
		}
	}
	if lowConfCount > 0 {
		s.AddDiscovery(store.Discovery{
			DomainKey:   domain,
			Category:    "missing_edge",
			Title:       fmt.Sprintf("%d low-confidence edges detected", lowConfCount),
			Description: fmt.Sprintf("Found %d edges with confidence below 0.7 after import. These relationships may need verification or additional evidence.", lowConfCount),
			Source:      "system",
			Confidence:  0.3,
		})
	}

	// Check for nodes without evidence
	nodes, err := s.ListNodes(store.NodeFilter{Domain: domain, Status: "active"})
	if err != nil {
		return
	}
	noEvidenceCount := 0
	for _, n := range nodes {
		evidence, _ := s.ListEvidenceByNode(n.NodeID)
		if len(evidence) == 0 {
			noEvidenceCount++
		}
	}
	if noEvidenceCount > 0 {
		s.AddDiscovery(store.Discovery{
			DomainKey:   domain,
			Category:    "missing_edge",
			Title:       fmt.Sprintf("%d nodes without evidence", noEvidenceCount),
			Description: fmt.Sprintf("Found %d active nodes with no evidence entries. These nodes have no proof of existence — consider adding file_path + line_start evidence.", noEvidenceCount),
			Source:      "system",
			Confidence:  0.5,
		})
	}

	// Check for modules without CONTAINS edges (structural completeness)
	modules := 0
	modulesWithContains := 0
	for _, n := range nodes {
		if n.NodeType == "module" {
			modules++
			for _, e := range edges {
				if e.FromNodeID == n.NodeID && e.EdgeType == "CONTAINS" && e.Active {
					modulesWithContains++
					break
				}
			}
		}
	}
	if modules > 0 && modulesWithContains < modules {
		orphanModules := modules - modulesWithContains
		s.AddDiscovery(store.Discovery{
			DomainKey:   domain,
			Category:    "unknown_pattern",
			Title:       fmt.Sprintf("%d modules without CONTAINS edges", orphanModules),
			Description: "Some modules don't have CONTAINS edges to their providers.",
			Source:      "system",
			Confidence:  0.6,
		})
	}

	// Always log scan summary as a discovery for audit trail
	nodeCount := len(nodes)
	edgeCount := 0
	edgeTypes := make(map[string]int)
	for _, e := range edges {
		if e.Active {
			edgeCount++
			edgeTypes[e.EdgeType]++
		}
	}

	// Check for common gaps
	if edgeTypes["EXPOSES_ENDPOINT"] == 0 {
		controllers := 0
		for _, n := range nodes {
			if n.NodeType == "controller" {
				controllers++
			}
		}
		if controllers > 0 {
			s.AddDiscovery(store.Discovery{
				DomainKey:       domain,
				Category:        "missing_edge",
				Severity:        "critical",
				Title:           fmt.Sprintf("%d controllers but 0 EXPOSES_ENDPOINT edges", controllers),
				Description:     "Controllers exist but no endpoints were extracted.",
				SuggestedAction: "Re-scan with focus on controller files: read each @Get/@Post/@Put/@Delete method and create contract:endpoint nodes",
				Source:          "system",
				Confidence:      0.8,
			})
		}
	}

	if edgeTypes["CALLS_SERVICE"] == 0 && edgeTypes["CALLS_ENDPOINT"] == 0 {
		services := 0
		for _, n := range nodes {
			if n.NodeType == "service" {
				services++
			}
		}
		if services > 1 {
			s.AddDiscovery(store.Discovery{
				DomainKey:       domain,
				Category:        "missing_edge",
				Severity:        "warning",
				Title:           fmt.Sprintf("%d services but no cross-service edges", services),
				Description:     "Multiple services but no CALLS_SERVICE or CALLS_ENDPOINT edges found.",
				SuggestedAction: "Read HTTP client files, look for fetch() with env URLs like *_API_URL, create CALLS_SERVICE edges",
				Source:          "system",
				Confidence:      0.7,
			})
		}
	}

	// Report scan quality summary
	layers := make(map[string]int)
	for _, n := range nodes {
		layers[n.Layer]++
	}
	hasData := layers["data"] > 0
	hasContract := layers["contract"] > 0

	quality := "good"
	gaps := []string{}
	if !hasData {
		gaps = append(gaps, "no data models")
		quality = "incomplete"
	}
	if !hasContract {
		gaps = append(gaps, "no contract endpoints")
		quality = "incomplete"
	}
	if edgeTypes["REFERENCES_MODEL"] == 0 && hasData {
		gaps = append(gaps, "no model relations")
	}
	if edgeTypes["USES_MODEL"] == 0 && hasData {
		gaps = append(gaps, "no USES_MODEL edges")
	}

	if len(gaps) > 0 {
		s.AddDiscovery(store.Discovery{
			DomainKey:   domain,
			Category:    "pattern",
			Title:       fmt.Sprintf("Scan quality: %s — %d nodes, %d edges", quality, nodeCount, edgeCount),
			Description: fmt.Sprintf("Gaps detected: %v. Consider re-scanning with focus on missing areas.", gaps),
			Source:      "system",
			Confidence:  0.9,
		})
	}
}

func makeSummary(toolName, resultJSON string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &data); err != nil {
		// Try as array
		var arr []any
		if err2 := json.Unmarshal([]byte(resultJSON), &arr); err2 == nil {
			return fmt.Sprintf("%d items", len(arr))
		}
		return ""
	}

	switch toolName {
	case "oracle_import_all":
		n, _ := data["nodes_created"].(float64)
		e, _ := data["edges_created"].(float64)
		ev, _ := data["evidence_created"].(float64)
		return fmt.Sprintf("%.0fn %.0fe %.0fev", n, e, ev)
	case "oracle_revision_create":
		id, _ := data["revision_id"].(float64)
		return fmt.Sprintf("rev #%.0f", id)
	case "oracle_node_upsert":
		id, _ := data["node_id"].(float64)
		return fmt.Sprintf("node_id: %.0f", id)
	case "oracle_edge_upsert":
		id, _ := data["edge_id"].(float64)
		return fmt.Sprintf("edge_id: %.0f", id)
	case "oracle_evidence_add":
		id, _ := data["evidence_id"].(float64)
		return fmt.Sprintf("evidence #%.0f", id)
	case "oracle_snapshot_create":
		id, _ := data["snapshot_id"].(float64)
		return fmt.Sprintf("snapshot #%.0f", id)
	case "oracle_stale_mark":
		n, _ := data["stale_nodes"].(float64)
		e, _ := data["stale_edges"].(float64)
		return fmt.Sprintf("%.0f nodes, %.0f edges stale", n, e)
	case "oracle_query_path":
		paths, _ := data["paths"].([]any)
		if len(paths) == 0 {
			return "0 paths"
		}
		return fmt.Sprintf("%d path(s)", len(paths))
	case "oracle_impact":
		total, _ := data["total_impacted"].(float64)
		return fmt.Sprintf("%.0f impacted", total)
	case "oracle_query_stats":
		n, _ := data["node_count"].(float64)
		e, _ := data["edge_count"].(float64)
		return fmt.Sprintf("%.0fn %.0fe", n, e)
	case "oracle_extraction_guide":
		return "guide returned"
	case "oracle_scan_status":
		domain, _ := data["domain"].(string)
		if domain != "" {
			return "domain: " + domain
		}
		return "status"
	case "oracle_node_get":
		return "node details"
	case "oracle_validate_graph":
		if issues, ok := data["issues"].([]any); ok {
			return fmt.Sprintf("%d issues", len(issues))
		}
		return "validated"
	case "oracle_invalidate_changed":
		stale, _ := data["stale_evidence"].(float64)
		edges, _ := data["affected_edges"].(float64)
		return fmt.Sprintf("%.0f stale, %.0f edges affected", stale, edges)
	case "oracle_finalize_incremental_scan":
		reval, _ := data["revalidated"].(float64)
		still, _ := data["still_stale"].(float64)
		contra, _ := data["contradicted"].(float64)
		return fmt.Sprintf("%.0f revalidated, %.0f stale, %.0f contradicted", reval, still, contra)
	default:
		return ""
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// NewServerWithLogging creates an MCP server with request logging to SQLite.
func NewServerWithLogging(g *graph.Graph, logStore *store.Store) *server.MCPServer {
	s := server.NewMCPServer("oracle", "0.1.0")

	add := func(tool mcplib.Tool, handler server.ToolHandlerFunc) {
		s.AddTool(tool, loggingWrap(logStore, tool.Name, handler))
	}

	add(revisionCreateTool(), revisionCreateHandler(g))
	add(nodeUpsertTool(), nodeUpsertHandler(g))
	add(nodeListTool(), nodeListHandler(g))
	add(nodeGetTool(), nodeGetHandler(g))
	add(edgeUpsertTool(), edgeUpsertHandler(g))
	add(edgeListTool(), edgeListHandler(g))
	add(evidenceAddTool(), evidenceAddHandler(g))
	add(importAllTool(), importAllHandler(g))
	add(queryDepsTool(), queryDepsHandler(g))
	add(queryReverseDepsTool(), queryReverseDepsHandler(g))
	add(queryStatsTool(), queryStatsHandler(g))
	add(snapshotCreateTool(), snapshotCreateHandler(g))
	add(staleMarkTool(), staleMarkHandler(g))
	add(invalidateChangedTool(), invalidateChangedHandler(g))
	add(finalizeIncrementalScanTool(), finalizeIncrementalScanHandler(g))
	add(queryPathTool(), queryPathHandler(g))
	add(impactTool(), impactHandler(g))
	add(extractionGuideTool(), extractionGuideHandler())
	add(scanStatusTool(), scanStatusHandler(g))
	add(saveManifestTool(), saveManifestHandler())
	add(resetDBTool(), resetDBHandler(g))
	add(reportDiscoveryTool(), reportDiscoveryHandler(g))
	add(getDiscoveriesTool(), getDiscoveriesHandler(g))
	add(adminURLTool(), adminURLHandler())
	add(defineTermTool(), defineTermHandler(g))
	add(getGlossaryTool(), getGlossaryHandler(g))
	add(checkLanguageTool(), checkLanguageHandler(g))
	add(commandTool(), commandHandler(g))
	add(diagramCreateTool(), diagramCreateHandler())
	add(diagramUpdateTool(), diagramUpdateHandler())
	add(diagramAnnotateTool(), diagramAnnotateHandler())

	return s
}

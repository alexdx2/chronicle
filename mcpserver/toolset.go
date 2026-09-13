package mcpserver

import (
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/mark3labs/mcp-go/server"
)

// Tools returns the full core toolset bound to g, in registration order.
func Tools(g *graph.Graph) []server.ServerTool {
	return []server.ServerTool{
		{Tool: revisionCreateTool(), Handler: revisionCreateHandler(g)},
		{Tool: nodeUpsertTool(), Handler: nodeUpsertHandler(g)},
		{Tool: nodeListTool(), Handler: nodeListHandler(g)},
		{Tool: nodeGetTool(), Handler: nodeGetHandler(g)},
		{Tool: edgeUpsertTool(), Handler: edgeUpsertHandler(g)},
		{Tool: edgeListTool(), Handler: edgeListHandler(g)},
		{Tool: evidenceAddTool(), Handler: evidenceAddHandler(g)},
		{Tool: evidenceVerifyTool(), Handler: evidenceVerifyHandler(g)},
		{Tool: resolveReviewTool(), Handler: resolveReviewHandler(g)},
		{Tool: fileGroupsTool(), Handler: fileGroupsHandler(g)},
		{Tool: discoverFilesTool(), Handler: discoverFilesHandler(g)},
		{Tool: scanNextFileTool(), Handler: scanNextFileHandler(g)},
		{Tool: fileExtractedTool(), Handler: fileExtractedHandler(g)},
		{Tool: importExtractionsTool(), Handler: importExtractionsHandler(g)},
		{Tool: resolveExtractionsTool(), Handler: resolveExtractionsHandler(g)},
		{Tool: importAllTool(), Handler: importAllHandler(g)},
		{Tool: nodeSearchTool(), Handler: nodeSearchHandler(g)},
		{Tool: subgraphTool(), Handler: subgraphHandler(g)},
		{Tool: insightsTool(), Handler: insightsHandler(g)},
		{Tool: queryDepsTool(), Handler: queryDepsHandler(g)},
		{Tool: queryReverseDepsTool(), Handler: queryReverseDepsHandler(g)},
		{Tool: queryStatsTool(), Handler: queryStatsHandler(g)},
		{Tool: snapshotCreateTool(), Handler: snapshotCreateHandler(g)},
		{Tool: staleMarkTool(), Handler: staleMarkHandler(g)},
		{Tool: invalidateChangedTool(), Handler: invalidateChangedHandler(g)},
		{Tool: reviewReportTool(), Handler: reviewReportHandler(g)},
		{Tool: finalizeIncrementalScanTool(), Handler: finalizeIncrementalScanHandler(g)},
		{Tool: queryPathTool(), Handler: queryPathHandler(g)},
		{Tool: impactTool(), Handler: impactHandler(g)},
		{Tool: schemaTool(), Handler: schemaHandler(g)},
		{Tool: extractionGuideTool(), Handler: extractionGuideHandler()},
		{Tool: extractionHintsTool(), Handler: extractionHintsHandler()},
		{Tool: instructionPacksTool(), Handler: instructionPacksHandler(g)},
		{Tool: getInstructionPackTool(), Handler: getInstructionPackHandler(g)},
		{Tool: saveCustomPackTool(), Handler: saveCustomPackHandler(g)},
		{Tool: scanConfirmTool(), Handler: scanConfirmHandler(g)},
		{Tool: scanStatusTool(), Handler: scanStatusHandler(g)},
		{Tool: freshnessTool(), Handler: freshnessHandler(g, serverRepoDir())},
		{Tool: scanPoolStatusTool(), Handler: scanPoolStatusHandler(g)},
		{Tool: scanCheckoutBatchTool(), Handler: scanCheckoutBatchHandler(g)},
		{Tool: commitScanOutboxTool(), Handler: commitScanOutboxHandler(g)},
		{Tool: fileExtractedBatchTool(), Handler: fileExtractedBatchHandler(g)},
		{Tool: scanMarkFailedTool(), Handler: scanMarkFailedHandler(g)},
		{Tool: scanReviewCandidatesTool(), Handler: scanReviewCandidatesHandler(g)},
		{Tool: saveManifestTool(), Handler: saveManifestHandler(g)},
		{Tool: resetDBTool(), Handler: resetDBHandler(g)},
		{Tool: reportDiscoveryTool(), Handler: reportDiscoveryHandler(g)},
		{Tool: getDiscoveriesTool(), Handler: getDiscoveriesHandler(g)},
		{Tool: adminURLTool(), Handler: adminURLHandler()},
		{Tool: defineTermTool(), Handler: defineTermHandler(g)},
		{Tool: getGlossaryTool(), Handler: getGlossaryHandler(g)},
		{Tool: checkLanguageTool(), Handler: checkLanguageHandler(g)},
		{Tool: mcpIdentityTool(), Handler: mcpIdentityHandler()},
		{Tool: commandTool(), Handler: commandHandler(g)},
		{Tool: diagramBuildTool(), Handler: diagramBuildHandler(g)},
		{Tool: salienceExplainTool(), Handler: salienceExplainHandler(g)},
		{Tool: domainListTool(), Handler: domainListHandler(g)},
		{Tool: resolveContextTool(), Handler: resolveContextHandler(g)},
		{Tool: contextListTool(), Handler: contextListHandler(g)},
		{Tool: contextCreateTool(), Handler: contextCreateHandler(g)},
		{Tool: contextArchiveTool(), Handler: contextArchiveHandler(g)},
		{Tool: changelogQueryTool(), Handler: changelogQueryHandler(g)},
	}
}

// ToolsWithLogging returns the same toolset with every handler wrapped in
// request logging to logStore. When a debug logger is active it additionally
// carries chronicle_debug_log — exactly mirroring what NewServerWithLogging
// has always registered (NewServer never exposes the debug tool).
func ToolsWithLogging(g *graph.Graph, logStore *store.Store) []server.ServerTool {
	tools := Tools(g)
	out := make([]server.ServerTool, len(tools), len(tools)+1)
	for i, st := range tools {
		out[i] = server.ServerTool{
			Tool:    st.Tool,
			Handler: loggingWrap(logStore, st.Tool.Name, st.Handler),
		}
	}
	if GetDebugLogger() != nil {
		dt := debugLogTool()
		out = append(out, server.ServerTool{
			Tool:    dt,
			Handler: loggingWrap(logStore, dt.Name, debugLogHandler()),
		})
	}
	return out
}

// ServerInstructions returns the server instructions text, including the
// debug addendum when a debug logger is active.
func ServerInstructions() string {
	instructions := serverInstructions
	if GetDebugLogger() != nil {
		instructions += "\n\n" + debugInstructions
	}
	return instructions
}

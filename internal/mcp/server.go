package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var manifestFilePath string
var adminPortValue int

func SetManifestPath(p string) { manifestFilePath = p }
func SetAdminPort(p int)       { adminPortValue = p }

// NewServer creates a new MCP server exposing all graph operations as tools.
func NewServer(g *graph.Graph) *server.MCPServer {
	s := server.NewMCPServer("chronicle", "0.1.0")

	s.AddTool(revisionCreateTool(), revisionCreateHandler(g))
	s.AddTool(nodeUpsertTool(), nodeUpsertHandler(g))
	s.AddTool(nodeListTool(), nodeListHandler(g))
	s.AddTool(nodeGetTool(), nodeGetHandler(g))
	s.AddTool(edgeUpsertTool(), edgeUpsertHandler(g))
	s.AddTool(edgeListTool(), edgeListHandler(g))
	s.AddTool(evidenceAddTool(), evidenceAddHandler(g))
	s.AddTool(importAllTool(), importAllHandler(g))
	s.AddTool(queryDepsTool(), queryDepsHandler(g))
	s.AddTool(queryReverseDepsTool(), queryReverseDepsHandler(g))
	s.AddTool(queryStatsTool(), queryStatsHandler(g))
	s.AddTool(snapshotCreateTool(), snapshotCreateHandler(g))
	s.AddTool(staleMarkTool(), staleMarkHandler(g))
	s.AddTool(invalidateChangedTool(), invalidateChangedHandler(g))
	s.AddTool(finalizeIncrementalScanTool(), finalizeIncrementalScanHandler(g))
	s.AddTool(queryPathTool(), queryPathHandler(g))
	s.AddTool(impactTool(), impactHandler(g))
	s.AddTool(extractionGuideTool(), extractionGuideHandler())
	s.AddTool(scanStatusTool(), scanStatusHandler(g))
	s.AddTool(saveManifestTool(), saveManifestHandler())
	s.AddTool(resetDBTool(), resetDBHandler(g))
	s.AddTool(reportDiscoveryTool(), reportDiscoveryHandler(g))
	s.AddTool(getDiscoveriesTool(), getDiscoveriesHandler(g))
	s.AddTool(adminURLTool(), adminURLHandler())
	s.AddTool(defineTermTool(), defineTermHandler(g))
	s.AddTool(getGlossaryTool(), getGlossaryHandler(g))
	s.AddTool(checkLanguageTool(), checkLanguageHandler(g))
	s.AddTool(commandTool(), commandHandler(g))
	s.AddTool(diagramCreateTool(), diagramCreateHandler())
	s.AddTool(diagramUpdateTool(), diagramUpdateHandler())
	s.AddTool(diagramAnnotateTool(), diagramAnnotateHandler())
	s.AddTool(resolveContextTool(), resolveContextHandler(g))
	s.AddTool(contextListTool(), contextListHandler(g))
	s.AddTool(contextCreateTool(), contextCreateHandler(g))
	s.AddTool(contextArchiveTool(), contextArchiveHandler(g))
	s.AddTool(changelogQueryTool(), changelogQueryHandler(g))

	return s
}

// ---------------------------------------------------------------------------
// Parameter helpers
// ---------------------------------------------------------------------------

func strParam(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func intParam(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	}
	return 0
}

func int64Param(args map[string]any, key string) int64 {
	switch v := args[key].(type) {
	case float64:
		return int64(v)
	}
	return 0
}

func float64Param(args map[string]any, key string) float64 {
	v, _ := args[key].(float64)
	return v
}

func jsonResult(v any) *mcp.CallToolResult {
	data, _ := json.Marshal(v)
	return mcp.NewToolResultText(string(data))
}

func errorResult(err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(err.Error())
}

// ---------------------------------------------------------------------------
// chronicle_revision_create
// ---------------------------------------------------------------------------

func revisionCreateTool() mcp.Tool {
	return mcp.NewTool("chronicle_revision_create",
		mcp.WithDescription("Create a new graph revision to track a scan pass. Call this at the start of every scan. Use trigger='manual' for human-initiated scans. Mode is 'full' for complete rescan or 'incremental' for partial. Returns revision_id to use in subsequent import calls."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithString("after_sha", mcp.Required(), mcp.Description("Git after SHA")),
		mcp.WithString("before_sha", mcp.Description("Git before SHA")),
		mcp.WithString("trigger", mcp.Description("Trigger kind (full_scan, manual, git_hook, push_webhook, release_webhook, ci_pipeline)")),
		mcp.WithString("mode", mcp.Description("Mode (full or incremental)")),
	)
}

func revisionCreateHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		afterSHA := strParam(args, "after_sha")
		beforeSHA := strParam(args, "before_sha")
		trigger := strParam(args, "trigger")
		mode := strParam(args, "mode")

		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}
		if afterSHA == "" {
			return errorResult(fmt.Errorf("after_sha is required")), nil
		}
		if trigger == "" {
			trigger = "manual"
		}
		if mode == "" {
			mode = "full"
		}

		id, err := g.Store().CreateRevision(domain, beforeSHA, afterSHA, trigger, mode, "{}")
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"revision_id": id}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_node_upsert
// ---------------------------------------------------------------------------

func nodeUpsertTool() mcp.Tool {
	return mcp.NewTool("chronicle_node_upsert",
		mcp.WithDescription("Create or update a graph node. Upsert by node_key — if the key exists, mutable fields (name, file_path, confidence, metadata) are updated. Immutable fields (layer, node_type, domain) must match or the upsert is rejected. Key format: layer:type:domain:qualified_name (all lowercase)."),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Node name")),
		mcp.WithString("node_key", mcp.Description("Node key (auto-generated if omitted)")),
		mcp.WithString("layer", mcp.Description("Layer (code, service, contract, flow, ownership, infra, ci)")),
		mcp.WithString("node_type", mcp.Description("Node type")),
		mcp.WithString("domain", mcp.Description("Domain key")),
		mcp.WithString("repo_name", mcp.Description("Repository name")),
		mcp.WithString("file_path", mcp.Description("File path")),
		mcp.WithString("metadata", mcp.Description("JSON metadata string")),
		mcp.WithNumber("confidence", mcp.Description("Confidence [0,1]")),
	)
}

func nodeUpsertHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		revisionID := int64Param(args, "revision_id")
		if revisionID == 0 {
			return errorResult(fmt.Errorf("revision_id is required")), nil
		}

		input := validate.NodeInput{
			NodeKey:    strParam(args, "node_key"),
			Layer:      strParam(args, "layer"),
			NodeType:   strParam(args, "node_type"),
			DomainKey:  strParam(args, "domain"),
			Name:       strParam(args, "name"),
			RepoName:   strParam(args, "repo_name"),
			FilePath:   strParam(args, "file_path"),
			Metadata:   strParam(args, "metadata"),
			Confidence: float64Param(args, "confidence"),
		}

		id, err := g.UpsertNode(input, revisionID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"node_id": id}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_node_list
// ---------------------------------------------------------------------------

func nodeListTool() mcp.Tool {
	return mcp.NewTool("chronicle_node_list",
		mcp.WithDescription("List graph nodes with optional filters. Filter by layer (code/service/contract/etc), node_type, domain, or status (active/stale/deleted)."),
		mcp.WithString("layer", mcp.Description("Filter by layer")),
		mcp.WithString("node_type", mcp.Description("Filter by node type")),
		mcp.WithString("domain", mcp.Description("Filter by domain key")),
		mcp.WithString("status", mcp.Description("Filter by status")),
	)
}

func nodeListHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		filter := store.NodeFilter{
			Layer:    strParam(args, "layer"),
			NodeType: strParam(args, "node_type"),
			Domain:   strParam(args, "domain"),
			Status:   strParam(args, "status"),
		}
		nodes, err := g.Store().ListNodes(filter)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(nodes), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_node_get
// ---------------------------------------------------------------------------

func nodeGetTool() mcp.Tool {
	return mcp.NewTool("chronicle_node_get",
		mcp.WithDescription("Get a single node by key with all its evidence entries. Use to inspect a specific entity and see what evidence supports its existence in the graph."),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Node key")),
	)
}

func nodeGetHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		nodeKey := strParam(args, "node_key")
		if nodeKey == "" {
			return errorResult(fmt.Errorf("node_key is required")), nil
		}

		node, err := g.Store().GetNodeByKey(nodeKey)
		if err != nil {
			return errorResult(err), nil
		}
		evidence, err := g.Store().ListEvidenceByNode(node.NodeID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{
			"node":     node,
			"evidence": evidence,
		}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_edge_upsert
// ---------------------------------------------------------------------------

func edgeUpsertTool() mcp.Tool {
	return mcp.NewTool("chronicle_edge_upsert",
		mcp.WithDescription("Create or update a graph edge. Upsert by edge_key (auto-generated as from->to:type if not provided). The from/to nodes must already exist. Edge type and layers are validated against the type registry. Derivation: hard (AST-level), linked (convention-based), inferred (guessed), unknown."),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID")),
		mcp.WithString("derivation_kind", mcp.Required(), mcp.Description("Derivation kind (hard, linked, inferred, unknown)")),
		mcp.WithString("from_node_key", mcp.Description("From node key")),
		mcp.WithString("to_node_key", mcp.Description("To node key")),
		mcp.WithString("edge_type", mcp.Description("Edge type")),
		mcp.WithString("edge_key", mcp.Description("Edge key (auto-generated if omitted)")),
		mcp.WithString("metadata", mcp.Description("JSON metadata string")),
		mcp.WithNumber("confidence", mcp.Description("Confidence [0,1]")),
	)
}

func edgeUpsertHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		revisionID := int64Param(args, "revision_id")
		if revisionID == 0 {
			return errorResult(fmt.Errorf("revision_id is required")), nil
		}

		fromNodeKey := strParam(args, "from_node_key")
		toNodeKey := strParam(args, "to_node_key")

		// Look up from/to nodes to get layers for validation.
		var fromLayer, toLayer string
		if fromNodeKey != "" {
			fromNode, err := g.Store().GetNodeByKey(fromNodeKey)
			if err != nil {
				return errorResult(fmt.Errorf("from_node_key: %w", err)), nil
			}
			fromLayer = fromNode.Layer
		}
		if toNodeKey != "" {
			toNode, err := g.Store().GetNodeByKey(toNodeKey)
			if err != nil {
				return errorResult(fmt.Errorf("to_node_key: %w", err)), nil
			}
			toLayer = toNode.Layer
		}

		input := validate.EdgeInput{
			EdgeKey:        strParam(args, "edge_key"),
			FromNodeKey:    fromNodeKey,
			ToNodeKey:      toNodeKey,
			EdgeType:       strParam(args, "edge_type"),
			DerivationKind: strParam(args, "derivation_kind"),
			FromLayer:      fromLayer,
			ToLayer:        toLayer,
			Metadata:       strParam(args, "metadata"),
			Confidence:     float64Param(args, "confidence"),
		}

		id, err := g.UpsertEdge(input, revisionID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"edge_id": id}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_edge_list
// ---------------------------------------------------------------------------

func edgeListTool() mcp.Tool {
	return mcp.NewTool("chronicle_edge_list",
		mcp.WithDescription("List graph edges with optional filters. Filter by source node, target node, or edge type. Returns all matching edges with derivation kind and confidence."),
		mcp.WithString("from_node_key", mcp.Description("Filter by from node key")),
		mcp.WithString("to_node_key", mcp.Description("Filter by to node key")),
		mcp.WithString("edge_type", mcp.Description("Filter by edge type")),
	)
}

func edgeListHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		filter := store.EdgeFilter{
			EdgeType: strParam(args, "edge_type"),
		}

		if fromKey := strParam(args, "from_node_key"); fromKey != "" {
			fromNode, err := g.Store().GetNodeByKey(fromKey)
			if err != nil {
				return errorResult(fmt.Errorf("from_node_key: %w", err)), nil
			}
			filter.FromNodeID = fromNode.NodeID
		}
		if toKey := strParam(args, "to_node_key"); toKey != "" {
			toNode, err := g.Store().GetNodeByKey(toKey)
			if err != nil {
				return errorResult(fmt.Errorf("to_node_key: %w", err)), nil
			}
			filter.ToNodeID = toNode.NodeID
		}

		edges, err := g.Store().ListEdges(filter)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(edges), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_evidence_add
// ---------------------------------------------------------------------------

func evidenceAddTool() mcp.Tool {
	return mcp.NewTool("chronicle_evidence_add",
		mcp.WithDescription("Add provenance evidence for a node or edge. For code evidence: include file_path and line_start, source_kind='file'. For user corrections: use source_kind='user_feedback', polarity='negative', confidence=0.95, and include the user's reason in metadata. Negative evidence with high confidence contradicts the fact and effectively removes it from the graph. Extractor_id should be 'claude-code'."),
		mcp.WithString("extractor_id", mcp.Required(), mcp.Description("Extractor ID")),
		mcp.WithString("extractor_version", mcp.Required(), mcp.Description("Extractor version")),
		mcp.WithString("target_kind", mcp.Description("Target kind: node or edge")),
		mcp.WithString("source_kind", mcp.Description("Source kind: 'file' for code evidence, 'user_feedback' for user corrections")),
		mcp.WithString("node_key", mcp.Description("Node key (for target_kind=node)")),
		mcp.WithString("edge_key", mcp.Description("Edge key (for target_kind=edge)")),
		mcp.WithString("repo_name", mcp.Description("Repository name")),
		mcp.WithString("file_path", mcp.Description("File path")),
		mcp.WithString("commit_sha", mcp.Description("Commit SHA")),
		mcp.WithNumber("line_start", mcp.Description("Start line number")),
		mcp.WithNumber("line_end", mcp.Description("End line number")),
		mcp.WithNumber("confidence", mcp.Description("Confidence [0,1]")),
		mcp.WithString("polarity", mcp.Description("Evidence polarity: positive (default) or negative. Use negative to explicitly record that a relationship was confirmed removed.")),
		mcp.WithNumber("revision_id", mcp.Description("Revision ID for this evidence")),
	)
}

func evidenceAddHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		extractorID := strParam(args, "extractor_id")
		extractorVersion := strParam(args, "extractor_version")
		if extractorID == "" {
			return errorResult(fmt.Errorf("extractor_id is required")), nil
		}
		if extractorVersion == "" {
			return errorResult(fmt.Errorf("extractor_version is required")), nil
		}

		input := validate.EvidenceInput{
			TargetKind:       strParam(args, "target_kind"),
			SourceKind:       strParam(args, "source_kind"),
			RepoName:         strParam(args, "repo_name"),
			FilePath:         strParam(args, "file_path"),
			CommitSHA:        strParam(args, "commit_sha"),
			ExtractorID:      extractorID,
			ExtractorVersion: extractorVersion,
			LineStart:        intParam(args, "line_start"),
			LineEnd:          intParam(args, "line_end"),
			Confidence:       float64Param(args, "confidence"),
			Polarity:         strParam(args, "polarity"),
			RevisionID:       int64Param(args, "revision_id"),
		}

		nodeKey := strParam(args, "node_key")
		edgeKey := strParam(args, "edge_key")

		var id int64
		var err error
		switch input.TargetKind {
		case "node":
			if nodeKey == "" {
				return errorResult(fmt.Errorf("node_key is required when target_kind=node")), nil
			}
			id, err = g.AddNodeEvidence(nodeKey, input)
		case "edge":
			if edgeKey == "" {
				return errorResult(fmt.Errorf("edge_key is required when target_kind=edge")), nil
			}
			id, err = g.AddEdgeEvidence(edgeKey, input)
		default:
			return errorResult(fmt.Errorf("target_kind must be 'node' or 'edge'")), nil
		}
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"evidence_id": id}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_import_all
// ---------------------------------------------------------------------------

func importAllTool() mcp.Tool {
	return mcp.NewTool("chronicle_import_all",
		mcp.WithDescription("Import nodes, edges, evidence in a single transaction. KEEP PAYLOADS SMALL — max ~15 nodes per call. Read one file → extract → import → move to next file. Do NOT accumulate."),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID")),
		mcp.WithString("payload", mcp.Required(), mcp.Description("JSON string containing nodes, edges, and evidence arrays")),
	)
}

func importAllHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		revisionID := int64Param(args, "revision_id")
		if revisionID == 0 {
			return errorResult(fmt.Errorf("revision_id is required")), nil
		}
		payloadStr := strParam(args, "payload")
		if payloadStr == "" {
			return errorResult(fmt.Errorf("payload is required")), nil
		}

		// Warn on large payloads — Claude should stream smaller batches
		if len(payloadStr) > 15000 {
			// Still accept it, but add warning to result
			var payload graph.ImportPayload
			if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
				return errorResult(fmt.Errorf("invalid payload JSON: %w", err)), nil
			}
			result, err := g.ImportAll(payload, revisionID)
			if err != nil {
				return errorResult(err), nil
			}
			return jsonResult(map[string]any{
				"nodes_created":    result.NodesCreated,
				"edges_created":    result.EdgesCreated,
				"evidence_created": result.EvidenceCreated,
				"warning":          fmt.Sprintf("Payload was %dKB — please use smaller batches (< 15 nodes per call). Read one file, import immediately, move on.", len(payloadStr)/1024),
			}), nil
		}

		var payload graph.ImportPayload
		if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
			return errorResult(fmt.Errorf("invalid payload JSON: %w", err)), nil
		}

		result, err := g.ImportAll(payload, revisionID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_query_deps
// ---------------------------------------------------------------------------

func queryDepsTool() mcp.Tool {
	return mcp.NewTool("chronicle_query_deps",
		mcp.WithDescription("Query forward dependencies of a node — what does this node depend on? BFS traversal of outgoing edges up to specified depth. Use --derivation to filter by confidence level."),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Starting node key")),
		mcp.WithString("derivation", mcp.Description("Comma-separated derivation kinds to follow")),
		mcp.WithNumber("depth", mcp.Description("Maximum BFS depth (default 3)")),
	)
}

func queryDepsHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		nodeKey := strParam(args, "node_key")
		if nodeKey == "" {
			return errorResult(fmt.Errorf("node_key is required")), nil
		}
		depth := intParam(args, "depth")
		if depth == 0 {
			depth = 3
		}
		var derivationFilter []string
		if d := strParam(args, "derivation"); d != "" {
			for _, part := range strings.Split(d, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					derivationFilter = append(derivationFilter, part)
				}
			}
		}

		nodes, err := g.QueryDeps(nodeKey, depth, derivationFilter)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(nodes), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_query_reverse_deps
// ---------------------------------------------------------------------------

func queryReverseDepsTool() mcp.Tool {
	return mcp.NewTool("chronicle_query_reverse_deps",
		mcp.WithDescription("Query reverse dependencies — who depends on this node? BFS traversal of incoming edges. Use to find all consumers of a service, endpoint, or topic."),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Starting node key")),
		mcp.WithString("derivation", mcp.Description("Comma-separated derivation kinds to follow")),
		mcp.WithNumber("depth", mcp.Description("Maximum BFS depth (default 3)")),
	)
}

func queryReverseDepsHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		nodeKey := strParam(args, "node_key")
		if nodeKey == "" {
			return errorResult(fmt.Errorf("node_key is required")), nil
		}
		depth := intParam(args, "depth")
		if depth == 0 {
			depth = 3
		}
		var derivationFilter []string
		if d := strParam(args, "derivation"); d != "" {
			for _, part := range strings.Split(d, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					derivationFilter = append(derivationFilter, part)
				}
			}
		}

		nodes, err := g.QueryReverseDeps(nodeKey, depth, derivationFilter)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(nodes), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_query_stats
// ---------------------------------------------------------------------------

func queryStatsTool() mcp.Tool {
	return mcp.NewTool("chronicle_query_stats",
		mcp.WithDescription("Get aggregate graph statistics for a domain: node/edge counts, breakdown by layer, edge type, derivation kind, active vs stale. Use to verify scan completeness."),
		mcp.WithString("domain", mcp.Description("Domain key (empty = all domains)")),
	)
}

func queryStatsHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")

		stats, err := g.QueryStats(domain)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(stats), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_snapshot_create
// ---------------------------------------------------------------------------

func snapshotCreateTool() mcp.Tool {
	return mcp.NewTool("chronicle_snapshot_create",
		mcp.WithDescription("Record a point-in-time snapshot after a scan completes. Captures node and edge counts. Call after chronicle_import_all and chronicle_stale_mark to close the scan lifecycle."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID")),
		mcp.WithNumber("node_count", mcp.Required(), mcp.Description("Node count")),
		mcp.WithNumber("edge_count", mcp.Required(), mcp.Description("Edge count")),
		mcp.WithString("kind", mcp.Description("Snapshot kind (full or incremental)")),
	)
}

func snapshotCreateHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}
		revisionID := int64Param(args, "revision_id")
		if revisionID == 0 {
			return errorResult(fmt.Errorf("revision_id is required")), nil
		}
		kind := strParam(args, "kind")
		if kind == "" {
			kind = "full"
		}

		snap := store.SnapshotRow{
			RevisionID: revisionID,
			DomainKey:  domain,
			Kind:       kind,
			NodeCount:  intParam(args, "node_count"),
			EdgeCount:  intParam(args, "edge_count"),
		}
		id, err := g.Store().CreateSnapshot(snap)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"snapshot_id": id}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_stale_mark
// ---------------------------------------------------------------------------

func staleMarkTool() mcp.Tool {
	return mcp.NewTool("chronicle_stale_mark",
		mcp.WithDescription("Mark nodes and edges not seen in the current revision as stale. Call after import to flag entities from previous scans that were not re-imported. Stale entities remain queryable but are flagged."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID threshold")),
	)
}

func staleMarkHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}
		revisionID := int64Param(args, "revision_id")
		if revisionID == 0 {
			return errorResult(fmt.Errorf("revision_id is required")), nil
		}

		staleNodes, err := g.Store().MarkStaleNodes(domain, revisionID)
		if err != nil {
			return errorResult(err), nil
		}
		staleEdges, err := g.Store().MarkStaleEdges(domain, revisionID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{
			"stale_nodes": staleNodes,
			"stale_edges": staleEdges,
		}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_invalidate_changed
// ---------------------------------------------------------------------------

func invalidateChangedTool() mcp.Tool {
	return mcp.NewTool("chronicle_invalidate_changed",
		mcp.WithDescription("Mark evidence from changed files as stale and recalculate trust scores. Call during incremental scans after getting changed files from git diff. Returns list of files to rescan."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Current revision ID")),
		mcp.WithString("changed_files", mcp.Required(), mcp.Description("JSON array of changed file paths")),
	)
}

func invalidateChangedHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}
		revisionID := int64Param(args, "revision_id")
		if revisionID == 0 {
			return errorResult(fmt.Errorf("revision_id is required")), nil
		}
		filesJSON := strParam(args, "changed_files")
		if filesJSON == "" {
			return errorResult(fmt.Errorf("changed_files is required")), nil
		}

		var files []string
		if err := json.Unmarshal([]byte(filesJSON), &files); err != nil {
			return errorResult(fmt.Errorf("changed_files must be a JSON array: %w", err)), nil
		}

		result, err := g.InvalidateChanged(domain, revisionID, files)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_finalize_incremental_scan
// ---------------------------------------------------------------------------

func finalizeIncrementalScanTool() mcp.Tool {
	return mcp.NewTool("chronicle_finalize_incremental_scan",
		mcp.WithDescription("Complete an incremental scan. Counts revalidated/stale/contradicted evidence and recalculates trust scores. Stale evidence stays stale — only negative evidence causes invalidation."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Current revision ID")),
	)
}

func finalizeIncrementalScanHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}
		revisionID := int64Param(args, "revision_id")
		if revisionID == 0 {
			return errorResult(fmt.Errorf("revision_id is required")), nil
		}

		result, err := g.FinalizeIncrementalScan(domain, revisionID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_query_path
// ---------------------------------------------------------------------------

func queryPathTool() mcp.Tool {
	return mcp.NewTool("chronicle_query_path",
		mcp.WithDescription("Find paths between two nodes. Default mode 'directed' follows edges in natural direction for dependency chains. Use 'connected' for undirected exploration. Structural edges (CONTAINS) excluded by default. Returns top-k paths ranked by path score."),
		mcp.WithString("from_node_key", mcp.Required(), mcp.Description("Source node key")),
		mcp.WithString("to_node_key", mcp.Required(), mcp.Description("Target node key")),
		mcp.WithNumber("max_depth", mcp.Description("Max depth (default 6)")),
		mcp.WithNumber("top_k", mcp.Description("Max paths (default 3)")),
		mcp.WithString("mode", mcp.Description("directed or connected (default directed)")),
		mcp.WithString("derivation", mcp.Description("Comma-separated derivation filter")),
	)
}

func queryPathHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		maxDepth := intParam(args, "max_depth")
		if maxDepth == 0 {
			maxDepth = 6
		}
		topK := intParam(args, "top_k")
		if topK == 0 {
			topK = 3
		}
		mode := strParam(args, "mode")
		if mode == "" {
			mode = "directed"
		}
		var filter []string
		if d := strParam(args, "derivation"); d != "" {
			for _, s := range strings.Split(d, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					filter = append(filter, s)
				}
			}
		}
		result, err := g.QueryPath(strParam(args, "from_node_key"), strParam(args, "to_node_key"), graph.PathOptions{
			MaxDepth: maxDepth, TopK: topK, Mode: mode, DerivationFilter: filter,
		})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_impact
// ---------------------------------------------------------------------------

func impactTool() mcp.Tool {
	return mcp.NewTool("chronicle_impact",
		mcp.WithDescription("Analyze blast radius of a node change. Reverse dependency traversal respecting traversal policy — structural edges excluded, EXPOSES_ENDPOINT doesn't propagate reverse. Returns scored impact list. Use to answer 'what breaks if I change X?'"),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Changed node key")),
		mcp.WithNumber("depth", mcp.Description("Max depth (default 4)")),
		mcp.WithString("derivation", mcp.Description("Comma-separated derivation filter")),
		mcp.WithNumber("min_score", mcp.Description("Minimum impact score (default 0.1)")),
		mcp.WithNumber("top_k", mcp.Description("Max results (default 50)")),
	)
}

func impactHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		depth := intParam(args, "depth")
		if depth == 0 {
			depth = 4
		}
		topK := intParam(args, "top_k")
		if topK == 0 {
			topK = 50
		}
		minScore := float64Param(args, "min_score")
		var filter []string
		if d := strParam(args, "derivation"); d != "" {
			for _, s := range strings.Split(d, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					filter = append(filter, s)
				}
			}
		}
		result, err := g.QueryImpact(strParam(args, "node_key"), graph.ImpactOptions{
			MaxDepth: depth, MinScore: minScore, TopK: topK, DerivationFilter: filter,
		})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_extraction_guide
// ---------------------------------------------------------------------------

func extractionGuideTool() mcp.Tool {
	return mcp.NewTool("chronicle_extraction_guide",
		mcp.WithDescription("Get the extraction methodology guide. Call this before scanning a codebase to understand what entities and relationships to extract, how to structure the import payload, and the recommended workflow. Returns comprehensive JSON instructions for analyzing TypeScript/NestJS, OpenAPI, and other codebases."),
		mcp.WithString("technology", mcp.Description("Filter guide to a specific technology: nestjs, openapi, or omit for full guide")),
	)
}

func extractionGuideHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tech := strParam(req.GetArguments(), "technology")
		guide := ExtractionGuide(tech)
		return mcp.NewToolResultText(guide), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_scan_status
// ---------------------------------------------------------------------------

func scanStatusTool() mcp.Tool {
	return mcp.NewTool("chronicle_scan_status",
		mcp.WithDescription("Get the current graph state for a domain. Returns last revision, graph statistics (node/edge counts by layer and type), and last snapshot. Use this before scanning to decide whether to do a full or incremental scan."),
		mcp.WithString("domain", mcp.Description("Domain key (from chronicle.domain.yaml). If omitted, returns a message to check the manifest.")),
	)
}

func scanStatusHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		domain := strParam(req.GetArguments(), "domain")

		// Check if this is a fresh project (no nodes at all)
		allNodes, _ := g.Store().ListNodes(store.NodeFilter{})
		isFirstRun := len(allNodes) == 0

		if domain == "" {
			// Try to detect domain from existing nodes or manifest
			if len(allNodes) > 0 {
				domain = allNodes[0].DomainKey
			}
		}

		result := map[string]any{"domain": domain}

		if isFirstRun {
			result["onboarding"] = map[string]any{
				"is_first_run": true,
				"message":      "This project has never been scanned. I recommend running an onboarding scan.",
				"ask_user":     "Would you like me to scan this project and build a knowledge graph? I'll discover the project structure, extract data models, code dependencies, and API surface.",
				"if_yes":       "Call chronicle_command(command='scan') to start the full scan.",
			}
			// Also include admin dashboard URL
			port := adminPortValue
			if port == 0 {
				port = 4200
			}
			result["admin_dashboard"] = fmt.Sprintf("http://localhost:%d", port)
			return jsonResult(result), nil
		}

		if domain != "" {
			rev, err := g.Store().GetLatestRevision(domain)
			if err != nil {
				result["last_revision"] = nil
			} else {
				result["last_revision"] = rev
			}

			stats, err := g.QueryStats(domain)
			if err == nil {
				result["graph_stats"] = stats
			}

			snap, err := g.Store().GetLatestSnapshot(domain)
			if err == nil {
				result["last_snapshot"] = snap
			}

			// Check discoveries
			discoveries, _ := g.Store().ListDiscoveries(domain, "")
			result["pending_discoveries"] = len(discoveries)

			// Check glossary
			terms, _ := g.Store().GetGlossary(domain)
			result["glossary_terms"] = len(terms)
		}

		port := adminPortValue
		if port == 0 {
			port = 4200
		}
		result["admin_dashboard"] = fmt.Sprintf("http://localhost:%d", port)

		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_save_manifest
// ---------------------------------------------------------------------------

func saveManifestTool() mcp.Tool {
	return mcp.NewTool("chronicle_save_manifest",
		mcp.WithDescription("Save the domain manifest (chronicle.domain.yaml). Use after auto-discovering the project structure — identify repos, tech stack, and domain name, then save. Claude should auto-discover and never ask the user to manually edit this file."),
		mcp.WithString("content", mcp.Required(), mcp.Description("Full YAML content for chronicle.domain.yaml")),
	)
}

func saveManifestHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := strParam(req.GetArguments(), "content")
		if content == "" {
			return errorResult(fmt.Errorf("content is required")), nil
		}
		path := manifestFilePath
		if path == "" {
			path = ".depbot/chronicle.domain.yaml"
		}
		os.MkdirAll(".depbot", 0755)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]string{"status": "saved", "path": path}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_reset_db
// ---------------------------------------------------------------------------

func resetDBTool() mcp.Tool {
	return mcp.NewTool("chronicle_reset_db",
		mcp.WithDescription("Reset the database — drops all tables and recreates the schema. Use when schema has changed or you want a clean re-scan. All existing graph data will be lost."),
	)
}

func resetDBHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := g.Store().ResetDB(); err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]string{"status": "reset", "message": "Database reset. All tables recreated. Ready for fresh scan."}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_report_discovery
// ---------------------------------------------------------------------------

func reportDiscoveryTool() mcp.Tool {
	return mcp.NewTool("chronicle_report_discovery",
		mcp.WithDescription("Report a discovery about the codebase. Use this when you learn something new during analysis that should be remembered for future scans. Categories: 'pattern' (new code pattern), 'correction' (previous extraction was wrong), 'insight' (user told you something), 'missing_edge' (relationship exists but wasn't captured), 'unknown_pattern' (code pattern you don't know how to classify)."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithString("category", mcp.Required(), mcp.Description("pattern, correction, insight, missing_edge, unknown_pattern")),
		mcp.WithString("title", mcp.Required(), mcp.Description("Short title for the discovery")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Detailed description of what was discovered")),
		mcp.WithString("source", mcp.Description("Who made this discovery: claude, user, system (default: claude)")),
		mcp.WithNumber("confidence", mcp.Description("How confident 0-1 (default: 0.5)")),
		mcp.WithString("related_nodes", mcp.Description("JSON array of related node_keys")),
	)
}

func reportDiscoveryHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		source := strParam(args, "source")
		if source == "" {
			source = "claude"
		}
		conf := float64Param(args, "confidence")
		if conf == 0 {
			conf = 0.5
		}
		relatedNodes := strParam(args, "related_nodes")
		if relatedNodes == "" {
			relatedNodes = "[]"
		}
		id, err := g.Store().AddDiscovery(store.Discovery{
			DomainKey:    strParam(args, "domain"),
			Category:     strParam(args, "category"),
			Title:        strParam(args, "title"),
			Description:  strParam(args, "description"),
			Source:       source,
			Confidence:   conf,
			RelatedNodes: relatedNodes,
		})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"discovery_id": id}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_get_discoveries
// ---------------------------------------------------------------------------

func getDiscoveriesTool() mcp.Tool {
	return mcp.NewTool("chronicle_get_discoveries",
		mcp.WithDescription("Get previous discoveries about this codebase. Call this before scanning to learn from past analysis sessions — corrections, patterns, insights from the user, and unknown patterns that need investigation."),
		mcp.WithString("domain", mcp.Description("Domain key (optional, filters by domain)")),
		mcp.WithString("category", mcp.Description("Filter by category: pattern, correction, insight, missing_edge, unknown_pattern")),
	)
}

func getDiscoveriesHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		discoveries, err := g.Store().ListDiscoveries(strParam(args, "domain"), strParam(args, "category"))
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(discoveries), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_admin_url
// ---------------------------------------------------------------------------

func adminURLTool() mcp.Tool {
	return mcp.NewTool("chronicle_admin_url",
		mcp.WithDescription("Get the admin dashboard URL. The dashboard shows the knowledge graph, MCP request log, discoveries, and scan metrics in a web browser."),
	)
}

func adminURLHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		port := adminPortValue
		if port == 0 {
			port = 4200
		}
		url := fmt.Sprintf("http://localhost:%d", port)
		return jsonResult(map[string]any{
			"url":     url,
			"port":    port,
			"message": fmt.Sprintf("Admin dashboard is running at %s — open in browser to see the knowledge graph, MCP requests, discoveries, and scan metrics.", url),
		}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_define_term
// ---------------------------------------------------------------------------

func defineTermTool() mcp.Tool {
	return mcp.NewTool("chronicle_define_term",
		mcp.WithDescription("Define or update a domain language term. Use this to build the project's ubiquitous language glossary. Include anti-patterns to detect naming violations in the codebase."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithString("term", mcp.Required(), mcp.Description("The canonical term (e.g. 'Order', 'Merchant', 'User')")),
		mcp.WithString("description", mcp.Required(), mcp.Description("What this term means in the domain")),
		mcp.WithString("context", mcp.Description("Bounded context (e.g. 'ordering', 'payments', 'auth')")),
		mcp.WithString("aliases", mcp.Description("JSON array of acceptable aliases: [\"Purchase\", \"Booking\"]")),
		mcp.WithString("anti_patterns", mcp.Description("JSON array of names that should NOT be used: [\"Buy\", \"Transaction\"]")),
		mcp.WithString("examples", mcp.Description("JSON array of correct usage examples: [\"OrderService\", \"OrderResolver\"]")),
	)
}

func defineTermHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		t := store.DomainTerm{
			DomainKey:   strParam(args, "domain"),
			Term:        strParam(args, "term"),
			Description: strParam(args, "description"),
			Context:     strParam(args, "context"),
		}
		// Parse JSON arrays
		if s := strParam(args, "aliases"); s != "" {
			json.Unmarshal([]byte(s), &t.Aliases)
		}
		if s := strParam(args, "anti_patterns"); s != "" {
			json.Unmarshal([]byte(s), &t.AntiPatterns)
		}
		if s := strParam(args, "examples"); s != "" {
			json.Unmarshal([]byte(s), &t.Examples)
		}
		id, err := g.Store().UpsertTerm(t)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"term_id": id, "term": t.Term}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_get_glossary
// ---------------------------------------------------------------------------

func getGlossaryTool() mcp.Tool {
	return mcp.NewTool("chronicle_get_glossary",
		mcp.WithDescription("Get the domain language glossary. Returns all defined terms with their aliases, anti-patterns, and descriptions. Use this to understand the project's ubiquitous language."),
		mcp.WithString("domain", mcp.Description("Domain key (optional)")),
	)
}

func getGlossaryHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		terms, err := g.Store().GetGlossary(strParam(req.GetArguments(), "domain"))
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(terms), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_check_language
// ---------------------------------------------------------------------------

func checkLanguageTool() mcp.Tool {
	return mcp.NewTool("chronicle_check_language",
		mcp.WithDescription("Check the knowledge graph for domain language violations. Scans all node names against anti-patterns defined in the glossary. Returns warnings for naming inconsistencies."),
		mcp.WithString("domain", mcp.Description("Domain key (optional)")),
	)
}

func checkLanguageHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		violations, err := g.Store().CheckLanguage(strParam(req.GetArguments(), "domain"))
		if err != nil {
			return errorResult(err), nil
		}
		if violations == nil {
			violations = []store.LanguageViolation{}
		}
		return jsonResult(map[string]any{
			"violations": violations,
			"total":      len(violations),
			"clean":      len(violations) == 0,
		}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_command — unified command executor
// ---------------------------------------------------------------------------

func commandTool() mcp.Tool {
	return mcp.NewTool("chronicle_command",
		mcp.WithDescription("Execute a Chronicle command. Available commands: scan, data, language, impact, deps, path, services, status, help. The user may type '/chronicle-scan' or 'chronicle scan' — call this tool with the command name."),
		mcp.WithString("command", mcp.Required(), mcp.Description("Command name: scan, data, language, impact, deps, path, services, status, help")),
	)
}

func commandHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cmd := strParam(req.GetArguments(), "command")

		// Check for custom override in project settings
		instructions := ""
		if customGuideStore != nil {
			if custom, err := customGuideStore.GetSetting("prompt_" + cmd); err == nil && custom != "" {
				instructions = custom
			}
		}
		if instructions == "" {
			var ok bool
			instructions, ok = CommandInstructions[cmd]
			if !ok {
				instructions = CommandInstructions["help"]
			}
		}
		return jsonResult(map[string]any{
			"command":      cmd,
			"instructions": instructions,
			"execute_now":  "Follow the instructions above step by step. Do not ask for confirmation — execute immediately.",
		}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_diagram_create
// ---------------------------------------------------------------------------

func diagramCreateTool() mcp.Tool {
	return mcp.NewTool("chronicle_diagram_create",
		mcp.WithDescription("Create a live diagram session. Returns a URL the user can open to see the diagram. Use chronicle_diagram_update to push content."),
		mcp.WithString("title", mcp.Description("Human-readable title for the diagram, e.g. 'Auth Flow' or 'Order Dependencies'")),
	)
}

func diagramCreateHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title := strParam(req.GetArguments(), "title")
		if title == "" {
			title = "Diagram"
		}
		sessionID := fmt.Sprintf("%x", time.Now().UnixNano())[len(fmt.Sprintf("%x", time.Now().UnixNano()))-8:]

		port := adminPortValue
		if port == 0 {
			port = 4200
		}

		body, _ := json.Marshal(map[string]string{"session_id": sessionID, "title": title})
		resp, err := http.Post(fmt.Sprintf("http://localhost:%d/api/diagram", port), "application/json", bytes.NewReader(body))
		if err != nil {
			return errorResult(fmt.Errorf("failed to create diagram session: %w", err)), nil
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_diagram_update
// ---------------------------------------------------------------------------

func diagramUpdateTool() mcp.Tool {
	return mcp.NewTool("chronicle_diagram_update",
		mcp.WithDescription("Push graph data to a live diagram. The dashboard updates in real-time. Can be called repeatedly to evolve the diagram. Payload uses standard Chronicle graph format: {nodes: [...], edges: [...]}"),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID from chronicle_diagram_create")),
		mcp.WithString("payload", mcp.Required(), mcp.Description("JSON string: {\"nodes\": [{\"node_id\": 1, \"node_key\": \"...\", \"name\": \"...\", \"layer\": \"...\", \"node_type\": \"...\"}], \"edges\": [{\"from_node_id\": 1, \"to_node_id\": 2, \"edge_type\": \"...\"}]}")),
	)
}

func diagramUpdateHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID := strParam(req.GetArguments(), "session_id")
		payloadStr := strParam(req.GetArguments(), "payload")
		if sessionID == "" || payloadStr == "" {
			return errorResult(fmt.Errorf("session_id and payload are required")), nil
		}

		port := adminPortValue
		if port == 0 {
			port = 4200
		}

		url := fmt.Sprintf("http://localhost:%d/api/diagram/%s", port, sessionID)
		httpReq, _ := http.NewRequest("PUT", url, strings.NewReader(payloadStr))
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return errorResult(fmt.Errorf("failed to update diagram: %w", err)), nil
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_diagram_annotate
// ---------------------------------------------------------------------------

func diagramAnnotateTool() mcp.Tool {
	return mcp.NewTool("chronicle_diagram_annotate",
		mcp.WithDescription("Add a highlight or text note to a node in a live diagram. The node gets a colored glow and/or a text label. Use 'step' to create presentation steps — user navigates with Next/Back buttons."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID from chronicle_diagram_create")),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("node_key of the node to annotate")),
		mcp.WithString("note", mcp.Description("Text note shown near the node, e.g. 'This is the bottleneck'")),
		mcp.WithString("highlight", mcp.Description("Highlight color — name or hex, e.g. 'red', '#ff6600', 'green'")),
		mcp.WithNumber("step", mcp.Description("Presentation step number (0-based). Omit for always-visible annotations. User sees Next/Back buttons to navigate steps.")),
		mcp.WithString("step_title", mcp.Description("Title for this step shown in the navigation bar, e.g. 'Tom attacks'")),
		mcp.WithString("step_description", mcp.Description("Longer description text shown below the diagram for this step")),
	)
}

func diagramAnnotateHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		sessionID := strParam(args, "session_id")
		nodeKey := strParam(args, "node_key")
		note := strParam(args, "note")
		highlight := strParam(args, "highlight")
		if sessionID == "" || nodeKey == "" {
			return errorResult(fmt.Errorf("session_id and node_key are required")), nil
		}
		if note == "" && highlight == "" {
			return errorResult(fmt.Errorf("at least one of note or highlight is required")), nil
		}

		port := adminPortValue
		if port == 0 {
			port = 4200
		}

		payload := map[string]any{"node_key": nodeKey, "note": note, "highlight": highlight}
		if stepVal, ok := args["step"]; ok {
			if stepNum, ok2 := stepVal.(float64); ok2 {
				step := int(stepNum)
				payload["step"] = step
			}
		}
		stepTitle := strParam(args, "step_title")
		if stepTitle != "" {
			payload["step_title"] = stepTitle
		}
		stepDesc := strParam(args, "step_description")
		if stepDesc != "" {
			payload["step_description"] = stepDesc
		}

		body, _ := json.Marshal(payload)
		url := fmt.Sprintf("http://localhost:%d/api/diagram/%s/annotate", port, sessionID)
		httpReq, _ := http.NewRequest("PUT", url, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return errorResult(fmt.Errorf("failed to annotate diagram: %w", err)), nil
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_resolve_context
// ---------------------------------------------------------------------------

func resolveContextTool() mcp.Tool {
	return mcp.NewTool("chronicle_resolve_context",
		mcp.WithDescription("Resolve the main knowledge context for a domain. Call this at the start of a conversation to determine which context to use for queries. Returns the context matching the 'main' git ref."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
	)
}

func resolveContextHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}

		c, err := g.Store().GetContextByRef(domain, "main")
		if err != nil {
			return jsonResult(map[string]any{
				"context": nil,
				"message": "No context found. Run chronicle scan first.",
			}), nil
		}
		return jsonResult(map[string]any{
			"context_id":       c.ContextID,
			"context_name":     c.Name,
			"head_revision_id": c.HeadRevisionID,
			"head_commit_sha":  c.HeadCommitSHA,
			"status":           c.Status,
		}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_context_list
// ---------------------------------------------------------------------------

func contextListTool() mcp.Tool {
	return mcp.NewTool("chronicle_context_list",
		mcp.WithDescription("List all knowledge contexts for a domain. Returns contexts with their status, head revision, and git ref."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
	)
}

func contextListHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}

		contexts, err := g.Store().ListContexts(domain)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(contexts), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_context_create
// ---------------------------------------------------------------------------

func contextCreateTool() mcp.Tool {
	return mcp.NewTool("chronicle_context_create",
		mcp.WithDescription("Create a new knowledge context for a domain. Use this to create branch-specific contexts for isolated graph work. Optionally fork from an existing context by providing base_context_id and base_revision_id."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Context name (e.g. 'main', 'feature/auth-refactor')")),
		mcp.WithString("git_ref", mcp.Required(), mcp.Description("Git ref this context tracks (e.g. 'main', 'feature/auth')")),
		mcp.WithNumber("base_context_id", mcp.Description("Context ID to fork from (optional)")),
		mcp.WithNumber("base_revision_id", mcp.Description("Revision ID to fork from (optional)")),
	)
}

func contextCreateHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		domain := strParam(args, "domain")
		name := strParam(args, "name")
		gitRef := strParam(args, "git_ref")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}
		if name == "" {
			return errorResult(fmt.Errorf("name is required")), nil
		}
		if gitRef == "" {
			return errorResult(fmt.Errorf("git_ref is required")), nil
		}

		baseContextID := int64Param(args, "base_context_id")
		baseRevisionID := int64Param(args, "base_revision_id")

		id, err := g.Store().CreateContext(domain, name, gitRef, baseContextID, baseRevisionID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"context_id": id}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_context_archive
// ---------------------------------------------------------------------------

func contextArchiveTool() mcp.Tool {
	return mcp.NewTool("chronicle_context_archive",
		mcp.WithDescription("Archive a knowledge context. Archived contexts are no longer active but their data is preserved. Use when a branch is merged or abandoned."),
		mcp.WithNumber("context_id", mcp.Required(), mcp.Description("Context ID to archive")),
	)
}

func contextArchiveHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		contextID := int64Param(args, "context_id")
		if contextID == 0 {
			return errorResult(fmt.Errorf("context_id is required")), nil
		}

		err := g.Store().ArchiveContext(contextID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"archived": true}), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_changelog_query
// ---------------------------------------------------------------------------

func changelogQueryTool() mcp.Tool {
	return mcp.NewTool("chronicle_changelog_query",
		mcp.WithDescription("Query the changelog for a context. Returns a list of changes (node/edge creates, updates, deletes) with optional filters on entity key and revision range. Use to see what changed between revisions."),
		mcp.WithNumber("context_id", mcp.Required(), mcp.Description("Context ID to query changelog for")),
		mcp.WithString("entity_key", mcp.Description("Filter by entity key (node_key or edge_key)")),
		mcp.WithNumber("from_revision", mcp.Description("Include only changes from this revision onward")),
		mcp.WithNumber("to_revision", mcp.Description("Include only changes up to this revision")),
	)
}

func changelogQueryHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		contextID := int64Param(args, "context_id")
		if contextID == 0 {
			return errorResult(fmt.Errorf("context_id is required")), nil
		}

		entityKey := strParam(args, "entity_key")
		fromRevision := int64Param(args, "from_revision")
		toRevision := int64Param(args, "to_revision")

		entries, err := g.Store().QueryChangelog(contextID, entityKey, fromRevision, toRevision)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(entries), nil
	}
}

package mcp

import (
	"context"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ---------------------------------------------------------------------------
// chronicle_node_search
// ---------------------------------------------------------------------------

func nodeSearchTool() mcp.Tool {
	return mcp.NewTool("chronicle_node_search",
		mcp.WithDescription("Find node keys by name fragment. Deterministic lexical match over node names, aliases, glossary terms, and file paths (exact > glossary > prefix > substring > path). Use this FIRST to resolve a name into a node_key before chronicle_query_deps / chronicle_impact / chronicle_query_path / chronicle_subgraph. Multi-word queries are AND."),
		mcp.WithString("q", mcp.Required(), mcp.Description("Name fragment(s), e.g. 'OrderController' or 'order controller'")),
		mcp.WithString("layer", mcp.Description("Filter by layer (code, service, contract, data, flow, infra)")),
		mcp.WithString("node_type", mcp.Description("Filter by node type")),
		mcp.WithString("domain", mcp.Description("Filter by domain key")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 10, cap 50)")),
	)
}

func nodeSearchHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		filter := store.NodeFilter{
			Layer:    strParam(args, "layer"),
			NodeType: strParam(args, "node_type"),
			Domain:   strParam(args, "domain"),
		}
		results, err := g.NodeSearch(strParam(args, "q"), filter, intParam(args, "limit"))
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(results), nil
	}
}

// ---------------------------------------------------------------------------
// chronicle_subgraph
// ---------------------------------------------------------------------------

func subgraphTool() mcp.Tool {
	return mcp.NewTool("chronicle_subgraph",
		mcp.WithDescription("Return the BFS neighborhood around a node in one call (nodes + typed edges with trust scores). Use after chronicle_node_search to understand a node's context instead of many chronicle_query_deps round-trips. Truncation at max_nodes drops lowest-trust periphery first and is reported explicitly."),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Node key OR name (e.g. 'OrderService')")),
		mcp.WithNumber("depth", mcp.Description("Hops from root (default 2)")),
		mcp.WithString("direction", mcp.Description("out (dependencies), in (dependents), or both (default)")),
		mcp.WithNumber("max_nodes", mcp.Description("Node cap (default 50)")),
	)
}

func subgraphHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		nodeKey, err := resolveKey(g, strParam(args, "node_key"))
		if err != nil {
			return errorResult(err), nil
		}
		result, err := g.Subgraph(nodeKey, graph.SubgraphOptions{
			Depth:     intParam(args, "depth"),
			Direction: strParam(args, "direction"),
			MaxNodes:  intParam(args, "max_nodes"),
		})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result), nil
	}
}

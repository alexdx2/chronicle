package mcpserver

import (
	"context"
	"time"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/live"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// publishLiveDiagram announces a built diagram to open dashboards. It never
// fails the tool: the diagram is the answer, the announcement a courtesy.
func publishLiveDiagram(st *store.Store, source string, d live.Diagram) map[string]any {
	ev, err := live.PublishDiagram(st, source, d)
	if err != nil {
		return map[string]any{"published": false, "error": err.Error()}
	}
	state := live.State([]live.Source{{Store: st}}, time.Now())
	return map[string]any{"published": true, "event_id": ev.ID, "dashboard_open": state.Open}
}

// mergeMarks lays over on top of base: the tool's marks parameter wins over
// view_spec.marks on the same key. Validation runs on the result.
func mergeMarks(base, over map[string]viewmodel.Mark) map[string]viewmodel.Mark {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := make(map[string]viewmodel.Mark, len(base)+len(over))
	for k, m := range base {
		out[k] = m
	}
	for k, m := range over {
		out[k] = m
	}
	return out
}

func hiddenCount(h []viewmodel.HiddenEntry) int {
	n := 0
	for _, e := range h {
		n += e.Count
	}
	return n
}

// DashboardStateTool is chronicle_dashboard_state's definition, shared with
// Chronicle Pro's federated handler.
func DashboardStateTool() mcp.Tool {
	return mcp.NewTool("chronicle_dashboard_state",
		mcp.WithDescription(`What the human is looking at in the Chronicle dashboard right now: the open view (preset, domain, spec, url), the selected node or arrow (key, name and type verified against the graph), the breadcrumb trail, and the last diagrams you built. Call it first when the user refers to the screen ("this node", "here", "the red one"). "open": false means no dashboard reported in the last 90 s — hand the human a link instead. Titles and breadcrumbs come from the browser and are not verified.`),
	)
}

func dashboardStateHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		state := live.State([]live.Source{{Store: g.Store()}}, time.Now())
		out := live.StateJSON(state, viewSpecURL)
		if !state.Open {
			if adminPortValue == AdminPortNone {
				out["admin"] = adminUnavailableMessage()
			} else {
				out["admin_url"] = adminBaseURL()
			}
		}
		return jsonResult(out), nil
	}
}

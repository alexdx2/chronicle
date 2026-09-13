package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/surface"
)

// ---------------------------------------------------------------------------
// chronicle_import_surface
// ---------------------------------------------------------------------------

func importSurfaceTool() mcp.Tool {
	return mcp.NewTool("chronicle_import_surface",
		mcp.WithDescription("Import a product's surface extract (surface.json) into the ui layer: the screens, panels and controls a user actually meets, linked to the endpoints they call and the data fields they write. The import is named by the commit the extract was made at — re-importing the same extract is a no-op, the same commit with different content is refused, and a control the file no longer mentions is tombstoned. Afterwards chronicle_impact on a data field answers \"which controls edit this?\"."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the surface.json extract")),
		mcp.WithString("domain", mcp.Description("Domain key (default: the store's only domain)")),
		mcp.WithBoolean("allow_unresolved", mcp.Description("Import what resolves and report the names that do not, instead of refusing")),
		mcp.WithBoolean("allow_diverged", mcp.Description("Import even though the extract's commit is not an ancestor of HEAD")),
		mcp.WithBoolean("force", mcp.Description("Re-import an extract already recorded as imported at this commit (after an importer upgrade, or to repair a partial import)")),
	)
}

func importSurfaceHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path := strParam(args, "path")
		if path == "" {
			return errorResult(fmt.Errorf("path is required")), nil
		}

		f, err := surface.Load(path)
		if err != nil {
			return errorResult(err), nil
		}
		res, err := surface.Import(g, f, surface.ImportOptions{
			Domain:          strParam(args, "domain"),
			RepoDir:         surfaceRepoDir(),
			AllowUnresolved: boolParam(args, "allow_unresolved"),
			AllowDiverged:   boolParam(args, "allow_diverged"),
			Force:           boolParam(args, "force"),
		})
		if err != nil {
			return refusalResult(err), nil
		}
		return jsonResult(res), nil
	}
}

// surfaceRepoDir is where the git ancestry check runs. The extract claims a
// commit of the product's repo, and that claim is checked against the same
// directory every other git question in this process uses — the CLI importer
// and this one must not disagree about which tree "HEAD" means.
func surfaceRepoDir() string { return paths.GitDir() }

// refusalKinds maps each refusal to the word an MCP caller branches on. The
// CLI says the same thing with exit code 2; a tool caller had only prose, and
// prose is not something a script can act on — so "regenerate the extract" and
// "Chronicle is broken" looked identical from here.
var refusalKinds = []struct {
	err  error
	kind string
}{
	{surface.ErrAlreadyImported, "already_imported"},
	{surface.ErrCommitChanged, "commit_changed"},
	{surface.ErrDiverged, "diverged"},
	{surface.ErrUnresolved, "unresolved"},
}

// refusalResult is errorResult plus a machine-readable kind. The payload stays
// an error result (IsError), so a caller that only reads text is unaffected.
func refusalResult(err error) *mcp.CallToolResult {
	kind := "invalid"
	for _, r := range refusalKinds {
		if errors.Is(err, r.err) {
			kind = r.kind
			break
		}
	}
	body, jerr := json.Marshal(map[string]string{"error": err.Error(), "refusal": kind})
	if jerr != nil {
		return errorResult(err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(string(body))},
		IsError: true,
	}
}

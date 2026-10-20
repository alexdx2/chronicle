package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/alexdx2/chronicle-core/graph"
)

const scanConfigDescription = `Read and change this repository's scan configuration (.depbot/scan.yaml): what is scanned, where addresses and channel names live, how message channels look in code and in documents. Use it when Chronicle misses or invents a channel or call because the project does something its built-in rules do not know (a custom broker wrapper, a topic registry file, names held in constants).

Actions:
- get: the project layer text, every rule in force (id, kind, source, reviewed) and the format reference.
- validate: parse a candidate (yaml) and list every problem at once.
- dry_run: run a candidate against the repository WITHOUT saving: the channel facts it finds, what it adds/removes against the configuration in force, and, when truth-expected.json exists, what the reference still misses or has extra.
- explain: what the configuration in force finds in one file (file).
- save: write the candidate to .depbot/scan.yaml. Refused if invalid or if it removes facts the current configuration finds, unless accept_removed is true.

Work loop: get -> write a candidate -> dry_run -> fix until it finds what you expect and nothing it should not -> save. Rules you write are marked unreviewed: they shape the scan, but the independent reference ignores them until a human sets reviewed: true.

Format (version 1): channels[{id, when{ext,contains,imports,glob}, patterns[{call: "producer.send({name}", side: produce|consume|declare, kind: topic|queue|stream|subject|exchange|subscription|bus, transport}], ignore_names}]; {name} is a string literal or an identifier resolved through same-file constants and config keys; {any} skips an expression. A definition pattern (side: define, e.g. "{id} = app.topic({name}") binds an identifier to a name across files; {ref} in another pattern ("@app.agent({ref})", "{ref}.send(") looks the identifier up. documents[{id, glob, select, when_keys, emit[{name: "{value}|{key:N}|{doc:path}|{each:doc:path}", side|side_by_key, kind, transport}]}] over json/yaml/properties. config{files[{glob,format}], env_defaults, keys[{match, as: channel|address|ignore}]}. scope{include,exclude}. clients[{call: "makeClient({addr})"}]. aliases[{host, service}]. noise[{name}|{glob, http: ignore}]. routes[{hint}] reaches the model as a convention.`

// ScanConfigBackend is the scan-config engine behind chronicle_scan_config. The
// scan package imports headless, which imports this package, so the engine is
// injected by the binary (internal/cli) rather than imported.
type ScanConfigBackend struct {
	Get     func(root string) any
	DryRun  func(root, yaml string) any
	Valid   func(root, yaml string) (bool, string)
	Explain func(root, file string) any
	Save    func(root, yaml string, acceptRemoved bool) (any, error)
}

// ScanConfig is set at startup by the binary; nil hides the tool's backend.
var ScanConfig *ScanConfigBackend

func scanConfigTool() mcp.Tool {
	return mcp.NewTool("chronicle_scan_config",
		mcp.WithDescription(scanConfigDescription),
		mcp.WithString("action", mcp.Required(), mcp.Description("get | validate | dry_run | explain | save"), mcp.Enum("get", "validate", "dry_run", "explain", "save")),
		mcp.WithString("yaml", mcp.Description("Candidate project layer (validate, dry_run, save).")),
		mcp.WithString("file", mcp.Description("Repository-relative file (explain).")),
		mcp.WithBoolean("accept_removed", mcp.Description("save: accept that facts found today disappear.")),
	)
}

func scanConfigHandler(_ *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ScanConfig == nil {
			return mcp.NewToolResultError("scan configuration is not available in this build"), nil
		}
		args := req.GetArguments()
		root := graph.ProjectRoot()
		y := strParam(args, "yaml")
		switch strParam(args, "action") {
		case "get":
			return jsonResult(ScanConfig.Get(root)), nil
		case "validate":
			ok, msg := ScanConfig.Valid(root, y)
			return jsonResult(map[string]any{"valid": ok, "errors": msg}), nil
		case "dry_run":
			return jsonResult(ScanConfig.DryRun(root, y)), nil
		case "explain":
			f := strParam(args, "file")
			if f == "" {
				return mcp.NewToolResultError("explain needs file"), nil
			}
			return jsonResult(ScanConfig.Explain(root, f)), nil
		case "save":
			acc, _ := args["accept_removed"].(bool)
			res, err := ScanConfig.Save(root, y, acc)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(res), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", strParam(args, "action"))), nil
	}
}

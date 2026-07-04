package mcp

// Client detection — Chronicle serves any MCP client, but the scan workflow
// depends on whether the client can spawn parallel subagents (Claude Code's
// Task tool). The client name arrives in the initialize handshake and selects
// which scan instructions chronicle_command returns.

import (
	"context"
	"strings"
	"sync/atomic"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// connectedClient is the clientInfo.name from the initialize handshake
// (e.g. "claude-code", "codex"). One client per stdio server process.
var connectedClient atomic.Value

func SetConnectedClient(name string) { connectedClient.Store(name) }

func ConnectedClient() string {
	v, _ := connectedClient.Load().(string)
	return v
}

// clientSupportsSubagents reports whether the connected client can spawn
// parallel subagents. Unknown clients default to the solo workflow: solo on a
// subagent-capable client is merely slower, while the orchestrator flow on a
// subagent-less client cannot work at all.
func clientSupportsSubagents() bool {
	return strings.Contains(strings.ToLower(ConnectedClient()), "claude")
}

// clientDetectionHooks captures the client name at initialize time.
func clientDetectionHooks() *server.Hooks {
	hooks := &server.Hooks{}
	hooks.AddAfterInitialize(func(ctx context.Context, id any, message *mcplib.InitializeRequest, result *mcplib.InitializeResult) {
		SetConnectedClient(message.Params.ClientInfo.Name)
	})
	return hooks
}

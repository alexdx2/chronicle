package wiring

// Codex delivery channel — ~/.codex/config.toml [mcp_servers.chronicle] block
// and SessionStart hook, both wrapped in sentinel comments; a user-managed
// [mcp_servers.chronicle] section outside the sentinels is never touched.
// Planners here are pure: byte-in, (action, newContent)-out.

import (
	"fmt"
	"strings"
)

const (
	codexSentinelStart = "# >>> chronicle mcp >>>"
	codexSentinelEnd   = "# <<< chronicle mcp <<<"

	codexHookSentinelStart = "# >>> chronicle hooks >>>"
	codexHookSentinelEnd   = "# <<< chronicle hooks <<<"
)

// codexServerBlock renders the [mcp_servers.chronicle] TOML section.
// startup_timeout_sec is raised above Codex's 10s default because Chronicle
// replays pending journal events on open; tool_timeout_sec above the 60s
// default because resolve/commit calls on large graphs can exceed it.
// default_tools_approval_mode: without it Codex cancels every MCP call in
// non-interactive runs ("user cancelled MCP tool call"). Graph mutations are
// recoverable via the journal, so auto-approval is acceptable.
func codexServerBlock(binaryPath string) string {
	return fmt.Sprintf(`[mcp_servers.chronicle]
command = %q
args = ["mcp", "serve"]
startup_timeout_sec = 30
tool_timeout_sec = 180
default_tools_approval_mode = "approve"
`, binaryPath)
}

// planCodexMCPConfig computes the create/update/skip/unchanged action for the
// chronicle server block in a Codex config.toml. The block is wrapped in
// sentinel comments so re-runs update in place; a [mcp_servers.chronicle]
// section outside the sentinels means the user manages it manually and is
// left untouched (ActionSkip).
func planCodexMCPConfig(existing []byte, fileExists bool, binaryPath string) (string, []byte) {
	block := codexSentinelStart + "\n" + codexServerBlock(binaryPath) + codexSentinelEnd + "\n"

	if !fileExists {
		return ActionCreate, []byte(block)
	}

	text := string(existing)
	if !strings.Contains(text, codexSentinelStart) && strings.Contains(text, "[mcp_servers.chronicle]") {
		return ActionSkip, nil
	}

	updated := spliceSentinelBlock(text, codexSentinelStart, codexSentinelEnd, block)
	if updated == text {
		return ActionUnchanged, nil
	}
	return ActionUpdate, []byte(updated)
}

// codexSessionHookBlock is the SessionStart reminder installed into Codex's
// config.toml: after startup/resume/clear/compact the echoed line re-primes
// the Chronicle entry point (context compaction loses AGENTS.md emphasis).
// The command is a TOML single-quoted literal — it must contain no single
// quotes and no newlines.
func codexSessionHookBlock() string {
	return codexHookSentinelStart + "\n" +
		`[[hooks.SessionStart]]
matcher = "startup|resume|clear|compact"

[[hooks.SessionStart.hooks]]
type = "command"
command = 'echo "Chronicle MCP: for architecture questions prefer the graph tools - start with chronicle_command help. Never call chronicle_scan_ tools directly outside chronicle_command instructions."'
` + codexHookSentinelEnd + "\n"
}

// planCodexSessionHook computes the create/update/unchanged action for the
// SessionStart reminder hook.
func planCodexSessionHook(existing []byte, fileExists bool) (string, []byte) {
	block := codexSessionHookBlock()

	if !fileExists {
		return ActionCreate, []byte(block)
	}

	text := string(existing)
	updated := spliceSentinelBlock(text, codexHookSentinelStart, codexHookSentinelEnd, block)
	if updated == text {
		return ActionUnchanged, nil
	}
	return ActionUpdate, []byte(updated)
}

// removeSentinelBlock strips the sentinel-delimited block from text,
// preserving surrounding content. Returns text unchanged when no sentinel
// pair is found.
func removeSentinelBlock(text, start, end string) string {
	s := strings.Index(text, start)
	e := strings.Index(text, end)
	if s < 0 || e <= s {
		return text
	}
	tail := strings.TrimPrefix(text[e+len(end):], "\n")
	updated := strings.TrimRight(text[:s], "\n")
	if updated != "" && tail != "" {
		updated += "\n\n"
	} else if updated != "" {
		updated += "\n"
	}
	updated += tail
	return updated
}

// codexPrompts are custom prompts installed to ~/.codex/prompts/ — each file
// becomes a /name slash command in Codex, mirroring Claude Code's
// /chronicle-* commands. All of them route through chronicle_command.
var codexPrompts = map[string]string{
	"chronicle-scan.md": `Run the Chronicle scan workflow.

Call chronicle_command(command="scan") and follow the returned instructions exactly.
Stop at every checkpoint and wait for my answer before continuing.`,

	"chronicle-status.md": `Show the current Chronicle graph state.

Call chronicle_command(command="status") and follow the returned instructions
(graph stats, last scan, discoveries, dashboard URL).`,

	"chronicle-impact.md": `Analyze change impact with Chronicle.

Call chronicle_command(command="impact") and follow the returned instructions for the
target I name: $ARGUMENTS. Resolve the node with chronicle_node_search first.`,

	"chronicle-update.md": `Incrementally update the Chronicle graph.

Call chronicle_command(command="update") and follow the returned instructions —
rescan only files changed since the last scan.`,

	"chronicle-help.md": `List Chronicle commands.

Call chronicle_command(command="help") and show me the available workflows.`,
}

// CodexPrompts returns the chronicle-owned Codex prompt files (name -> body).
func CodexPrompts() map[string]string {
	return codexPrompts
}

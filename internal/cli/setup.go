package cli

// Agent setup — delivers Chronicle usage instructions to coding agents that
// don't read CLAUDE.md (Codex, OpenCode, Gemini CLI, ...).
//
// Two delivery channels, both idempotent:
//   - AGENTS.md sections wrapped in <!-- chronicle:start/end --> markers,
//     upserted without touching surrounding user content
//   - ~/.codex/config.toml [mcp_servers.chronicle] block wrapped in sentinel
//     comments; a user-managed section outside the sentinels is never touched

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	agentsMarkerStart = "<!-- chronicle:start -->"
	agentsMarkerEnd   = "<!-- chronicle:end -->"

	codexSentinelStart = "# >>> chronicle mcp >>>"
	codexSentinelEnd   = "# <<< chronicle mcp <<<"
)

// Actions reported by upsert helpers.
const (
	setupCreated   = "created"
	setupUpdated   = "updated"
	setupUnchanged = "unchanged"
	setupSkipped   = "skipped (user-managed)"
)

// projectAgentsSection is the chronicle-managed section of a project's
// AGENTS.md: query decision matrix + scan gating. Client-neutral wording —
// any AGENTS.md-reading agent (Codex, OpenCode, Gemini, ...) gets the same text.
func projectAgentsSection() string {
	return `# Chronicle Knowledge Graph

This project uses Chronicle MCP — an evidence-backed knowledge graph of the codebase
(services, endpoints, models, dependencies). Prefer graph tools over grep/file search
for architecture questions.

## Entry point

For any multi-step Chronicle task, FIRST call chronicle_command(command="help") to list
available workflows, then chronicle_command(command="<name>") and follow the returned
instructions exactly.

## Query tools (safe to call anytime)

| Question | Tool call |
|----------|-----------|
| Find a service/endpoint/model by name | chronicle_node_search(query="orders") |
| What does X depend on? | chronicle_query_deps(node_key=...) |
| What depends on X? | chronicle_query_reverse_deps(node_key=...) |
| What breaks if I change X? | chronicle_impact(node_key=..., max_depth=4) |
| How does A connect to B? | chronicle_query_path(from=..., to=...) |
| Neighborhood around a node | chronicle_subgraph(node_key=...) |
| Hotspots / complexity / coupling | chronicle_insights() |
| Graph size / last scan | chronicle_query_stats(), chronicle_scan_status() |
| Visual dashboard URL | chronicle_admin_url() |

node_key format is layer:type:domain:name — resolve names with chronicle_node_search first.

## Building or updating the graph (scan)

Run scans via chronicle_command(command="scan") and follow the returned instructions
exactly, stopping at every checkpoint. Clients with parallel subagents (Claude Code)
get the parallel workflow; clients without them (Codex, Cursor, ...) automatically get
a single-agent workflow — you read the files and write the artifacts yourself.

Do NOT call scan-pipeline tools directly (chronicle_scan_*, chronicle_import_*,
chronicle_commit_scan_outbox, chronicle_resolve_extractions, chronicle_file_extracted*,
chronicle_revision_create, chronicle_finalize_incremental_scan) — they are internal
steps driven by chronicle_command instructions and calling them cold corrupts scan state.

## Gotchas

- Empty or surprisingly small query results may mean the graph is incomplete — verify
  in source code, then persist findings with chronicle_evidence_add so the graph improves.
- Trust/confidence is derived from evidence; never try to set it directly.`
}

// globalAgentsSection is the chronicle-managed section of ~/.codex/AGENTS.md —
// project-agnostic, just enough to recognize Chronicle projects and find the
// entry point.
func globalAgentsSection() string {
	return `# Chronicle MCP (knowledge graph for codebases)

Projects containing a .depbot/ directory use Chronicle — an MCP server exposing an
evidence-backed knowledge graph of the codebase (services, endpoints, models, dependencies).

- Entry point: call chronicle_command(command="help") to list workflows, then follow
  the returned instructions exactly.
- Prefer chronicle_node_search / chronicle_query_deps / chronicle_impact /
  chronicle_query_path over grep for architecture questions.
- To build or update the graph, call chronicle_command(command="scan") and follow the
  returned instructions — clients without subagents get a single-agent workflow.
- Do NOT call chronicle_scan_* or other scan-pipeline tools outside those instructions.
- The project-level AGENTS.md has the full tool matrix.`
}

// upsertMarkedSection creates or updates the chronicle-managed marker section
// in a markdown file, preserving all surrounding content. Returns whether the
// file was modified.
func upsertMarkedSection(path, content string) (bool, error) {
	section := agentsMarkerStart + "\n" + strings.TrimSpace(content) + "\n" + agentsMarkerEnd + "\n"

	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return true, writeFileMkdir(path, section)
	}
	if err != nil {
		return false, err
	}

	text := string(existing)
	start := strings.Index(text, agentsMarkerStart)
	end := strings.Index(text, agentsMarkerEnd)

	var updated string
	if start >= 0 && end > start {
		tail := text[end+len(agentsMarkerEnd):]
		tail = strings.TrimPrefix(tail, "\n")
		updated = text[:start] + section + tail
	} else {
		updated = text
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		updated += "\n" + section
	}

	if updated == text {
		return false, nil
	}
	return true, writeFileMkdir(path, updated)
}

// codexServerBlock renders the [mcp_servers.chronicle] TOML section.
// startup_timeout_sec is raised above Codex's 10s default because Chronicle
// replays pending journal events on open; tool_timeout_sec above the 60s
// default because resolve/commit calls on large graphs can exceed it.
func codexServerBlock(binaryPath string) string {
	return fmt.Sprintf(`[mcp_servers.chronicle]
command = %q
args = ["mcp", "serve"]
startup_timeout_sec = 30
tool_timeout_sec = 180
`, binaryPath)
}

// upsertCodexMCPConfig ensures the chronicle server block exists in a Codex
// config.toml. The block is wrapped in sentinel comments so re-runs update in
// place; a [mcp_servers.chronicle] section outside the sentinels means the
// user manages it manually and is left untouched.
func upsertCodexMCPConfig(configPath, binaryPath string) (string, error) {
	block := codexSentinelStart + "\n" + codexServerBlock(binaryPath) + codexSentinelEnd + "\n"

	existing, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return setupCreated, writeFileMkdir(configPath, block)
	}
	if err != nil {
		return "", err
	}

	text := string(existing)
	start := strings.Index(text, codexSentinelStart)
	end := strings.Index(text, codexSentinelEnd)

	if start >= 0 && end > start {
		tail := text[end+len(codexSentinelEnd):]
		tail = strings.TrimPrefix(tail, "\n")
		updated := text[:start] + block + tail
		if updated == text {
			return setupUnchanged, nil
		}
		return setupUpdated, writeFileMkdir(configPath, updated)
	}

	if strings.Contains(text, "[mcp_servers.chronicle]") {
		return setupSkipped, nil
	}

	updated := text
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += "\n" + block
	return setupUpdated, writeFileMkdir(configPath, updated)
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

// writeCodexPrompts installs the chronicle custom prompts into a Codex
// prompts directory. Files are fully chronicle-owned — always overwritten.
// Returns the number of files written.
func writeCodexPrompts(dir string) (int, error) {
	for name, content := range codexPrompts {
		if err := writeFileMkdir(filepath.Join(dir, name), content+"\n"); err != nil {
			return 0, err
		}
	}
	return len(codexPrompts), nil
}

// writeFileMkdir writes a file, creating parent directories as needed.
func writeFileMkdir(path, content string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// chronicleBinaryPath returns the path to write into agent configs. Prefers
// the invocation path (keeps a ~/.local/bin symlink pointing at "latest"
// working after upgrades) over the fully resolved executable.
func chronicleBinaryPath() string {
	if p, err := exec.LookPath(os.Args[0]); err == nil {
		if abs, absErr := filepath.Abs(p); absErr == nil {
			return abs
		}
		return p
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "chronicle"
}

func newSetupCmd() *cobra.Command {
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Configure coding agents to use Chronicle MCP",
	}
	setup.AddCommand(newSetupCodexCmd())
	return setup
}

func newSetupCodexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "codex",
		Short: "Register Chronicle in ~/.codex/config.toml and write AGENTS.md guidance",
		Run: func(cmd *cobra.Command, args []string) {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(os.Stderr, "cannot resolve home directory: %v\n", err)
				os.Exit(1)
			}

			configPath := filepath.Join(home, ".codex", "config.toml")
			action, err := upsertCodexMCPConfig(configPath, chronicleBinaryPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error updating %s: %v\n", configPath, err)
				os.Exit(1)
			}
			fmt.Printf("%-40s %s\n", configPath, action)

			globalAgents := filepath.Join(home, ".codex", "AGENTS.md")
			changed, err := upsertMarkedSection(globalAgents, globalAgentsSection())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error updating %s: %v\n", globalAgents, err)
				os.Exit(1)
			}
			fmt.Printf("%-40s %s\n", globalAgents, changedWord(changed))

			promptsDir := filepath.Join(home, ".codex", "prompts")
			n, err := writeCodexPrompts(promptsDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error writing prompts to %s: %v\n", promptsDir, err)
				os.Exit(1)
			}
			fmt.Printf("%-40s %d prompts (/chronicle-scan, /chronicle-impact, ...)\n", promptsDir, n)

			// Project-level AGENTS.md only when run inside a Chronicle project;
			// other projects get it automatically on first chronicle command.
			if _, statErr := os.Stat(depbotDir); statErr == nil {
				changed, err = upsertMarkedSection("AGENTS.md", projectAgentsSection())
				if err != nil {
					fmt.Fprintf(os.Stderr, "error updating AGENTS.md: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("%-40s %s\n", "AGENTS.md", changedWord(changed))
			}

			fmt.Println("\nDone. Restart Codex to pick up the MCP server.")
		},
	}
}

func changedWord(changed bool) string {
	if changed {
		return setupUpdated
	}
	return setupUnchanged
}

package wiring

// Agent-facing content and marker-section planning — the AGENTS.md delivery
// channel for coding agents that don't read CLAUDE.md (Codex, OpenCode,
// Gemini CLI, ...). Planners here are pure: byte-in, (action, newContent)-out.
// No filesystem access — all writes go through wiring.ApplyPlan.

import (
	"strings"
)

const (
	AgentsMarkerStart = "<!-- chronicle:start -->"
	AgentsMarkerEnd   = "<!-- chronicle:end -->"
)

// ProjectAgentsSection is the chronicle-managed section of a project's
// AGENTS.md: query decision matrix + scan gating. Client-neutral wording —
// any AGENTS.md-reading agent (Codex, OpenCode, Gemini, ...) gets the same text.
func ProjectAgentsSection() string {
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
| Find a service/endpoint/model by name | chronicle_node_search(q="orders") |
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

// GlobalAgentsSection is the chronicle-managed section of ~/.codex/AGENTS.md —
// project-agnostic, just enough to recognize Chronicle projects and find the
// entry point.
func GlobalAgentsSection() string {
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

// PlanMarkedSection computes the create/update/unchanged action for the
// chronicle-managed marker section in a markdown file, preserving all
// surrounding content. Pure: no filesystem access.
func PlanMarkedSection(existing []byte, fileExists bool, content string) (string, []byte) {
	section := AgentsMarkerStart + "\n" + strings.TrimSpace(content) + "\n" + AgentsMarkerEnd + "\n"
	if !fileExists {
		return ActionCreate, []byte(section)
	}
	text := string(existing)
	start := strings.Index(text, AgentsMarkerStart)
	end := strings.Index(text, AgentsMarkerEnd)
	var updated string
	if start >= 0 && end > start {
		tail := strings.TrimPrefix(text[end+len(AgentsMarkerEnd):], "\n")
		updated = text[:start] + section + tail
	} else {
		updated = text
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		updated += "\n" + section
	}
	if updated == text {
		return ActionUnchanged, nil
	}
	return ActionUpdate, []byte(updated)
}

// RemoveMarkedSection strips the chronicle-managed marker section from text,
// reporting whether a marker was found.
func RemoveMarkedSection(existing []byte) (string, []byte, bool) {
	text := string(existing)
	start := strings.Index(text, AgentsMarkerStart)
	end := strings.Index(text, AgentsMarkerEnd)
	if start < 0 || end <= start {
		return ActionUnchanged, nil, false
	}
	tail := strings.TrimPrefix(text[end+len(AgentsMarkerEnd):], "\n")
	updated := strings.TrimRight(text[:start], "\n")
	if updated != "" && tail != "" {
		updated += "\n\n"
	} else if updated != "" {
		updated += "\n"
	}
	updated += tail
	return ActionUpdate, []byte(updated), true
}

// spliceSentinelBlock replaces the sentinel-delimited block in text, or
// appends it when absent, preserving all surrounding content.
func spliceSentinelBlock(text, sentinelStart, sentinelEnd, block string) string {
	start := strings.Index(text, sentinelStart)
	end := strings.Index(text, sentinelEnd)
	if start >= 0 && end > start {
		tail := text[end+len(sentinelEnd):]
		tail = strings.TrimPrefix(tail, "\n")
		return text[:start] + block + tail
	}
	updated := text
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	if updated != "" {
		updated += "\n"
	}
	return updated + block
}

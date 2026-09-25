package mcpserver

// Command definitions for user-facing slash commands.
// These are returned by chronicle_extraction_guide and documented in CLAUDE.md.

import "github.com/alexdx2/chronicle-core/graph/prompts"

func init() {
	// Inject dynamically-built checkpoint flow + MCP preflight into scan command
	CommandInstructions["scan"] = buildScanCommand()
	soloScanInstructions = buildSoloScanCommand()
	// The querying guide is authored as an embedded pack so it stays close to
	// the tools it teaches and can be reused by the dashboard / hooks.
	CommandInstructions["query"] = prompts.Querying
}

// soloScanInstructions is the scan command variant for clients without
// subagents (Codex, Cursor, ...). commandHandler serves it when the connected
// client can't spawn parallel agents; CommandInstructions["scan"] stays the
// orchestrator flow.
var soloScanInstructions string

func soloScanCommand() string { return soloScanInstructions }

const soloScanCommandTemplate = `Scan this project using the artifact-pool workflow in SOLO mode — you are the only agent; no subagents are involved.

__MCP_PREFLIGHT__

CRITICAL RULES (artifact-pool, solo):
  ❌ NEVER call chronicle_import_all during a scan
  ❌ NEVER start a new wave before commit_scan_outbox completes the current wave
  ❌ NEVER skip checkpoints — STOP and wait at each one
  ❌ NEVER save the manifest without user approval
  ❌ NEVER guess the domain — use domain_key from checkout batches
  ✅ YOU are the sole extractor: scan_pool_status → scan_checkout_batch → read files → write outbox JSON artifacts → commit_scan_outbox → repeat
  ✅ Artifacts go to .depbot/scan-outbox/{revision_id}/ only
  ✅ Use chronicle_resolve_extractions(allow_degraded=true) only when pool shows stuck/failed obligations with extractions present
  ✅ When votes_needed>1, artifacts must include vote_group and vote_index from checkout_batch

CRITICAL — CONTAINS edges (structural backbone):
  ✅ Every module MUST have CONTAINS edges to its controllers and providers
  ✅ CONTAINS edges link: repository→module, module→controller, module→provider
  ✅ These come from "provides" facts emitted by @Module files with from_type="module"
  ❌ A graph with no CONTAINS edges is broken — emit "provides" for every controller/provider in every @Module

__STAGES__`

var UserCommands = map[string]string{
	"scan":        "Full project scan — discover structure, extract all layers, import graph, define domain language",
	"data":        "Analyze data models only — Prisma/TypeORM schemas, relations, enums",
	"language":    "Define or update domain language glossary, check for violations",
	"impact":      "Analyze impact of a specific change — 'what breaks if I change X?'",
	"query":       "How to answer a codebase question via the graph — resolve names with node_search, then deps/path/impact/subgraph",
	"deps":        "Show dependencies of a node — 'what does X depend on?'",
	"path":        "Find path between two nodes — 'how does A connect to B?'",
	"flows":       "Analyze business use cases — what the system does, end-to-end processes",
	"services":    "Analyze service architecture — cross-service deps, API surface",
	"status":      "Show current graph state — nodes, edges, layers, last scan",
	"version":     "Show MCP server identity — codename, fingerprint, capabilities (call before scan)",
	"update":      "Incremental update — rescan only changed files since last scan via git diff",
	"review":      "MR/PR review report — changed entities, blast radius (field-level where known), external services affected",
	"verify":      "Verify low-confidence edges — find code evidence to confirm or reject inferred relationships",
	"help":        "Show available Chronicle commands",
	"diagram":     "Show a live diagram to explain architecture",
	"topology":    "Show federation topology — how domains connect via cross-repo edges",
	"connections": "Show cross-repo connections — external node inventory with resolution status",
	"setup":       "Configure which directories to scan — interactive manifest builder",
	"surface":     "Import a product's surface extract (surface.json) — screens, panels and controls into the ui layer",
}

var CommandInstructions = map[string]string{
	"scan": `Scan this project using the chronicle_scan_next_file workflow.

__MCP_PREFLIGHT__

CRITICAL RULES (artifact-pool — default):
  ❌ NEVER call chronicle_import_all during a scan
  ❌ NEVER let subagents call Chronicle MCP (no file_extracted from subagents)
  ❌ NEVER start a new wave before commit_scan_outbox completes the current wave
  ❌ NEVER skip checkpoints — STOP and wait at each one
  ❌ NEVER save the manifest without user approval
  ❌ NEVER guess the domain — use domain_key from checkout batches
  ✅ Orchestrator workflow: scan_pool_status → scan_checkout_batch → spawn extractors → commit_scan_outbox → repeat
  ✅ Subagents write JSON artifacts to .depbot/scan-outbox/{revision_id}/ only
  ✅ YOU are the orchestrator. Subagents read files and write artifacts — you commit to MCP.
  ✅ Use chronicle_resolve_extractions(allow_degraded=true) only when pool shows stuck/failed obligations with extractions present
  ✅ Scan profiles: 1 or 3 touches × cheap (fast) or expensive (strong) model; custom N if user asks
  ✅ When votes_needed>1, outbox artifacts must include vote_group and vote_index from checkout_batch

CRITICAL — CONTAINS edges (structural backbone):
  ✅ Every module MUST have CONTAINS edges to its controllers and providers
  ✅ CONTAINS edges link: repository→module, module→controller, module→provider
  ✅ These come from "provides" facts emitted by @Module files with from_type="module"
  ❌ A graph with no CONTAINS edges is broken — subagents must emit "provides" for every controller/provider in every @Module

__STAGES__`,

	"data": `Analyze data models:
1. Call chronicle_schema({ from_layer: 'code', to_layer: 'data', include: 'edges' }) to see valid data edge types
2. Find schema files: Glob for prisma/schema.prisma, *.entity.ts
3. Read each schema file
4. Extract: models → data:model nodes, enums → data:enum nodes
5. Extract relations: @relation → REFERENCES_MODEL edges
6. Import immediately after each file
7. Find services that use these models → USES_MODEL edges
8. Report what you found`,

	"language": `Domain language analysis:
1. Call chronicle_get_glossary to see existing terms
2. Analyze the codebase for key domain concepts
3. For each concept: call chronicle_define_term with term, description, context, aliases, anti_patterns
4. Call chronicle_check_language to find violations
5. Report violations and suggestions`,

	"impact": `Impact analysis:
1. Find the node_key: call chronicle_node_list with filters
2. Call chronicle_impact with the node_key, depth 4
3. Report the blast radius — impacted services, endpoints (from affected_surface), models
4. Trust results with impact_score >= 80. For scores < 50, verify by reading source.
5. If the result is empty or surprisingly small, search source code — graph may be incomplete.
6. Only verify against code when the answer matters for a real decision (refactor, deletion, etc.)
7. If you verified something by reading code, persist the finding:
   - Confirmed: chronicle_evidence_add(target_kind="edge", edge_key, source_kind="file", file_path, line_start, line_end, confidence=0.95, polarity="positive")
   - Disproved: chronicle_evidence_add(..., polarity="negative", confidence=0.90)
   This raises/lowers confidence for future queries.`,

	"review": `MR/PR review report:
1. Call chronicle_review_report (defaults: base = merge-base with main/master, head = working tree).
   For a specific MR branch pass base/head refs explicitly.
2. Present the returned markdown verbatim — it is the report.
3. Offer drill-down: chronicle_impact on any listed entity, depth 4.
4. If "Unmapped changes" is non-empty, say those files have NO graph coverage —
   their impact is unknown, not zero. Suggest a scan/update to close the gap.
5. precision=model rows are potential impact (reached via the parent model);
   precision=field rows are exact (code names that field). Never present
   model-precision rows as confirmed breakage.`,

	"deps": `Dependency analysis:
1. Find the node_key
2. Call chronicle_query_deps for forward deps
3. Call chronicle_query_reverse_deps for reverse deps
4. Trust results. If empty or surprisingly few, grep source code — graph may be incomplete.
5. Stale nodes (status="stale") may have changed since last scan — verify if acting on them.
6. If you find a dependency by reading code that the graph missed, add evidence:
   chronicle_evidence_add(target_kind="edge", edge_key="from->to:EDGE_TYPE", source_kind="file", file_path, line_start, line_end, confidence=0.95, polarity="positive")
7. Explain both directions`,

	"path": `Path finding:
1. Find both node_keys
2. Call chronicle_query_path with mode='directed'
3. If no path found, try mode='connected'
4. Trust paths with path_score > 0.3. For lower scores, verify the weakest edge if acting on results.
5. If no path in either mode, search source code — graph may not cover all connections.
6. If you discover a connection by reading code, add evidence to strengthen it for next time:
   chronicle_evidence_add(target_kind="edge", edge_key, source_kind="file", file_path, line_start, line_end, confidence=0.95)
7. Explain the path with edge types`,

	"flows": `Business flow / use case analysis:
1. Call chronicle_schema({ from_layer: 'flow', include: 'edges' }) to see valid flow edge types
2. Call chronicle_extraction_guide for flow extraction rules
3. Read the main service files to identify key business processes
3. For each use case (e.g. PlaceOrder, UserSignup):
   a. Create flow:use_case node
   b. Find which endpoint triggers it → TRIGGERS_FLOW edge
   c. Find which services it calls → REQUIRES edges
   d. Find which models it reads/writes → REQUIRES edges
   e. Find what events/outcomes it produces → PRODUCES_OUTCOME edges
4. Import flows via chronicle_import_all
5. Report the discovered use cases to the user`,

	"services": `Service architecture analysis:
1. Call chronicle_node_list with layer='service'
2. For each service, call chronicle_query_deps to see what it depends on
3. Call chronicle_edge_list with type='CALLS_SERVICE' for cross-service deps
4. Call chronicle_edge_list with type='CALLS_ENDPOINT' for specific endpoint deps
5. Summarize the service dependency map`,

	"version": `MCP server identity (call before every scan):
1. Call chronicle_mcp_identity (preferred) or chronicle_command(command=version)
2. Show the user the "banner" line verbatim
3. Verify release_codename and fingerprint match the expected release in how_to_verify
4. Verify all scan_contract values are true
5. If stale → STOP. Tell user to rebuild chronicle MCP and restart their MCP client`,

	"status": `Graph status:
1. Call chronicle_scan_status
2. Call chronicle_query_stats
3. Call chronicle_get_discoveries
4. Call chronicle_get_glossary
5. Call chronicle_admin_url
6. Summarize: nodes, edges, layers, last scan, discoveries, domain terms, dashboard URL`,

	"diagram": `Diagram for the user — one question, one diagram, from the graph.

## Rule
Never hand-author nodes or edges for things the graph knows. chronicle_diagram_build
builds the diagram from the graph through the view engine: it decides what is
visible at the chosen altitude (salience), folds graph edges into arrows ("HTTP ×3"),
frames topics inside their broker, dims what is outside the scope, and lists what it
hid and why. You choose the QUESTION (preset) and the SUBJECT (target).

## How
1. Pick the preset from the user's question (table below).
2. Resolve the subject when one is needed: chronicle_node_search / chronicle_node_list
   for a node key, or a service name for c3.
3. Call chronicle_diagram_build(title, preset, target?, domain?).
4. Answer with the "mermaid" block (the diagram itself, paste it) and the "url"
   ("open {url} for the live version"). Use "text" when the reader wants evidence per
   element, or when you need to reason about the diagram. Mention "hidden" if it is
   non-empty ("2 DTOs and 1 cache are hidden: …").
5. Mark what matters in the answer: marks={"<key>": {"kind": "breaks|changed|focus|note",
   "note": "…"}} colors and captions the elements you name (impact: the subject "changed",
   the dependents "breaks"). Marks never add elements — "unplaced" lists keys this view does
   not draw, "missing" keys the graph lacks.
6. The diagram also appears in the human's open dashboard as a card ("live.dashboard_open").
   When the user says "this", "here" or points at something on screen, call
   chronicle_dashboard_state to see the open view and the selected node or arrow.

## Presets — the questions
| User asks | preset | target |
|---|---|---|
| "architecture overview", "context", "who do we talk to" | c1 | — |
| "how is the system organized", "services", "how do they communicate" | c2 | — |
| "what's inside X", "how is X built", "components of X" | c3 | service key or name |
| "what does X depend on" | deps | node key |
| "what breaks if I change X", "blast radius", "who uses X" | impact | node key |
| "how does A reach B", "trace A to B" | path | "A,B" (two node keys) |
| "show the flow / use case X" | flow | flow node key |
| "data model", "entities" | data | — |
| "API surface", "endpoints" | api | — |
| "all flows / use cases" | flows | — |

Examples:
  chronicle_diagram_build(title: "Containers", preset: "c2")
  chronicle_diagram_build(title: "Inside arena-api", preset: "c3", target: "arena-api")
  chronicle_diagram_build(title: "Impact of Score", preset: "impact", target: "data:model:tom-and-jerry:score", marks: "{\"data:model:tom-and-jerry:score\":{\"kind\":\"changed\"}}")
  chronicle_diagram_build(title: "Attack → scoreboard", preset: "path", target: "contract:endpoint:tom-and-jerry:post:/arena/attack,service:service:tom-and-jerry:scoreboardapi")

## Custom selection
A hand-picked set of real nodes (e.g. "show me these five things together"):
  chronicle_diagram_build(title: "...", node_keys: '["key1","key2"]')
Edges between them are discovered from the graph; you still get mermaid/text/url.

## Reading the output
- Solid arrow: hard, evidence-backed edge. Dashed: inferred, no evidence, or confidence < 70%.
  Dotted: synthesized (declared in the manifest, or a bypassed node). Say so when it matters.
- A frame marked "inferred" (e.g. topics under the only declared broker) is a reasonable
  placement, not evidence.
- "hidden" lists what the altitude hides (DTOs, helpers, isolated infra) with the reason.
  chronicle_salience_explain(node_key, level) explains one node in depth.

## Things the graph does not know
Only for planned or external systems that have no node (a future service, a vendor
box), pass nodes/edges explicitly — and say in the answer that those are not from the
graph. Never mix hand-authored nodes with real node_keys for the same entity.`,

	"topology": `Domain topology — how this graph's domains connect through external references:

1. Call chronicle_query_stats("") for node/edge counts per domain
2. Call chronicle_node_list with status="external" to find every external node reference
3. Group externals by (source domain → target domain); target domain is the node_key's
   3rd segment (e.g. "tomandjerry" in "contract:topic:tomandjerry:battle-results")
4. Report the text summary: N domains, N cross-domain references, N unresolved, one line
   per domain pair with the edge types involved
5. For a picture, do NOT invent domain boxes: build the connections diagram from the real
   nodes (see /chronicle-connections). Domain-level topology diagrams across repos are a
   Chronicle Pro federation view.`,

	"connections": `Cross-boundary connections — every edge that leaves this graph's domains:

1. Call chronicle_node_list with status="external" to find all external nodes
2. For each external node:
   a. Note: node_key, name, layer, node_type
   b. Call chronicle_edge_list to find edges pointing TO this external node — note the edge_type and source node
   c. Check if node_key exists as "active" in another domain — resolved vs unresolved
3. Group results two ways:
   a. By edge type: CONSUMES_TOPIC (N), CALLS_SERVICE (N), CALLS_ENDPOINT (N), etc.
   b. By domain pair: analytics→tomandjerry (N links), analytics→orders (N links)
4. Diagram: chronicle_diagram_build(title: "Cross-boundary connections", node_keys: [source nodes + external target nodes])
   — real nodes only; the engine discovers the edges. Unresolved externals are real
   nodes too (status external), so they appear; say in the answer which ones are unresolved.
5. Answer with the mermaid block and the URL, then the text summary:
   - N external nodes total, N resolved, N unresolved
   - Breakdown by edge type
   - Breakdown by domain pair
   - List unresolved nodes with their source domain and edge type`,

	"setup": `Project setup — configure scan scope:
1. Call chronicle_file_groups — shows all git-tracked files grouped by directory with counts
2. Present the groups to the user, organized by role:
   - Backend (services, controllers, resolvers, gateways, modules)
   - Data (schemas, migrations, prisma)
   - Config (docker-compose, Dockerfiles, configs)
   - Frontend presentation (components, hooks, pages, screens) — exclude by default
   - Tests, generated, dist — exclude
3. Ask user which directories to EXCLUDE
4. Write scan.include and scan.exclude patterns to each domain in the manifest's domains: array
5. Call chronicle_discover_files to validate the final count
6. Show: "X files will be scanned. Ready to scan?"

User makes DIRECTORY-level decisions, not file-level.`,

	"help": `Show all available Chronicle commands:
- /chronicle-scan — Full project scan
- /chronicle-data — Analyze data models
- /chronicle-language — Domain language glossary
- /chronicle-impact — Change impact analysis
- /chronicle-deps — Dependency analysis
- /chronicle-path — Find paths between nodes
- /chronicle-flows — Business use case analysis
- /chronicle-services — Service architecture
- /chronicle-update — Incremental update (changed files only)
- /chronicle-verify — Verify low-confidence edges with code evidence
- /chronicle-diagram — Architecture diagram from the graph (mermaid + text + live URL)
- /chronicle-topology — Domain topology through external references
- /chronicle-connections — Cross-boundary edge inventory
- /chronicle-surface — Import a product's surface extract (screens/panels/controls) into the ui layer
- /chronicle-status — Current graph state
- /chronicle-version — MCP identity (codename + fingerprint — call before scan)
- /chronicle-help — This help

Context management tools (called directly, not via commands):
- chronicle_resolve_context — Resolve the main context for a domain
- chronicle_context_list — List all contexts for a domain
- chronicle_context_create — Create a new branch context
- chronicle_context_archive — Archive a context
- chronicle_changelog_query — Query changelog for a context`,

	"update": `Incremental graph update — rescan only files changed since the last scan:

1. Call chronicle_resolve_context(domain) to get current context
   - If no context and on a non-main branch, call chronicle_context_create to create one
   - If no context and no previous scan, tell user to run "chronicle scan" first
2. Get last revision in current context → before_sha (from context's head_commit_sha)
3. Get current HEAD: run git rev-parse HEAD → this is after_sha
4. If before_sha == after_sha, tell the user "Graph is up to date — no changes since last scan" and stop
5. Run git diff --name-only {before_sha} {after_sha} to get the list of changed files
   - If no files changed, tell the user and stop
6. Create an incremental revision:
   chronicle_revision_create(domain, context_id, mode="incremental", before_sha, after_sha, trigger_kind="manual")
7. Invalidate changed files:
   chronicle_invalidate_changed(domain, revision_id, changed_files as JSON array)
   → closes old evidence validity, inserts stale versions, writes changelog
   → returns stale_evidence count and files_to_rescan
8. Read ONLY the files listed in files_to_rescan (skip deleted files)
9. For each file: extract nodes/edges following chronicle_schema types and chronicle_extraction_guide methodology
   → chronicle_import_all for re-extracted facts (positive evidence, max 10-15 nodes per call)
10. For relationships confirmed removed (e.g. deleted imports, removed dependencies):
    create negative evidence via chronicle_evidence_add(polarity="negative")
11. Finalize: chronicle_finalize_incremental_scan(domain, revision_id)
12. Snapshot: chronicle_snapshot_create(domain, revision_id)
13. Report summary: files scanned, nodes/edges updated, evidence revalidated vs still stale`,

	"verify": `Verify low-confidence edges — find concrete code evidence for inferred relationships:

IMPORTANT: Do NOT increase confidence directly. Your job is to collect proof. The system recalculates confidence deterministically from evidence.

1. Call chronicle_edge_list to find edges with low confidence (trust_score < 0.75)
   - Focus on edges with derivation_kind = 'inferred' first
2. For each low-confidence edge (A → B):
   a. Identify the source files for both nodes
   b. Read those files and search for concrete evidence that A depends on B:
      - Import statements (A imports B)
      - Constructor injection (A receives B as parameter)
      - Function calls (A calls methods on B)
      - DB queries (A queries B's model/table)
      - Type references (A uses B as return type or parameter type)
      - Test assertions (tests verify A-B interaction)
      - HTTP/message calls (A calls B's endpoint or publishes to B's topic)
   c. For each piece of evidence found, call chronicle_evidence_add:
      {
        "target_kind": "edge",
        "edge_key": "<the edge key>",
        "source_kind": "file",
        "file_path": "<path where evidence found>",
        "line_start": <line number>,
        "line_end": <line number>,
        "extractor_id": "<your agent id: claude-code, codex, ...>",
        "extractor_version": "1.0",
        "confidence": 0.95,
        "polarity": "positive"
      }
   d. If you confirm the relationship does NOT exist (e.g. no imports, no calls, no references):
      Add negative evidence with polarity="negative" and confidence=0.90
3. After adding evidence, confidence is automatically recalculated by the system
4. Report: which edges were verified, evidence found, new confidence scores

Evidence quality tiers (the system caps confidence based on best evidence available):
- Runtime/DB evidence (prisma, runtime): unlocks up to 92%
- Code evidence (file, openapi, graphql, etc.): unlocks up to 85%
- LLM inference only: capped at 65%
- Manual confirmation (user_feedback): unlocks up to 95%`,

	"surface": `Import this product's surface extract into the ui layer.

The extract (surface.json) is produced by the PRODUCT, not by a scan: only the
product knows which screen a panel belongs to and which mutation a toggle
writes through. Chronicle resolves those names against the graph a scan already
built and records the result as ui nodes anchored to the extract's commit.

1. Generate the extract in the product repo (its own tooling), then either:
   - run 'chronicle surface import <file>' in the product directory, or
   - call chronicle_import_surface(path="docs/surface/<product>.surface.json")
2. Read the result: nodes/edges written, "deleted" (controls the file no longer
   mentions — tombstoned), "unresolved" (names the graph could not confirm).
   Re-running the same extract is a no-op (already_imported: true).
3. Then ask the graph what the surface means: chronicle_impact on a data field
   now answers "which controls edit this?", and chronicle_query_reverse_deps on
   a mutation answers "which controls write through it?".

Refusals worth understanding:
- "same commit, different content" — regenerate the extract at a new commit;
  one commit must not mean two different surfaces.
- "not an ancestor of HEAD" — the extract was made on a branch nobody merged;
  pass allow_diverged only if that is genuinely what you want.
- unresolved names fail the import by default; pass allow_unresolved to import
  what does resolve and get the rest listed.`,
}

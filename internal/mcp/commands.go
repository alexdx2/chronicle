package mcp

// Command definitions for user-facing slash commands.
// These are returned by chronicle_extraction_guide and documented in CLAUDE.md.

func init() {
	// Inject dynamically-built checkpoint flow into scan command
	CommandInstructions["scan"] = buildScanCommand()
}

var UserCommands = map[string]string{
	"scan":     "Full project scan — discover structure, extract all layers, import graph, define domain language",
	"data":     "Analyze data models only — Prisma/TypeORM schemas, relations, enums",
	"language": "Define or update domain language glossary, check for violations",
	"impact":   "Analyze impact of a specific change — 'what breaks if I change X?'",
	"deps":     "Show dependencies of a node — 'what does X depend on?'",
	"path":     "Find path between two nodes — 'how does A connect to B?'",
	"flows":    "Analyze business use cases — what the system does, end-to-end processes",
	"services": "Analyze service architecture — cross-service deps, API surface",
	"status":   "Show current graph state — nodes, edges, layers, last scan",
	"update":  "Incremental update — rescan only changed files since last scan via git diff",
	"verify":  "Verify low-confidence edges — find code evidence to confirm or reject inferred relationships",
	"help":    "Show available Chronicle commands",
	"diagram": "Show a live diagram to explain architecture",
	"setup":   "Configure which directories to scan — interactive manifest builder",
}

var CommandInstructions = map[string]string{
	"scan": `Scan this project using the chronicle_scan_next_file workflow.

CRITICAL RULES:
  ❌ NEVER call chronicle_import_all during a scan — use chronicle_file_extracted
  ❌ NEVER read files and extract facts yourself — subagents do extraction
  ❌ NEVER skip checkpoints — STOP and wait at each one
  ❌ NEVER save the manifest without user approval
  ❌ NEVER guess the domain — the manifest defines multiple domains with scan.include/exclude patterns
  ✅ The ONLY extraction workflow is: scan_next_file → read file → file_extracted → repeat
  ✅ YOU are the orchestrator. Subagents do the extraction work.
  ✅ Each file in the scan batch includes a domain_key field — use it when extracting facts
  ✅ Pass the domain_key from each file's batch entry to tool calls — do NOT hardcode a single domain

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

	"status": `Graph status:
1. Call chronicle_scan_status
2. Call chronicle_query_stats
3. Call chronicle_get_discoveries
4. Call chronicle_get_glossary
5. Call chronicle_admin_url
6. Summarize: nodes, edges, layers, last scan, discoveries, domain terms, dashboard URL`,

	"diagram": `Live diagram for the user. Use the Diagram Type Catalog below to construct diagrams.

## How to Create a Diagram

1. Identify the diagram type from the user's question (see catalog below)
2. Query the graph to gather data for the diagram type
3. Call chronicle_diagram_build with the appropriate payload
4. Share the returned URL with user: "Open {url} to see the diagram"

## When to use node_keys vs nodes

- **node_keys**: resolve REAL entities from the graph DB. Use for Domain Detail, Flow, Impact — when you need actual services/controllers/providers.
- **nodes**: SYNTHETIC diagram-only entities. Use for domains, infrastructure, external actors — things that represent groups or systems, not individual code entities.
- **NEVER use node_keys for Overview** — Overview shows domains as collapsed blocks, not individual services.
- **Domain Detail mixes both**: node_keys for services inside the target domain + nodes for neighboring domains.

## Diagram Type Catalog

### Type 1: Overview (Context Diagram)
Trigger: "show architecture", "overview", "how is the system organized", "high-level diagram"
Purpose: Domains as blocks, infrastructure as bridges, external systems as actors. NO individual services — only domain blocks.

CRITICAL: Use ONLY "nodes" (synthetic). NEVER use "node_keys". Each domain from the manifest becomes ONE node with kind "domain".

Construction:
1. Call chronicle_domain_list to get all domains
2. Each domain → one node with kind "domain". Infrastructure from manifest → kind "infrastructure". External systems → kind "external".
3. Edges: aggregated cross-domain relationships with protocol labels (HTTP, async, data)
4. Call chronicle_diagram_build with nodes + edges ONLY — no node_keys

Example:
  chronicle_diagram_build(
    title: "Overview: {project} Architecture",
    nodes: '[{"key":"domain:core-api","label":"Core API","kind":"domain"},{"key":"infra:kafka","label":"Kafka","kind":"infrastructure"},{"key":"ext:stripe","label":"Stripe","kind":"external"}]',
    edges: '[{"from":"domain:core-api","to":"infra:kafka","label":"publish order.created","kind":"async"}]'
  )

### Type 2: Domain Detail
Trigger: "show X in detail", "what's inside X", "expand X"
Purpose: One domain expanded with services inside. Other domains as collapsed blocks.

Construction:
1. Call chronicle_node_list(domain="{target}") to get services in the target domain
2. Use node_keys for real services in the target domain
3. Add synthetic nodes (kind "domain") for neighboring domains
4. Add infrastructure nodes if domain uses async
5. Set groups: {"field":"domain"} for visual grouping

Example:
  chronicle_diagram_build(
    title: "Domain: Core API",
    node_keys: '["service:code:core:OrdersService","service:code:core:PaymentsService"]',
    nodes: '[{"key":"domain:crm","label":"CRM","kind":"domain"},{"key":"infra:kafka","label":"Kafka","kind":"infrastructure"}]',
    edges: '[{"from":"service:code:core:OrdersService","to":"infra:kafka","label":"publish order.created","kind":"async"},{"from":"infra:kafka","to":"domain:crm","label":"consume","kind":"async"}]',
    groups: '{"field":"domain"}'
  )

### Type 3: Request Flow
Trigger: "how does X flow to Y", "trace a request", "explain how X calls Y"
Purpose: Trace a request path across domains. Domains as swimlanes.

Construction:
1. Identify start and end from user's question
2. Call chronicle_query_path(from, to) to get the path
3. Use node_keys for all nodes on the path
4. Set groups: {"field":"domain"} for domain swimlanes
5. Build steps array: each step = one hop, active nodes highlighted (#f59e0b), others dimmed

Example:
  chronicle_diagram_build(
    title: "Flow: Create Order",
    node_keys: '["service:code:mobile:Gateway","service:code:core:OrdersService"]',
    nodes: '[{"key":"infra:kafka","label":"Kafka","kind":"infrastructure"}]',
    groups: '{"field":"domain"}',
    steps: '[{"title":"Entry","description":"Mobile sends POST /orders","highlights":{"service:code:mobile:Gateway":"#f59e0b"}},{"title":"Processing","description":"OrdersService validates","highlights":{"service:code:core:OrdersService":"#f59e0b"}}]'
  )

### Type 4: Dependency Map (Impact)
Trigger: "what depends on X", "what breaks if I change X", "blast radius"
Purpose: Show what a change affects. Grouped by domain with heatmap.

Construction:
1. Call chronicle_impact(node_key) or chronicle_query_deps + chronicle_query_reverse_deps
2. Collect affected nodes up to 2 hops / 15 nodes
3. Use node_keys for all affected nodes
4. Set groups: {"field":"domain"}
5. Use annotations for heatmap: changed (#ef4444), direct (#f59e0b), indirect (#eab308)

Example:
  chronicle_diagram_build(
    title: "Impact: Changing OrdersService",
    node_keys: '["service:code:core:OrdersService","service:code:core:PaymentsService","service:code:crm:CrmConsumer"]',
    groups: '{"field":"domain"}',
    annotations: '{"service:code:core:OrdersService":{"highlight":"#ef4444","note":"CHANGED"},"service:code:crm:CrmConsumer":{"highlight":"#f59e0b","note":"Consumes order.created events"}}'
  )

## Cross-cutting Rules

- Node kinds: domain, infrastructure, external, service, endpoint, model, topic, queue, database
- Edge kinds: http, async, data, structural
- NEVER exceed 15 real nodes (node_keys). Synthetic nodes (domain/infra/external) don't count.
- Infrastructure is ALWAYS explicit — show Kafka/Redis/RabbitMQ as nodes between publisher and consumer. Never a direct edge from publisher to consumer.
- Edge labels: always show protocol type (HTTP, publish, consume, uses).
- Kafka topics: include topic names as edge labels.
- Annotate focal nodes explaining WHY they matter.
- Use groups: {"field":"domain"} for types 2-4 to show domain boundaries.`,

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
- /chronicle-diagram — Live architecture diagram
- /chronicle-status — Current graph state
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
        "extractor_id": "claude-code",
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
}

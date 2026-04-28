package mcp

// Command definitions for user-facing slash commands.
// These are returned by chronicle_extraction_guide and documented in CLAUDE.md.

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
	"help":    "Show available Chronicle commands",
	"diagram": "Show a live diagram to explain architecture",
}

var CommandInstructions = map[string]string{
	"scan": `Full project scan:
1. Call chronicle_get_discoveries to learn from previous scans
2. Call chronicle_extraction_guide for methodology
3. Auto-discover project, save manifest
4. Create revision
5. Scan in passes: data models → code structure → contracts/endpoints → cross-service edges
6. For each file: read → extract → chronicle_import_all immediately (max 10-15 nodes per call)
7. Snapshot + stale mark
8. Define domain language terms (chronicle_define_term) + check violations
9. Report discoveries

Incremental scan (when user says "update the graph" or "rescan changes"):
1. git diff to find changed files
2. chronicle_revision_create(mode="incremental", before_sha, after_sha)
3. chronicle_invalidate_changed(domain, revision_id, changed_files as JSON array)
   → returns stale_evidence count and files_to_rescan
4. Read ONLY the files listed in files_to_rescan
5. chronicle_import_all for re-extracted facts (positive evidence)
6. For facts confirmed removed: create negative evidence via chronicle_evidence_add(polarity="negative")
7. chronicle_finalize_incremental_scan(domain, revision_id)
8. chronicle_snapshot_create`,

	"data": `Analyze data models:
1. Call chronicle_extraction_guide(technology='prisma')
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

	"impact": `Impact analysis (user will specify the node):
1. Ask user: "What entity/service/model do you want to analyze?"
2. Find the node_key: call chronicle_node_list with filters
3. Call chronicle_impact with the node_key, depth 4
4. Explain the blast radius — which services, endpoints, models are affected
5. Show the dependency chain with evidence`,

	"deps": `Dependency analysis:
1. Ask user: "What do you want to check dependencies for?"
2. Find the node_key
3. Call chronicle_query_deps for forward deps
4. Call chronicle_query_reverse_deps for reverse deps
5. Explain both directions`,

	"path": `Path finding:
1. Ask user for start and end nodes
2. Find both node_keys
3. Call chronicle_query_path with mode='directed'
4. If no path found, try mode='connected'
5. Explain the path with edge types`,

	"flows": `Business flow / use case analysis:
1. Call chronicle_extraction_guide(technology='flow') for detailed instructions
2. Read the main service files to identify key business processes
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

	"diagram": `Live diagram for the user. Use the Diagram Type Catalog below to construct proper diagrams.

## How to Create a Diagram

1. Identify the diagram type from the user's question (see catalog below)
2. Call chronicle_diagram_create(title="{Type}: {subject}") — get session_id and URL
3. Share URL with user: "Open {url} to see the diagram"
4. Query the graph using the right tool for the diagram type:
   - System Overview: chronicle_node_list(layer='service') + chronicle_edge_list()
   - Request Flow: chronicle_query_path(from, to)
   - Impact Analysis: chronicle_impact(node_key)
   - Dependency Map: chronicle_query_deps(node_key, depth=1)
   - Domain Model: chronicle_node_list(layer='data') + chronicle_edge_list(edge_type='REFERENCES_MODEL')
5. Filter results — only include nodes/edges specified by the diagram type rules
6. Build payload and call chronicle_diagram_update(session_id, payload)
7. Add annotations with chronicle_diagram_annotate (see step rules per type)
8. As conversation evolves, call chronicle_diagram_update again to refine

## Diagram Type Catalog

### Type 1: System Overview
Trigger: "show architecture", "show services", "how does the system work"
Nodes: All service-layer nodes + contract:topic nodes (async channels only, NO endpoints)
Edges: CALLS_SERVICE, PUBLISHES_TOPIC, CONSUMES_TOPIC
Styling: Layer-level — services (#ef4444), topics (#10b981)
Steps: Single step (total_steps=1). Annotate the central orchestrating service.
Title: "Overview: {project name} Architecture"
Description: Summarize how many services and communication patterns (sync/async).
Node cap: 5-10. If >10 services, show only primary ones.

### Type 2: Request Flow
Trigger: "how does X flow to Y", "trace a request", "explain how X calls Y"
Nodes: Only nodes on the request path — controllers, providers, endpoints, services crossed.
  Optional: include data models if a provider on the path uses one (USES_MODEL).
Edges: INJECTS, CALLS_SERVICE, CALLS_ENDPOINT, EXPOSES_ENDPOINT, USES_MODEL (optional)
Styling: Node-type-level — controllers/providers (#3b82f6), endpoints (#10b981), services (#ef4444), models (#8b5cf6)
Steps: Multi-step walkthrough. Each step advances one hop:
  - Step 0: Entry point (endpoint + controller). Highlight: #f59e0b. Dim all others.
  - Step 1..N-1: Each injection or cross-service call. Highlight active nodes, dim inactive.
  - Step N: Arrival at destination.
  step_title: short imperative ("Entry Point", "Cross-Service Call", "Arrival")
  step_description: explain what happens at this hop and why
Title: "Flow: {entry} -> {destination}"
Node cap: 8-12. Only the direct path, no tangential deps.

### Type 3: Impact Analysis
Trigger: "what breaks if I change X", "blast radius of X"
Nodes: Start from changed node, traverse INCOMING edges transitively. Stop at 3 hops or 15 nodes.
Edges: USES_MODEL, INJECTS, EXPOSES_ENDPOINT, REFERENCES_MODEL — anything pointing TO the changed node.
Styling: Heat map by distance from change:
  - Distance 0 (changed): highlight="#ef4444"
  - Distance 1 (direct): highlight="#f59e0b"
  - Distance 2+ (ripple): highlight="#eab308"
Steps: Multi-step, one per distance level:
  - Step 0 "The Change": highlight changed node + co-affected (e.g. referenced models). Dim all others.
  - Step 1 "Direct Impact": reveal distance-1 nodes, explain WHY each is affected.
  - Step 2 "Ripple Effect": reveal distance-2 nodes, show full blast radius.
  step_description: explain WHY each layer is affected, not just that it is.
Title: "Impact: Changing {node name}"
Node cap: 12-15.

### Type 4: Dependency Map
Trigger: "what does X depend on", "dependencies of X"
Nodes: Subject node + all nodes reachable via outgoing edges (1 hop). Include called endpoints.
Edges: CALLS_SERVICE, CALLS_ENDPOINT, USES_MODEL, PUBLISHES_TOPIC, CONSUMES_TOPIC, INJECTS (if code-level subject)
Styling: Node-type-level — services (#ef4444), models (#8b5cf6), topics (#10b981), endpoints (#10b981)
Steps: Single step. Annotate the subject node with its role.
Title: "Dependencies: {node name}"
Description: Summarize dependency profile — how many services, models, async channels.
Node cap: 10-15. If many endpoints, show only the ones actually called.

### Type 5: Domain Model
Trigger: "show data models", "show entities", "show schema"
Nodes: All data:model nodes. Enums are LOW PRIORITY — only include if user asks or total <10 nodes.
Edges: REFERENCES_MODEL only.
Styling: Node-type-level — models (#8b5cf6), enums (#8b5cf6 with note "enum")
Steps: Single step. Annotate models that span service boundaries.
Title: "Domain: {project name} Data Models"
Description: Summarize count of models and which services own what.
Node cap: All data models (typically <20).

## Cross-cutting Rules (apply to ALL types)

- NEVER exceed 15 nodes. Filter aggressively — prioritize nodes that answer the question.
- Every diagram MUST annotate the focal node(s) explaining WHY they matter.
- Only include edge types specified by the diagram type. No CONTAINS in flow diagrams, no INJECTS in overviews.
- For multi-step: all nodes present from start but dimmed. Steps reveal progressively.
- Step descriptions: write as if narrating to someone unfamiliar with the codebase.
- Node payload must include: node_id, node_key, name, layer, node_type.
- Highlight colors must match the diagram type visual language.
- When in doubt, use single step. Multi-step only when there's a clear sequential narrative.`,

	"help": `Show all available Chronicle commands:
- /chronicle-scan — Full project scan
- /chronicle-data — Analyze data models
- /chronicle-language — Domain language glossary
- /chronicle-impact — Change impact analysis
- /chronicle-deps — Dependency analysis
- /chronicle-path — Find paths between nodes
- /chronicle-services — Service architecture
- /chronicle-status — Current graph state
- /chronicle-help — This help`,
}

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

	"diagram": `Live diagram for the user:

IMPORTANT — Node selection priority (less is more):
  1. Services (layer=service) — ALWAYS start here. Show the big picture first.
  2. Data models (layer=data) — add only if the question is about data flow or model relationships.
  3. Controllers/providers (layer=code) — add only when drilling into a specific service's internals.
  4. Endpoints (layer=contract) — add ONLY if the user specifically asks about API surface or a specific route.
  Keep diagrams focused: 8-15 nodes is ideal. 20+ nodes becomes noise.
  NEVER dump the entire graph into a diagram. Pick the nodes that tell the story.

Steps:
1. Call chronicle_diagram_create(title="descriptive name") — get session_id and URL
2. Share URL with user: "Open {url} to see the diagram"
3. Query the graph — pick the RIGHT query for the story:
   - Service map: chronicle_node_list(layer='service') + chronicle_edge_list (start here!)
   - Dependency: chronicle_query_deps(node_key, depth=2)
   - Impact: chronicle_impact(node_key)
   - Path: chronicle_query_path(from, to)
4. Filter results — only include nodes that matter for THIS explanation
5. Build payload: {nodes: [{node_id, node_key, name, layer, node_type}, ...], edges: [{from_node_id, to_node_id, edge_type}, ...]}
6. Call chronicle_diagram_update(session_id, payload)
7. Add step-through presentation with chronicle_diagram_annotate:
   - Use "step" param (0, 1, 2...) to create a guided walkthrough
   - Each step should highlight 1-3 nodes max with a clear story beat
   - Include step_title (short) and step_description (1-2 sentences, conversational)
   - Step descriptions are shown prominently below the diagram — make them count
8. As conversation evolves, call chronicle_diagram_update again to refine

Diagram types: service map, dependency, impact, path, custom (explanatory)`,

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

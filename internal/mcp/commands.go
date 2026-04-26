package mcp

// Command definitions for user-facing slash commands.
// These are returned by oracle_extraction_guide and documented in CLAUDE.md.

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
	"help":    "Show available Oracle commands",
	"diagram": "Show a live diagram to explain architecture",
}

var CommandInstructions = map[string]string{
	"scan": `Full project scan:
1. Call oracle_get_discoveries to learn from previous scans
2. Call oracle_extraction_guide for methodology
3. Auto-discover project, save manifest
4. Create revision
5. Scan in passes: data models → code structure → contracts/endpoints → cross-service edges
6. For each file: read → extract → oracle_import_all immediately (max 10-15 nodes per call)
7. Snapshot + stale mark
8. Define domain language terms (oracle_define_term) + check violations
9. Report discoveries

Incremental scan (when user says "update the graph" or "rescan changes"):
1. git diff to find changed files
2. oracle_revision_create(mode="incremental", before_sha, after_sha)
3. oracle_invalidate_changed(domain, revision_id, changed_files as JSON array)
   → returns stale_evidence count and files_to_rescan
4. Read ONLY the files listed in files_to_rescan
5. oracle_import_all for re-extracted facts (positive evidence)
6. For facts confirmed removed: create negative evidence via oracle_evidence_add(polarity="negative")
7. oracle_finalize_incremental_scan(domain, revision_id)
8. oracle_snapshot_create`,

	"data": `Analyze data models:
1. Call oracle_extraction_guide(technology='prisma')
2. Find schema files: Glob for prisma/schema.prisma, *.entity.ts
3. Read each schema file
4. Extract: models → data:model nodes, enums → data:enum nodes
5. Extract relations: @relation → REFERENCES_MODEL edges
6. Import immediately after each file
7. Find services that use these models → USES_MODEL edges
8. Report what you found`,

	"language": `Domain language analysis:
1. Call oracle_get_glossary to see existing terms
2. Analyze the codebase for key domain concepts
3. For each concept: call oracle_define_term with term, description, context, aliases, anti_patterns
4. Call oracle_check_language to find violations
5. Report violations and suggestions`,

	"impact": `Impact analysis (user will specify the node):
1. Ask user: "What entity/service/model do you want to analyze?"
2. Find the node_key: call oracle_node_list with filters
3. Call oracle_impact with the node_key, depth 4
4. Explain the blast radius — which services, endpoints, models are affected
5. Show the dependency chain with evidence`,

	"deps": `Dependency analysis:
1. Ask user: "What do you want to check dependencies for?"
2. Find the node_key
3. Call oracle_query_deps for forward deps
4. Call oracle_query_reverse_deps for reverse deps
5. Explain both directions`,

	"path": `Path finding:
1. Ask user for start and end nodes
2. Find both node_keys
3. Call oracle_query_path with mode='directed'
4. If no path found, try mode='connected'
5. Explain the path with edge types`,

	"flows": `Business flow / use case analysis:
1. Call oracle_extraction_guide(technology='flow') for detailed instructions
2. Read the main service files to identify key business processes
3. For each use case (e.g. PlaceOrder, UserSignup):
   a. Create flow:use_case node
   b. Find which endpoint triggers it → TRIGGERS_FLOW edge
   c. Find which services it calls → REQUIRES edges
   d. Find which models it reads/writes → REQUIRES edges
   e. Find what events/outcomes it produces → PRODUCES_OUTCOME edges
4. Import flows via oracle_import_all
5. Report the discovered use cases to the user`,

	"services": `Service architecture analysis:
1. Call oracle_node_list with layer='service'
2. For each service, call oracle_query_deps to see what it depends on
3. Call oracle_edge_list with type='CALLS_SERVICE' for cross-service deps
4. Call oracle_edge_list with type='CALLS_ENDPOINT' for specific endpoint deps
5. Summarize the service dependency map`,

	"status": `Graph status:
1. Call oracle_scan_status
2. Call oracle_query_stats
3. Call oracle_get_discoveries
4. Call oracle_get_glossary
5. Call oracle_admin_url
6. Summarize: nodes, edges, layers, last scan, discoveries, domain terms, dashboard URL`,

	"diagram": `Live diagram for the user:
1. Ask user: "Want me to show this as a diagram?"
2. Call oracle_diagram_create(title="descriptive name") — get session_id and URL
3. Share URL with user: "Open {url} to see the diagram"
4. Query the graph to build the payload:
   - Dependency diagram: oracle_query_deps(node_key, depth=2)
   - Impact diagram: oracle_impact(node_key)
   - Path diagram: oracle_query_path(from, to)
   - Service map: oracle_node_list(layer='service') + oracle_edge_list
   - Custom: invent nodes with layer for color (service=red, data=purple, code=blue)
5. Build payload: {nodes: [{node_id: 1, node_key: "...", name: "...", layer: "...", node_type: "..."}, ...], edges: [{from_node_id: 1, to_node_id: 2, edge_type: "CALLS_SERVICE"}, ...]}
6. Call oracle_diagram_update(session_id, payload) — dashboard updates live
7. Call oracle_diagram_annotate(session_id, node_key, note="explanation", highlight="red") — highlight key nodes
8. As conversation evolves, call oracle_diagram_update again to add/remove/change nodes
Diagram types: dependency, impact, path, service map, custom (explanatory)`,

	"help": `Show all available Oracle commands:
- /oracle-scan — Full project scan
- /oracle-data — Analyze data models
- /oracle-language — Domain language glossary
- /oracle-impact — Change impact analysis
- /oracle-deps — Dependency analysis
- /oracle-path — Find paths between nodes
- /oracle-services — Service architecture
- /oracle-status — Current graph state
- /oracle-help — This help`,
}

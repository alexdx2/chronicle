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
	"help":     "Show available Oracle commands",
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
9. Report discoveries`,

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

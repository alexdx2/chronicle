package mcp

import (
	"encoding/json"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// customGuideStore is set by the MCP server to allow reading custom prompts.
var customGuideStore *store.Store

// SetGuideStore sets the store for reading custom extraction prompts.
func SetGuideStore(s *store.Store) { customGuideStore = s }

// INVARIANT: This guide MUST NEVER enumerate full edge types or layer constraints.
// Only illustrative examples are allowed. Schema is the single source of truth.
// If you need to list valid types, tell Claude to call chronicle_schema().

// ExtractionGuide returns the universal extraction methodology.
// The technology parameter is DEPRECATED and ignored — always returns the full guide.
func ExtractionGuide(technology string) string {
	guide := map[string]any{
		"schema_reference": "For valid edge types, node types, and layer constraints, call chronicle_schema(). This guide uses illustrative examples only — schema is the single source of truth.",

		"workflow": []string{
			"1. Call chronicle_get_discoveries — learn from previous scans",
			"2. Call chronicle_schema — learn what layers, types, and edges exist",
			"3. Call chronicle_extraction_guide — learn how to extract (you're reading this now)",
			"4. Auto-discover project, call chronicle_save_manifest",
			"4.5. Call chronicle_resolve_context(domain) — if no context exists, it auto-creates \"main\"",
			"5. Create revision (include context_id)",
			"6. Scan in passes: data models → code structure → contracts/endpoints → cross-service edges → flows",
			"7. For each file: READ → extract → chronicle_import_all → move on. NEVER accumulate data in context. Max ~10-15 nodes per call.",
			"8. chronicle_snapshot_create + chronicle_stale_mark",
			"9. Domain language: chronicle_get_glossary → chronicle_define_term → chronicle_check_language",
			"10. chronicle_report_discovery with severity (critical/warning/insight) and suggested_action",
		},

		"key_rules": map[string]string{
			"format":            "node_key = layer:type:domain:name (all lowercase)",
			"import":            "STREAM: read 1 file → import immediately → forget. Max ~10 nodes per import call.",
			"no_scripts":        "Do NOT write bash/grep scripts. READ files with Read tool. You understand code better than regex.",
			"evidence":          "Every edge needs evidence with assertion: file_path, assertion_kind, assertion JSON. This enables mechanical verification.",
			"negative_evidence": "During incremental scans, if a relationship was confirmed removed, create negative evidence via chronicle_evidence_add with polarity='negative' and checked_scope.",
			"partial_accept":    "chronicle_import_all writes valid items and skips invalid ones. Check the 'rejected' array — each entry has error and suggested fix.",
		},

		"universal_extraction": map[string]any{
			"principle": "You are looking for ARCHITECTURAL FACTS, not code patterns. Don't search for specific decorators or syntax — understand what the code DOES and extract the relationships.",

			"what_to_extract": map[string]string{
				"boundaries":    "What are the separately deployable units? (services, apps, packages, containers). Each → service layer node.",
				"data_models":   "What are the persistent data structures? (database models, schemas, entities). Each → data layer node. FK/relations → REFERENCES_MODEL edges.",
				"code_units":    "What are the key code components? (modules, controllers, services, resolvers, handlers, gateways). Each → code layer node.",
				"contracts":     "What does this system expose to the outside? (HTTP endpoints, GraphQL operations, WebSocket events, message queue topics, gRPC methods). Each → contract layer node.",
				"dependencies":  "What depends on what? (constructor injection, imports of internal modules, service-to-service calls). Each → appropriate edge.",
				"external":      "What external systems does this call? (payment APIs, push notification services, 3rd party APIs, managed databases). Each → service:external_system node.",
				"flows":         "What are the key business processes? (order creation, payment processing, user registration). Each → flow layer node.",
			},

			"how_to_read_a_file": []string{
				"1. WHAT IS THIS FILE? Identify its role: is it a service, controller, model, config, gateway, consumer, producer, utility?",
				"2. WHAT DOES IT RECEIVE? Any incoming requests, events, messages, scheduled triggers? → contract layer nodes.",
				"3. WHAT DOES IT DEPEND ON? Constructor params, imported modules, injected services? → INJECTS/DEPENDS_ON edges.",
				"4. WHAT DOES IT CALL? Other services, external APIs, databases, message queues, event emitters? → edges to targets.",
				"5. WHAT DOES IT SEND OUT? HTTP responses, emitted events, published messages, push notifications? → contract layer or CALLS_SERVICE.",
				"6. DOES IT DELEGATE? If it calls a factory, strategy, or handler that does the real work — FOLLOW that delegation. Read that file too.",
				"7. WHAT BUSINESS PROCESS DOES IT IMPLEMENT? If it orchestrates multiple steps (validate → save → notify → respond) — that's a flow.",
			},

			"delegation_rule": "CRITICAL: If a file delegates behavior to another class/function (factory.create(), handler.process(), service.register()), you MUST follow that chain. The delegated code contains the real architectural relationships. Don't stop at the surface.",

			"evidence_with_assertions": map[string]string{
				"why":    "Evidence without assertions is just a line number. When the file changes, line numbers break. Assertions describe WHAT was observed and can be mechanically verified.",
				"how":    "When adding evidence, include assertion_kind and assertion JSON. Example: assertion_kind='go_module_require', assertion='{\"module\": \"github.com/foo/bar\"}'",
				"kinds":  "manifest_dependency (package.json/go.mod deps), import_specifier (import statements), call_expression (method calls), decorator (class/method decorators), yaml_key_exists (config keys), prisma_model (schema models), text_contains (fallback substring match)",
			},
		},

		"node_role_classification": map[string]string{
			"how":      "Set node metadata to {\"role\":\"<role>\",\"role_confidence\":0-1,\"role_reason\":\"<evidence>\"}. Pick role from chronicle_schema field salience_roles (closed vocab). Drives diagram salience (box vs hidden vs badge).",
			"evidence": "For an auditable/multi-extractor claim, also emit evidence with source_kind=role_classification, assertion={\"role\":..,\"role_reason\":..}, confidence=role_confidence. The highest-confidence claim wins (winning_role) and overrides metadata.",
			"boundary": "CLASSIFY role only — never decide visibility (no should_show); a deterministic renderer does that. role_confidence is YOUR extractor confidence, not system trust. No fit → role \"unknown\" + proposed_role via chronicle_report_discovery (don't invent roles inline).",
		},

		"layer_guide": map[string]string{
			"data":     "Persistent data structures. Look for: schema definitions, model declarations, entity classes, migration files. NOT DTOs, NOT request/response types — only what's stored.",
			"code":     "Code components that implement logic. Look for: anything with constructor injection, anything that processes requests, anything that orchestrates business logic. The key signal is DEPENDENCIES — what does this class need to work?",
			"contract": "System boundaries — what goes in, what comes out. Look for: ANY way the system receives requests (HTTP, WebSocket, GraphQL, gRPC, message queue, cron) and ANY way it sends responses or events. The key signal is EXTERNAL COMMUNICATION.",
			"flow":     "Business processes that span multiple components. Look for: methods that call multiple services in sequence, orchestration logic, saga/workflow patterns. See flow_extraction_rules.",
			"service":  "Deployable units and external systems. Look for: docker-compose services, Dockerfile definitions, separate package.json apps, and ANY external system the code calls (payment APIs, notification services, search engines, etc).",
		},

		"flow_extraction_rules": map[string]any{
			"create_when": []string{
				"An endpoint mutates state (POST/PUT/PATCH/DELETE handler)",
				"An async handler processes events or jobs (message consumer, scheduled task)",
				"Multi-step orchestration exists across services (service A calls service B calls service C)",
			},
			"skip": []string{
				"Trivial CRUD with a single repository call and no branching/side effects",
				"Pure GET endpoints that only read data",
			},
			"connection_rules": "A use_case MUST connect to ALL THREE layers: code (providers it calls), data (models it reads/writes), and contract (endpoint that triggers it). A flow connected only to other flows is incomplete — trace the implementation.",
			"how_to_trace": []string{
				"1. Find the entry point: which endpoint or event triggers this flow? → TRIGGERS_FLOW from contract to flow:use_case",
				"2. Trace the code: which providers/services execute this flow? → REQUIRES from flow to each code:provider",
				"3. Trace the data: which models does each provider read or write? → REQUIRES from flow to each data:model",
				"4. Trace outcomes: what records are created, events emitted? → PRODUCES_OUTCOME from flow to data:model or contract:topic",
				"5. Chain flows: does this flow trigger another flow via events? → TRANSITIONS_TO between flows",
			},
		},

		"edge_intent": map[string]string{
			"INVOKES":    "runtime call — the flow/code actively calls this service/function at runtime",
			"REQUIRES":   "correctness dependency — the flow cannot complete without this service/model",
			"DEPENDS_ON": "structural/static dependency — references this but doesn't directly call it (config, shared types)",
			"INJECTS":    "constructor/DI parameter — framework injects this dependency",
			"USES_MODEL": "data model query — service queries this database model",
		},

		"edge_derivation": map[string]string{
			"hard":     "Direct evidence: import statement, constructor param, schema FK, explicit config. Use for most edges.",
			"linked":   "Indirect but strong: HTTP URL matches endpoint, env var points to service. Cross-service edges.",
			"inferred": "LLM guess or heuristic. Low confidence. Needs verification.",
		},

		"common_mistakes": []string{
			"STOP: Don't only look for framework-specific decorators. socket.on('event') is the same as @SubscribeMessage('event') — both are incoming event handlers.",
			"STOP: Don't skip factory/strategy patterns. If a constructor receives a Factory and calls factory.create(), the REAL dependencies are inside that factory.",
			"STOP: Don't create edges to infrastructure libraries (@nestjs/common, express, lodash). These appear everywhere and add no architectural insight.",
			"STOP: Don't create 50 edges from every file to PrismaService. One INJECTS edge per class is enough.",
			"STOP: Don't ignore authentication/authorization middleware. Auth flows are architectural — they define access patterns.",
			"STOP: Don't ignore event EMITTERS. code that calls emit('event', data) or publish('topic', message) is creating an architectural contract, even without a decorator.",
		},

		"trust_aware_queries": map[string]string{
			"description": "Query results include trust_score (0-1) and freshness (0-1) for each node/edge.",
			"trust_high":  "trust >= 0.8: use directly in your answer",
			"trust_mid":   "trust 0.4-0.8: mention uncertainty",
			"trust_low":   "trust < 0.4: read the source file to verify before answering",
		},

		"user_corrections": map[string]string{
			"description":  "When user says a graph fact is wrong, create negative evidence to correct it.",
			"how":          "chronicle_evidence_add with polarity='negative', source_kind='user_feedback', confidence=0.95.",
			"what_happens": "Strong negative evidence (>=0.8) marks edge as 'contradicted'.",
		},

		"user_commands": "When user says 'chronicle scan', 'chronicle data', etc — call chronicle_command(command='scan') and follow the instructions.",
		"onboarding":    "ALWAYS call chronicle_scan_status first. If response contains 'onboarding.is_first_run=true', ask the user if they want to scan.",
		"hints":         "For framework-specific tips, call chronicle_extraction_hints(). But hints are OPTIONAL — the universal extraction rules above work for any codebase.",

		"diagrams": map[string]any{
			"when": "When explaining architecture, offer to show a live diagram",
			"how":  "chronicle_diagram_create() → share URL → chronicle_diagram_update() with {nodes, edges}",
		},
	}

	// Append custom project-level instructions if set
	if customGuideStore != nil {
		if custom, err := customGuideStore.GetSetting("extraction_prompt"); err == nil && custom != "" {
			guide["project_custom_instructions"] = custom
		}
	}

	data, _ := json.MarshalIndent(guide, "", "  ")
	return string(data)
}

// ExtractionHints returns optional framework-specific tips.
// These are recognition patterns, not extraction rules.
// The universal extraction guide above works without hints.
func ExtractionHints(technology string) string {
	tech := strings.ToLower(strings.TrimSpace(technology))

	hints := map[string]any{
		"note": "These are RECOGNITION PATTERNS to help you spot architectural elements in specific frameworks. The universal extraction rules in chronicle_extraction_guide() work for any codebase — hints just help you recognize patterns faster.",
	}

	switch tech {
	case "nestjs", "typescript", "ts":
		hints["technology"] = "nestjs"
		hints["recognition_patterns"] = map[string]string{
			"DI injection":     "constructor(private x: ServiceName) — but also @Inject('TOKEN') for non-class providers",
			"HTTP endpoints":   "@Get/@Post/@Put/@Delete — but also custom method decorators, Express middleware routes",
			"WebSocket events": "@SubscribeMessage('name') — BUT ALSO socket.on('name', handler) for programmatic registration. FOLLOW delegation to factories/services that register handlers.",
			"GraphQL":          "@Query/@Mutation/@Subscription — resolvers are like controllers for GraphQL",
			"Modules":          "@Module imports/providers/controllers define the DI container structure",
			"Async":            "@Cron, @OnEvent, @Process (Bull) — scheduled and event-driven handlers",
			"Guards/pipes":     "@UseGuards, @UsePipes — define access control and validation chains",
		}

	case "prisma", "data", "models":
		hints["technology"] = "prisma"
		hints["recognition_patterns"] = map[string]string{
			"models":    "model X { ... } — each is a data:model node",
			"relations": "@relation(fields:[fk], references:[pk]) — REFERENCES_MODEL edge",
			"enums":     "enum X { ... } — only track if used across multiple services/modules",
		}

	case "go", "golang":
		hints["technology"] = "go"
		hints["recognition_patterns"] = map[string]string{
			"DI":         "Struct fields or constructor params (func NewService(db *sql.DB, cache *redis.Client))",
			"HTTP":       "http.HandleFunc, gin.GET, echo.POST, chi.Route — any HTTP handler registration",
			"gRPC":       "RegisterXxxServer, pb.UnimplementedXxxServer — gRPC service implementations",
			"interfaces": "Type implementing an interface = injectable dependency",
			"goroutines": "go func() with channel/select = async processing pattern",
		}

	case "django", "python":
		hints["technology"] = "django"
		hints["recognition_patterns"] = map[string]string{
			"models":     "class X(models.Model) — data layer",
			"views":      "class X(APIView), class X(ViewSet), def view(request) — code layer",
			"urls":       "path('url', view) — contract layer endpoints",
			"signals":    "post_save.connect, @receiver(post_save) — async event handlers",
			"celery":     "@app.task, @shared_task — async job handlers",
			"middleware":  "classes in MIDDLEWARE setting — cross-cutting concerns",
		}

	case "dotnet", "csharp", "cs", "aspnet", "aspnetcore":
		hints["technology"] = "dotnet"
		hints["recognition_patterns"] = map[string]string{
			"DI":          "Constructor injection; IScoreService → ScoreService convention. Skip ILogger/IConfiguration/IServiceScopeFactory.",
			"HTTP":        "[ApiController] + [HttpGet/Post/...]; [Route(\"api/[controller]\")] — substitute [controller] with the class name minus 'Controller', lowercased",
			"EF Core":     "DbSet<X> in DbContext → model nodes; FK property (OrderId) → model_relation from the FK side ONLY",
			"SignalR":     "Hub subclass = controller (WS surface); public hub methods → WS endpoints; IHubContext<XHub> injection → injects XHub",
			"Kafka":       "consumer.Subscribe(\"topic\") / ProduceAsync(\"topic\") — the topic STRING is the target, never the payload class",
			"background":  "BackgroundService/IHostedService = provider; extract consumes + service calls from ExecuteAsync",
		}

	case "spring", "java":
		hints["technology"] = "spring"
		hints["recognition_patterns"] = map[string]string{
			"DI":         "@Autowired, constructor injection, @Qualifier",
			"HTTP":       "@RestController + @GetMapping/@PostMapping — endpoints",
			"JPA":        "@Entity — data models, @Repository — data access",
			"messaging":  "@KafkaListener, @RabbitListener, @JmsListener — async consumers",
			"scheduling": "@Scheduled — cron/periodic tasks",
			"events":     "ApplicationEventPublisher, @EventListener — internal event system",
		}

	default:
		hints["technology"] = tech
		hints["message"] = "No specific hints for " + tech + ". Use the universal extraction rules in chronicle_extraction_guide() — they work for any technology."
	}

	data, _ := json.MarshalIndent(hints, "", "  ")
	return string(data)
}

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
			"evidence":          "Every node needs evidence: target_kind, node_key, source_kind=file, file_path, line_start, extractor_id=claude-code, extractor_version=1.0",
			"negative_evidence": "During incremental scans, if a relationship was confirmed removed (e.g. constructor no longer injects a service), create negative evidence via chronicle_evidence_add with polarity='negative'.",
			"partial_accept":    "chronicle_import_all writes valid items and skips invalid ones. Check the 'rejected' array in the response — each entry has the error and a suggested fix. Re-import rejected items after fixing.",
		},
		"layer_guide": map[string]string{
			"data":     "Look for schema definitions (Prisma models, TypeORM entities, SQL DDL, GraphQL types). Each model → data:model, each enum → data:enum. Relations between models → REFERENCES_MODEL.",
			"code":     "Look for modules, controllers/handlers, services/providers, resolvers, gateways. DI constructor params → INJECTS. Route decorators/handlers → EXPOSES_ENDPOINT. Module membership → CONTAINS.",
			"contract": "Endpoints from route handlers (POST /orders → contract:endpoint). Topics from pub/sub patterns. GraphQL operations from schema files.",
			"flow":     "See flow_extraction_rules below.",
			"service":  "Deployable services. Usually one per package.json/go.mod/Dockerfile. External services (Stripe, Redis) → service:external_system.",
		},
		"flow_extraction_rules": map[string]any{
			"create_when": []string{
				"An endpoint mutates state (POST/PUT/PATCH/DELETE handler)",
				"An async handler processes events or jobs (Kafka consumer, Bull processor, cron)",
				"Multi-step orchestration exists across services (service A calls service B calls service C)",
			},
			"skip": []string{
				"Trivial CRUD with a single repository call and no branching/side effects",
				"Pure GET endpoints that only read data",
			},
			"edge_routing": map[string]string{
				"TRIGGERS_FLOW":    "Only from entry-point nodes (contract:endpoint, contract:topic, code:provider) to flow:use_case. NEVER between flows.",
				"REQUIRES":         "flow:use_case → every service and data:model it depends on for correctness",
				"PRODUCES_OUTCOME": "flow:use_case → every record, event, or notification it creates",
				"PRECEDES":         "Between flow:flow_step nodes within the same use case (ordering)",
				"TRANSITIONS_TO":   "Between flow:use_case nodes when one flow leads to another via events/topics",
			},
		},
		"edge_intent": map[string]string{
			"INVOKES":    "runtime call — the flow/code actively calls this service/function at runtime",
			"REQUIRES":   "correctness dependency — the flow cannot complete without this service/model",
			"DEPENDS_ON": "structural/static dependency — references this but doesn't directly call it (config, shared types)",
			"INJECTS":    "DI constructor parameter — framework injects this dependency",
			"USES_MODEL": "data model query — service queries this Prisma/ORM model",
		},
		"edge_derivation": map[string]string{
			"hard":     "Direct evidence: import statement, constructor param, decorator, schema FK. Use for most edges.",
			"linked":   "Indirect but strong: HTTP client URL matches endpoint path, env var points to service. Cross-service edges.",
			"inferred": "LLM guess or heuristic match. Low confidence. Needs verification.",
		},
		"trust_aware_queries": map[string]string{
			"description": "Query results include trust_score (0-1) and freshness (0-1) for each node/edge.",
			"trust_high":  "trust >= 0.8: use directly in your answer",
			"trust_mid":   "trust 0.4-0.8: mention uncertainty ('based on last scan, but file has changed...')",
			"trust_low":   "trust < 0.4: read the source file to verify before answering",
			"impact":      "When running impact analysis, note if trust_chain < 0.7 — the path may be broken",
		},
		"user_corrections": map[string]string{
			"description":  "When user says a graph fact is wrong, create negative evidence to correct it.",
			"how":          "chronicle_evidence_add with polarity='negative', source_kind='user_feedback', confidence=0.95. Include reason in metadata.",
			"what_happens": "Strong negative evidence (>=0.8) marks edge as 'contradicted', removed from queries.",
		},
		"user_commands": "When user says 'chronicle scan', 'chronicle data', etc — call chronicle_command(command='scan') and follow the instructions.",
		"onboarding":    "ALWAYS call chronicle_scan_status first. If response contains 'onboarding.is_first_run=true', ask the user: 'This project hasn't been scanned yet. Would you like me to scan it and build a knowledge graph?' If yes, call chronicle_command(command='scan').",
		"hints":         "For framework-specific tips (NestJS decorators, Prisma patterns, etc), call chronicle_extraction_hints(technology='nestjs').",
		"diagrams": map[string]any{
			"when": "When explaining architecture, dependencies, impact, or flows to the user, offer to show a live diagram",
			"how":  "Call chronicle_diagram_create() to get a URL, share it, then chronicle_diagram_update() with {nodes, edges} payload",
			"tips": []string{
				"Start simple — 3-5 key nodes, add detail incrementally",
				"Use chronicle_diagram_annotate to highlight what you're talking about",
				"Pull nodes from chronicle_node_list or chronicle_query_deps results",
				"Update the diagram as the conversation evolves",
				"For custom explanatory diagrams, invent node_keys like custom:box:explain:name",
				"Use layer to control color: service=red, data=purple, code=blue, flow=pink, contract=green",
			},
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
// These are patterns, not rules — for valid types, call chronicle_schema().
func ExtractionHints(technology string) string {
	tech := strings.ToLower(strings.TrimSpace(technology))

	var hints map[string]any

	switch tech {
	case "nestjs", "typescript", "ts":
		hints = map[string]any{
			"technology": "nestjs",
			"hints": map[string]string{
				"@Controller('prefix')":              "code:controller — combine prefix with method decorators for endpoint paths",
				"@Injectable()":                      "code:provider — services, guards, interceptors, pipes, gateways",
				"@Module({ providers, controllers })": "code:module — CONTAINS edge to each provider/controller listed",
				"constructor(private svc: X)":         "INJECTS edge from this provider to X",
				"@UseGuards(X)":                       "INJECTS edge from controller to guard",
				"@Get/@Post/@Put/@Delete('path')":     "EXPOSES_ENDPOINT from controller to contract:endpoint:domain:method:/prefix/path",
				"@WebSocketGateway":                   "code:provider — @SubscribeMessage creates contract:topic",
				"@Cron, @OnEvent, @Process":           "code:provider with relevant trigger pattern",
				"PrismaService.model.findMany()":      "USES_MODEL edge to the data:model",
				"@Resolver(() => Type)":               "code:resolver — treat like controller for GraphQL",
			},
			"note": "These are patterns, not rules. For valid types and constraints, call chronicle_schema().",
		}

	case "prisma", "data", "models":
		hints = map[string]any{
			"technology": "prisma",
			"hints": map[string]string{
				"model X { ... }":        "data:model:domain:x — lowercase model name",
				"enum X { ... }":         "data:enum:domain:x — USES_ENUM from models that reference it",
				"@relation(fields:[fk])":  "REFERENCES_MODEL from this model to the related model",
				"Array field (X[])":       "REFERENCES_MODEL from parent to child model",
				"prisma.x.findMany()":     "USES_MODEL from service provider to data:model",
				"@@map / @@unique / @@id": "Metadata on the data:model node, not separate nodes",
			},
			"note": "These are patterns, not rules. For valid types and constraints, call chronicle_schema().",
		}

	case "openapi":
		hints = map[string]any{
			"technology": "openapi",
			"hints": map[string]string{
				"paths./x.get":   "contract:endpoint:domain:get:/x",
				"paths./x.post":  "contract:endpoint:domain:post:/x",
				"info.title":     "service or contract:http_api name",
				"$ref components": "May indicate data:model or contract:graphql_type",
			},
			"note": "These are patterns, not rules. For valid types and constraints, call chronicle_schema().",
		}

	case "django", "python":
		hints = map[string]any{
			"technology": "django",
			"hints": map[string]string{
				"class X(models.Model)":    "data:model:domain:x",
				"class X(APIView)":         "code:controller:domain:x",
				"class X(ViewSet)":         "code:controller:domain:x — each action is an endpoint",
				"@action(detail=...)":      "EXPOSES_ENDPOINT from viewset to endpoint",
				"path('url', view)":        "contract:endpoint:domain:method:/url",
				"class X(serializers.Serializer)": "Metadata on related model, not a separate node",
			},
			"note": "These are patterns, not rules. For valid types and constraints, call chronicle_schema().",
		}

	case "spring", "java":
		hints = map[string]any{
			"technology": "spring",
			"hints": map[string]string{
				"@RestController":       "code:controller",
				"@Service":              "code:provider",
				"@Repository":           "code:provider — USES_MODEL to the entity it manages",
				"@GetMapping/@PostMapping": "EXPOSES_ENDPOINT from controller to endpoint",
				"@Autowired / constructor": "INJECTS edge from this bean to injected bean",
				"@Entity":                  "data:model",
				"@KafkaListener":           "CONSUMES_TOPIC from provider to contract:topic",
			},
			"note": "These are patterns, not rules. For valid types and constraints, call chronicle_schema().",
		}

	default:
		hints = map[string]any{
			"technology": tech,
			"message":    "No specific hints for " + tech + ". Use chronicle_schema() for valid types and chronicle_extraction_guide() for methodology. Chronicle works with any technology — read the code and map to layers (code/data/contract/flow/service).",
		}
	}

	data, _ := json.MarshalIndent(hints, "", "  ")
	return string(data)
}

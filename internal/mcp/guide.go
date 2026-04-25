package mcp

import (
	"encoding/json"

	"github.com/anthropics/depbot/internal/store"
)

// customGuideStore is set by the MCP server to allow reading custom prompts.
var customGuideStore *store.Store

// SetGuideStore sets the store for reading custom extraction prompts.
func SetGuideStore(s *store.Store) { customGuideStore = s }

// ExtractionGuide returns the extraction methodology.
// If a custom prompt is stored in project settings, it's appended.
func ExtractionGuide(technology string) string {
	if technology != "" {
		return detailedGuide(technology)
	}

	// Default: compact workflow — just enough for Claude to start scanning
	guide := map[string]any{
		"workflow": []string{
			"1. Call oracle_get_discoveries — learn from previous scans",
			"2. Call oracle_scan_status — check current graph state",
			"3. If first scan: auto-discover project, call oracle_save_manifest",
			"4. Call oracle_revision_create",
			"5. For large projects (>= 5 modules): spawn Agent per service. Each agent reads files and imports IMMEDIATELY per file.",
			"6. For each file: READ → extract → oracle_import_all → move on. NEVER accumulate data in context.",
			"7. Cross-service pass: read HTTP client files, create CALLS_SERVICE + CALLS_ENDPOINT edges (derivation: linked)",
			"8. FLOW extraction: identify key business use cases (e.g. PlaceOrder, UserSignup, ProcessPayment). Create flow:use_case nodes + REQUIRES edges to services/models + TRIGGERS_FLOW from endpoints + PRODUCES_OUTCOME to events/state changes.",
			"9. oracle_snapshot_create + oracle_stale_mark",
			"10. Domain language: oracle_get_glossary → oracle_define_term → oracle_check_language",
			"11. oracle_report_discovery with severity (critical/warning/insight) and suggested_action",
		},
		"key_rules": map[string]string{
			"format":     "node_key = layer:type:domain:name (all lowercase)",
			"import":     "STREAM: read 1 file → import immediately → forget. Max ~10 nodes per import call.",
			"no_scripts": "Do NOT write bash/grep scripts. READ files with Read tool. You understand code better than regex.",
			"evidence":   "Every node needs evidence: target_kind, node_key, source_kind=file, file_path, line_start, extractor_id=claude-code, extractor_version=1.0",
		},
		"user_commands": "When user says 'oracle scan', 'oracle data', etc — call oracle_command(command='scan') and follow the instructions.",
		"onboarding": "ALWAYS call oracle_scan_status first. If response contains 'onboarding.is_first_run=true', ask the user: 'This project hasn't been scanned yet. Would you like me to scan it and build a knowledge graph?' If yes, call oracle_command(command='scan').",
		"layers": map[string]string{
			"data":     "Prisma models, entities, enums → data:model, data:enum",
			"code":     "modules, controllers, providers, resolvers, guards → code:module, code:controller, code:provider",
			"contract": "HTTP endpoints, Kafka topics, GraphQL operations → contract:endpoint, contract:topic",
			"service":  "Deployable services → service:service",
			"flow":     "Business use cases, processes → flow:use_case, flow:flow_step, flow:trigger, flow:outcome",
		},
		"edge_types": map[string]string{
			"CONTAINS":          "module → providers (structural, hard)",
			"INJECTS":           "constructor DI, @UseGuards, @UseInterceptors (hard)",
			"EXPOSES_ENDPOINT":  "controller → @Get/@Post route (hard)",
			"CALLS_SERVICE":     "HTTP client → service via env URL (linked)",
			"CALLS_ENDPOINT":    "HTTP client → specific endpoint (linked)",
			"PUBLISHES_TOPIC":   "producer → Kafka topic (hard)",
			"CONSUMES_TOPIC":    "consumer ← Kafka topic (hard)",
			"USES_MODEL":        "service → Prisma model it queries (hard)",
			"REFERENCES_MODEL":  "model → model via @relation/FK (hard)",
			"DEFINES_MODEL":     "repo → model it defines (hard)",
		},
		"flow_edge_types": map[string]any{
			"WARNING":          "Use ONLY these edge types for flows. Do NOT invent types like 'CALLS', 'USES', 'INVOKES'. Those will be rejected.",
			"TRIGGERS_FLOW":    "endpoint/event → flow:use_case (what triggers this business process)",
			"REQUIRES":         "flow → code/data/service (what the flow depends on — use this instead of CALLS/USES/DEPENDS_ON)",
			"INVOKES":          "flow → code/service (alternative to REQUIRES — flow calls a service)",
			"PRODUCES_OUTCOME": "flow → contract/data (what the flow produces: events, state changes)",
			"PRECEDES":         "flow_step → flow_step (ordering within a flow)",
			"TRANSITIONS_TO":   "flow → flow (one use case leads to another)",
			"node_type":        "MUST be 'use_case' or 'flow' (not 'usecase' — underscore required, though both are now accepted)",
		},
		"call_oracle_extraction_guide_with_technology": "For detailed rules, call again with technology='nestjs', 'prisma', 'openapi', or 'flow'",
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

func detailedGuide(technology string) string {
	sections := map[string]any{}

	switch technology {
	case "nestjs", "typescript", "ts", "code":
		sections = map[string]any{
			"modules": map[string]string{
				"identify": "@Module decorator",
				"key":      "code:module:domain:modulename",
				"edges":    "CONTAINS → every provider/controller in @Module({providers:[...], controllers:[...]})",
			},
			"controllers": map[string]any{
				"identify": "@Controller('prefix') decorator",
				"key":      "code:controller:domain:controllername",
				"endpoints": map[string]string{
					"how":     "For EACH @Get/@Post/@Put/@Delete/@Patch method, create contract:endpoint node",
					"key":     "contract:endpoint:domain:method:/prefix/path",
					"edge":    "EXPOSES_ENDPOINT from controller to endpoint (hard)",
					"example": "@Controller('users') + @Post(':id/verify') → contract:endpoint:domain:post:/users/:id/verify",
				},
			},
			"providers": map[string]string{
				"identify": "@Injectable() — services, guards, interceptors, pipes, gateways",
				"key":      "code:provider:domain:servicename",
				"di":       "Constructor params → INJECTS edge to each injected provider",
			},
			"guards_interceptors": map[string]string{
				"guards":       "@UseGuards(X) on controller/method → INJECTS from controller to guard",
				"interceptors": "@UseInterceptors(X) → INJECTS from controller to interceptor",
				"middleware":   "configure(consumer) { consumer.apply(X) } → INJECTS from module to middleware",
			},
			"special": map[string]string{
				"websocket":      "@WebSocketGateway → code:provider. @SubscribeMessage → contract topic.",
				"cron":           "@Cron → code:provider (extract as provider)",
				"event_emitter":  "@OnEvent → code:provider",
				"bull_queue":     "@Processor/@Process → code:provider",
				"shared_lib":     "packages/ dirs → code:package node",
			},
		}

	case "prisma", "data", "models":
		sections = map[string]any{
			"models": map[string]any{
				"identify": "model X { ... } blocks in schema.prisma",
				"key":      "data:model:domain:modelname (lowercase)",
				"evidence": "schema file + line number of model declaration",
			},
			"enums": map[string]any{
				"identify": "enum X { ... } blocks",
				"key":      "data:enum:domain:enumname",
				"edges":    "Create USES_ENUM edge from each model that has a field of this enum type. E.g. Character has 'mood CatMood' → Character USES_ENUM CatMood",
			},
			"relations": map[string]any{
				"how": "Look for @relation directive and array fields",
				"examples": []string{
					"owner Cat @relation(fields:[ownerId]) → CatWeapon REFERENCES_MODEL Cat",
					"weapons CatWeapon[] → Cat REFERENCES_MODEL CatWeapon",
					"tomId String (no @relation but refers to Cat) → BattleEvent REFERENCES_MODEL Cat (derivation: linked)",
				},
			},
			"usage": map[string]string{
				"how":     "Services that call prisma.model.findMany() etc → USES_MODEL edge",
				"edge":    "code:provider → data:model (hard)",
				"define":  "Repo that contains the schema → DEFINES_MODEL edge to each model",
			},
		}

	case "openapi":
		sections = map[string]any{
			"endpoints": map[string]string{
				"identify": "Each method+path under 'paths' in openapi.yaml",
				"key":      "contract:endpoint:domain:method:/path",
			},
			"api": map[string]string{
				"identify": "Top-level 'info' section",
				"key":      "contract:http_api:domain:apiname",
			},
		}

	case "flow", "flows", "use_cases", "usecases":
		sections = map[string]any{
			"what": "Business use cases — the WHY behind the code. Not code structure, but what the system DOES.",
			"how_to_extract": map[string]any{
				"step_1": "Identify key user actions/business processes: PlaceOrder, UserSignup, ProcessPayment, SendNotification",
				"step_2": "For each use case, trace the call chain: which endpoint triggers it → which services execute it → which models it reads/writes → what events/outcomes it produces",
				"step_3": "Create nodes and edges per use case",
			},
			"node_types": map[string]any{
				"use_case": map[string]string{
					"key":     "flow:use_case:domain:placeorder",
					"what":    "A complete business process triggered by a user action or event",
					"example": "PlaceOrder, UserSignup, ProcessRefund, SendDailyReport",
				},
				"flow_step": map[string]string{
					"key":     "flow:flow_step:domain:placeorder.validate-cart",
					"what":    "A step within a use case",
					"example": "ValidateCart, ChargePayment, CreateOrderRecord, SendConfirmation",
				},
				"trigger": map[string]string{
					"key":     "flow:trigger:domain:post:/orders",
					"what":    "What initiates the use case (endpoint, event, cron)",
				},
				"outcome": map[string]string{
					"key":     "flow:outcome:domain:order-created-event",
					"what":    "What the use case produces (event, state change, notification)",
				},
			},
			"edge_types": map[string]any{
				"TRIGGERS_FLOW": map[string]string{
					"from":    "contract:endpoint or contract:topic",
					"to":      "flow:use_case",
					"meaning": "This endpoint/event starts this business process",
					"example": "POST /orders → TRIGGERS_FLOW → PlaceOrder",
				},
				"REQUIRES": map[string]string{
					"from":    "flow:use_case",
					"to":      "code:provider or data:model or service:service",
					"meaning": "The use case depends on this service/model/service",
					"example": "PlaceOrder REQUIRES OrderService, REQUIRES Product model",
				},
				"PRODUCES_OUTCOME": map[string]string{
					"from":    "flow:use_case",
					"to":      "contract:topic or data:model",
					"meaning": "The use case produces this event or state change",
					"example": "PlaceOrder PRODUCES_OUTCOME order-created topic",
				},
				"PRECEDES": map[string]string{
					"from":    "flow:flow_step",
					"to":      "flow:flow_step",
					"meaning": "This step happens before that step",
				},
			},
			"example_flow": map[string]any{
				"name":    "PlaceOrder",
				"trigger": "POST /orders endpoint",
				"steps": []string{
					"1. ValidateCart → reads Cart model, checks MenuItem prices",
					"2. ChargePayment → calls PaymentService → calls external payment-api",
					"3. CreateOrder → writes Order model, OrderItem models",
					"4. SendConfirmation → publishes order-created topic",
				},
				"requires": []string{"OrderService", "PaymentService", "Cart model", "Order model", "MenuItem model"},
				"produces": []string{"order-created event", "Order record in DB"},
			},
		}

	default:
		return ExtractionGuide("") // fallback to compact guide
	}

	sections["common_mistakes"] = map[string]string{
		"missing_endpoints":    "Every @Get/@Post method MUST create an endpoint node",
		"missing_guard_edges":  "@UseGuards(X) MUST create INJECTS from controller to guard",
		"missing_contains":     "Every provider in @Module({providers:[...]}) MUST have CONTAINS edge",
		"accumulating_context": "Do NOT read many files then build one giant payload. Import after EACH file.",
	}

	data, _ := json.MarshalIndent(sections, "", "  ")
	return string(data)
}

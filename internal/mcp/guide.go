package mcp

import "encoding/json"

// ExtractionGuide returns the extraction methodology.
// If technology is empty, returns the compact workflow guide.
// If technology is set, returns detailed extraction rules for that tech.
func ExtractionGuide(technology string) string {
	// If specific tech requested, return detailed section
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
			"8. oracle_snapshot_create + oracle_stale_mark",
			"9. Domain language: call oracle_get_glossary. If empty, analyze the codebase and define key terms via oracle_define_term (entities, actions, concepts). Then call oracle_check_language for violations.",
			"10. oracle_report_discovery for unusual patterns, missing edges, quality assessment",
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
			"code":     "modules, controllers, providers, resolvers, guards, gateways → code:module, code:controller, code:provider",
			"contract": "HTTP endpoints, Kafka topics, GraphQL operations → contract:endpoint, contract:topic",
			"service":  "Deployable services → service:service",
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
		"call_oracle_extraction_guide_with_technology": "For detailed extraction rules, call this tool again with technology='nestjs', 'prisma', or 'openapi'",
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
			"enums": map[string]string{
				"identify": "enum X { ... } blocks",
				"key":      "data:enum:domain:enumname",
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

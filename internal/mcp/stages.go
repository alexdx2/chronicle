package mcp

import (
	"fmt"
	"strings"
)

// ScanStage represents one step in the scan pipeline.
// Types:
//   - "checkpoint" — presents options, waits for user choice
//   - "action"     — orchestrator executes automatically
//   - "agents"     — orchestrator spawns subagents, runs AfterAgents when done
type ScanStage struct {
	ID          string
	Name        string
	Type        string // "checkpoint", "action", "agents"
	Instruction string
	AgentModel  string // for "agents": role hint — "fast" or "strong" (orchestrator picks actual model)
	AfterAgents string // for "agents": orchestrator steps after agents finish
}

// AgentRole describes the capability needed, not a specific model.
// "fast"   — high-throughput extraction, pattern matching (e.g. Haiku, GPT-4o-mini, Gemini Flash)
// "strong" — reasoning, flow tracing, pack authoring (e.g. Sonnet, GPT-4o, Gemini Pro)

// scanStages is the complete ordered scan pipeline.
var scanStages = []ScanStage{
	// ─── Discovery ───
	{
		ID:   "discover",
		Name: "Discovery",
		Type: "action",
		Instruction: `Check chronicle_scan_status — note the build hash.
  Read package.json, go.mod, or similar to identify the project name and tech stack.
  Call chronicle_file_groups to see directory structure with file counts.
  Call chronicle_instruction_packs to get available instruction packs.

  After discovery, show a summary to the user:
    Project: [name]
    Tech: [detected stack]
    Services/apps: [list detected services with directory paths]
    Infrastructure: [any brokers, caches, databases detected]
    Files: [total count]

  This summary becomes the basis for the manifest. The user should confirm
  the detected structure is correct before proceeding to scope selection.`,
	},

	// ─── Checkpoint: Scope ───
	{
		ID:   "scope",
		Name: "Scope",
		Type: "checkpoint",
		Instruction: `Present 2-3 scan scope variants as compact cards.

  Each variant: [Letter]. [Name] — ~X files | Includes: [dirs] | Enables: [questions]

  Rules:
  - Recommend one variant with a concrete reason
  - Ask only: "Choose A/B/C."
  - After user chooses, proceed immediately to the next checkpoint. Do NOT ask "Continue?" or "A) Continue B) Change"`,
	},

	// ─── Checkpoint: Packs ───
	{
		ID:   "packs",
		Name: "Instruction packs",
		Type: "checkpoint",
		Instruction: `Show loaded packs (e.g. ✅ typescript, nestjs, prisma).

  If all detected techs are covered: say "All techs covered" and ask "Proceed? or change packs?"
  If gaps exist: recommend adding specific packs and ask "A) Add recommended B) Skip C) Custom"

  After user chooses, proceed immediately. Do NOT ask "Continue?" again.`,
	},

	// ─── Create missing packs ───
	{
		ID:         "create_packs",
		Name:       "Create missing instruction packs",
		Type:       "agents",
		AgentModel: "strong",
		Instruction: `For each confirmed MISSING pack, spawn ONE strong-model subagent.
  ⚠️ USE A STRONG MODEL (sonnet/gpt-4o/gemini-pro) — NOT a fast model.
  Each agent:
    1. Calls chronicle_get_instruction_pack(id="guide/pack_authoring")
    2. Reads 3-5 representative project files
    3. Writes a pack mapping patterns → core fact kinds
    4. Calls chronicle_save_custom_pack(id="custom/<tech>", content=<pack>)
    5. Reports the pack ID
  Skip this step if no packs are missing.`,
		AfterAgents: `Add created pack IDs to instruction_packs in the manifest.`,
	},

	// ─── Checkpoint: Scan quality ───
	{
		ID:   "scan_mode",
		Name: "Scan quality",
		Type: "checkpoint",
		Instruction: `Show scan profiles as compact A/B/C/D options:

  A. Fast — 1 agent, haiku | B. Balanced — 1 agent, sonnet ← RECOMMENDED
  C. Voting — 3 agents, haiku | D. Maximum — 3 agents, sonnet

  Show estimated reads for each. Ask: "Choose A/B/C/D."
  After user chooses, proceed immediately to final review. No "Continue?" confirmation.`,
	},

	// ─── Checkpoint: Final review ───
	{
		ID:   "final_review",
		Name: "Final review",
		Type: "checkpoint",
		Instruction: `Show the complete scan plan in one compact summary:

  Scope: [areas included]
  Packs: [list]
  Quality: [chosen profile]
  Excluded: [what's skipped]

  This scan will enable:
  - [3-5 concrete questions Chronicle can answer]

  "A) Start scan  B) Change scope  C) Change packs  D) Change quality"`,
	},

	// ─── Finalize setup ───
	{
		ID:   "finalize_setup",
		Name: "Finalize setup",
		Type: "action",
		Instruction: `Save manifest and discover files:
  a. Call chronicle_save_manifest with the approved manifest. Full manifest format:

     domains:
       - name: domain-name
         description: ...
         owner: ...
         scan:
           include: ["src/**"]
           exclude: ["**/*.test.ts"]

     tech: [nestjs, prisma, kafka]

     infrastructure:
       - name: kafka
         type: broker
         address: kafka:9092
         description: Event bus
       - name: redis
         type: cache
         address: redis:6379

     instruction_packs: [typescript, nestjs]

     Include all discovered domains with scan patterns, tech stack, and any infrastructure
     (brokers, caches, databases) the project uses. Infrastructure entries are imported as
     infra-layer nodes in the graph — agents will receive the infrastructure list in scan
     actions so they can link topics/queues to specific broker nodes.

  b. For each domain in the manifest, call chronicle_revision_create(domain, after_sha=HEAD, mode="full", trigger="manual")
  c. Call chronicle_discover_files(revision_id, votes_needed) — files are auto-assigned domain_key based on scan.include/exclude patterns
  d. Call chronicle_scan_next_file — it will return a CHECKPOINT with action="confirm"
     Show the checkpoint to the user and call chronicle_scan_confirm to proceed.
  e. Show discovery summary:
     "Discovered X files across N domains (Y git-tracked, Z excluded). Starting scan."`,
	},

	// ─── Phase 1: Extraction ───
	{
		ID:         "phase1",
		Name:       "Phase 1 — extraction",
		Type:       "agents",
		AgentModel: "fast",
		Instruction: `Worker pool pattern — you are the pool manager, NOT a worker.

  LOOP:
    1. Call chronicle_scan_pool_status(domain)
       -> returns { claimable_now, in_progress, completed, spawn_count, batch_size, ... }
    2. If claimable_now = 0 AND in_progress = 0: EXIT LOOP — all work done
    3. If claimable_now = 0 AND in_progress > 0: wait 10 seconds, go to 1
    4. Spawn spawn_count fast-model subagents in parallel

    Each agent (ONE batch, then DONE — agent must NOT loop):
      a. Call chronicle_scan_next_file(domain)
         -> returns { action: "extract_files", files_with_ast: [...] }
         Each file includes: obligation_id, path, domain_key, ast_facts, vote_group, vote_index
      b. For each file in the batch:
         - Read the file
         - Extract facts following the fact_schema
         - Call chronicle_file_extracted with:
           obligation_id, file_path, domain, revision_id, status, facts,
           vote_group, vote_index (pass exactly what scan_next_file returned)
      c. Agent is DONE — do NOT call scan_next_file again, do NOT loop

    5. Wait for ALL agents to finish
    6. Go back to step 1

  CRITICAL — HIERARCHY facts (structural backbone of the graph):
  1. "provides" — For every @Module file: emit for EVERY controller AND provider in its arrays.
     Set from_type="module". Missing provides = missing CONTAINS edges.
     Example: @Module({ controllers: [OrderController] }) -> {"kind":"provides","to":"OrderController"}
  2. "parent" — For every controller/provider: emit parent fact pointing to the owning module.
     Only emit when evidence is clear (declared in @Module, not just imported/used).
     Example: {"kind":"parent","to":"arena.module","reason":"declared in @Module.controllers"}
  3. "declares_service" — For every package.json with a server entrypoint: emit service declaration.
     Example: {"kind":"declares_service","to":"arena-api"}

  RATE LIMITS: If 429/overloaded, wait 10s and retry. Stagger agent launches by 2-3s.

  IMPORTANT:
  - chronicle_resolve_extractions WILL REFUSE to run if obligations are incomplete.
  - After each wave, check chronicle_scan_pool_status — it shows ready_to_resolve flag.
  - If subagents fail, process remaining obligations yourself (you have MCP tools).
  - Do NOT use chronicle_import_all as a fallback — it is for dev/debug only.
  - The scan is complete ONLY when ready_to_resolve = true.`,
		AfterAgents: `a. Call chronicle_resolve_extractions(domain, revision_id)
  b. Call chronicle_scan_pool_status(domain) to check next phase
  c. If action is "reconcile_endpoints" -> continue to endpoint reconciliation stage
  d. If action is "trace_flow" -> skip reconciliation, continue to flow tracing
  e. If action is "done" -> skip all remaining phases, finalize:
     1. chronicle_snapshot_create(domain, revision_id)
     2. chronicle_stale_mark(domain, revision_id)
     CRITICAL: Use the SAME revision_id for snapshot and stale_mark.`,
	},

	// ─── Phase 1.5: Endpoint reconciliation ───
	{
		ID:         "endpoint_reconcile",
		Name:       "Phase 1.5 — endpoint reconciliation",
		Type:       "agents",
		AgentModel: "strong",
		Instruction: `scan_next_file returned "reconcile_endpoints" with endpoint_reconcile context.
  This means some HTTP calls could not be automatically matched to known endpoints
  (e.g. client calls /users/usr-abc-123 but the endpoint is GET /users/:id).

  For each unmatched call, review the known_endpoints list and emit calls_endpoint facts
  for matches you can identify. Then call chronicle_file_extracted with the facts.

  Example — unmatched call:
    from: "UsersClient", path: "/v1/users/usr-abc-123", method: "GET"
    known_endpoints: ["GET /v1/users", "GET /v1/users/:id", "POST /v1/users"]
  → emit: {"kind": "calls_endpoint", "from": "UsersClient", "from_type": "provider", "target": "/v1/users/:id", "method": "GET"}

  Rules:
  - Match concrete values to parameterized paths (UUIDs, IDs, slugs → :param)
  - Respect HTTP method — GET /users/123 matches GET /users/:id, NOT POST /users/:id
  - If no match exists, skip it — don't force matches
  - If action is NOT "reconcile_endpoints": STOP — nothing to reconcile`,
		AfterAgents: `a. Call chronicle_resolve_extractions(domain, revision_id) to apply matched endpoints
  b. Call chronicle_scan_next_file(domain) to proceed to phase 2
  c. If action is "trace_flow" → continue to next stage
  d. If action is "done" → skip phase 2, finalize`,
	},

	// ─── Phase 2: Flow tracing ───
	{
		ID:         "phase2",
		Name:       "Phase 2 — flow tracing",
		Type:       "agents",
		AgentModel: "strong",
		Instruction: `scan_next_file returned "trace_flow" with flow_context.
  Spawn strong-model subagents (2-3). Each agent loops:
    1. Call chronicle_scan_next_file — each file includes a domain_key
    2. If action is "trace_flow": read trigger file + files_to_read, use the file's domain_key, emit flow facts, go to 1
    3. If action is NOT "trace_flow": STOP`,
		AfterAgents: `a. Call chronicle_resolve_extractions(domain, revision_id)
  b. Call chronicle_scan_next_file(domain) — should return done=true
  c. Finalize the graph:
     1. chronicle_snapshot_create(domain, revision_id) — captures current graph state
     2. chronicle_stale_mark(domain, revision_id) — marks nodes not seen in this revision as stale
     CRITICAL: Use the SAME revision_id for snapshot and stale_mark. Do NOT create a new revision.
  d. Report scan results to user`,
	},
}

// BuildScanStagesInstruction assembles the complete scan pipeline instruction.
func BuildScanStagesInstruction() string {
	var parts []string

	parts = append(parts, `WIZARD FORMAT RULES:
  - Present choices as A/B/C/D cards, not yes/no questions.
  - Each card: name, includes, enables (what questions can be answered), tradeoff.
  - ALWAYS recommend one option with a concrete repo-specific reason.
  - After each choice, confirm with "A) Continue  B) Change [thing]".
  - The user should be able to answer with a single letter.
  - Do NOT ask yes/no questions unless user chose Custom.

WORKER POOL PATTERN — applies to ALL agent stages:
  1. Call chronicle_scan_pool_status to get claimable count and spawn recommendation
  2. Spawn recommended number of agents — each agent processes ONE batch, then dies
  3. Wait for ALL agents to finish
  4. Check pool status again — if claimable > 0, spawn more agents
  5. When claimable = 0 and in_progress = 0: run the "AFTER AGENTS" steps
  6. Then proceed to the next stage`)

	stepNum := 0
	checkpointNum := 0

	for _, stage := range scanStages {
		var section string

		switch stage.Type {
		case "checkpoint":
			checkpointNum++
			section = fmt.Sprintf("── CHECKPOINT %d: %s ──\n  %s\n  ⛔ STOP. WAIT for user response.",
				checkpointNum, stage.Name, stage.Instruction)

		case "action":
			section = fmt.Sprintf("── %s ──\n  %s", stage.Name, stage.Instruction)

		case "agents":
			stepNum++
			roleDesc := expandRole(stage.AgentModel)
			section = fmt.Sprintf("STEP %d — %s (%s):\n  %s",
				stepNum, stage.Name, roleDesc, stage.Instruction)
			if stage.AfterAgents != "" {
				section += fmt.Sprintf("\n\n  ⚠️ AFTER ALL AGENTS FINISH — orchestrator does this:\n  %s",
					stage.AfterAgents)
			}
		}

		parts = append(parts, section)
	}

	return strings.Join(parts, "\n\n")
}

// expandRole converts a role hint into a human-readable description.
func expandRole(role string) string {
	switch role {
	case "fast":
		return "fast model — e.g. haiku, gpt-4o-mini, gemini-flash"
	case "strong":
		return "strong model — e.g. sonnet, gpt-4o, gemini-pro"
	default:
		return role + " subagents"
	}
}

// ─── Public API ─────────────────────────────────────────────────────────────

func GetScanStages() []ScanStage {
	return scanStages
}

func AddScanStage(stage ScanStage, afterID string) {
	if afterID == "" {
		scanStages = append(scanStages, stage)
		return
	}
	for i, existing := range scanStages {
		if existing.ID == afterID {
			scanStages = append(scanStages[:i+2], scanStages[i+1:]...)
			scanStages[i+1] = stage
			return
		}
	}
	scanStages = append(scanStages, stage)
}

// ─── Legacy compat ──────────────────────────────────────────────────────────

type Checkpoint struct {
	ID, Name, Instruction, AfterAction string
}

func BuildScanCheckpointsInstruction() string { return BuildScanStagesInstruction() }

func buildScanCommand() string {
	return strings.Replace(CommandInstructions["scan"], "__STAGES__", BuildScanStagesInstruction(), 1)
}

func GetScanCheckpoints() []Checkpoint {
	var cps []Checkpoint
	for _, s := range scanStages {
		if s.Type == "checkpoint" {
			cps = append(cps, Checkpoint{ID: s.ID, Name: s.Name, Instruction: s.Instruction})
		}
	}
	return cps
}

func AddScanCheckpoint(cp Checkpoint, afterID string) {
	AddScanStage(ScanStage{ID: cp.ID, Name: cp.Name, Type: "checkpoint", Instruction: cp.Instruction}, afterID)
}

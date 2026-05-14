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
	AgentModel  string // for "agents": "haiku", "sonnet"
	AfterAgents string // for "agents": orchestrator steps after agents finish
}

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
  Call chronicle_instruction_packs to get available instruction packs.`,
	},

	// ─── Checkpoint: Scope ───
	{
		ID:   "scope",
		Name: "Scope",
		Type: "checkpoint",
		Instruction: `Present 3-4 scan scope variants as A/B/C/D cards.

  Build variants from the discovered project structure. Each variant:
    [Letter]. [Name] — ~X files
    Includes: [concrete directories]
    Enables: [what questions Chronicle can answer after this scan]
    Skips: [what's excluded] or Tradeoff: [cost of including more]

  Typical variants:
  A. Core backend — API services, data schema, shared packages
  B. Backend + primary frontend — A plus the frontend most coupled to the API
  C. Full product graph — all source across all apps
  D. Custom — choose areas manually

  Rules:
  - ALWAYS recommend one variant with a concrete repo-specific reason
  - "Enables" is more important than file counts — show what questions the user can ask after scanning
  - Keep each card to 3-5 lines
  - Ask only: "Choose A/B/C/D."
  - Do NOT ask separate yes/no questions for each area

  After user chooses, show final scope summary and ask:
  "A) Continue  B) Change scope"`,
	},

	// ─── Checkpoint: Packs ───
	{
		ID:   "packs",
		Name: "Instruction packs",
		Type: "checkpoint",
		Instruction: `Present instruction pack strategy as A/B/C variants.

  Show loaded packs: ✅ typescript, nestjs, graphql, prisma

  Then variants:
  A. Use loaded packs only
     Faster. Extraction may miss [specific patterns] for [missing techs].

  B. Add recommended packs — [list names]
     Adds: [what each pack enables for the chosen scope].
     Recommended — packs only guide extraction, they don't modify code.

  C. Custom — choose packs manually

  Rules:
  - Recommend B when missing packs match the selected scope
  - For each recommended pack, say what it enables (e.g. "React: maps component→API calls")
  - Ask only: "Choose A/B/C."

  After user chooses, confirm:
  "Packs: [final list]. A) Continue  B) Change packs"`,
	},

	// ─── Create missing packs ───
	{
		ID:         "create_packs",
		Name:       "Create missing instruction packs",
		Type:       "agents",
		AgentModel: "sonnet",
		Instruction: `For each confirmed MISSING pack, spawn ONE sonnet subagent.
  ⚠️ USE A STRONG MODEL (sonnet/opus) — NOT haiku.
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
		Instruction: `Show how many agents per file and what it costs. Let user pick a number.

  Show a table:
  Agents | Reads    | Time     | Quality
  1      | X reads  | ~Y min   | fast iteration
  2      | 2X reads | ~Y min   | moderate
  3      | 3X reads | ~Y min   | reliable (agents vote)
  5      | 5X reads | ~Y min   | maximum consensus

  Make a recommendation: "I recommend N for this project because [reason]."
  Ask: "How many agents per file? (1/2/3/5)"

  The user answers with a number. Accept any positive integer.`,
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
		AgentModel: "haiku",
		Instruction: `Spawn haiku subagents.
  Each agent runs this loop:
    1. Call chronicle_scan_next_file — each file in the batch includes a domain_key
    2. Check "action" field:
       - "extract_files": read the files, use each file's domain_key when calling chronicle_file_extracted, go to 1
       - "call_resolve_extractions": call chronicle_resolve_extractions, go to 1
       - "wait": sleep a few seconds, go to 1
       - "done" or "trace_flow": STOP — agent is done
    3. Response includes fact_schema + instruction_packs — follow them exactly
    4. Each file includes a domain_key field — use it for tool calls on that file

  CRITICAL — CONTAINS edges (structural backbone of the graph):
  - For every @Module file: emit "provides" facts for EVERY controller AND provider declared in it
    → set from_type="module" so the resolver creates CONTAINS edges (module→controller, module→provider)
  - For every repository root: emit a "provides" fact pointing to its main module
  - Missing "provides" from module files = missing CONTAINS edges = broken graph structure
  Example: @Module({ controllers: [OrderController], providers: [OrderService] })
    → {"kind":"provides","to":"OrderController"} + {"kind":"provides","to":"OrderService"} with from_type="module"

  Parallelism:
  - votes=1: spawn 3-5 parallel haiku agents
  - votes>1: spawn votes_needed agents per file with vote_group/vote_index

  RATE LIMITS: If 429/overloaded, wait 10s and retry. Stagger launches by 2-3s.`,
		AfterAgents: `a. Call chronicle_resolve_extractions(domain, revision_id)
  b. Call chronicle_scan_next_file(domain) to check for phase 2
  c. If action is "trace_flow" → continue to next stage
  d. If action is "done" → skip phase 2, scan complete`,
	},

	// ─── Phase 2: Flow tracing ───
	{
		ID:         "phase2",
		Name:       "Phase 2 — flow tracing",
		Type:       "agents",
		AgentModel: "sonnet",
		Instruction: `scan_next_file returned "trace_flow" with flow_context.
  Spawn sonnet subagents (2-3). Each agent loops:
    1. Call chronicle_scan_next_file — each file includes a domain_key
    2. If action is "trace_flow": read trigger file + files_to_read, use the file's domain_key, emit flow facts, go to 1
    3. If action is NOT "trace_flow": STOP`,
		AfterAgents: `a. Call chronicle_resolve_extractions(domain, revision_id)
  b. Call chronicle_scan_next_file(domain) — should return done=true
  c. Report scan results to user`,
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

ORCHESTRATOR PATTERN — applies to ALL agent stages:
  1. Spawn agents — each file batch entry includes its own domain_key
  2. Wait for ALL agents to finish
  3. Run the "AFTER AGENTS" steps yourself — do NOT skip them
  4. Then proceed to the next stage`)

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
			section = fmt.Sprintf("STEP %d — %s (%s subagents):\n  %s",
				stepNum, stage.Name, stage.AgentModel, stage.Instruction)
			if stage.AfterAgents != "" {
				section += fmt.Sprintf("\n\n  ⚠️ AFTER ALL %s AGENTS FINISH — orchestrator does this:\n  %s",
					strings.ToUpper(stage.AgentModel), stage.AfterAgents)
			}
		}

		parts = append(parts, section)
	}

	return strings.Join(parts, "\n\n")
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

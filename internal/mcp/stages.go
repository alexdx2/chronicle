package mcp

import (
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/version"
)

// ScanStage represents one step in the scan pipeline.
// Types:
//   - "checkpoint" — presents options, waits for user choice
//   - "action"     — orchestrator executes automatically
//   - "agents"     — orchestrator spawns subagents, runs AfterAgents when done
//
// SoloInstruction is the no-subagent variant of Instruction, served to clients
// without a Task tool (Codex, Cursor, ...): the same artifact-pool mechanics,
// but the agent reads files and writes outbox artifacts itself. Stages whose
// Instruction is already client-neutral leave it empty.
type ScanStage struct {
	ID              string
	Name            string
	Type            string // "checkpoint", "action", "agents"
	Instruction     string
	SoloInstruction string // no-subagent variant; empty = Instruction works for both
	AgentModel      string // for "agents": role hint — "fast" or "strong" (orchestrator picks actual model)
	AfterAgents     string // for "agents": orchestrator steps after agents finish
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
		Instruction: `Discover the workspace by reading the full file tree.

  STEP 1 — Read the tree:
  a. Call chronicle_file_groups — returns EVERY directory with ALL filenames.
  b. Study the tree. From filenames, extensions, and directory structure alone you can identify:
     - Project boundaries (directories with build/dependency files)
     - Languages and frameworks (file extensions, config files, directory conventions)
     - Infrastructure (docker-compose, deployment manifests, connection configs)
     - What to exclude (node_modules, build output, generated code, .depbot/)
  c. Read a few key files to confirm what you inferred — build files, entry points,
     config files. Focus on files that reveal dependencies and architecture.

  STEP 2 — Build tech list:
  Read each project's dependency file (package.json dependencies, .csproj PackageReference,
  go.mod require, requirements.txt, etc.)
  From these, build a flat list of architectural technologies — things that define how
  code is structured, how services communicate, how data is stored.
  Each technology is a SEPARATE entry. Do not group them.
  Wrong: "ASP.NET Core / EF Core / SignalR / Kafka → dotnet"
  Right: "aspnet", "efcore", "signalr", "kafka" — each checked independently.

  STEP 3 — Match instruction packs:
  a. Call chronicle_instruction_packs — returns ALL packs with "match" sections.
  b. For EACH technology in your tech list, check if any pack's match criteria fits.

  STEP 4 — Show discovery to user:
    Workspace: [single project / monorepo / polyrepo]
    Projects found:
      1. [name] — [path] — [language/framework] — [role]
      2. ...
    Tech list: [every technology from step 2]
    Pack coverage:
      ✅ [tech] → [pack_id]
      ❌ [tech] → no pack
    Files: [total] ([excluded] excluded)`,
	},

	// ─── Manifest interview + scope ───
	{
		ID:   "manifest",
		Name: "Manifest confirmation",
		Type: "checkpoint",
		Instruction: `Show the manifest draft and ask ONE question.

  Present your discovery as a compact summary — projects, infra, scope, domain name.
  Pick the best scope yourself (recommend it, don't list options).

  Then ask exactly ONE thing: "Anything to add or change?"

  The user will either approve or tell you what's wrong.
  Do NOT save the manifest until the user approves.
  After approval, proceed immediately.`,
	},

	// ─── Checkpoint: Packs ───
	{
		ID:   "packs",
		Name: "Instruction packs",
		Type: "checkpoint",
		Instruction: `Show the pack coverage table from discovery (the ✅/❌ list).

  If ANY ❌ gaps exist:
    "These technologies have no pack: [list].
    Without packs, agents won't recognize [tech]-specific patterns — edges WILL be missed.
    Create packs for: [list]? (or skip — lower quality)"

    Do NOT proceed until the user says "create" or "skip."

  If all ✅: "All technologies covered."

  STOP HERE. Do not show scan quality options in this message.`,
	},

	// ─── Create missing packs ───
	{
		ID:         "create_packs",
		Name:       "Create missing instruction packs",
		Type:       "agents",
		AgentModel: "strong",
		Instruction: `For each confirmed MISSING pack, spawn ONE strong-model subagent.
  Each agent:
    1. Calls chronicle_get_instruction_pack(id="guide/pack_authoring")
    2. Reads 3-5 representative project files
    3. Writes a pack with a "## Match" section + pattern→fact mappings
    4. Calls chronicle_save_custom_pack(id="<tech-name>", content=<pack>)
       The pack is saved to .depbot/packs/<tech-name>.md
    5. Reports the pack ID
  Skip this step if no packs are missing.`,
		SoloInstruction: `For each confirmed MISSING pack, author it yourself:
    1. Call chronicle_get_instruction_pack(id="guide/pack_authoring")
    2. Read 3-5 representative project files
    3. Write a pack with a "## Match" section + pattern→fact mappings
    4. Call chronicle_save_custom_pack(id="<tech-name>", content=<pack>)
       The pack is saved to .depbot/packs/<tech-name>.md
    5. Note the pack ID
  Skip this step if no packs are missing.`,
		AfterAgents: `Add created pack IDs to tech list in the manifest.`,
	},

	// ─── Checkpoint: Scan quality ───
	{
		ID:   "scan_mode",
		Name: "Scan quality",
		Type: "checkpoint",
		Instruction: `Present scan profiles — you choose layout (table, cards, etc.). Artifact-pool only.

  Profiles (passes × model cost):
  A. Fast — 1 touch, cheap/fast model ← RECOMMENDED
  B. Balanced — 1 touch, expensive/strong model
  C. Voting — 3 touches, cheap/fast model
  D. Maximum — 3 touches, expensive/strong model

  Each touch = one independent read of the same file; multiple touches merge at resolve.
  Show estimated file reads (files × touches). Recommend one option with a short reason.
  If the user names a custom count (e.g. "5 touches cheap"), honor it.
  Ask ONE question, then stop.`,
		SoloInstruction: `Present scan profiles — you choose layout. Artifact-pool, solo mode.

  In solo mode YOU read every file yourself; multiple touches mean re-reading
  the same file multiple times and merging at resolve.

  A. Fast — 1 touch ← RECOMMENDED (solo re-reads rarely add signal)
  C. Voting — 3 touches (only if the user wants extra redundancy)

  Show estimated file reads (files × touches). Recommend A with a short reason.
  If the user names a custom count, honor it.
  Ask ONE question, then stop.`,
	},

	// ─── Finalize setup ───
	{
		ID:   "finalize_setup",
		Name: "Finalize setup",
		Type: "action",
		Instruction: `Save the confirmed manifest and prepare scan:

  a. Call chronicle_save_manifest with the user-approved manifest. Include:
     - domains with scan include/exclude patterns
     - tech stack
     - infrastructure (with type and address)
     - instruction_packs list
  b. For each domain, call chronicle_revision_create(domain, after_sha=HEAD, mode="full", trigger="manual")
  c. Call chronicle_discover_files(revision_id, votes_needed=<touches from scan_mode>)
     A/B = 1, C/D = 3, or custom if user named a number
  d. Call chronicle_scan_next_file — it will return a CHECKPOINT. Call chronicle_scan_confirm.
  e. Show: "Discovered X files, Y obligations (files × passes). Starting scan."`,
	},

	// ─── Phase 1: Extraction (artifact-pool) ───
	{
		ID:         "phase1",
		Name:       "Phase 1 — extraction",
		Type:       "agents",
		AgentModel: "fast",
		Instruction: `ARTIFACT POOL — you are the orchestrator. Subagents NEVER call Chronicle MCP.

  ATOMIC WAVE CONTRACT (no new claims until wave is committed):
    1. Call chronicle_scan_pool_status(domain)
    2. If wave_complete=true AND claimable_now=0 AND in_progress=0: EXIT LOOP
    3. If in_progress>0 and oldest_in_progress_sec > claim_ttl_minutes*60: call chronicle_scan_mark_failed on stuck IDs OR wait 10s
    4. Call chronicle_scan_checkout_batch(domain, limit=spawn_count*batch_size) ONCE for this wave
       Response is file-backed: read items_path (not inline items). max_wave_limit caps oversized batches.
    5. Spawn spawn_count subagents using the model from scan_mode (fast or strong) — disjoint batches

    SUBAGENT PROMPT (NO MCP — write JSON only):
      - Read fact_schema from fact_schema_path in checkout response (do NOT require inline fact_schema)
      - Read items from items_path in checkout response
      - Each item lists file_path, obligation_id, vote_group, vote_index
      - If deterministic_candidates_path is set, read candidates and merge into facts (accept high-confidence hints)
      - outbox_dir from checkout response
      - For EACH item, write ONE JSON artifact:
          { file_path, status, from_type, facts: [...], obligation_id, revision_id, domain,
            vote_group, vote_index }
        facts MUST be a JSON array (per fact_schema.md), NOT a quoted string.
        Filename: path/with/slashes → path__with__slashes.json (append .vN before .json when vote_index>1)
      - Subagent is DONE after writing artifacts — must NOT loop, must NOT call MCP

    6. Wait for ALL subagents to finish
    7. Call chronicle_commit_scan_outbox(domain, revision_id) — orchestrator single-writer commit
    8. Verify wave_complete=true in chronicle_scan_pool_status before starting next wave
    9. Go to step 1

  Fact kinds that build the graph backbone:
  - "provides" from @Module files (from_type="module")
  - "parent" when ownership is clear
  - "declares_service" for service entry points

  If subagents fail: read files yourself, write outbox JSON, then commit_scan_outbox.
  If MCP stops responding: STOP and tell the user to restart.
  Do NOT use chronicle_import_all during scans.`,
		SoloInstruction: `SOLO EXTRACTION — you are the sole extractor. No other agents are involved.

  ATOMIC WAVE CONTRACT (no new claims until the wave is committed):
    1. Call chronicle_scan_pool_status(domain)
    2. If wave_complete=true AND claimable_now=0 AND in_progress=0: EXIT LOOP
    3. If in_progress>0 and oldest_in_progress_sec > claim_ttl_minutes*60: call chronicle_scan_mark_failed on stuck IDs OR wait 10s
    4. Call chronicle_scan_checkout_batch(domain, limit=10) ONCE for this wave
       Response is file-backed: read items_path (not inline items). Read fact_schema from fact_schema_path.
    5. For EACH item in the batch:
       - Read the source file at file_path
       - Extract facts per fact_schema.md
       - If deterministic_candidates_path is set, read candidates and merge into facts (accept high-confidence hints)
       - Write ONE JSON artifact to outbox_dir:
           { file_path, status, from_type, facts: [...], obligation_id, revision_id, domain,
             vote_group, vote_index }
         facts MUST be a JSON array (per fact_schema.md), NOT a quoted string.
         Filename: path/with/slashes → path__with__slashes.json (append .vN before .json when vote_index>1)
    6. Call chronicle_commit_scan_outbox(domain, revision_id) — single-writer commit
    7. Verify wave_complete=true in chronicle_scan_pool_status before starting the next wave
    8. Go to step 1

  Fact kinds that build the graph backbone:
  - "provides" from @Module files (from_type="module")
  - "parent" when ownership is clear
  - "declares_service" for service entry points

  If MCP stops responding: STOP and tell the user to restart.
  Do NOT use chronicle_import_all during scans.`,
		AfterAgents: `a. Call chronicle_resolve_extractions(domain, revision_id, allow_degraded=true) if pool shows failed>0
     else chronicle_resolve_extractions(domain, revision_id)
  b. Call chronicle_scan_next_file — if checkpoint phase1_review, show quality_warnings + review_candidates
     and call chronicle_scan_confirm(checkpoint_id='phase1_review', answer='proceed' or 're-review files')
  c. If checkpoint phase1_summary (phase 2), show user and call chronicle_scan_confirm
  d. Continue per scan_next_file action (endpoint_reconcile / trace_flow / done)`,
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
  for matches you can identify. Write outbox JSON artifacts, then commit_scan_outbox.

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
  Artifact-pool for flow tracing — orchestrator checks out, subagents write outbox JSON (no MCP):
    1. chronicle_scan_checkout_batch(domain, obligation_type="trace_flow")
    2. Spawn strong-model subagents with flow fact_schema + files_to_read
    3. commit_scan_outbox after each wave
    4. Repeat until no trace_flow obligations remain`,
		SoloInstruction: `scan_next_file returned "trace_flow" with flow_context.
  Solo flow tracing — you trace each flow yourself:
    1. chronicle_scan_checkout_batch(domain, obligation_type="trace_flow")
    2. For each item: read the files in files_to_read, trace the flow end-to-end,
       write the outbox JSON artifact per the flow fact_schema
    3. commit_scan_outbox after each wave
    4. Repeat until no trace_flow obligations remain`,
		AfterAgents: `a. Call chronicle_resolve_extractions(domain, revision_id)
  b. Call chronicle_scan_next_file(domain) — should return done=true
  c. Finalize the graph:
     1. chronicle_snapshot_create(domain, revision_id) — captures current graph state
     2. chronicle_stale_mark(domain, revision_id) — marks nodes not seen in this revision as stale
     CRITICAL: Use the SAME revision_id for snapshot and stale_mark. Do NOT create a new revision.
  d. Report scan results to user`,
	},
}

const stagesInteractionRules = `INTERACTION RULES:
  - Present choices as compact A/B/C/D cards when applicable; layout is up to you.
  - ALWAYS recommend one option with a concrete reason.
  - After user makes a choice, proceed immediately. NEVER ask "Continue?" or "A) Continue B) Change".
  - Discovery shows structure first, technologies second.
  - Manifest must be shown to user and approved before saving.
  - Do NOT save manifest, start scan, or resolve without user confirmation.

CRITICAL — ONE CHECKPOINT PER MESSAGE:
  - Show ONE checkpoint, ask ONE question, then STOP. Say nothing else.
  - Do NOT mention, preview, or show any later checkpoints.
  - Do NOT combine checkpoint 1 + 2, or 2 + 3, etc.
  - Wait for the user to respond before moving to the next stage.
  - The user must never see two "CHECKPOINT" headers in a single message.`

// BuildScanStagesInstruction assembles the orchestrator (subagent) scan
// pipeline instruction — the default for clients with a Task tool.
func BuildScanStagesInstruction() string {
	return buildStagesInstruction(false)
}

// BuildSoloScanStagesInstruction assembles the solo scan pipeline instruction
// for clients without subagents (Codex, Cursor, ...): identical pool
// mechanics and checkpoints, but the agent extracts everything itself.
func BuildSoloScanStagesInstruction() string {
	return buildStagesInstruction(true)
}

func buildStagesInstruction(solo bool) string {
	var parts []string

	if solo {
		parts = append(parts, stagesInteractionRules+`

ARTIFACT POOL PATTERN — SOLO MODE (you are the sole extractor):
  1. YOU call Chronicle MCP (checkout_batch, commit_scan_outbox, pool_status)
     AND read the source files AND write the JSON artifacts yourself
  2. Artifacts go to .depbot/scan-outbox/{revision_id}/
  3. One atomic wave: checkout → extract → commit → verify wave_complete before next wave
  4. When ready_to_resolve=true: run the "AFTER EXTRACTION" steps`)
	} else {
		parts = append(parts, stagesInteractionRules+`

ARTIFACT POOL PATTERN — default for ALL agent stages:
  1. Orchestrator ONLY calls Chronicle MCP (checkout_batch, commit_scan_outbox, pool_status)
  2. Subagents read source files and write JSON to .depbot/scan-outbox/{revision_id}/
  3. One atomic wave: checkout → extract → commit → verify wave_complete before next wave
  4. When ready_to_resolve=true: run the "AFTER AGENTS" steps`)
	}

	stepNum := 0
	checkpointNum := 0

	for _, stage := range scanStages {
		instruction := stage.Instruction
		if solo && stage.SoloInstruction != "" {
			instruction = stage.SoloInstruction
		}

		var section string
		switch stage.Type {
		case "checkpoint":
			checkpointNum++
			section = fmt.Sprintf("── CHECKPOINT %d: %s ──\n  %s\n  ⛔ STOP HERE. Show ONLY this checkpoint. Ask ONE question. Do NOT show anything below this line until the user responds.",
				checkpointNum, stage.Name, instruction)

		case "action":
			section = fmt.Sprintf("── %s ──\n  %s", stage.Name, instruction)

		case "agents":
			stepNum++
			if solo {
				section = fmt.Sprintf("STEP %d — %s (solo — you do the extraction):\n  %s",
					stepNum, stage.Name, instruction)
				if stage.AfterAgents != "" {
					section += fmt.Sprintf("\n\n  ⚠️ AFTER EXTRACTION — do this:\n  %s",
						stage.AfterAgents)
				}
			} else {
				section = fmt.Sprintf("STEP %d — %s (%s):\n  %s",
					stepNum, stage.Name, expandRole(stage.AgentModel), instruction)
				if stage.AfterAgents != "" {
					section += fmt.Sprintf("\n\n  ⚠️ AFTER ALL AGENTS FINISH — orchestrator does this:\n  %s",
						stage.AfterAgents)
				}
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
	s := CommandInstructions["scan"]
	s = strings.Replace(s, "__MCP_PREFLIGHT__", version.ScanPreflightBlock(), 1)
	s = strings.Replace(s, "__STAGES__", BuildScanStagesInstruction(), 1)
	return s
}

func buildSoloScanCommand() string {
	s := soloScanCommandTemplate
	s = strings.Replace(s, "__MCP_PREFLIGHT__", version.ScanPreflightBlock(), 1)
	s = strings.Replace(s, "__STAGES__", BuildSoloScanStagesInstruction(), 1)
	return s
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

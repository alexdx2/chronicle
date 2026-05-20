#!/bin/bash
# E2E test: Claude handles a breaking schema change using Chronicle for impact analysis
#
# Scenario: Rename Cat.health → Cat.hitPoints in Prisma schema
# This is a breaking change that affects TomService → TomController → 3 endpoints.
#
# What we study (not just assert):
#   - Does Claude check impact BEFORE coding? How deeply does it explore?
#   - Does Claude mention the affected services/endpoints by name?
#   - Does Claude update ALL affected files (schema + service + fallback)?
#   - Does Claude update the graph after? What exactly does it write?
#   - What's the full MCP call sequence? Any retries? Wasted calls?
#   - Does Claude's understanding match the actual graph data?
#
# Usage: ./e2e/breaking_change_agent_test.sh

set +e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CHRONICLE="$PROJECT_DIR/chronicle"
FIXTURE_DIR="$PROJECT_DIR/fixtures/tom-and-jerry"
WORK_DIR=$(mktemp -d)
RESULTS_DIR="$PROJECT_DIR/e2e/results"
ERRORS=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

pass() { echo -e "${GREEN}  ✓ $1${NC}"; }
fail() { echo -e "${RED}  ✗ $1${NC}"; ERRORS=$((ERRORS + 1)); }
info() { echo -e "${YELLOW}→ $1${NC}"; }
warn() { echo -e "  ${YELLOW}⚠ $1${NC}"; }
section() { echo -e "\n${CYAN}── $1 ──${NC}"; }
detail() { echo -e "  ${BOLD}$1${NC}"; }

cleanup() {
  echo ""
  info "Work dir preserved at: $WORK_DIR"
  info "Results saved to: $RESULTS_DIR/"
}
trap cleanup EXIT

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Breaking Change — Claude Agent E2E Test"
echo "══════════════════════════════════════════════════════"
echo "  Scenario: rename Cat.health → Cat.hitPoints"
echo "══════════════════════════════════════════════════════"

mkdir -p "$RESULTS_DIR"

# ─── Step 0: Build ───
section "Build"
cd "$PROJECT_DIR"
go build -o chronicle ./cmd/chronicle 2>/dev/null
pass "Chronicle binary built"

# ─── Step 1: Setup ───
section "Setup"
cp -r "$FIXTURE_DIR"/* "$WORK_DIR/"
cp -r "$FIXTURE_DIR/.depbot" "$WORK_DIR/.depbot" 2>/dev/null
cd "$WORK_DIR"

DB_PATH="$WORK_DIR/.depbot/chronicle.db"

# Baseline snapshot
NODES_BEFORE=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE status='active'" 2>/dev/null)
EDGES_BEFORE=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_edges WHERE active=1" 2>/dev/null)
CAT_NODE_ID=$(sqlite3 "$DB_PATH" "SELECT node_id FROM graph_nodes WHERE node_key='data:model:tomandjerry:cat'" 2>/dev/null)

# Run ground truth impact for comparison
GROUND_TRUTH_IMPACT=$("$CHRONICLE" impact data:model:tomandjerry:cat --depth 4 --db "$DB_PATH" 2>/dev/null)
GT_IMPACTED=$(echo "$GROUND_TRUTH_IMPACT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('total_impacted',0))" 2>/dev/null)
GT_NAMES=$(echo "$GROUND_TRUTH_IMPACT" | python3 -c "import sys,json; print(', '.join(i['name'] for i in json.load(sys.stdin).get('impacts',[])))" 2>/dev/null)
GT_ENDPOINTS=$(echo "$GROUND_TRUTH_IMPACT" | python3 -c "
import sys,json
d=json.load(sys.stdin)
eps = d.get('affected_surface',{}).get('endpoints',[])
print(', '.join(e['name'] for e in eps))
" 2>/dev/null)

info "Graph: $NODES_BEFORE nodes, $EDGES_BEFORE edges"
info "Ground truth impact from Cat: $GT_IMPACTED nodes ($GT_NAMES)"
info "Ground truth affected endpoints: $GT_ENDPOINTS"
pass "Fixture loaded with known impact chain"

# Snapshot files
SCHEMA_HASH_BEFORE=$(md5sum "$WORK_DIR/tom-api/prisma/schema.prisma" | cut -d' ' -f1)
SERVICE_HASH_BEFORE=$(md5sum "$WORK_DIR/tom-api/src/tom/tom.service.ts" | cut -d' ' -f1)

# ─── Step 2: Run Claude ───
section "Claude Breaking Change"

MCP_CONFIG="$WORK_DIR/mcp.json"
cat > "$MCP_CONFIG" << MCPEOF
{
  "mcpServers": {
    "chronicle": {
      "command": "$CHRONICLE",
      "args": ["mcp", "serve", "--db", "$DB_PATH", "--no-admin"]
    }
  }
}
MCPEOF

info "Asking Claude to rename Cat.health → Cat.hitPoints..."

CLAUDE_PROMPT="You have Chronicle MCP tools available. This is a Tom & Jerry NestJS project with Prisma.

YOUR TASK: Rename the field Cat.health to Cat.hitPoints across the entire codebase.

This is a BREAKING CHANGE. Before making any code changes, you MUST understand the blast radius.

REQUIRED WORKFLOW:

STEP 1 — IMPACT ANALYSIS (before any code changes):
  - Use chronicle_impact on the Cat model to see what's affected
  - Use chronicle_query_deps or chronicle_node_get to understand the dependency chain
  - List the affected services, controllers, and endpoints in your response

STEP 2 — MAKE THE CHANGES:
  - Rename health → hitPoints in the Prisma schema (tom-api/prisma/schema.prisma)
  - Update all references in tom-api/src/tom/tom.service.ts
  - Update any other files that reference Cat.health

STEP 3 — UPDATE THE GRAPH:
  - Use chronicle_import_all or chronicle_node_upsert to reflect the schema change
  - The Cat model's metadata or the field reference should show hitPoints, not health

After ALL steps, write a summary that includes:
  - How many services/endpoints are in the blast radius
  - Which specific files you changed and why
  - What you updated in the graph

Do NOT ask questions. Execute all steps."

cd "$WORK_DIR"
claude --print \
  --dangerously-skip-permissions \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  "$CLAUDE_PROMPT" > "$RESULTS_DIR/breaking_change_output.txt" 2>&1 || true

CLAUDE_EXIT=$?
OUTPUT_LINES=$(wc -l < "$RESULTS_DIR/breaking_change_output.txt")
OUTPUT=$(cat "$RESULTS_DIR/breaking_change_output.txt")
pass "Claude finished (exit: $CLAUDE_EXIT, $OUTPUT_LINES lines)"

# ═══════════════════════════════════════════════════
# DEEP ANALYSIS — not just pass/fail
# ═══════════════════════════════════════════════════

# ─── A: MCP Call Sequence Analysis ───
section "A. MCP Call Sequence"

TOTAL_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log" 2>/dev/null || echo "0")
info "Total MCP calls: $TOTAL_CALLS"

echo ""
detail "Full call timeline:"
sqlite3 "$DB_PATH" "
SELECT printf('    %2d. [%4dms] %-35s %s',
  rowid, duration_ms, tool_name,
  CASE
    WHEN error_message != '' AND error_message IS NOT NULL
    THEN '✗ ' || substr(error_message, 1, 80)
    ELSE substr(summary, 1, 80)
  END)
FROM mcp_request_log ORDER BY rowid
" 2>/dev/null

# Categorize calls
echo ""
detail "Call categories:"
READS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name IN ('chronicle_impact','chronicle_query_deps','chronicle_query_reverse_deps','chronicle_node_get','chronicle_edge_list','chronicle_node_list','chronicle_query_path','chronicle_query_stats','chronicle_schema')" 2>/dev/null || echo "0")
WRITES=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name IN ('chronicle_import_all','chronicle_node_upsert','chronicle_edge_upsert','chronicle_evidence_add','chronicle_revision_create')" 2>/dev/null || echo "0")
ERRS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE error_message != '' AND error_message IS NOT NULL" 2>/dev/null || echo "0")
echo "    Read/query:  $READS"
echo "    Write/update: $WRITES"
echo "    Errors:       $ERRS"

# Identify retries
RETRIES=$(sqlite3 "$DB_PATH" "
SELECT COUNT(*) FROM mcp_request_log r1
WHERE r1.error_message != '' AND r1.error_message IS NOT NULL
AND EXISTS (
  SELECT 1 FROM mcp_request_log r2
  WHERE r2.tool_name = r1.tool_name
    AND r2.rowid > r1.rowid
    AND r2.rowid <= r1.rowid + 3
    AND (r2.error_message = '' OR r2.error_message IS NULL)
)
" 2>/dev/null || echo "0")
echo "    Retries:      $RETRIES"

# Wasted calls (same tool+params called twice successfully)
echo ""
detail "Duplicate calls (same tool, same result):"
sqlite3 "$DB_PATH" "
SELECT printf('    %s called %d times (result: %s)', tool_name, cnt, summary)
FROM (
  SELECT tool_name, summary, COUNT(*) as cnt
  FROM mcp_request_log
  WHERE error_message = '' OR error_message IS NULL
  GROUP BY tool_name, summary
  HAVING cnt > 1
)
" 2>/dev/null || echo "    (none)"

# ─── B: Impact Understanding ───
section "B. Impact Understanding"

# Did Claude call impact?
IMPACT_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_impact'" 2>/dev/null || echo "0")
if [ "$IMPACT_CALLS" -ge 1 ]; then
  pass "Called chronicle_impact ($IMPACT_CALLS times)"

  # What did Claude query impact on?
  detail "Impact queries:"
  sqlite3 "$DB_PATH" "
  SELECT printf('    target: %s → result: %s',
    json_extract(params_json, '$.node_key'), summary)
  FROM mcp_request_log WHERE tool_name='chronicle_impact'
  " 2>/dev/null

  # Did Claude get the Cat model impact?
  CAT_IMPACT_CALLED=$(sqlite3 "$DB_PATH" "
  SELECT COUNT(*) FROM mcp_request_log
  WHERE tool_name='chronicle_impact'
    AND (params_json LIKE '%cat%' OR params_json LIKE '%Cat%')
  " 2>/dev/null || echo "0")
  if [ "$CAT_IMPACT_CALLED" -ge 1 ]; then
    pass "Queried impact specifically on Cat model"
  else
    fail "Didn't query impact on Cat model (queried something else)"
  fi
else
  fail "Never called chronicle_impact"
fi

# Did Claude mention the affected components?
echo ""
detail "Claude's impact awareness (checking output for key names):"
for NAME in TomService TomController; do
  if echo "$OUTPUT" | grep -qi "$NAME"; then
    pass "Mentioned $NAME as affected"
  else
    fail "Didn't mention $NAME (ground truth: it IS affected)"
  fi
done

# Check if Claude mentioned endpoints
ENDPOINT_MENTIONS=0
for EP in "/tom/status" "/tom/weapons" "/tom/arm"; do
  if echo "$OUTPUT" | grep -qi "$EP"; then
    ENDPOINT_MENTIONS=$((ENDPOINT_MENTIONS + 1))
  fi
done
if [ "$ENDPOINT_MENTIONS" -ge 2 ]; then
  pass "Mentioned $ENDPOINT_MENTIONS/3 affected endpoints"
elif [ "$ENDPOINT_MENTIONS" -ge 1 ]; then
  warn "Mentioned only $ENDPOINT_MENTIONS/3 affected endpoints"
else
  warn "Didn't mention specific affected endpoints"
fi

# Compare Claude's impact count to ground truth
CLAUDE_IMPACT_COUNT=$(echo "$OUTPUT" | grep -oP '\d+' | head -20 | sort -rn | head -1)
detail "Ground truth: $GT_IMPACTED impacted nodes. Claude's largest number mentioned: $CLAUDE_IMPACT_COUNT"

# ─── C: Dependency Exploration Depth ───
section "C. Dependency Exploration"

DEP_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name IN ('chronicle_query_deps','chronicle_query_reverse_deps')" 2>/dev/null || echo "0")
NODEGET_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_node_get'" 2>/dev/null || echo "0")
EDGELIST_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_edge_list'" 2>/dev/null || echo "0")
SCHEMA_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_schema'" 2>/dev/null || echo "0")

detail "Exploration breadth:"
echo "    chronicle_impact:        $IMPACT_CALLS calls"
echo "    chronicle_query_deps:    $DEP_CALLS calls"
echo "    chronicle_node_get:      $NODEGET_CALLS calls"
echo "    chronicle_edge_list:     $EDGELIST_CALLS calls"
echo "    chronicle_schema:        $SCHEMA_CALLS calls"

TOTAL_EXPLORE=$((IMPACT_CALLS + DEP_CALLS + NODEGET_CALLS + EDGELIST_CALLS))
if [ "$TOTAL_EXPLORE" -ge 3 ]; then
  pass "Deep exploration: $TOTAL_EXPLORE graph queries before coding"
elif [ "$TOTAL_EXPLORE" -ge 1 ]; then
  warn "Shallow exploration: only $TOTAL_EXPLORE graph queries"
else
  fail "No graph exploration at all"
fi

# What nodes did Claude inspect?
if [ "$NODEGET_CALLS" -ge 1 ]; then
  detail "Nodes inspected:"
  sqlite3 "$DB_PATH" "
  SELECT printf('    %s → %s',
    json_extract(params_json, '$.node_key'), summary)
  FROM mcp_request_log WHERE tool_name='chronicle_node_get'
  " 2>/dev/null
fi

# ─── D: Code Changes ───
section "D. Code Changes"

SCHEMA_FILE="$WORK_DIR/tom-api/prisma/schema.prisma"
SERVICE_FILE="$WORK_DIR/tom-api/src/tom/tom.service.ts"
CONTROLLER_FILE="$WORK_DIR/tom-api/src/tom/tom.controller.ts"

SCHEMA_HASH_AFTER=$(md5sum "$SCHEMA_FILE" | cut -d' ' -f1)
SERVICE_HASH_AFTER=$(md5sum "$SERVICE_FILE" | cut -d' ' -f1)

# Schema change
if [ "$SCHEMA_HASH_BEFORE" != "$SCHEMA_HASH_AFTER" ]; then
  pass "Prisma schema modified"
  if grep -q "hitPoints" "$SCHEMA_FILE"; then
    pass "Schema contains 'hitPoints'"
  else
    fail "Schema modified but 'hitPoints' not found"
  fi
  if grep -q "health" "$SCHEMA_FILE"; then
    fail "Schema still contains 'health' (incomplete rename)"
  else
    pass "Schema no longer contains 'health' (clean rename)"
  fi
else
  fail "Prisma schema NOT modified"
fi

# Service change
if [ "$SERVICE_HASH_BEFORE" != "$SERVICE_HASH_AFTER" ]; then
  pass "TomService modified"
  if grep -q "hitPoints" "$SERVICE_FILE"; then
    pass "Service uses 'hitPoints'"
  else
    warn "Service modified but 'hitPoints' not found"
  fi
  OLD_HEALTH_REFS=$(grep -c "health" "$SERVICE_FILE" 2>/dev/null; true)
  if [ "$OLD_HEALTH_REFS" -eq 0 ]; then
    pass "Service has zero remaining 'health' references"
  else
    warn "Service still has $OLD_HEALTH_REFS 'health' reference(s) — may be partial rename"
  fi
else
  fail "TomService NOT modified"
fi

# Check which files Claude changed overall
detail "File change summary:"
for F in \
  "tom-api/prisma/schema.prisma" \
  "tom-api/src/tom/tom.service.ts" \
  "tom-api/src/tom/tom.controller.ts" \
  "jerry-api/src/jerry/jerry.service.ts" \
  "arena-api/src/arena/arena.service.ts" \
; do
  FULL="$WORK_DIR/$F"
  if [ -f "$FULL" ]; then
    ORIG_HASH=$(cd "$FIXTURE_DIR" && md5sum "$F" 2>/dev/null | cut -d' ' -f1)
    NEW_HASH=$(md5sum "$FULL" | cut -d' ' -f1)
    if [ "$ORIG_HASH" != "$NEW_HASH" ]; then
      echo -e "    ${GREEN}CHANGED${NC}  $F"
    else
      echo -e "    ${YELLOW}unchanged${NC} $F"
    fi
  fi
done

# ─── E: Graph Update Quality ───
section "E. Graph Update"

# What did Claude write to the graph?
detail "Write operations:"
sqlite3 "$DB_PATH" "
SELECT printf('    %2d. %-30s %s',
  rowid, tool_name,
  CASE
    WHEN error_message != '' AND error_message IS NOT NULL
    THEN '✗ ' || substr(error_message, 1, 80)
    ELSE substr(COALESCE(summary, result_json), 1, 100)
  END)
FROM mcp_request_log
WHERE tool_name IN ('chronicle_import_all','chronicle_node_upsert','chronicle_edge_upsert','chronicle_evidence_add','chronicle_revision_create')
ORDER BY rowid
" 2>/dev/null

WRITE_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name IN ('chronicle_import_all','chronicle_node_upsert','chronicle_edge_upsert','chronicle_evidence_add','chronicle_revision_create')" 2>/dev/null || echo "0")

if [ "$WRITE_CALLS" -ge 1 ]; then
  pass "Claude updated the graph ($WRITE_CALLS write operations)"
else
  fail "Claude didn't update the graph"
fi

# Check if import_all had rejections
REJECTED=$(sqlite3 "$DB_PATH" "
SELECT result_json FROM mcp_request_log
WHERE tool_name='chronicle_import_all'
  AND result_json LIKE '%rejected%'
  AND result_json NOT LIKE '%\"rejected\":[]%'
" 2>/dev/null)
if [ -n "$REJECTED" ]; then
  warn "Some imports were rejected:"
  echo "$REJECTED" | python3 -c "
import sys,json
for line in sys.stdin:
  try:
    d = json.loads(line.strip())
    for r in d.get('rejected', []):
      print(f'    {r.get(\"error\", \"unknown\")}')
  except: pass
" 2>/dev/null
fi

# Check graph consistency after
NODES_AFTER=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE status='active'" 2>/dev/null)
EDGES_AFTER=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_edges WHERE active=1" 2>/dev/null)

detail "Graph delta: $NODES_BEFORE → $NODES_AFTER nodes, $EDGES_BEFORE → $EDGES_AFTER edges"

# Check if Cat model node was updated or a duplicate was created
CAT_NODES=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE node_key='data:model:tomandjerry:cat' AND status='active'" 2>/dev/null)
if [ "$CAT_NODES" -eq 1 ]; then
  pass "Cat model: exactly 1 node (no duplicates)"
elif [ "$CAT_NODES" -gt 1 ]; then
  fail "Cat model: $CAT_NODES nodes — duplicate created!"
  sqlite3 "$DB_PATH" "SELECT node_key, name FROM graph_nodes WHERE node_key='data:model:tomandjerry:cat' AND status='active'" 2>/dev/null
else
  warn "Cat model: 0 active nodes (was it deactivated?)"
fi

# ─── F: Workflow Order ───
section "F. Workflow Order"

FIRST_READ=$(sqlite3 "$DB_PATH" "
SELECT COALESCE(MIN(rowid), 999999) FROM mcp_request_log
WHERE tool_name IN ('chronicle_impact','chronicle_query_deps','chronicle_query_reverse_deps','chronicle_node_get','chronicle_edge_list','chronicle_node_list')
" 2>/dev/null || echo "999999")

FIRST_WRITE=$(sqlite3 "$DB_PATH" "
SELECT COALESCE(MIN(rowid), 0) FROM mcp_request_log
WHERE tool_name IN ('chronicle_import_all','chronicle_node_upsert','chronicle_edge_upsert')
" 2>/dev/null || echo "0")

LAST_READ=$(sqlite3 "$DB_PATH" "
SELECT COALESCE(MAX(rowid), 0) FROM mcp_request_log
WHERE tool_name IN ('chronicle_impact','chronicle_query_deps','chronicle_query_reverse_deps','chronicle_node_get','chronicle_edge_list','chronicle_node_list')
" 2>/dev/null || echo "0")

detail "Timeline:"
echo "    First read:  seq $FIRST_READ"
echo "    Last read:   seq $LAST_READ"
echo "    First write: seq $FIRST_WRITE"

if [ "$FIRST_READ" -ne 999999 ] && [ "$FIRST_WRITE" -ne 0 ] && [ "$FIRST_READ" -lt "$FIRST_WRITE" ]; then
  pass "Correct order: read ($FIRST_READ) → code → write ($FIRST_WRITE)"
else
  if [ "$FIRST_READ" -eq 999999 ]; then
    fail "No read operations"
  elif [ "$FIRST_WRITE" -eq 0 ]; then
    fail "No write operations"
  else
    fail "Wrong order: wrote to graph (seq $FIRST_WRITE) before reading (seq $FIRST_READ)"
  fi
fi

# Check if there were reads AFTER writes (verification pass?)
if [ "$LAST_READ" -gt "$FIRST_WRITE" ] && [ "$FIRST_WRITE" -ne 0 ]; then
  info "Claude did additional reads after writing — possible verification pass"
fi

# ─── G: Response Quality ───
section "G. Response Quality"

# Did Claude write a proper summary?
WORD_COUNT=$(echo "$OUTPUT" | wc -w)
detail "Response: $OUTPUT_LINES lines, $WORD_COUNT words"

# Check for structured analysis
if echo "$OUTPUT" | grep -qi "blast.radius\|impact.*analysis\|affected\|breaking"; then
  pass "Response discusses impact/blast radius"
else
  warn "Response doesn't explicitly discuss blast radius"
fi

if echo "$OUTPUT" | grep -qi "schema\|prisma\|model"; then
  pass "Response mentions schema/Prisma changes"
else
  warn "Response doesn't mention schema changes"
fi

if echo "$OUTPUT" | grep -qi "graph.*update\|import_all\|updated.*graph\|chronicle"; then
  pass "Response describes graph updates"
else
  warn "Response doesn't describe graph updates"
fi

# ═══ Final Scorecard ═══
echo ""
echo "══════════════════════════════════════════════════════"
echo "  SCORECARD"
echo "══════════════════════════════════════════════════════"
echo ""

# Calculate scores per area
echo "  MCP Usage:"
echo "    Total calls:     $TOTAL_CALLS"
echo "    Read calls:      $READS"
echo "    Write calls:     $WRITES"
echo "    Errors:          $ERRS"
echo "    Retries:         $RETRIES"
echo ""
echo "  Understanding:"
echo "    Impact called:   $([ "$IMPACT_CALLS" -ge 1 ] && echo "yes" || echo "NO")"
echo "    Correct target:  $([ "$CAT_IMPACT_CALLED" -ge 1 ] && echo "yes" || echo "NO")"
echo "    Named TomService: $(echo "$OUTPUT" | grep -qi "TomService" && echo "yes" || echo "no")"
echo "    Named TomController: $(echo "$OUTPUT" | grep -qi "TomController" && echo "yes" || echo "no")"
echo "    Named endpoints: $ENDPOINT_MENTIONS/3"
echo ""
echo "  Code Quality:"
echo "    Schema renamed:  $(grep -q "hitPoints" "$SCHEMA_FILE" 2>/dev/null && echo "yes" || echo "NO")"
echo "    Service updated: $([ "$SERVICE_HASH_BEFORE" != "$SERVICE_HASH_AFTER" ] && echo "yes" || echo "NO")"
echo "    No old refs:     $([ "$(grep -c 'health' "$SCHEMA_FILE" 2>/dev/null)" -eq 0 ] && echo "yes" || echo "partial")"
echo ""
echo "  Graph Hygiene:"
echo "    Updated graph:   $([ "$WRITE_CALLS" -ge 1 ] && echo "yes" || echo "NO")"
echo "    No duplicates:   $([ "$CAT_NODES" -eq 1 ] && echo "yes" || echo "NO")"
echo "    Read→Write order: $([ "$FIRST_READ" -lt "$FIRST_WRITE" ] 2>/dev/null && echo "yes" || echo "NO")"

echo ""
echo "══════════════════════════════════════════════════════"
if [ "$ERRORS" -eq 0 ]; then
  echo -e "${GREEN}  ALL CHECKS PASSED ✓${NC}"
else
  echo -e "${RED}  $ERRORS CHECK(S) FAILED${NC}"
fi
echo "══════════════════════════════════════════════════════"

# Save detailed results
cat > "$RESULTS_DIR/breaking_change_summary.json" << SUMEOF
{
  "timestamp": "$(date -Iseconds)",
  "errors": $ERRORS,
  "mcp": {
    "total_calls": $TOTAL_CALLS,
    "reads": $READS,
    "writes": $WRITES,
    "errors": $ERRS,
    "retries": $RETRIES
  },
  "impact": {
    "ground_truth_impacted": $GT_IMPACTED,
    "claude_called_impact": $([ "$IMPACT_CALLS" -ge 1 ] && echo "true" || echo "false"),
    "correct_target": $([ "$CAT_IMPACT_CALLED" -ge 1 ] && echo "true" || echo "false"),
    "mentioned_tomservice": $(echo "$OUTPUT" | grep -qi "TomService" && echo "true" || echo "false"),
    "mentioned_tomcontroller": $(echo "$OUTPUT" | grep -qi "TomController" && echo "true" || echo "false"),
    "endpoint_mentions": $ENDPOINT_MENTIONS
  },
  "code": {
    "schema_modified": $([ "$SCHEMA_HASH_BEFORE" != "$SCHEMA_HASH_AFTER" ] && echo "true" || echo "false"),
    "service_modified": $([ "$SERVICE_HASH_BEFORE" != "$SERVICE_HASH_AFTER" ] && echo "true" || echo "false"),
    "hitpoints_in_schema": $(grep -q "hitPoints" "$SCHEMA_FILE" 2>/dev/null && echo "true" || echo "false"),
    "health_removed_from_schema": $([ "$(grep -c 'health' "$SCHEMA_FILE" 2>/dev/null)" -eq 0 ] && echo "true" || echo "false")
  },
  "graph": {
    "nodes_before": $NODES_BEFORE,
    "nodes_after": $NODES_AFTER,
    "edges_before": $EDGES_BEFORE,
    "edges_after": $EDGES_AFTER,
    "write_calls": $WRITE_CALLS,
    "cat_node_count": $CAT_NODES,
    "read_before_write": $([ "$FIRST_READ" -lt "$FIRST_WRITE" ] 2>/dev/null && echo "true" || echo "false")
  }
}
SUMEOF

info "Results saved to e2e/results/"
echo "  → breaking_change_output.txt   (Claude's full response)"
echo "  → breaking_change_summary.json (detailed metrics)"

exit $ERRORS

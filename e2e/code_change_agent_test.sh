#!/bin/bash
# E2E test: Claude makes a code change using Chronicle for impact analysis
#
# Scenario: Add DELETE /tom/disarm endpoint to TomController
# Expected Claude behavior:
#   1. Check impact/deps of TomController BEFORE coding
#   2. Write the code change
#   3. Update the graph AFTER the change (import_all or node/edge upsert)
#
# Usage: ./e2e/code_change_agent_test.sh

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
NC='\033[0m'

pass() { echo -e "${GREEN}  ✓ $1${NC}"; }
fail() { echo -e "${RED}  ✗ $1${NC}"; ERRORS=$((ERRORS + 1)); }
info() { echo -e "${YELLOW}→ $1${NC}"; }
section() { echo -e "\n${CYAN}── $1 ──${NC}"; }

cleanup() {
  echo ""
  info "Work dir preserved at: $WORK_DIR"
  info "Results saved to: $RESULTS_DIR/"
}
trap cleanup EXIT

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Code Change — Claude Agent E2E Test"
echo "══════════════════════════════════════════════════════"

mkdir -p "$RESULTS_DIR"

# ─── Step 0: Build chronicle ───
section "Build"
cd "$PROJECT_DIR"
go build -o chronicle ./cmd/chronicle 2>/dev/null
pass "Chronicle binary built"

# ─── Step 1: Setup fixture ───
section "Setup"
cp -r "$FIXTURE_DIR"/* "$WORK_DIR/"
cp -r "$FIXTURE_DIR/.depbot" "$WORK_DIR/.depbot" 2>/dev/null
cd "$WORK_DIR"

DB_PATH="$WORK_DIR/.depbot/chronicle.db"

NODE_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE status='active'" 2>/dev/null || echo "0")
EDGE_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_edges WHERE active=1" 2>/dev/null || echo "0")
info "Starting graph: $NODE_COUNT nodes, $EDGE_COUNT edges"
pass "Fixture loaded"

# Snapshot graph state before Claude
NODES_BEFORE=$NODE_COUNT
EDGES_BEFORE=$EDGE_COUNT
ENDPOINTS_BEFORE=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE node_type='endpoint' AND status='active'" 2>/dev/null || echo "0")

# Snapshot file state
CONTROLLER_LINES_BEFORE=$(wc -l < "$WORK_DIR/tom-api/src/tom/tom.controller.ts")
SERVICE_LINES_BEFORE=$(wc -l < "$WORK_DIR/tom-api/src/tom/tom.service.ts")
info "Before: $ENDPOINTS_BEFORE endpoints, controller=$CONTROLLER_LINES_BEFORE lines, service=$SERVICE_LINES_BEFORE lines"

# ─── Step 2: Run Claude with task ───
section "Claude Code Change"

MCP_CONFIG="$WORK_DIR/mcp.json"
cat > "$MCP_CONFIG" << MCPEOF
{
  "mcpServers": {
    "chronicle": {
      "command": "$CHRONICLE",
      "args": ["mcp", "serve", "--project", "$WORK_DIR", "--no-admin"]
    }
  }
}
MCPEOF

info "Asking Claude to add DELETE /tom/disarm endpoint..."

CLAUDE_PROMPT="You have Chronicle MCP tools available. This is a Tom & Jerry battle simulation project (NestJS + Prisma).

YOUR TASK: Add a new endpoint DELETE /tom/disarm to TomController that removes all of Tom's weapons.

REQUIRED WORKFLOW — follow these steps IN ORDER:

STEP 1 — BEFORE coding:
  - Use chronicle_impact or chronicle_query_deps to understand what depends on TomController and TomService
  - Use chronicle_node_get to inspect the TomController node and see its current endpoints
  - This helps you understand the blast radius of your change

STEP 2 — Write the code:
  - Add a disarm() method to TomService in tom-api/src/tom/tom.service.ts
  - Add a DELETE /tom/disarm endpoint to TomController in tom-api/src/tom/tom.controller.ts
  - The endpoint should call tomService.disarm() which deletes all CatWeapon records for Tom

STEP 3 — AFTER coding, update the graph:
  - Use chronicle_import_all to add the new endpoint node and EXPOSES_ENDPOINT edge
  - The new endpoint node should be: layer=contract, node_type=endpoint, name='DELETE /tom/disarm'
  - Add EXPOSES_ENDPOINT edge from TomController to the new endpoint

Do NOT ask questions. Execute all 3 steps."

cd "$WORK_DIR"
claude --print \
  --dangerously-skip-permissions \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  "$CLAUDE_PROMPT" > "$RESULTS_DIR/code_change_output.txt" 2>&1 || true

CLAUDE_EXIT=$?
OUTPUT_LINES=$(wc -l < "$RESULTS_DIR/code_change_output.txt")
pass "Claude finished (exit: $CLAUDE_EXIT, $OUTPUT_LINES lines)"

# ─── Step 3: Verify BEFORE-phase (impact analysis) ───
section "Phase 1: Before — Impact Analysis"

# Did Claude query the graph before coding?
IMPACT_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name IN ('chronicle_impact', 'chronicle_query_deps', 'chronicle_query_reverse_deps')" 2>/dev/null || echo "0")
NODEGET_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_node_get'" 2>/dev/null || echo "0")
QUERY_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name IN ('chronicle_impact', 'chronicle_query_deps', 'chronicle_query_reverse_deps', 'chronicle_node_get', 'chronicle_edge_list', 'chronicle_node_list')" 2>/dev/null || echo "0")

if [ "$QUERY_CALLS" -ge 1 ]; then
  pass "Claude queried the graph before coding ($QUERY_CALLS query calls)"
else
  fail "Claude didn't query the graph before coding"
fi

if [ "$IMPACT_CALLS" -ge 1 ]; then
  pass "Claude ran impact/dependency analysis ($IMPACT_CALLS calls)"
else
  echo -e "  ${YELLOW}⚠ Claude didn't run chronicle_impact or chronicle_query_deps${NC}"
fi

if [ "$NODEGET_CALLS" -ge 1 ]; then
  pass "Claude inspected node details ($NODEGET_CALLS node_get calls)"
else
  echo -e "  ${YELLOW}⚠ Claude didn't call chronicle_node_get${NC}"
fi

# ─── Step 4: Verify CODING phase ───
section "Phase 2: Code Change"

# Check controller was modified
CONTROLLER_FILE="$WORK_DIR/tom-api/src/tom/tom.controller.ts"
SERVICE_FILE="$WORK_DIR/tom-api/src/tom/tom.service.ts"

CONTROLLER_LINES_AFTER=$(wc -l < "$CONTROLLER_FILE" 2>/dev/null || echo "0")
SERVICE_LINES_AFTER=$(wc -l < "$SERVICE_FILE" 2>/dev/null || echo "0")

if [ "$CONTROLLER_LINES_AFTER" -gt "$CONTROLLER_LINES_BEFORE" ]; then
  pass "Controller modified ($CONTROLLER_LINES_BEFORE → $CONTROLLER_LINES_AFTER lines)"
else
  fail "Controller not modified (still $CONTROLLER_LINES_BEFORE lines)"
fi

if [ "$SERVICE_LINES_AFTER" -gt "$SERVICE_LINES_BEFORE" ]; then
  pass "Service modified ($SERVICE_LINES_BEFORE → $SERVICE_LINES_AFTER lines)"
else
  fail "Service not modified (still $SERVICE_LINES_BEFORE lines)"
fi

# Check for disarm in controller
if grep -qi "disarm" "$CONTROLLER_FILE"; then
  pass "Controller contains 'disarm' method"
else
  fail "Controller missing 'disarm' method"
fi

# Check for Delete decorator
if grep -qi "@Delete\|delete" "$CONTROLLER_FILE"; then
  pass "Controller has DELETE decorator/method"
else
  fail "Controller missing DELETE decorator"
fi

# Check for disarm in service
if grep -qi "disarm" "$SERVICE_FILE"; then
  pass "Service contains 'disarm' method"
else
  fail "Service missing 'disarm' method"
fi

# Check service uses Prisma to delete weapons
if grep -qi "catWeapon\|deleteMany\|delete" "$SERVICE_FILE"; then
  pass "Service interacts with weapon data"
else
  echo -e "  ${YELLOW}⚠ Service may not properly delete weapons${NC}"
fi

# ─── Step 5: Verify AFTER phase (graph update) ───
section "Phase 3: After — Graph Update"

# Did Claude update the graph?
IMPORT_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_import_all'" 2>/dev/null || echo "0")
UPSERT_NODE_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_node_upsert'" 2>/dev/null || echo "0")
UPSERT_EDGE_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_edge_upsert'" 2>/dev/null || echo "0")
WRITE_CALLS=$((IMPORT_CALLS + UPSERT_NODE_CALLS + UPSERT_EDGE_CALLS))

if [ "$WRITE_CALLS" -ge 1 ]; then
  pass "Claude updated the graph after coding ($WRITE_CALLS write calls: import=$IMPORT_CALLS, node_upsert=$UPSERT_NODE_CALLS, edge_upsert=$UPSERT_EDGE_CALLS)"
else
  fail "Claude didn't update the graph after coding (0 write calls)"
fi

# Check if new endpoint exists in graph
DISARM_NODE=$(sqlite3 "$DB_PATH" "
  SELECT node_key FROM graph_nodes
  WHERE (node_key LIKE '%disarm%' OR name LIKE '%disarm%')
    AND status='active'
  LIMIT 1
" 2>/dev/null || echo "")

if [ -n "$DISARM_NODE" ]; then
  pass "New endpoint node created: $DISARM_NODE"
else
  fail "No 'disarm' endpoint node found in graph"
fi

# Check if EXPOSES_ENDPOINT edge exists for new endpoint
if [ -n "$DISARM_NODE" ]; then
  EXPOSE_EDGE=$(sqlite3 "$DB_PATH" "
    SELECT e.edge_key FROM graph_edges e
    JOIN graph_nodes fn ON e.from_node_id = fn.node_id
    JOIN graph_nodes tn ON e.to_node_id = tn.node_id
    WHERE tn.node_key = '$DISARM_NODE'
      AND e.edge_type = 'EXPOSES_ENDPOINT'
      AND e.active = 1
    LIMIT 1
  " 2>/dev/null || echo "")
  if [ -n "$EXPOSE_EDGE" ]; then
    pass "EXPOSES_ENDPOINT edge created: $EXPOSE_EDGE"
  else
    fail "No EXPOSES_ENDPOINT edge for disarm endpoint"
  fi
fi

# Check graph grew
NODES_AFTER=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE status='active'" 2>/dev/null || echo "0")
EDGES_AFTER=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_edges WHERE active=1" 2>/dev/null || echo "0")
ENDPOINTS_AFTER=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE node_type='endpoint' AND status='active'" 2>/dev/null || echo "0")

info "Graph: $NODES_BEFORE → $NODES_AFTER nodes, $EDGES_BEFORE → $EDGES_AFTER edges, $ENDPOINTS_BEFORE → $ENDPOINTS_AFTER endpoints"

if [ "$ENDPOINTS_AFTER" -gt "$ENDPOINTS_BEFORE" ]; then
  pass "Endpoint count increased ($ENDPOINTS_BEFORE → $ENDPOINTS_AFTER)"
else
  fail "Endpoint count didn't increase ($ENDPOINTS_BEFORE → $ENDPOINTS_AFTER)"
fi

# ─── Step 6: Verify workflow order ───
section "Workflow Order"

# Get the sequence of tool calls to verify order: query → (code) → write
FIRST_QUERY_SEQ=$(sqlite3 "$DB_PATH" "
  SELECT COALESCE(MIN(rowid), 999999) FROM mcp_request_log
  WHERE tool_name IN ('chronicle_impact', 'chronicle_query_deps', 'chronicle_query_reverse_deps', 'chronicle_node_get', 'chronicle_edge_list', 'chronicle_node_list')
" 2>/dev/null || echo "999999")

FIRST_WRITE_SEQ=$(sqlite3 "$DB_PATH" "
  SELECT COALESCE(MIN(rowid), 0) FROM mcp_request_log
  WHERE tool_name IN ('chronicle_import_all', 'chronicle_node_upsert', 'chronicle_edge_upsert')
" 2>/dev/null || echo "0")

if [ "$FIRST_QUERY_SEQ" -ne 999999 ] && [ "$FIRST_WRITE_SEQ" -ne 0 ] && [ "$FIRST_QUERY_SEQ" -lt "$FIRST_WRITE_SEQ" ]; then
  pass "Correct order: queried graph (seq $FIRST_QUERY_SEQ) before writing (seq $FIRST_WRITE_SEQ)"
else
  if [ "$FIRST_QUERY_SEQ" -eq 999999 ]; then
    fail "No query calls found"
  elif [ "$FIRST_WRITE_SEQ" -eq 0 ]; then
    fail "No write calls found"
  else
    fail "Wrong order: first write (seq $FIRST_WRITE_SEQ) before first query (seq $FIRST_QUERY_SEQ)"
  fi
fi

# ─── Step 7: MCP usage analysis ───
section "MCP Usage Analysis"

TOTAL_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log" 2>/dev/null || echo "0")
ERROR_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE error_message != '' AND error_message IS NOT NULL" 2>/dev/null || echo "0")
info "Total MCP calls: $TOTAL_CALLS (errors: $ERROR_CALLS)"

echo "  Call sequence:"
sqlite3 "$DB_PATH" "
SELECT printf('    %2d. %-35s %4dms  %s',
  rowid, tool_name, duration_ms,
  CASE WHEN error_message != '' AND error_message IS NOT NULL THEN '✗ ' || substr(error_message,1,60) ELSE summary END)
FROM mcp_request_log ORDER BY rowid
" 2>/dev/null

echo ""
echo "  Tool categories:"
QUERY_TOTAL=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name IN ('chronicle_impact','chronicle_query_deps','chronicle_query_reverse_deps','chronicle_node_get','chronicle_edge_list','chronicle_node_list','chronicle_query_path','chronicle_query_stats')" 2>/dev/null || echo "0")
WRITE_TOTAL=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name IN ('chronicle_import_all','chronicle_node_upsert','chronicle_edge_upsert','chronicle_evidence_add','chronicle_revision_create')" 2>/dev/null || echo "0")
OTHER_TOTAL=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name NOT IN ('chronicle_impact','chronicle_query_deps','chronicle_query_reverse_deps','chronicle_node_get','chronicle_edge_list','chronicle_node_list','chronicle_query_path','chronicle_query_stats','chronicle_import_all','chronicle_node_upsert','chronicle_edge_upsert','chronicle_evidence_add','chronicle_revision_create')" 2>/dev/null || echo "0")
echo "    Read/query: $QUERY_TOTAL"
echo "    Write/update: $WRITE_TOTAL"
echo "    Other: $OTHER_TOTAL"

if [ "$ERROR_CALLS" -eq 0 ]; then
  pass "Zero MCP errors"
elif [ "$ERROR_CALLS" -le 2 ]; then
  echo -e "  ${YELLOW}⚠ $ERROR_CALLS MCP error(s) — acceptable${NC}"
else
  fail "$ERROR_CALLS MCP errors (too many retries)"
fi

# ═══ Summary ═══
echo ""
echo "══════════════════════════════════════════════════════"
if [ "$ERRORS" -eq 0 ]; then
  echo -e "${GREEN}  ALL CHECKS PASSED ✓${NC}"
else
  echo -e "${RED}  $ERRORS CHECK(S) FAILED${NC}"
fi
echo "══════════════════════════════════════════════════════"

cat > "$RESULTS_DIR/code_change_summary.json" << SUMEOF
{
  "timestamp": "$(date -Iseconds)",
  "errors": $ERRORS,
  "nodes_before": $NODES_BEFORE,
  "nodes_after": $NODES_AFTER,
  "edges_before": $EDGES_BEFORE,
  "edges_after": $EDGES_AFTER,
  "endpoints_before": $ENDPOINTS_BEFORE,
  "endpoints_after": $ENDPOINTS_AFTER,
  "mcp_total_calls": $TOTAL_CALLS,
  "mcp_error_calls": $ERROR_CALLS,
  "mcp_query_calls": $QUERY_TOTAL,
  "mcp_write_calls": $WRITE_TOTAL,
  "query_before_write": $( [ "$FIRST_QUERY_SEQ" -lt "$FIRST_WRITE_SEQ" ] 2>/dev/null && echo "true" || echo "false" ),
  "disarm_endpoint_created": $( [ -n "$DISARM_NODE" ] && echo "true" || echo "false" ),
  "controller_modified": $( [ "$CONTROLLER_LINES_AFTER" -gt "$CONTROLLER_LINES_BEFORE" ] && echo "true" || echo "false" ),
  "service_modified": $( [ "$SERVICE_LINES_AFTER" -gt "$SERVICE_LINES_BEFORE" ] && echo "true" || echo "false" )
}
SUMEOF

info "Results saved to e2e/results/"
echo "  → code_change_output.txt   (Claude's full response)"
echo "  → code_change_summary.json (test results)"

exit $ERRORS

#!/bin/bash
# E2E test: Claude agent uses live-check to detect stale evidence after file changes
#
# Tests whether Claude:
# 1. Can query a node and see _changed flags from live-check
# 2. Understands the _changed output and explains what drifted
# 3. Knows to suggest rescanning the affected files
#
# Usage: ./e2e/live_check_agent_test.sh
#
# Prerequisites:
#   - chronicle binary built (or go installed to build it)
#   - claude CLI available and authenticated

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
echo "  Live Check — Claude Agent E2E Test"
echo "══════════════════════════════════════════════════════"

mkdir -p "$RESULTS_DIR"

# ─── Step 0: Build chronicle ───
section "Build"
cd "$PROJECT_DIR"
go build -o chronicle ./cmd/chronicle 2>/dev/null
pass "Chronicle binary built"

# ─── Step 1: Setup fixture with pre-built DB ───
section "Setup"
cp -r "$FIXTURE_DIR"/* "$WORK_DIR/"
cp -r "$FIXTURE_DIR/.depbot" "$WORK_DIR/.depbot" 2>/dev/null

DB_PATH="$WORK_DIR/.depbot/chronicle.db"

if [ ! -f "$DB_PATH" ]; then
  info "No pre-built DB, initializing and importing..."
  cd "$WORK_DIR"
  "$CHRONICLE" init > /dev/null 2>&1
  EXPECTED="$FIXTURE_DIR/expected-graph.json"
  if [ -f "$EXPECTED" ]; then
    "$CHRONICLE" import --file "$EXPECTED" --project "$WORK_DIR" > /dev/null 2>&1
    pass "Graph imported from expected-graph.json"
  else
    fail "No fixture DB and no expected-graph.json"
    exit 1
  fi
fi

# Verify graph has data
NODE_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE status='active'" 2>/dev/null || echo "0")
EDGE_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_edges WHERE active=1" 2>/dev/null || echo "0")
EVIDENCE_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_evidence" 2>/dev/null || echo "0")
info "Starting graph: $NODE_COUNT nodes, $EDGE_COUNT edges, $EVIDENCE_COUNT evidence rows"

if [ "$NODE_COUNT" -lt 5 ]; then
  fail "Graph too small ($NODE_COUNT nodes), cannot test live-check"
  exit 1
fi
pass "Fixture graph loaded"

# ─── Step 2: Seed assertion-backed evidence ───
section "Seed Evidence"

# The fixture DB may not have assertion-backed evidence.
# We'll seed a known assertion on TomController so live-check can verify it.

# The target: tom.controller.ts imports TomService
TARGET_NODE="code:controller:tomandjerry:tomcontroller"

# The file lives at tom-api/src/tom/tom.controller.ts in the fixture,
# but evidence file_path must be an absolute path for os.ReadFile to work.
TARGET_FILE="$WORK_DIR/tom-api/src/tom/tom.controller.ts"
ASSERTION_KIND="import_specifier"

if [ ! -f "$TARGET_FILE" ]; then
  fail "Target file not found: $TARGET_FILE"
  exit 1
fi

# Get the node_id for TomController
TARGET_NODE_ID=$(sqlite3 "$DB_PATH" "SELECT node_id FROM graph_nodes WHERE node_key='$TARGET_NODE'" 2>/dev/null || echo "0")
if [ "$TARGET_NODE_ID" = "0" ] || [ -z "$TARGET_NODE_ID" ]; then
  fail "TomController node not found in DB"
  exit 1
fi

# Seed assertion evidence: TomController imports TomService from ./tom.service
sqlite3 "$DB_PATH" "
INSERT INTO graph_evidence (
  target_kind, node_id, source_kind, file_path, line_start, line_end,
  extractor_id, extractor_version, confidence,
  evidence_status, evidence_polarity,
  assertion_kind, assertion, assertion_version,
  verification_status,
  observed_at
) VALUES (
  'node', $TARGET_NODE_ID, 'file', '$TARGET_FILE', 2, 2,
  'claude-code', '1.0', 0.95,
  'valid', 'positive',
  'import_specifier', '{\"module\": \"./tom.service\", \"symbols\": [\"TomService\"]}', 'v1',
  'unverified',
  strftime('%Y-%m-%dT%H:%M:%SZ','now')
);
" 2>/dev/null

SEEDED_ID=$(sqlite3 "$DB_PATH" "SELECT MAX(evidence_id) FROM graph_evidence" 2>/dev/null)
pass "Seeded assertion evidence (id: $SEEDED_ID) on $TARGET_NODE"
info "Assertion: import_specifier — TomService from ./tom.service"

# ─── Step 3: Mutate the source file ───
section "Mutate Source"

FULL_PATH="$TARGET_FILE"
ORIGINAL_CONTENT=$(cat "$FULL_PATH")
ORIGINAL_LINES=$(wc -l < "$FULL_PATH")
info "Original file: $ORIGINAL_LINES lines"

# Mutation: empty the file (all assertions will be missing)
echo "// FILE CLEARED FOR LIVE-CHECK TEST" > "$FULL_PATH"
pass "File mutated (cleared to 1 line)"

# ─── Step 4: Run Claude with live-check ───
section "Claude Live-Check Query"

MCP_CONFIG="$WORK_DIR/mcp.json"
cat > "$MCP_CONFIG" << MCPEOF
{
  "mcpServers": {
    "chronicle": {
      "command": "$CHRONICLE",
      "args": ["mcp", "serve", "--project", "$WORK_DIR", "--no-admin", "--live-check"]
    }
  }
}
MCPEOF

info "Running Claude to query the graph with --live-check enabled..."

QUERY_TARGET="$TARGET_NODE"

CLAUDE_PROMPT="You have Chronicle MCP tools available with live-check enabled.

Do these steps IN ORDER:

1. Call chronicle_node_get with node_key='$QUERY_TARGET' to inspect this node.
2. Look at the response carefully — especially the '_changed' field.
3. Explain what you found:
   - Is the evidence still valid or has something changed?
   - Which files have drifted?
   - What assertion_kind failed and why?
4. What action would you recommend to fix the stale evidence?

IMPORTANT: Your response MUST include:
- The word 'changed' or '_changed' if you see changed evidence
- The file path that drifted in your explanation
- A recommendation mentioning 'rescan' or 'update' or 'invalidate'

Do NOT ask questions. Execute immediately and report findings."

claude --print \
  --dangerously-skip-permissions \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  "$CLAUDE_PROMPT" > "$RESULTS_DIR/live_check_output.txt" 2>&1 || true

CLAUDE_EXIT=$?
OUTPUT_LINES=$(wc -l < "$RESULTS_DIR/live_check_output.txt")
pass "Claude finished (exit: $CLAUDE_EXIT, $OUTPUT_LINES lines)"

# ─── Step 5: Verify Claude's response ───
section "Verify Claude Output"

OUTPUT=$(cat "$RESULTS_DIR/live_check_output.txt")

# 5a: Did Claude call the right tool? (check MCP request log, not prose)
NODEGET_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name='chronicle_node_get'" 2>/dev/null || echo "0")
if [ "$NODEGET_CALLS" -ge 1 ]; then
  pass "Claude called chronicle_node_get ($NODEGET_CALLS times)"
else
  fail "Claude didn't call chronicle_node_get (0 calls in MCP log)"
fi

# 5b: Did Claude see the _changed flag?
if echo "$OUTPUT" | grep -qi "_changed\|changed.*evidence\|evidence.*changed\|stale\|drift"; then
  pass "Claude detected changed/stale evidence"
else
  fail "Claude didn't mention changed or stale evidence"
fi

# 5c: Did Claude mention the specific file?
TARGET_BASENAME=$(basename "$TARGET_FILE")
if echo "$OUTPUT" | grep -qi "$TARGET_BASENAME\|$TARGET_FILE"; then
  pass "Claude identified the affected file ($TARGET_BASENAME)"
else
  fail "Claude didn't mention the affected file ($TARGET_BASENAME)"
fi

# 5d: Did Claude identify the assertion kind?
if echo "$OUTPUT" | grep -qi "$ASSERTION_KIND\|import\|manifest\|decorator\|assertion"; then
  pass "Claude identified the assertion type ($ASSERTION_KIND)"
else
  fail "Claude didn't identify the assertion type"
fi

# 5e: Did Claude recommend action?
if echo "$OUTPUT" | grep -qi "rescan\|re-scan\|update\|invalidate\|re-extract\|scan.*again\|refresh"; then
  pass "Claude recommended remediation action"
else
  fail "Claude didn't recommend a rescan/update action"
fi

# 5f: Did Claude respond with useful explanation (not just raw data)?
if echo "$OUTPUT" | grep -qi "evidence\|import\|drift\|file"; then
  pass "Claude provided meaningful explanation of the results"
else
  fail "Claude response lacks explanation"
fi

# 5g: Check the actual MCP response had _changed
RESPONSE_HAS_CHANGED=$(sqlite3 "$DB_PATH" "
  SELECT CASE WHEN result_json LIKE '%_changed%' THEN 1 ELSE 0 END
  FROM mcp_request_log
  WHERE tool_name='chronicle_node_get'
  ORDER BY rowid DESC LIMIT 1
" 2>/dev/null || echo "0")
if [ "$RESPONSE_HAS_CHANGED" = "1" ]; then
  pass "MCP response contained _changed field (live-check worked)"
else
  fail "MCP response didn't contain _changed field"
  info "Last node_get response:"
  sqlite3 "$DB_PATH" "SELECT substr(result_json, 1, 500) FROM mcp_request_log WHERE tool_name='chronicle_node_get' ORDER BY rowid DESC LIMIT 1" 2>/dev/null
fi

# ─── Step 6: Restore and run clean query for comparison ───
section "Control: Clean Query"

# Restore original file
echo "$ORIGINAL_CONTENT" > "$FULL_PATH"
pass "File restored to original"

# Run another query — should have NO _changed
CLAUDE_CONTROL="You have Chronicle MCP tools. Call chronicle_node_get with node_key='$QUERY_TARGET'. Report whether you see any '_changed' field in the response. Just answer YES or NO and explain briefly."

claude --print \
  --dangerously-skip-permissions \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  "$CLAUDE_CONTROL" > "$RESULTS_DIR/live_check_control.txt" 2>&1 || true

CONTROL_OUTPUT=$(cat "$RESULTS_DIR/live_check_control.txt")

CONTROL_RESPONSE=$(sqlite3 "$DB_PATH" "
  SELECT CASE WHEN result_json LIKE '%_changed%' THEN 1 ELSE 0 END
  FROM mcp_request_log
  WHERE tool_name='chronicle_node_get'
  ORDER BY rowid DESC LIMIT 1
" 2>/dev/null || echo "1")

if [ "$CONTROL_RESPONSE" = "0" ]; then
  pass "Control: restored file has no _changed flag (clean)"
else
  fail "Control: restored file still shows _changed (verifier may be wrong)"
fi

# ─── Step 7: MCP tool usage analysis ───
section "MCP Usage Analysis"

TOTAL_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log" 2>/dev/null || echo "0")
ERROR_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE error_message != '' AND error_message IS NOT NULL" 2>/dev/null || echo "0")
info "Total MCP calls: $TOTAL_CALLS (errors: $ERROR_CALLS)"

echo "  Per-tool breakdown:"
sqlite3 "$DB_PATH" "
SELECT printf('    %-35s %3d calls  %3dms avg',
  tool_name, COUNT(*), AVG(duration_ms))
FROM mcp_request_log GROUP BY tool_name ORDER BY COUNT(*) DESC
" 2>/dev/null

# ═══ Summary ═══
echo ""
echo "══════════════════════════════════════════════════════"
if [ "$ERRORS" -eq 0 ]; then
  echo -e "${GREEN}  ALL CHECKS PASSED ✓${NC}"
else
  echo -e "${RED}  $ERRORS CHECK(S) FAILED${NC}"
fi
echo "══════════════════════════════════════════════════════"

cat > "$RESULTS_DIR/live_check_summary.json" << SUMEOF
{
  "timestamp": "$(date -Iseconds)",
  "target_file": "$TARGET_FILE",
  "target_node": "$QUERY_TARGET",
  "assertion_kind": "$ASSERTION_KIND",
  "errors": $ERRORS,
  "total_mcp_calls": $TOTAL_CALLS,
  "error_mcp_calls": $ERROR_CALLS,
  "mcp_response_had_changed": $RESPONSE_HAS_CHANGED,
  "control_clean": $( [ "$CONTROL_RESPONSE" = "0" ] && echo "true" || echo "false" )
}
SUMEOF

info "Results saved to e2e/results/"
echo "  → live_check_output.txt    (Claude's live-check response)"
echo "  → live_check_control.txt   (Claude's control response)"
echo "  → live_check_summary.json  (test results)"

exit $ERRORS

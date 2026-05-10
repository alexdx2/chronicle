#!/bin/bash
# E2E test: Claude agent builds a diagram from real graph entities
#
# Usage: ./e2e/diagram_agent_test.sh
#
# Prerequisites:
#   - chronicle binary built (or go installed)
#   - claude CLI available and authenticated
#   - fixtures/tom-and-jerry/.depbot/chronicle.db exists (pre-built graph)

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CHRONICLE="$PROJECT_DIR/chronicle"
FIXTURE_DIR="$PROJECT_DIR/fixtures/tom-and-jerry"
WORK_DIR=$(mktemp -d)
RESULTS_DIR="$PROJECT_DIR/e2e/results"
ERRORS=0

# Colors
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
  info "Work dir preserved at: $WORK_DIR"
}
trap cleanup EXIT

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Diagram Build — Claude Agent Smoke Test"
echo "══════════════════════════════════════════════════════"

mkdir -p "$RESULTS_DIR"

# ─── Step 0: Check prerequisites ───
section "Prerequisites"

if ! command -v claude &>/dev/null; then
  echo -e "${RED}claude CLI not found — skipping test${NC}"
  exit 0
fi
pass "claude CLI available"

if [ ! -f "$FIXTURE_DIR/.depbot/chronicle.db" ]; then
  fail "Pre-built fixture DB not found at $FIXTURE_DIR/.depbot/chronicle.db"
  exit 1
fi
pass "Fixture DB exists"

# ─── Step 1: Build chronicle ───
section "Build"
cd "$PROJECT_DIR"
go build -o chronicle ./cmd/chronicle 2>/dev/null
pass "Chronicle binary built"

# ─── Step 2: Setup work dir with pre-built graph ───
section "Setup"
cp -r "$FIXTURE_DIR"/* "$WORK_DIR/"
cp -r "$FIXTURE_DIR/.depbot" "$WORK_DIR/.depbot"
cd "$WORK_DIR"

DB_PATH="$WORK_DIR/.depbot/chronicle.db"

# Verify graph has data
NODE_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_nodes WHERE valid_to_revision_id IS NULL OR valid_to_revision_id = 0")
if [ "$NODE_COUNT" -lt 20 ]; then
  fail "Fixture DB has only $NODE_COUNT nodes (want >= 20)"
  exit 1
fi
pass "Fixture graph: $NODE_COUNT nodes"

# Create MCP config
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
pass "MCP config created"

# ─── Step 3: Run Claude ───
section "Claude Agent"
info "Asking Claude to build a system overview diagram..."

CLAUDE_PROMPT="You have Chronicle MCP tools available. The project graph is already populated with data.

Show me the service architecture of this project as a live diagram.

IMPORTANT RULES:
- Use chronicle_diagram_build to build the diagram (NOT chronicle_diagram_create + chronicle_diagram_update)
- Pass node_keys from real graph nodes (query them first with chronicle_node_list)
- This is a System Overview — show services and their connections
- Do NOT include data:model nodes or code:provider/code:controller nodes in the overview
- Do NOT ask questions. Execute immediately."

claude --print \
  --model haiku \
  --dangerously-skip-permissions \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  "$CLAUDE_PROMPT" > "$RESULTS_DIR/diagram_test_output.txt" 2>&1 || true

pass "Claude finished"
OUTPUT_LINES=$(wc -l < "$RESULTS_DIR/diagram_test_output.txt")
info "Output: $OUTPUT_LINES lines"

# ─── Step 4: Verify Tool Usage ───
section "Tool Usage"

# Check chronicle_diagram_build was called
BUILD_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name = 'chronicle_diagram_build'" 2>/dev/null || echo "0")
if [ "$BUILD_CALLS" -ge 1 ]; then
  pass "chronicle_diagram_build called ($BUILD_CALLS time(s))"
else
  fail "chronicle_diagram_build NOT called (want >= 1)"
fi

# Check chronicle_diagram_create was NOT called
CREATE_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name = 'chronicle_diagram_create'" 2>/dev/null || echo "0")
if [ "$CREATE_CALLS" -eq 0 ]; then
  pass "chronicle_diagram_create NOT called (correct)"
else
  fail "chronicle_diagram_create was called $CREATE_CALLS time(s) — should use diagram_build instead"
fi

# Check chronicle_diagram_update was NOT called
UPDATE_CALLS=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name = 'chronicle_diagram_update'" 2>/dev/null || echo "0")
if [ "$UPDATE_CALLS" -eq 0 ]; then
  pass "chronicle_diagram_update NOT called (correct)"
else
  fail "chronicle_diagram_update was called $UPDATE_CALLS time(s) — should use diagram_build instead"
fi

# ─── Step 5: Verify Node Selection ───
section "Node Selection"

if [ "$BUILD_CALLS" -eq 0 ]; then
  fail "Cannot verify nodes — diagram_build was not called"
else
  # Extract node_keys from the diagram_build call params
  NODE_KEYS=$(sqlite3 "$DB_PATH" "SELECT params_json FROM mcp_request_log WHERE tool_name = 'chronicle_diagram_build' ORDER BY id DESC LIMIT 1" 2>/dev/null)

  # Parse node_keys and run assertions
  python3 << PYEOF
import json, sys

params = json.loads('''$NODE_KEYS''')

# Get node_keys — might be a string (JSON) or already a list
node_keys_raw = params.get('node_keys', '[]')
if isinstance(node_keys_raw, str):
    node_keys = json.loads(node_keys_raw)
else:
    node_keys = node_keys_raw

print(f"  node_keys ({len(node_keys)}): {node_keys}")

errors = 0

# MUST INCLUDE: all 4 services (fuzzy match)
services = {
    'tom': False,
    'jerry': False,
    'arena': False,
    'spectator': False,
}
for key in node_keys:
    key_lower = key.lower()
    for svc in services:
        if svc in key_lower:
            services[svc] = True

for svc, found in services.items():
    if found:
        print(f"  \033[0;32m✓ Service '{svc}' found\033[0m")
    else:
        print(f"  \033[0;31m✗ Service '{svc}' MISSING\033[0m")
        errors += 1

# MUST NOT INCLUDE: data:model nodes
model_nodes = [k for k in node_keys if k.startswith('data:model:')]
if model_nodes:
    print(f"  \033[0;31m✗ data:model nodes found (should not be in overview): {model_nodes}\033[0m")
    errors += 1
else:
    print(f"  \033[0;32m✓ No data:model nodes (correct for overview)\033[0m")

# MUST NOT INCLUDE: code:provider or code:controller
low_level = [k for k in node_keys if k.startswith('code:provider:') or k.startswith('code:controller:')]
if low_level:
    print(f"  \033[0;31m✗ Low-level code nodes found: {low_level}\033[0m")
    errors += 1
else:
    print(f"  \033[0;32m✓ No provider/controller nodes (correct for overview)\033[0m")

# MAX COUNT: <= 15
if len(node_keys) <= 15:
    print(f"  \033[0;32m✓ Node count: {len(node_keys)} (<= 15)\033[0m")
else:
    print(f"  \033[0;31m✗ Node count: {len(node_keys)} (exceeds 15 limit)\033[0m")
    errors += 1

sys.exit(errors)
PYEOF

  PYEXIT=$?
  if [ "$PYEXIT" -eq 0 ]; then
    pass "All node selection assertions passed"
  else
    ERRORS=$((ERRORS + PYEXIT))
  fi
fi

# ═══ Summary ═══
echo ""
echo "══════════════════════════════════════════════════════"
if [ "$ERRORS" -eq 0 ]; then
  echo -e "${GREEN}  DIAGRAM TEST PASSED ✓${NC}"
else
  echo -e "${RED}  $ERRORS CHECK(S) FAILED${NC}"
fi
echo "══════════════════════════════════════════════════════"

exit $ERRORS

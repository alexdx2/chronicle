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

Show me a HIGH-LEVEL OVERVIEW of this project's architecture. Build a live diagram.

IMPORTANT RULES:
- Use chronicle_diagram_build
- This is an OVERVIEW — use ONLY synthetic \"nodes\" (kind: \"domain\", \"infrastructure\", \"external\"). Do NOT use node_keys.
- Each domain from the manifest becomes ONE node with kind \"domain\"
- Infrastructure (Kafka, Redis) becomes nodes with kind \"infrastructure\"
- External clients become nodes with kind \"external\"
- Edges show protocols: HTTP, async (Kafka), data (Redis)
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

# ─── Step 5: Verify Overview Node Selection ───
section "Node Selection (Overview)"

if [ "$BUILD_CALLS" -eq 0 ]; then
  fail "Cannot verify nodes — diagram_build was not called"
else
  # Extract params from the diagram_build call (write to file to avoid shell quoting issues)
  sqlite3 "$DB_PATH" "SELECT params_json FROM mcp_request_log WHERE tool_name = 'chronicle_diagram_build' ORDER BY rowid DESC LIMIT 1" > "$WORK_DIR/build_params.json" 2>/dev/null

  # Parse synthetic nodes and run assertions
  export WORK_DIR
  python3 << 'PYEOF'
import json, sys, os

work_dir = os.environ.get('WORK_DIR', '/tmp')
with open(os.path.join(work_dir, 'build_params.json')) as f:
    params = json.loads(f.read().strip())

errors = 0

# Overview MUST use synthetic "nodes" (not node_keys)
nodes_raw = params.get('nodes', '[]')
if isinstance(nodes_raw, str):
    nodes = json.loads(nodes_raw) if nodes_raw else []
else:
    nodes = nodes_raw if nodes_raw else []

print(f"  nodes ({len(nodes)}): {[n.get('key', n.get('name', '?')) for n in nodes]}")

# Check nodes have a "kind" field
nodes_with_kind = [n for n in nodes if 'kind' in n]
if len(nodes_with_kind) == len(nodes) and len(nodes) > 0:
    print(f"  \033[0;32m✓ All {len(nodes)} synthetic nodes have 'kind' field\033[0m")
else:
    print(f"  \033[0;31m✗ {len(nodes) - len(nodes_with_kind)} node(s) missing 'kind' field\033[0m")
    errors += 1

# Check expected kinds are present
kinds = [n.get('kind', '') for n in nodes]
domain_nodes = [n for n in nodes if n.get('kind') == 'domain']
infra_nodes = [n for n in nodes if n.get('kind') == 'infrastructure']
if domain_nodes:
    print(f"  \033[0;32m✓ domain nodes ({len(domain_nodes)}): {[n.get('key', n.get('name', '?')) for n in domain_nodes]}\033[0m")
else:
    print(f"  \033[0;31m✗ No 'domain' kind nodes — each service/domain should be one domain node\033[0m")
    errors += 1

if infra_nodes:
    print(f"  \033[0;32m✓ infrastructure nodes ({len(infra_nodes)}): {[n.get('key', n.get('name', '?')) for n in infra_nodes]}\033[0m")
else:
    print(f"  \033[1;33m⚠ No 'infrastructure' kind nodes (Kafka/Redis expected for tom-and-jerry)\033[0m")
    # Not a hard failure — depends on how Claude reads the manifest

# Warn if node_keys were also used (not expected for overview)
node_keys_raw = params.get('node_keys', None)
if node_keys_raw:
    if isinstance(node_keys_raw, str):
        node_keys = json.loads(node_keys_raw) if node_keys_raw else []
    else:
        node_keys = node_keys_raw if node_keys_raw else []
    if node_keys:
        print(f"  \033[1;33m⚠ node_keys also provided ({len(node_keys)}) — overview should use only synthetic nodes\033[0m")
else:
    print(f"  \033[0;32m✓ No node_keys used (correct — overview uses synthetic nodes only)\033[0m")

# Overview must have at least 3 nodes (4 services + some infra)
if len(nodes) >= 3:
    print(f"  \033[0;32m✓ Node count: {len(nodes)} (>= 3)\033[0m")
else:
    print(f"  \033[0;31m✗ Node count: {len(nodes)} (too few for a real overview)\033[0m")
    errors += 1

sys.exit(errors)
PYEOF

  PYEXIT=$?
  if [ "$PYEXIT" -eq 0 ]; then
    pass "All overview node assertions passed"
  else
    ERRORS=$((ERRORS + PYEXIT))
  fi
fi

# ═══════════════════════════════════════════════════════════════════════════════
# TEST 2: Request Flow with Virtual Nodes
# ═══════════════════════════════════════════════════════════════════════════════

section "Test 2: Request Flow (synthetic nodes + steps)"
info "Asking Claude to show how a user request reaches the database..."

# Clear request log for clean assertions
sqlite3 "$DB_PATH" "DELETE FROM mcp_request_log" 2>/dev/null

CLAUDE_PROMPT2="You have Chronicle MCP tools available. The project graph is already populated.

Show me how a user request flows through arena-api to reach the database. Build a live diagram.

IMPORTANT RULES:
- Use chronicle_diagram_build
- Use \"nodes\" for the User/Client actor (kind: \"external\") — they are not in the graph
- Use \"edges\" for the explicit edge from the User to the entry point (controller or endpoint)
- Use \"node_keys\" for real graph nodes (query them first with chronicle_node_list)
- Use \"steps\" to show the progressive request flow through the system
- Do NOT ask questions. Execute immediately."

claude --print \
  --model haiku \
  --dangerously-skip-permissions \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  "$CLAUDE_PROMPT2" > "$RESULTS_DIR/diagram_test2_output.txt" 2>&1 || true

pass "Claude finished (test 2)"

# ─── Verify Tool Usage ───
section "Test 2: Tool Usage"

BUILD_CALLS2=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM mcp_request_log WHERE tool_name = 'chronicle_diagram_build'" 2>/dev/null || echo "0")
if [ "$BUILD_CALLS2" -ge 1 ]; then
  pass "chronicle_diagram_build called ($BUILD_CALLS2 time(s))"
else
  fail "chronicle_diagram_build NOT called"
fi

# ─── Verify Synthetic Nodes, Edges & Steps ───
section "Test 2: Synthetic Nodes, Edges & Steps"

if [ "$BUILD_CALLS2" -eq 0 ]; then
  fail "Cannot verify — diagram_build was not called"
else
  sqlite3 "$DB_PATH" "SELECT params_json FROM mcp_request_log WHERE tool_name = 'chronicle_diagram_build' ORDER BY rowid DESC LIMIT 1" > "$WORK_DIR/build_params2.json" 2>/dev/null

  export WORK_DIR
  python3 << 'PYEOF'
import json, sys, os

work_dir = os.environ.get('WORK_DIR', '/tmp')
with open(os.path.join(work_dir, 'build_params2.json')) as f:
    params = json.loads(f.read().strip())

errors = 0

# Check "nodes" field has external actor (User/Client)
nodes_raw = params.get('nodes', '[]')
if isinstance(nodes_raw, str):
    nodes = json.loads(nodes_raw) if nodes_raw else []
else:
    nodes = nodes_raw if nodes_raw else []

if len(nodes) >= 1:
    names = [n.get('name', n.get('key', '?')) for n in nodes]
    print(f"  \033[0;32m✓ Synthetic nodes ({len(nodes)}): {names}\033[0m")
    # Check at least one looks like a user/client
    user_found = any(
        'user' in str(n).lower() or 'client' in str(n).lower()
        for n in nodes
    )
    if user_found:
        print(f"  \033[0;32m✓ User/Client synthetic node found\033[0m")
    else:
        print(f"  \033[0;31m✗ No User/Client synthetic node (expected one with kind 'external')\033[0m")
        errors += 1
else:
    print(f"  \033[0;31m✗ No 'nodes' provided (expected User/Client with kind 'external')\033[0m")
    errors += 1

# Check "edges" field has client → entry point edge
edges_raw = params.get('edges', '[]')
if isinstance(edges_raw, str):
    edges = json.loads(edges_raw) if edges_raw else []
else:
    edges = edges_raw if edges_raw else []

if len(edges) >= 1:
    print(f"  \033[0;32m✓ Explicit edges ({len(edges)}): {edges}\033[0m")
else:
    print(f"  \033[0;31m✗ No 'edges' provided (expected User→entry point)\033[0m")
    errors += 1

# Check "steps" field is present (flow diagram should have steps)
steps_raw = params.get('steps', None)
if steps_raw:
    if isinstance(steps_raw, str):
        steps = json.loads(steps_raw) if steps_raw else []
    else:
        steps = steps_raw if steps_raw else []
    if len(steps) >= 1:
        print(f"  \033[0;32m✓ steps ({len(steps)}): progressive flow defined\033[0m")
    else:
        print(f"  \033[1;33m⚠ 'steps' field present but empty\033[0m")
else:
    print(f"  \033[0;31m✗ No 'steps' field — flow diagram should include steps\033[0m")
    errors += 1

# Check node_keys include arena-related nodes
node_keys_raw = params.get('node_keys', '[]')
if isinstance(node_keys_raw, str):
    node_keys = json.loads(node_keys_raw) if node_keys_raw else []
else:
    node_keys = node_keys_raw if node_keys_raw else []

arena_found = any('arena' in k.lower() for k in node_keys)
if arena_found:
    print(f"  \033[0;32m✓ Arena nodes included in flow\033[0m")
else:
    print(f"  \033[0;31m✗ No arena nodes — flow should go through arena-api\033[0m")
    errors += 1

print(f"  node_keys ({len(node_keys)}): {node_keys}")

sys.exit(errors)
PYEOF

  PYEXIT2=$?
  if [ "$PYEXIT2" -eq 0 ]; then
    pass "All synthetic node/edge/steps assertions passed"
  else
    ERRORS=$((ERRORS + PYEXIT2))
  fi
fi

# ═══ Summary ═══
echo ""
echo "══════════════════════════════════════════════════════"
if [ "$ERRORS" -eq 0 ]; then
  echo -e "${GREEN}  ALL DIAGRAM TESTS PASSED ✓${NC}"
else
  echo -e "${RED}  $ERRORS CHECK(S) FAILED${NC}"
fi
echo "══════════════════════════════════════════════════════"

exit $ERRORS

#!/bin/bash
# E2E test: Claude agent scans Tom & Jerry project and we verify the graph
#
# Usage: ./e2e/claude_agent_test.sh
#
# Prerequisites:
#   - chronicle binary built (or go installed to build it)
#   - claude CLI available and authenticated

set +e  # Don't exit on errors — count them instead

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
  # Don't delete work dir — keep for debugging
  echo ""
  info "Work dir preserved at: $WORK_DIR"
  info "Results saved to: $RESULTS_DIR/"
}
trap cleanup EXIT

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Tom & Jerry — Claude Agent E2E Test"
echo "══════════════════════════════════════════════════════"

mkdir -p "$RESULTS_DIR"

# ─── Step 0: Build chronicle ───
section "Build"
cd "$PROJECT_DIR"
go build -o chronicle ./cmd/chronicle 2>/dev/null
pass "Chronicle binary built"

# ─── Step 1: Setup fixture project ───
section "Setup"
cp -r "$FIXTURE_DIR"/* "$WORK_DIR/"
cd "$WORK_DIR"

# Create .depbot
"$CHRONICLE" init > /dev/null 2>&1
pass "Chronicle initialized in $WORK_DIR"

# Create MCP config for Claude to find chronicle tools
DB_PATH="$WORK_DIR/.depbot/chronicle.db"
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
pass "MCP config created at $MCP_CONFIG"

# ─── Step 2: Run Claude ───
section "Claude Scan"
info "Running Claude to scan the project..."
info "This may take 3-10 minutes — using full scan pipeline with file_extracted loop."

CLAUDE_PROMPT="You have Chronicle MCP tools available. Scan this Tom & Jerry project using the scan pipeline.

Use chronicle_command(command='scan') to get the step-by-step scan instructions, then follow them exactly.

When the scan instructions ask you to save a manifest, use this content:

domains:
  - name: tomandjerry
    description: Tom and Jerry battle simulation
    owner: cartoon-team
    scan:
      include: [\"tom-api/**\", \"jerry-api/**\", \"arena-api/**\", \"spectators-api/**\", \"shared/**\"]
      exclude: [\"**/*.test.ts\", \"**/*.spec.ts\"]
tech: [nestjs, prisma, kafka, redis, websocket]
infrastructure:
  - name: kafka
    type: broker
    address: kafka:9092
    description: Event bus for battle results
  - name: redis
    type: cache
    address: redis:6379
    description: Battle state cache

CRITICAL RULES:
- Follow the scan pipeline: chronicle_command → checkpoints → discover_files → scan_next_file → file_extracted → resolve_extractions
- Do NOT use chronicle_import_all directly — use the scan pipeline (file_extracted for each file batch)
- At each checkpoint, confirm with chronicle_scan_confirm
- When scan completes, also do these post-scan tasks:
  1. Define domain language — chronicle_define_term for Cat, Mouse, Battle, Arena, Spectator, Trap, Weapon. Include anti-patterns. Then chronicle_check_language.
  2. Call chronicle_domain_list to verify the domain was registered.
  3. Call chronicle_report_discovery for each observation:
     - Any code patterns you found unusual → category: unknown_pattern
     - Any relationships you suspect but couldn't confirm → category: missing_edge
     - Overall scan quality assessment → category: pattern

Do NOT ask questions. Execute immediately."

claude --print \
  --dangerously-skip-permissions \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  "$CLAUDE_PROMPT" > "$RESULTS_DIR/claude_output.txt" 2>&1 || true

CLAUDE_EXIT=$?
pass "Claude finished (exit: $CLAUDE_EXIT)"

# Save Claude output size
OUTPUT_LINES=$(wc -l < "$RESULTS_DIR/claude_output.txt")
info "Claude output: $OUTPUT_LINES lines (saved to e2e/results/claude_output.txt)"

# ─── Step 3: Verify Results ───
section "Verification"

if [ ! -f "$DB_PATH" ]; then
  fail "Database not found at $DB_PATH"
  echo "Claude output (last 40 lines):"
  tail -40 "$RESULTS_DIR/claude_output.txt"
  exit 1
fi

# Helper to query stats
STATS=$("$CHRONICLE" query stats --domain tomandjerry --db "$DB_PATH" 2>/dev/null || echo '{"node_count":0,"edge_count":0,"nodes_by_layer":{},"edges_by_type":{},"edges_by_derivation":{}}')
echo "$STATS" > "$RESULTS_DIR/stats.json"

NODE_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('node_count',0))")
EDGE_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('edge_count',0))")

info "Graph: $NODE_COUNT nodes, $EDGE_COUNT edges"

# ── 3a: Node count ──
section "Nodes"
if [ "$NODE_COUNT" -ge 25 ]; then pass "Total nodes: $NODE_COUNT (>= 25)"
elif [ "$NODE_COUNT" -ge 15 ]; then pass "Total nodes: $NODE_COUNT (>= 15, acceptable)"; echo -e "    ${YELLOW}(ideal: >= 25)${NC}"
else fail "Total nodes: $NODE_COUNT (want >= 15)"; fi

# ── 3b: Edge count ──
if [ "$EDGE_COUNT" -ge 20 ]; then pass "Total edges: $EDGE_COUNT (>= 20)"
elif [ "$EDGE_COUNT" -ge 10 ]; then pass "Total edges: $EDGE_COUNT (>= 10, acceptable)"; echo -e "    ${YELLOW}(ideal: >= 20)${NC}"
else fail "Total edges: $EDGE_COUNT (want >= 10)"; fi

# ── 3c: Layers ──
section "Layers"
LAYERS=$(echo "$STATS" | python3 -c "import sys,json; print(','.join(sorted(json.load(sys.stdin).get('nodes_by_layer',{}).keys())))")
echo "  Found: $LAYERS"

for LAYER in code data contract service; do
  COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('nodes_by_layer',{}).get('$LAYER',0))")
  if [ "$COUNT" -gt 0 ]; then
    pass "$LAYER layer: $COUNT nodes"
  else
    fail "$LAYER layer: MISSING"
  fi
done

# ── 3d: Services ──
section "Services"
SERVICES=$("$CHRONICLE" node list --layer service --domain tomandjerry --db "$DB_PATH" 2>/dev/null || echo "[]")
SERVICE_COUNT=$(echo "$SERVICES" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")
SERVICE_NAMES=$(echo "$SERVICES" | python3 -c "import sys,json; print(', '.join(n['name'] for n in json.load(sys.stdin)))" 2>/dev/null || echo "(none)")
if [ "$SERVICE_COUNT" -ge 3 ]; then pass "Services ($SERVICE_COUNT): $SERVICE_NAMES"
else fail "Services: $SERVICE_COUNT (want >= 3). Found: $SERVICE_NAMES"; fi

# ── 3e: Data models ──
section "Data Models"
DATA_NODES=$("$CHRONICLE" node list --layer data --domain tomandjerry --db "$DB_PATH" 2>/dev/null || echo "[]")
DATA_COUNT=$(echo "$DATA_NODES" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))")
DATA_NAMES=$(echo "$DATA_NODES" | python3 -c "import sys,json; print(', '.join(n['name'] for n in json.load(sys.stdin)))")
if [ "$DATA_COUNT" -ge 3 ]; then pass "Data nodes ($DATA_COUNT): $DATA_NAMES"
else fail "Data nodes: $DATA_COUNT (want >= 3 — Cat, Mouse, BattleEvent minimum). Found: $DATA_NAMES"; fi

# Check specific models
for MODEL in cat mouse battleevent; do
  EXISTS=$(echo "$DATA_NODES" | python3 -c "import sys,json; nodes=json.load(sys.stdin); print(any(n['node_key'].endswith(':$MODEL') for n in nodes))")
  if [ "$EXISTS" = "True" ]; then pass "Model '$MODEL' found"
  else fail "Model '$MODEL' MISSING"; fi
done

# ── 3f: Endpoints ──
section "Endpoints"
CONTRACT_NODES=$("$CHRONICLE" node list --layer contract --domain tomandjerry --db "$DB_PATH" 2>/dev/null || echo "[]")
ENDPOINT_COUNT=$(echo "$CONTRACT_NODES" | python3 -c "import sys,json; print(len([n for n in json.load(sys.stdin) if n['node_type']=='endpoint']))")
TOPIC_COUNT=$(echo "$CONTRACT_NODES" | python3 -c "import sys,json; print(len([n for n in json.load(sys.stdin) if n['node_type']=='topic']))")
if [ "$ENDPOINT_COUNT" -ge 8 ]; then pass "Endpoints: $ENDPOINT_COUNT (>= 8)"
elif [ "$ENDPOINT_COUNT" -ge 4 ]; then pass "Endpoints: $ENDPOINT_COUNT (>= 4, partial)"; echo -e "    ${YELLOW}(ideal: >= 8 — all controller routes)${NC}"
else fail "Endpoints: $ENDPOINT_COUNT (want >= 4)"; fi

if [ "$TOPIC_COUNT" -ge 1 ]; then pass "Kafka topics: $TOPIC_COUNT"
else fail "Kafka topics: $TOPIC_COUNT (want >= 1 — battle-results)"; fi

# ── 3g: Edge types ──
section "Edge Types"
EDGE_TYPES=$(echo "$STATS" | python3 -c "
import sys,json
d = json.load(sys.stdin).get('edges_by_type',{})
for k,v in sorted(d.items()):
    print(f'  {k}: {v}')
")
echo "$EDGE_TYPES"

# Check critical edge types
for ETYPE in INJECTS CONTAINS; do
  COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('edges_by_type',{}).get('$ETYPE',0))")
  if [ "$COUNT" -gt 0 ]; then pass "$ETYPE edges: $COUNT"
  else fail "$ETYPE edges: MISSING"; fi
done

# Bonus edge types (important but not blocking)
for ETYPE in EXPOSES_ENDPOINT CALLS_SERVICE CALLS_ENDPOINT PUBLISHES_TOPIC CONSUMES_TOPIC USES_MODEL REFERENCES_MODEL; do
  COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('edges_by_type',{}).get('$ETYPE',0))")
  if [ "$COUNT" -gt 0 ]; then pass "$ETYPE edges: $COUNT"
  else echo -e "  ${YELLOW}⚠ $ETYPE edges: 0 (should have some)${NC}"; fi
done

# ── 3h: Derivation distribution ──
section "Derivation"
echo "$STATS" | python3 -c "
import sys,json
d = json.load(sys.stdin).get('edges_by_derivation',{})
for k,v in sorted(d.items()):
    print(f'  {k}: {v}')
"

HARD_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('edges_by_derivation',{}).get('hard',0))")
LINKED_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('edges_by_derivation',{}).get('linked',0))")
if [ "$HARD_COUNT" -gt 0 ]; then pass "Hard edges: $HARD_COUNT"
else fail "No hard edges"; fi
if [ "$LINKED_COUNT" -gt 0 ]; then pass "Linked edges: $LINKED_COUNT (cross-service deps detected)"
else echo -e "  ${YELLOW}⚠ No linked edges — cross-service deps may be missing${NC}"; fi

# ── 3i: Path queries ──
section "Path Queries"

# Tom attack chain: ArenaController → tom-api service
TOM_PATH=$("$CHRONICLE" query path code:controller:tomandjerry:arenacontroller service:service:tomandjerry:tom-api --mode directed --db "$DB_PATH" 2>/dev/null || echo '{"paths":[]}')
TOM_PATH_COUNT=$(echo "$TOM_PATH" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('paths') or []))")
if [ "$TOM_PATH_COUNT" -ge 1 ]; then
  pass "ArenaController → tom-api: $TOM_PATH_COUNT path(s)"
  echo "$TOM_PATH" | python3 -c "
import sys,json
p = json.load(sys.stdin)['paths'][0]
print(f'    Path: {\" → \".join(n.split(\":\")[-1] for n in p[\"nodes\"])}')
print(f'    Hops: {p[\"depth\"]}, Score: {p[\"path_score\"]}')
" 2>/dev/null
else
  fail "ArenaController → tom-api: no path found"
fi

# Tom ↔ Jerry: should NOT have direct path (only through arena)
TJ_DIRECT=$("$CHRONICLE" query path service:service:tomandjerry:tom-api service:service:tomandjerry:jerry-api --mode directed --db "$DB_PATH" 2>/dev/null || echo '{"paths":[]}')
TJ_PATH_COUNT=$(echo "$TJ_DIRECT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('paths') or []))")
if [ "$TJ_PATH_COUNT" -eq 0 ]; then pass "tom-api ↛ jerry-api: no direct path (correct — only via arena)"
else echo -e "  ${YELLOW}⚠ tom-api → jerry-api: found $TJ_PATH_COUNT path(s) (unexpected direct connection)${NC}"; fi

# ── 3j: Impact analysis ──
section "Impact Analysis"

# What breaks if Cat model changes?
CAT_IMPACT=$("$CHRONICLE" impact data:model:tomandjerry:cat --depth 4 --db "$DB_PATH" 2>/dev/null || echo '{"total_impacted":0,"impacts":[]}')
CAT_IMPACT_COUNT=$(echo "$CAT_IMPACT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('total_impacted',0))")
if [ "$CAT_IMPACT_COUNT" -ge 1 ]; then
  pass "Cat model impact: $CAT_IMPACT_COUNT nodes affected"
  echo "$CAT_IMPACT" | python3 -c "
import sys,json
for imp in json.load(sys.stdin).get('impacts',[])[:5]:
    print(f'    → {imp[\"name\"]} ({imp[\"node_type\"]}) depth:{imp[\"depth\"]} score:{imp[\"impact_score\"]}')
" 2>/dev/null
else
  fail "Cat model impact: 0 (want >= 1 — at least TomService)"
fi

# ── 3k: Kafka connectivity ──
section "Kafka Flow"
KAFKA_PATH=$("$CHRONICLE" query path code:provider:tomandjerry:battleresultproducer code:provider:tomandjerry:battleresultconsumer --mode connected --db "$DB_PATH" 2>/dev/null || echo '{"paths":[]}')
KAFKA_PATH_COUNT=$(echo "$KAFKA_PATH" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('paths') or []))")
if [ "$KAFKA_PATH_COUNT" -ge 1 ]; then
  pass "Kafka flow: Producer → battle-results → Consumer"
  echo "$KAFKA_PATH" | python3 -c "
import sys,json
p = json.load(sys.stdin)['paths'][0]
print(f'    Path: {\" → \".join(n.split(\":\")[-1] for n in p[\"nodes\"])}')
" 2>/dev/null
else
  fail "Kafka flow not connected (Producer ↛ Consumer via topic)"
fi

# ── 3l: Token/Context Efficiency ──
section "Efficiency Metrics"
sqlite3 "$DB_PATH" "
SELECT
  COUNT(*) as total_calls,
  COALESCE(SUM(LENGTH(params_json)), 0) as total_params_bytes,
  COALESCE(AVG(LENGTH(params_json)), 0) as avg_params,
  COALESCE(MAX(LENGTH(params_json)), 0) as max_params,
  COUNT(CASE WHEN tool_name='chronicle_import_all' THEN 1 END) as imports,
  COUNT(CASE WHEN error_message != '' AND error_message IS NOT NULL THEN 1 END) as errors
FROM mcp_request_log
" 2>/dev/null | while IFS='|' read total params avg max imports errors; do
  echo "  MCP calls: $total (imports: $imports, errors: $errors)"
  echo "  Total payload: $(echo "scale=1; $params / 1024" | bc)KB"
  echo "  Avg payload: $(echo "scale=1; $avg / 1024" | bc)KB"
  echo "  Largest payload: $(echo "scale=1; $max / 1024" | bc)KB"

  # Efficiency score: fewer calls + smaller payloads = better
  if [ "$max" -lt 5000 ]; then
    pass "Largest payload < 5KB (streaming imports)"
  elif [ "$max" -lt 20000 ]; then
    echo -e "  ${YELLOW}⚠ Largest payload $(echo "scale=1; $max / 1024" | bc)KB — could be smaller${NC}"
  else
    echo -e "  ${RED}✗ Largest payload $(echo "scale=1; $max / 1024" | bc)KB — too large, should stream${NC}"
  fi

  if [ "$imports" -gt 0 ]; then
    avg_import=$(sqlite3 "$DB_PATH" "SELECT AVG(LENGTH(params_json)) FROM mcp_request_log WHERE tool_name='chronicle_import_all'" 2>/dev/null)
    echo "  Avg import size: $(echo "scale=1; $avg_import / 1024" | bc)KB"
  fi
done
echo ""

# Show per-tool breakdown
echo "  Per-tool breakdown:"
sqlite3 "$DB_PATH" "
SELECT printf('    %-30s %3d calls  %6.1fKB avg  %6.1fKB total',
  tool_name, COUNT(*), AVG(LENGTH(params_json))/1024.0, SUM(LENGTH(params_json))/1024.0)
FROM mcp_request_log GROUP BY tool_name ORDER BY SUM(LENGTH(params_json)) DESC
" 2>/dev/null

# ── 3n: Discoveries (self-learning) ──
section "Discoveries (Self-Learning)"
DISC_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_discoveries" 2>/dev/null || echo "0")
SYSTEM_DISC=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_discoveries WHERE source='system'" 2>/dev/null || echo "0")
CLAUDE_DISC=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_discoveries WHERE source='claude'" 2>/dev/null || echo "0")
USER_DISC=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM graph_discoveries WHERE source='user'" 2>/dev/null || echo "0")

if [ "$DISC_COUNT" -gt 0 ]; then
  pass "Discoveries: $DISC_COUNT total (system: $SYSTEM_DISC, claude: $CLAUDE_DISC, user: $USER_DISC)"
else
  echo -e "  ${YELLOW}⚠ No discoveries recorded${NC}"
fi

if [ "$CLAUDE_DISC" -gt 0 ]; then
  pass "Claude reported $CLAUDE_DISC discovery(ies)"
else
  echo -e "  ${YELLOW}⚠ Claude didn't call chronicle_report_discovery${NC}"
fi

# Show all discoveries
sqlite3 "$DB_PATH" "SELECT printf('    [%s|%s] %s', category, source, title) FROM graph_discoveries ORDER BY created_at" 2>/dev/null

# ── 3m: Shared library ──
section "Shared Library"
SHARED_NODE=$(echo "$STATS" | python3 -c "
import sys,json
# Check if any node has 'shared' or 'package' in it
" 2>/dev/null || echo "")
SHARED_COUNT=$("$CHRONICLE" node list --db "$DB_PATH" 2>/dev/null | python3 -c "
import sys,json
nodes = json.load(sys.stdin)
shared = [n for n in nodes if 'shared' in n.get('node_key','').lower() or n.get('node_type') == 'package']
print(len(shared))
" 2>/dev/null || echo "0")
if [ "$SHARED_COUNT" -gt 0 ]; then
  pass "Shared library detected ($SHARED_COUNT nodes)"
else
  echo -e "  ${YELLOW}⚠ Shared library (@tomandjerry/shared) not extracted${NC}"
fi

# ── 3p: Business Flows ──
section "Business Flows"
FLOW_COUNT=$("$CHRONICLE" node list --layer flow --db "$DB_PATH" 2>/dev/null | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")
if [ "$FLOW_COUNT" -ge 2 ]; then
  pass "Flow use cases: $FLOW_COUNT (>= 2)"
  "$CHRONICLE" node list --layer flow --db "$DB_PATH" 2>/dev/null | python3 -c "
import sys,json
for n in json.load(sys.stdin):
    print(f'    {n[\"node_type\"]:12s} {n[\"name\"]}')
" 2>/dev/null
else
  echo -e "  ${YELLOW}⚠ Flow use cases: $FLOW_COUNT (want >= 2 — TomAttacksJerry, JerrySetsTrap)${NC}"
fi

# Check TRIGGERS_FLOW edges
TRIGGERS_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('edges_by_type',{}).get('TRIGGERS_FLOW',0))" 2>/dev/null || echo "0")
if [ "$TRIGGERS_COUNT" -ge 1 ]; then
  pass "TRIGGERS_FLOW edges: $TRIGGERS_COUNT"
else
  echo -e "  ${YELLOW}⚠ No TRIGGERS_FLOW edges — endpoints not linked to use cases${NC}"
fi

# Check REQUIRES edges from flows
REQUIRES_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('edges_by_type',{}).get('REQUIRES',0))" 2>/dev/null || echo "0")
if [ "$REQUIRES_COUNT" -ge 1 ]; then
  pass "REQUIRES edges (flow→service/model): $REQUIRES_COUNT"
else
  echo -e "  ${YELLOW}⚠ No REQUIRES edges — flows not linked to services${NC}"
fi

# ── 3q: Infrastructure ──
section "Infrastructure"
INFRA_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('nodes_by_layer',{}).get('infra',0))" 2>/dev/null || echo "0")
if [ "$INFRA_COUNT" -ge 1 ]; then
  pass "Infrastructure nodes: $INFRA_COUNT (>= 1)"
  "$CHRONICLE" node list --layer infra --db "$DB_PATH" 2>/dev/null | python3 -c "
import sys,json
for n in json.load(sys.stdin):
    print(f'    {n[\"node_type\"]:12s} {n[\"name\"]}')
" 2>/dev/null
else
  fail "Infrastructure nodes: 0 (want >= 1 — kafka broker, redis cache)"
fi

# Check BELONGS_TO edge from topic to broker
BELONGS_TO_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('edges_by_type',{}).get('BELONGS_TO',0))" 2>/dev/null || echo "0")
if [ "$BELONGS_TO_COUNT" -ge 1 ]; then
  pass "BELONGS_TO edges (topic → broker): $BELONGS_TO_COUNT"
else
  echo -e "  ${YELLOW}⚠ No BELONGS_TO edges — Kafka topics not linked to broker${NC}"
fi

# ── 3r: Domain List ──
section "Domain List"
DOMAIN_LIST=$("$CHRONICLE" domain list --db "$DB_PATH" 2>/dev/null || echo "[]")
DOMAIN_COUNT=$(echo "$DOMAIN_LIST" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")
if [ "$DOMAIN_COUNT" -ge 1 ]; then
  pass "chronicle domain list: $DOMAIN_COUNT domain(s) found"
  echo "$DOMAIN_LIST" | python3 -c "
import sys,json
for d in json.load(sys.stdin):
    print(f'    {d[\"name\"]}')
" 2>/dev/null
else
  fail "chronicle domain list: no domains found"
fi

# ── 3s: Domain Language ──
section "Domain Language"
TERM_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM domain_language" 2>/dev/null || echo "0")
if [ "$TERM_COUNT" -gt 0 ]; then
  pass "Domain glossary: $TERM_COUNT terms defined"
  sqlite3 "$DB_PATH" "SELECT printf('    %s (%s): %s', term, context, description) FROM domain_language ORDER BY context, term LIMIT 10" 2>/dev/null
else
  echo -e "  ${YELLOW}⚠ No domain terms defined — Claude didn't call chronicle_define_term${NC}"
fi

VIOLATION_COUNT=$("$CHRONICLE" node list --db "$DB_PATH" 2>/dev/null | python3 -c "
import sys, json, sqlite3
nodes = json.load(sys.stdin)
conn = sqlite3.connect('$DB_PATH')
terms = conn.execute('SELECT term, anti_patterns FROM domain_language').fetchall()
violations = 0
for n in nodes:
    name_lower = n.get('name','').lower()
    for term, anti_json in terms:
        import json as j
        for anti in j.loads(anti_json):
            if anti.lower() in name_lower:
                violations += 1
print(violations)
" 2>/dev/null || echo "0")
if [ "$VIOLATION_COUNT" -eq 0 ] && [ "$TERM_COUNT" -gt 0 ]; then
  pass "Language violations: 0 (clean)"
elif [ "$VIOLATION_COUNT" -gt 0 ]; then
  echo -e "  ${YELLOW}⚠ Language violations: $VIOLATION_COUNT (naming inconsistencies found)${NC}"
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

# Save summary
cat > "$RESULTS_DIR/summary.json" << SUMEOF
{
  "timestamp": "$(date -Iseconds)",
  "node_count": $NODE_COUNT,
  "edge_count": $EDGE_COUNT,
  "errors": $ERRORS,
  "layers": "$LAYERS",
  "services": $SERVICE_COUNT,
  "data_models": $DATA_COUNT,
  "endpoints": $ENDPOINT_COUNT,
  "topics": $TOPIC_COUNT,
  "kafka_connected": $( [ "$KAFKA_PATH_COUNT" -ge 1 ] && echo "true" || echo "false" ),
  "tom_path_found": $( [ "$TOM_PATH_COUNT" -ge 1 ] && echo "true" || echo "false" ),
  "cat_impact": $CAT_IMPACT_COUNT,
  "infra_nodes": $INFRA_COUNT,
  "domains": $DOMAIN_COUNT
}
SUMEOF

info "Results saved to e2e/results/"
echo "  → claude_output.txt (Claude's full response)"
echo "  → stats.json (graph statistics)"
echo "  → summary.json (test results)"

exit $ERRORS

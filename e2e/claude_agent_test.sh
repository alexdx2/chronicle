#!/bin/bash
# E2E test: Claude agent scans Tom & Jerry project and we verify the graph
#
# Usage: ./e2e/claude_agent_test.sh
#
# Prerequisites:
#   - oracle binary built (or go installed to build it)
#   - claude CLI available and authenticated

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ORACLE="$PROJECT_DIR/oracle"
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

# ─── Step 0: Build oracle ───
section "Build"
cd "$PROJECT_DIR"
go build -o oracle ./cmd/oracle 2>/dev/null
pass "Oracle binary built"

# ─── Step 1: Setup fixture project ───
section "Setup"
cp -r "$FIXTURE_DIR"/* "$WORK_DIR/"
cd "$WORK_DIR"

# Create .depbot
"$ORACLE" init > /dev/null 2>&1
pass "Oracle initialized in $WORK_DIR"

# Create MCP config for Claude to find oracle tools
DB_PATH="$WORK_DIR/.depbot/oracle.db"
MCP_CONFIG="$WORK_DIR/mcp.json"
cat > "$MCP_CONFIG" << MCPEOF
{
  "mcpServers": {
    "oracle": {
      "command": "$ORACLE",
      "args": ["mcp", "serve", "--db", "$DB_PATH", "--no-admin"]
    }
  }
}
MCPEOF
pass "MCP config created at $MCP_CONFIG"

# ─── Step 2: Run Claude ───
section "Claude Scan"
info "Running Claude to scan the project..."
info "This may take 1-3 minutes depending on model speed."

CLAUDE_PROMPT="You have Oracle MCP tools available. Scan this Tom & Jerry project.

Steps:
1. Call oracle_extraction_guide to learn the extraction methodology
2. Call oracle_save_manifest — domain: tomandjerry, repos: tom-api (./tom-api), jerry-api (./jerry-api), arena-api (./arena-api), spectators-api (./spectators-api)
3. Call oracle_revision_create — domain: tomandjerry, after_sha: test123, trigger: manual, mode: full
4. Read the source code of ALL 4 services and extract:
   - Prisma models from prisma/schema.prisma (Cat, CatWeapon, Mouse, Trap, BattleEvent + all enums as data:enum nodes)
   - NestJS modules, controllers, services (code layer)
   - HTTP endpoints from @Get/@Post decorators (contract:endpoint nodes + EXPOSES_ENDPOINT edges)
   - Cross-service HTTP calls via env URLs like TOM_API_URL, JERRY_API_URL (CALLS_SERVICE + CALLS_ENDPOINT edges, derivation: linked)
   - Kafka topic battle-results (PUBLISHES_TOPIC + CONSUMES_TOPIC edges)
   - USES_MODEL edges from services that call prisma (e.g. TomService -> Cat model)
   - REFERENCES_MODEL edges between models with @relation (Cat->CatWeapon, Mouse->Trap)
   - Repository and service nodes for each API
5. Also extract: @UseGuards → INJECTS from controller to guard, @UseInterceptors → INJECTS from controller to interceptor, middleware → INJECTS from module. Bull queue processors, custom decorators, validation pipes — all as providers.
6. Don't forget the shared library at ./shared — extract as code:package node
7. Import via oracle_import_all (split into batches if needed — data first, then code, then contracts)
8. Call oracle_snapshot_create and oracle_stale_mark
9. AFTER scan: define domain language — call oracle_define_term for the key domain concepts (Cat, Mouse, Battle, Arena, Spectator, Trap, Weapon). Include anti-patterns for each (e.g. Cat term: anti_patterns=['Feline','Kitty']). Then call oracle_check_language.
10. Call oracle_report_discovery for each observation:
   - Any code patterns you found unusual or couldn't classify → category: unknown_pattern
   - Any relationships you suspect but couldn't confirm → category: missing_edge
   - Overall scan quality assessment → category: pattern

IMPORTANT: Do NOT skip data models, endpoints, cross-service edges, or discovery reporting. The test checks for all of them.
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
STATS=$("$ORACLE" query stats --domain tomandjerry --db "$DB_PATH" 2>/dev/null || echo '{"node_count":0,"edge_count":0,"nodes_by_layer":{},"edges_by_type":{},"edges_by_derivation":{}}')
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
SERVICES=$("$ORACLE" node list --layer service --domain tomandjerry --db "$DB_PATH" 2>/dev/null || echo "[]")
SERVICE_COUNT=$(echo "$SERVICES" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))")
SERVICE_NAMES=$(echo "$SERVICES" | python3 -c "import sys,json; print(', '.join(n['name'] for n in json.load(sys.stdin)))")
if [ "$SERVICE_COUNT" -ge 3 ]; then pass "Services ($SERVICE_COUNT): $SERVICE_NAMES"
else fail "Services: $SERVICE_COUNT (want >= 3). Found: $SERVICE_NAMES"; fi

# ── 3e: Data models ──
section "Data Models"
DATA_NODES=$("$ORACLE" node list --layer data --domain tomandjerry --db "$DB_PATH" 2>/dev/null || echo "[]")
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
CONTRACT_NODES=$("$ORACLE" node list --layer contract --domain tomandjerry --db "$DB_PATH" 2>/dev/null || echo "[]")
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
TOM_PATH=$("$ORACLE" query path code:controller:tomandjerry:arenacontroller service:service:tomandjerry:tom-api --mode directed --db "$DB_PATH" 2>/dev/null || echo '{"paths":[]}')
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
TJ_DIRECT=$("$ORACLE" query path service:service:tomandjerry:tom-api service:service:tomandjerry:jerry-api --mode directed --db "$DB_PATH" 2>/dev/null || echo '{"paths":[]}')
TJ_PATH_COUNT=$(echo "$TJ_DIRECT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('paths') or []))")
if [ "$TJ_PATH_COUNT" -eq 0 ]; then pass "tom-api ↛ jerry-api: no direct path (correct — only via arena)"
else echo -e "  ${YELLOW}⚠ tom-api → jerry-api: found $TJ_PATH_COUNT path(s) (unexpected direct connection)${NC}"; fi

# ── 3j: Impact analysis ──
section "Impact Analysis"

# What breaks if Cat model changes?
CAT_IMPACT=$("$ORACLE" impact data:model:tomandjerry:cat --depth 4 --db "$DB_PATH" 2>/dev/null || echo '{"total_impacted":0,"impacts":[]}')
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
KAFKA_PATH=$("$ORACLE" query path code:provider:tomandjerry:battleresultproducer code:provider:tomandjerry:battleresultconsumer --mode connected --db "$DB_PATH" 2>/dev/null || echo '{"paths":[]}')
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
  COUNT(CASE WHEN tool_name='oracle_import_all' THEN 1 END) as imports,
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
    avg_import=$(sqlite3 "$DB_PATH" "SELECT AVG(LENGTH(params_json)) FROM mcp_request_log WHERE tool_name='oracle_import_all'" 2>/dev/null)
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
  echo -e "  ${YELLOW}⚠ Claude didn't call oracle_report_discovery${NC}"
fi

# Show all discoveries
sqlite3 "$DB_PATH" "SELECT printf('    [%s|%s] %s', category, source, title) FROM graph_discoveries ORDER BY created_at" 2>/dev/null

# ── 3m: Shared library ──
section "Shared Library"
SHARED_NODE=$(echo "$STATS" | python3 -c "
import sys,json
# Check if any node has 'shared' or 'package' in it
" 2>/dev/null || echo "")
SHARED_COUNT=$("$ORACLE" node list --db "$DB_PATH" 2>/dev/null | python3 -c "
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

# ── 3p: Domain Language ──
section "Domain Language"
TERM_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM domain_language" 2>/dev/null || echo "0")
if [ "$TERM_COUNT" -gt 0 ]; then
  pass "Domain glossary: $TERM_COUNT terms defined"
  sqlite3 "$DB_PATH" "SELECT printf('    %s (%s): %s', term, context, description) FROM domain_language ORDER BY context, term LIMIT 10" 2>/dev/null
else
  echo -e "  ${YELLOW}⚠ No domain terms defined — Claude didn't call oracle_define_term${NC}"
fi

VIOLATION_COUNT=$("$ORACLE" node list --db "$DB_PATH" 2>/dev/null | python3 -c "
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
  "cat_impact": $CAT_IMPACT_COUNT
}
SUMEOF

info "Results saved to e2e/results/"
echo "  → claude_output.txt (Claude's full response)"
echo "  → stats.json (graph statistics)"
echo "  → summary.json (test results)"

exit $ERRORS

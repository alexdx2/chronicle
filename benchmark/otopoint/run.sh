#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="/home/alex/personal/otopoint"
RESULTS_DIR="$SCRIPT_DIR/results"
EMPTY_MCP="$SCRIPT_DIR/../empty-mcp.json"
MCP_CONFIG="/tmp/otopoint-mcp.json"
RUNS_PER_MODE=3

mkdir -p "$RESULTS_DIR"

# Ensure MCP config exists
cat > "$MCP_CONFIG" << 'EOF'
{"mcpServers":{"chronicle":{"command":"chronicle","args":["mcp","serve"]}}}
EOF

SYSTEM_PROMPT="You are analyzing a NestJS monolith backend (OtoPoint) with Prisma, GraphQL, Bull queues, WebSockets, Centrifugo, and Stripe. Answer precisely based on what you can verify. If you cannot find something, say so. Do not guess or hallucinate dependencies. IMPORTANT: Do not read any files in the benchmark/ directory."

declare -A TASKS
TASKS[1_impact]='What breaks if I change the Order Prisma model? List all directly affected services, transitive dependencies, affected API endpoints (REST and GraphQL), and external systems (Redis, Stripe, WebSocket, etc). Provide file references.

Format:
## Direct Dependencies
## Transitive Dependencies
## Affected Endpoints
## External Systems
## Confidence (0-100)'

TASKS[2_flow]='Trace the full flow when a customer creates an order. Start from the GraphQL mutation or REST endpoint, through all services, side effects (voucher application, points, socket events, notifications), to database persistence.

Format:
## Request Chain (ordered)
## Cross-Service Calls
## Async Side Effects (queues, WebSocket, push)
## Data Models Touched'

TASKS[3_reverse]='What components depend on SocketService? List all services that inject it, what events they emit, and through which gateways (web, mobile, CRM).

Format:
## Services that inject SocketService
## Events emitted
## WebSocket Gateways
## Downstream consumers'

TASKS[4_trap]='What components depend on InventoryService? List all dependencies and affected endpoints.'

TASKS[5_voucher]='If I refactor VoucherApplicationService, what needs to change? Trace all callers, the data models involved, any queue processors, and API endpoints that expose voucher functionality.

Format:
## Direct Callers
## Data Models
## Queue Processors
## API Endpoints (REST + GraphQL)
## External Integrations'

run_task() {
    local task_name="$1"
    local mode="$2"
    local run_num="$3"
    local prompt="${TASKS[$task_name]}"
    local outfile="$RESULTS_DIR/${task_name}_${mode}_run${run_num}.json"

    echo ">>> Running: $task_name / $mode / run $run_num"

    if [[ "$mode" == "mcp" ]]; then
        echo "$prompt" | claude -p \
            --output-format json \
            --system-prompt "$SYSTEM_PROMPT" \
            --mcp-config "$MCP_CONFIG" \
            --permission-mode bypassPermissions \
            --model sonnet \
            > "$outfile" 2>/dev/null
    else
        echo "$prompt" | claude -p \
            --output-format json \
            --system-prompt "$SYSTEM_PROMPT" \
            --strict-mcp-config --mcp-config "$EMPTY_MCP" \
            --permission-mode bypassPermissions \
            --model sonnet \
            > "$outfile" 2>/dev/null
    fi

    local tokens cost duration
    tokens=$(python3 -c "import json,sys; d=json.load(open('$outfile')); u=d['usage']; print(f\"in={u.get('input_tokens',0)+u.get('cache_creation_input_tokens',0)+u.get('cache_read_input_tokens',0)} out={u.get('output_tokens',0)}\")")
    cost=$(python3 -c "import json; d=json.load(open('$outfile')); print(f\"\${d['total_cost_usd']:.4f}\")")
    duration=$(python3 -c "import json; d=json.load(open('$outfile')); print(f\"{d['duration_ms']/1000:.1f}s\")")
    echo "    $tokens | cost=$cost | duration=$duration"
    echo ""
}

TASK_FILTER="${1:-all}"
MODE_FILTER="${2:-all}"

echo "============================================="
echo "Chronicle MCP — OtoPoint Benchmark"
echo "============================================="
echo "Project: $PROJECT_DIR"
echo "Results: $RESULTS_DIR"
echo "Task filter: $TASK_FILTER"
echo "Mode filter: $MODE_FILTER"
echo "============================================="
echo ""

cd "$PROJECT_DIR"

for task_name in 1_impact 2_flow 3_reverse 4_trap 5_voucher; do
    if [[ "$TASK_FILTER" != "all" && "$task_name" != *"$TASK_FILTER"* ]]; then
        continue
    fi
    for mode in mcp baseline; do
        if [[ "$MODE_FILTER" != "all" && "$mode" != "$MODE_FILTER" ]]; then
            continue
        fi
        for run in $(seq 1 $RUNS_PER_MODE); do
            run_task "$task_name" "$mode" "$run"
        done
    done
done

echo "============================================="
echo "All runs complete. Generating summary..."
echo "============================================="

python3 "$SCRIPT_DIR/../score.py" "$RESULTS_DIR"

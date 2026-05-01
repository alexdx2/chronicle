#!/usr/bin/env bash
set -euo pipefail

# Chronicle MCP — A/B Benchmark Runner
# Runs each task 3x in MCP mode and 3x in baseline mode
# Results saved to benchmark/results/

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR/../fixtures/tom-and-jerry"
RESULTS_DIR="$SCRIPT_DIR/results"
EMPTY_MCP="$SCRIPT_DIR/empty-mcp.json"
MCP_CONFIG="$SCRIPT_DIR/../.claude/settings.json"
RUNS_PER_MODE=3

mkdir -p "$RESULTS_DIR"

# Empty MCP config for baseline mode
cat > "$EMPTY_MCP" << 'EOF'
{"mcpServers":{}}
EOF

# System prompt — identical for both modes, no hints
SYSTEM_PROMPT="You are analyzing a NestJS microservices codebase with 4 services: tom-api, jerry-api, arena-api, spectators-api. Answer precisely based on what you can verify. If you cannot find something, say so. Do not guess or hallucinate dependencies. IMPORTANT: Do not read any files in the benchmark/ directory — those contain test metadata and must not influence your answers."

# Tasks — prompt text for each
declare -A TASKS
TASKS[1_impact]='What breaks if I change the Cat data model? List all directly affected components, transitive dependencies (up to depth 3), affected API endpoints, and external systems. Provide file references where possible.

Format your answer as:
## Direct Dependencies
## Transitive Dependencies
## Affected Endpoints
## External Systems
## Confidence (0-100)'

TASKS[2_flow]='Trace the full request flow for POST /arena/attack. Show every component involved from the HTTP request to any async side effects (Kafka, webhooks, etc). Include cross-service calls.

Format:
## Request Chain (ordered)
## Cross-Service Calls
## Async Side Effects
## Data Models Touched'

TASKS[3_reverse]='What components depend on the "battle-results" Kafka topic? List producers, consumers, and the services they belong to. What happens downstream after a message is published?

Format:
## Producers
## Consumers
## Downstream Flow
## Services Involved'

TASKS[4_trap]='What components depend on CacheInvalidator? List all dependencies and affected endpoints.'

TASKS[5_path]='How is spectators-api connected to tom-api? Find all paths between these two services. Show both direct and indirect connections.

Format:
## Direct Connections
## Indirect Connections (via other services)
## Shared Dependencies'

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

    # Print quick summary
    local tokens cost duration
    tokens=$(python3 -c "import json,sys; d=json.load(open('$outfile')); u=d['usage']; print(f\"in={u.get('input_tokens',0)+u.get('cache_read_input_tokens',0)+u.get('cache_creation_input_tokens',0)} out={u.get('output_tokens',0)}\")")
    cost=$(python3 -c "import json; d=json.load(open('$outfile')); print(f\"\${d['total_cost_usd']:.4f}\")")
    duration=$(python3 -c "import json; d=json.load(open('$outfile')); print(f\"{d['duration_ms']/1000:.1f}s\")")
    echo "    $tokens | cost=$cost | duration=$duration"
    echo ""
}

# Parse args
TASK_FILTER="${1:-all}"
MODE_FILTER="${2:-all}"

echo "============================================="
echo "Chronicle MCP — A/B Benchmark"
echo "============================================="
echo "Project: $PROJECT_DIR"
echo "Results: $RESULTS_DIR"
echo "Runs per mode: $RUNS_PER_MODE"
echo "Task filter: $TASK_FILTER"
echo "Mode filter: $MODE_FILTER"
echo "============================================="
echo ""

cd "$PROJECT_DIR"

for task_name in 1_impact 2_flow 3_reverse 4_trap 5_path; do
    # Filter tasks if specified
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

# Generate summary
python3 "$SCRIPT_DIR/score.py" "$RESULTS_DIR"

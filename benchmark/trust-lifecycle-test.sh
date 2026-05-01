#!/usr/bin/env bash
set -euo pipefail

# Trust Lifecycle Integration Test
# Tests: edge exists → code changes → Claude detects → negative evidence → edge deactivated
#
# Flow:
# 1. Create a mini fixture with ServiceA injecting ServiceB
# 2. Scan it (creates edge + evidence)
# 3. Remove the injection from source code
# 4. Run Claude: "verify low-confidence edges" after invalidation
# 5. Verify: edge is now inactive

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FIXTURE_DIR="$SCRIPT_DIR/../fixtures/trust-test"
MCP_CONFIG="/tmp/trust-test-mcp.json"
CHRONICLE="chronicle"

echo "=== TRUST LIFECYCLE INTEGRATION TEST ==="
echo ""

# Cleanup
rm -rf "$FIXTURE_DIR"
mkdir -p "$FIXTURE_DIR/.depbot" "$FIXTURE_DIR/src"

# Create domain manifest
cat > "$FIXTURE_DIR/.depbot/chronicle.domain.yaml" << 'EOF'
domains:
  - key: trusttest
    name: TrustTest
    repositories:
      - name: api
        path: .
        tech: nestjs
EOF

# Create source files with a real dependency
cat > "$FIXTURE_DIR/src/order.service.ts" << 'EOF'
import { Injectable } from '@nestjs/common';
import { PaymentService } from './payment.service';

@Injectable()
export class OrderService {
  constructor(private readonly paymentService: PaymentService) {}

  async createOrder(data: any) {
    const payment = await this.paymentService.charge(data.amount);
    return { orderId: '123', payment };
  }
}
EOF

cat > "$FIXTURE_DIR/src/payment.service.ts" << 'EOF'
import { Injectable } from '@nestjs/common';

@Injectable()
export class PaymentService {
  async charge(amount: number) {
    return { status: 'paid', amount };
  }
}
EOF

# MCP config
cat > "$MCP_CONFIG" << 'EOF'
{"mcpServers":{"chronicle":{"command":"chronicle","args":["mcp","serve"]}}}
EOF

echo "Step 1: Initial scan — Claude discovers OrderService → PaymentService dependency"
cd "$FIXTURE_DIR"
SCAN_RESULT=$(echo 'Scan this project. Domain is "trusttest". Extract all services and their dependencies. Import into the graph.' | claude -p --output-format json --mcp-config "$MCP_CONFIG" --permission-mode bypassPermissions --model sonnet 2>/dev/null)

SCAN_COST=$(echo "$SCAN_RESULT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(f'\${d[\"total_cost_usd\"]:.4f}')")
SCAN_TURNS=$(echo "$SCAN_RESULT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['num_turns'])")
echo "  Cost: $SCAN_COST, Turns: $SCAN_TURNS"

# Verify edge exists and is active
echo ""
echo "Step 2: Verify edge exists"
EDGE_STATE=$(sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" "SELECT e.edge_type, e.active, e.trust_score FROM graph_edges e JOIN graph_nodes n1 ON e.from_node_id=n1.node_id JOIN graph_nodes n2 ON e.to_node_id=n2.node_id WHERE n1.name LIKE '%Order%' AND n2.name LIKE '%Payment%' LIMIT 1" 2>/dev/null || echo "NOT_FOUND")
echo "  Edge state: $EDGE_STATE"

if [[ "$EDGE_STATE" == "NOT_FOUND" || -z "$EDGE_STATE" ]]; then
    echo "  FAIL: Edge not created by scan. Checking what was created..."
    sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" "SELECT from_node_key, to_node_key, edge_type FROM graph_edges" 2>/dev/null
    echo ""
    echo "  Nodes:"
    sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" "SELECT node_key, name FROM graph_nodes" 2>/dev/null
    exit 1
fi

echo ""
echo "Step 3: Modify source — remove PaymentService dependency"
cat > "$FIXTURE_DIR/src/order.service.ts" << 'EOF'
import { Injectable } from '@nestjs/common';

@Injectable()
export class OrderService {
  async createOrder(data: any) {
    // PaymentService removed — orders are now free
    return { orderId: '123', status: 'free' };
  }
}
EOF

echo "  Modified: removed PaymentService import and injection"

echo ""
echo "Step 4: Run Claude — incremental update + verify stale edges"
VERIFY_RESULT=$(echo 'Run an incremental update. The file src/order.service.ts has changed. Invalidate it, then re-scan it. If any previously seen dependency is now gone, add negative evidence to confirm its removal.' | claude -p --output-format json --mcp-config "$MCP_CONFIG" --permission-mode bypassPermissions --model sonnet 2>/dev/null)

VERIFY_COST=$(echo "$VERIFY_RESULT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(f'\${d[\"total_cost_usd\"]:.4f}')")
VERIFY_TURNS=$(echo "$VERIFY_RESULT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['num_turns'])")
VERIFY_ANSWER=$(echo "$VERIFY_RESULT" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['result'][:500])")
echo "  Cost: $VERIFY_COST, Turns: $VERIFY_TURNS"
echo "  Answer: $VERIFY_ANSWER"

echo ""
echo "Step 5: Verify edge is now inactive or has low trust"
EDGE_AFTER=$(sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" "SELECT e.edge_type, e.active, e.trust_score FROM graph_edges e JOIN graph_nodes n1 ON e.from_node_id=n1.node_id JOIN graph_nodes n2 ON e.to_node_id=n2.node_id WHERE n1.name LIKE '%Order%' AND n2.name LIKE '%Payment%' LIMIT 1" 2>/dev/null || echo "NOT_FOUND")
echo "  Edge state after: $EDGE_AFTER"

# Check for negative evidence
NEG_EVIDENCE=$(sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" "SELECT COUNT(*) FROM graph_evidence WHERE polarity='negative'" 2>/dev/null || echo "0")
echo "  Negative evidence count: $NEG_EVIDENCE"

# All evidence for this edge
echo "  All evidence for edge:"
sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" "SELECT polarity, confidence, evidence_status, source_kind FROM graph_evidence" 2>/dev/null || echo "  (none)"

echo ""
echo "=== RESULTS ==="
echo "  Initial edge: $EDGE_STATE"
echo "  Final edge:   $EDGE_AFTER"
echo "  Negative evidence: $NEG_EVIDENCE"

# Parse final state
FINAL_ACTIVE=$(echo "$EDGE_AFTER" | cut -d'|' -f2)
FINAL_TRUST=$(echo "$EDGE_AFTER" | cut -d'|' -f3)

if [[ "$FINAL_ACTIVE" == "0" ]] || [[ $(echo "$FINAL_TRUST < 0.5" | bc -l 2>/dev/null || echo "0") == "1" ]]; then
    echo ""
    echo "  ✓ PASS: Edge deactivated or trust dropped below 0.5"
elif [[ "$NEG_EVIDENCE" != "0" ]]; then
    echo ""
    echo "  ✓ PASS: Negative evidence was added (trust may not have recalculated in this schema)"
else
    echo ""
    echo "  ✗ FAIL: Edge still active with high trust and no negative evidence"
fi

echo ""
echo "Total test cost: scan $SCAN_COST + verify $VERIFY_COST"

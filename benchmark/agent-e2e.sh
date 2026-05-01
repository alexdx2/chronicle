#!/usr/bin/env bash
set -euo pipefail

# Chronicle — Claude Agent E2E Tests
# 5 tests that verify Claude's behavior with the graph.
# Cost: ~$2-3 per full run.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FIXTURE_DIR="$SCRIPT_DIR/../fixtures/agent-test"
MCP_CONFIG="/tmp/agent-test-mcp.json"
PASS=0
FAIL=0
TOTAL=0

cat > "$MCP_CONFIG" << 'EOF'
{"mcpServers":{"chronicle":{"command":"chronicle","args":["mcp","serve"]}}}
EOF

setup_fixture() {
    rm -rf "$FIXTURE_DIR"
    mkdir -p "$FIXTURE_DIR/.depbot" "$FIXTURE_DIR/src" "$FIXTURE_DIR/prisma"

    cat > "$FIXTURE_DIR/.depbot/chronicle.domain.yaml" << 'EOF'
domains:
  - key: agenttest
    name: AgentTest
    repositories:
      - name: api
        path: .
        tech: nestjs
EOF

    # 3 services: OrderService → PaymentService → StripeClient
    # 1 Kafka topic: order-events (produced by OrderService)
    # 1 Prisma model: Order
    cat > "$FIXTURE_DIR/src/order.service.ts" << 'SRCEOF'
import { Injectable } from '@nestjs/common';
import { PaymentService } from './payment.service';
import { PrismaService } from './prisma.service';
import { KafkaProducer } from './kafka.producer';

@Injectable()
export class OrderService {
  constructor(
    private readonly paymentService: PaymentService,
    private readonly prisma: PrismaService,
    private readonly kafkaProducer: KafkaProducer,
  ) {}

  async createOrder(data: any) {
    const order = await this.prisma.order.create({ data });
    await this.paymentService.charge(order.total);
    await this.kafkaProducer.publish('order-events', { type: 'ORDER_CREATED', order });
    return order;
  }
}
SRCEOF

    cat > "$FIXTURE_DIR/src/payment.service.ts" << 'SRCEOF'
import { Injectable } from '@nestjs/common';
import { StripeClient } from './stripe.client';

@Injectable()
export class PaymentService {
  constructor(private readonly stripe: StripeClient) {}

  async charge(amount: number) {
    return this.stripe.createPaymentIntent(amount);
  }
}
SRCEOF

    cat > "$FIXTURE_DIR/src/stripe.client.ts" << 'SRCEOF'
import { Injectable } from '@nestjs/common';

@Injectable()
export class StripeClient {
  async createPaymentIntent(amount: number) {
    // calls Stripe API
    return { id: 'pi_123', amount, status: 'succeeded' };
  }
}
SRCEOF

    cat > "$FIXTURE_DIR/src/kafka.producer.ts" << 'SRCEOF'
import { Injectable } from '@nestjs/common';

@Injectable()
export class KafkaProducer {
  async publish(topic: string, message: any) {
    // publishes to Kafka
    console.log(`Published to ${topic}:`, message);
  }
}
SRCEOF

    cat > "$FIXTURE_DIR/src/notification.consumer.ts" << 'SRCEOF'
import { Injectable } from '@nestjs/common';

@Injectable()
export class NotificationConsumer {
  // Consumes from 'order-events' topic
  async handleOrderCreated(event: any) {
    console.log('Sending notification for order:', event.order.id);
  }
}
SRCEOF

    cat > "$FIXTURE_DIR/src/prisma.service.ts" << 'SRCEOF'
import { Injectable } from '@nestjs/common';

@Injectable()
export class PrismaService {
  order = {
    create: async (args: any) => ({ id: '1', ...args.data }),
    findMany: async () => [],
  };
}
SRCEOF

    cat > "$FIXTURE_DIR/prisma/schema.prisma" << 'SRCEOF'
generator client {
  provider = "prisma-client-js"
}

datasource db {
  provider = "postgresql"
  url      = env("DATABASE_URL")
}

model Order {
  id        String   @id @default(uuid())
  total     Float
  status    String   @default("pending")
  items     OrderItem[]
  createdAt DateTime @default(now())
}

model OrderItem {
  id      String @id @default(uuid())
  name    String
  price   Float
  orderId String
  order   Order  @relation(fields: [orderId], references: [id])
}
SRCEOF
}

run_claude() {
    local prompt="$1"
    echo "$prompt" | claude -p --output-format json \
        --mcp-config "$MCP_CONFIG" \
        --permission-mode bypassPermissions \
        --model sonnet 2>/dev/null
}

check_result() {
    local test_name="$1"
    local passed="$2"
    local detail="$3"
    TOTAL=$((TOTAL + 1))
    if [[ "$passed" == "true" ]]; then
        PASS=$((PASS + 1))
        echo "  PASS: $test_name — $detail"
    else
        FAIL=$((FAIL + 1))
        echo "  FAIL: $test_name — $detail"
    fi
}

scan_fixture() {
    echo "  Scanning fixture..."
    local result
    result=$(run_claude 'Scan this project fully. Domain is "agenttest". Extract all services, their dependencies (INJECTS), data models from prisma/schema.prisma (REFERENCES_MODEL), Kafka topics (PUBLISHES_TOPIC, CONSUMES_TOPIC), and external service calls (CALLS_SERVICE for Stripe). Import everything.')
    local cost turns
    cost=$(echo "$result" | python3 -c "import json,sys; print(f'\${json.load(sys.stdin)[\"total_cost_usd\"]:.2f}')")
    turns=$(echo "$result" | python3 -c "import json,sys; print(json.load(sys.stdin)['num_turns'])")
    echo "  Scan done: cost=$cost, turns=$turns"
}

echo "============================================="
echo "Chronicle — Claude Agent E2E Tests"
echo "============================================="
echo ""

# =============================================
# TEST 5.1: High confidence → direct answer
# =============================================
echo "--- Test 5.1: High confidence → direct answer ---"
setup_fixture
cd "$FIXTURE_DIR"
scan_fixture

# Query with a name — should answer from graph in few turns
RESULT=$(run_claude 'What does OrderService depend on? Use chronicle_query_deps.')
TURNS=$(echo "$RESULT" | python3 -c "import json,sys; print(json.load(sys.stdin)['num_turns'])")
ANSWER=$(echo "$RESULT" | python3 -c "import json,sys; print(json.load(sys.stdin)['result'])")
HAS_PAYMENT=$(echo "$ANSWER" | grep -ci "payment" || true)

check_result "5.1a Answers in few turns" "$([ "$TURNS" -le 4 ] && echo true || echo false)" "turns=$TURNS (want <=4)"
check_result "5.1b Finds PaymentService" "$([ "$HAS_PAYMENT" -gt 0 ] && echo true || echo false)" "payment mentions=$HAS_PAYMENT"

echo ""

# =============================================
# TEST 5.2: Empty result → code fallback
# =============================================
echo "--- Test 5.2: Empty result → code fallback ---"
# Delete the DB and recreate empty — graph has no data
rm -f "$FIXTURE_DIR/.depbot/chronicle.db"
chronicle revision create --domain agenttest --after-sha empty --trigger manual --project "$FIXTURE_DIR" > /dev/null 2>&1 || true

RESULT=$(run_claude 'What does OrderService depend on? Check the graph first, then fall back to reading code if needed.')
ANSWER=$(echo "$RESULT" | python3 -c "import json,sys; print(json.load(sys.stdin)['result'])")
TURNS=$(echo "$RESULT" | python3 -c "import json,sys; print(json.load(sys.stdin)['num_turns'])")
HAS_PAYMENT=$(echo "$ANSWER" | grep -ci "payment" || true)
HAS_FILE_REF=$(echo "$ANSWER" | grep -ci "order.service\|src/\|\.ts\|constructor\|import" || true)

check_result "5.2a Finds PaymentService despite empty graph" "$([ "$HAS_PAYMENT" -gt 0 ] && echo true || echo false)" "payment mentions=$HAS_PAYMENT"
check_result "5.2b References source files (code fallback)" "$([ "$HAS_FILE_REF" -gt 0 ] && echo true || echo false)" "file refs=$HAS_FILE_REF"

echo ""

# =============================================
# TEST 5.3: Discovery → evidence added
# =============================================
echo "--- Test 5.3: Discovery → evidence added ---"
setup_fixture
cd "$FIXTURE_DIR"
scan_fixture

# Check evidence count before
EV_BEFORE=$(sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" "SELECT COUNT(*) FROM graph_evidence" 2>/dev/null || echo "0")

RESULT=$(run_claude 'Verify the dependency from OrderService to PaymentService. Read the source code to confirm it exists, then add evidence using chronicle_evidence_add with the file path and line numbers where you found the import/injection.')
ANSWER=$(echo "$RESULT" | python3 -c "import json,sys; print(json.load(sys.stdin)['result'][:300])")

EV_AFTER=$(sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" "SELECT COUNT(*) FROM graph_evidence" 2>/dev/null || echo "0")

check_result "5.3 Evidence added to graph" "$([ "$EV_AFTER" -gt "$EV_BEFORE" ] && echo true || echo false)" "evidence: before=$EV_BEFORE, after=$EV_AFTER"

echo ""

# =============================================
# TEST 7.1: Wrong positive edge → Claude detects and kills
# =============================================
echo "--- Test 7.1: Wrong positive edge → Claude verifies and removes ---"
setup_fixture
cd "$FIXTURE_DIR"
scan_fixture

# Verify edge exists first
EDGE_BEFORE=$(sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" \
    "SELECT e.active, e.trust_score FROM graph_edges e
     JOIN graph_nodes n1 ON e.from_node_id=n1.node_id
     JOIN graph_nodes n2 ON e.to_node_id=n2.node_id
     WHERE n1.name LIKE '%Order%' AND n2.name LIKE '%Payment%' AND e.edge_type='INJECTS'
     LIMIT 1" 2>/dev/null || echo "NOT_FOUND")
echo "  Edge before: $EDGE_BEFORE"

# Remove the dependency from code
cat > "$FIXTURE_DIR/src/order.service.ts" << 'SRCEOF'
import { Injectable } from '@nestjs/common';
import { PrismaService } from './prisma.service';

@Injectable()
export class OrderService {
  constructor(private readonly prisma: PrismaService) {}

  async createOrder(data: any) {
    // PaymentService and KafkaProducer removed
    return await this.prisma.order.create({ data });
  }
}
SRCEOF

RESULT=$(run_claude 'The file src/order.service.ts has changed. Run an incremental update: invalidate changed files, re-scan, and if any dependency was removed, add negative evidence.')
ANSWER=$(echo "$RESULT" | python3 -c "import json,sys; print(json.load(sys.stdin)['result'][:400])")
echo "  Claude says: ${ANSWER:0:200}"

EDGE_AFTER=$(sqlite3 "$FIXTURE_DIR/.depbot/chronicle.db" \
    "SELECT e.active, e.trust_score FROM graph_edges e
     JOIN graph_nodes n1 ON e.from_node_id=n1.node_id
     JOIN graph_nodes n2 ON e.to_node_id=n2.node_id
     WHERE n1.name LIKE '%Order%' AND n2.name LIKE '%Payment%' AND e.edge_type='INJECTS'
     LIMIT 1" 2>/dev/null || echo "NOT_FOUND")

ACTIVE_AFTER=$(echo "$EDGE_AFTER" | cut -d'|' -f1)
check_result "7.1 Edge deactivated after code change" "$([ "$ACTIVE_AFTER" = "0" ] && echo true || echo false)" "edge before=$EDGE_BEFORE, after=$EDGE_AFTER"

echo ""

# =============================================
# TEST 7.2: Partial graph coverage → Claude warns and reads code
# =============================================
echo "--- Test 7.2: Partial graph → Claude warns incomplete ---"
setup_fixture
cd "$FIXTURE_DIR"

# Partial scan: only import services, NOT data models
rm -f "$FIXTURE_DIR/.depbot/chronicle.db"
RESULT=$(run_claude 'Scan ONLY the service files in src/ (not prisma schema). Domain is "agenttest". Import service nodes and INJECTS edges only.')

# Now ask about data models — graph has none
RESULT=$(run_claude 'What Prisma models does OrderService use? Check the graph first. If the graph does not have data models, say so and check the prisma schema file.')
ANSWER=$(echo "$RESULT" | python3 -c "import json,sys; print(json.load(sys.stdin)['result'])")
TURNS=$(echo "$RESULT" | python3 -c "import json,sys; print(json.load(sys.stdin)['num_turns'])")

HAS_ORDER_MODEL=$(echo "$ANSWER" | grep -ci "order" || true)
HAS_INCOMPLETE=$(echo "$ANSWER" | grep -ci "incomplete\|not.*found\|no.*data\|no.*model\|missing\|schema" || true)

check_result "7.2a Finds Order model from prisma file" "$([ "$HAS_ORDER_MODEL" -gt 0 ] && echo true || echo false)" "order model mentions=$HAS_ORDER_MODEL"
check_result "7.2b Mentions graph incomplete or reads schema" "$([ "$HAS_INCOMPLETE" -gt 0 ] && echo true || echo false)" "incomplete/schema mentions=$HAS_INCOMPLETE (turns=$TURNS)"

echo ""

# =============================================
# SUMMARY
# =============================================
echo "============================================="
echo "Results: $PASS/$TOTAL passed, $FAIL failed"
echo "============================================="

# Cleanup
rm -rf "$FIXTURE_DIR"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi

# Diagram Construction Rules Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the generic diagram instructions in Chronicle's MCP `chronicle_command(command='diagram')` with the detailed Diagram Type Catalog so Claude constructs proper, consistent diagrams.

**Architecture:** The entire change is in `internal/mcp/commands.go` — replacing the `CommandInstructions["diagram"]` string with the full catalog rules. The existing MCP tools (`chronicle_diagram_create`, `chronicle_diagram_update`, `chronicle_diagram_annotate`) are unchanged. Then we smoke-test against the tom-and-jerry fixture by running each diagram type via the admin dashboard.

**Tech Stack:** Go (string literal replacement), Chronicle MCP tools, tom-and-jerry fixture

---

### Task 1: Replace diagram instructions in commands.go

**Files:**
- Modify: `internal/mcp/commands.go:108-136`

**Context:** The `CommandInstructions` map at the top of `commands.go` holds instruction text for each chronicle command. The `"diagram"` key currently has ~28 lines of generic instructions. We replace it with the full catalog.

- [ ] **Step 1: Read the current diagram instructions**

Verify the current text starts at line 108 and ends at line 136. The current content is:

```go
"diagram": `Live diagram for the user:

IMPORTANT — Node selection priority (less is more):
  1. Services (layer=service) — ALWAYS start here. Show the big picture first.
  ...
Diagram types: service map, dependency, impact, path, custom (explanatory)`,
```

- [ ] **Step 2: Replace with the full catalog instructions**

Replace the `"diagram"` entry (lines 108-136) with the following. This is the complete replacement text:

```go
	"diagram": `Live diagram for the user. Use the Diagram Type Catalog below to construct proper diagrams.

## How to Create a Diagram

1. Identify the diagram type from the user's question (see catalog below)
2. Call chronicle_diagram_create(title="{Type}: {subject}") — get session_id and URL
3. Share URL with user: "Open {url} to see the diagram"
4. Query the graph using the right tool for the diagram type:
   - System Overview: chronicle_node_list(layer='service') + chronicle_edge_list()
   - Request Flow: chronicle_query_path(from, to)
   - Impact Analysis: chronicle_impact(node_key)
   - Dependency Map: chronicle_query_deps(node_key, depth=1)
   - Domain Model: chronicle_node_list(layer='data') + chronicle_edge_list(edge_type='REFERENCES_MODEL')
5. Filter results — only include nodes/edges specified by the diagram type rules
6. Build payload and call chronicle_diagram_update(session_id, payload)
7. Add annotations with chronicle_diagram_annotate (see step rules per type)
8. As conversation evolves, call chronicle_diagram_update again to refine

## Diagram Type Catalog

### Type 1: System Overview
Trigger: "show architecture", "show services", "how does the system work"
Nodes: All service-layer nodes + contract:topic nodes (async channels only, NO endpoints)
Edges: CALLS_SERVICE, PUBLISHES_TOPIC, CONSUMES_TOPIC
Styling: Layer-level — services (#ef4444), topics (#10b981)
Steps: Single step (total_steps=1). Annotate the central orchestrating service.
Title: "Overview: {project name} Architecture"
Description: Summarize how many services and communication patterns (sync/async).
Node cap: 5-10. If >10 services, show only primary ones.

### Type 2: Request Flow
Trigger: "how does X flow to Y", "trace a request", "explain how X calls Y"
Nodes: Only nodes on the request path — controllers, providers, endpoints, services crossed.
  Optional: include data models if a provider on the path uses one (USES_MODEL).
Edges: INJECTS, CALLS_SERVICE, CALLS_ENDPOINT, EXPOSES_ENDPOINT, USES_MODEL (optional)
Styling: Node-type-level — controllers/providers (#3b82f6), endpoints (#10b981), services (#ef4444), models (#8b5cf6)
Steps: Multi-step walkthrough. Each step advances one hop:
  - Step 0: Entry point (endpoint + controller). Highlight: #f59e0b. Dim all others.
  - Step 1..N-1: Each injection or cross-service call. Highlight active nodes, dim inactive.
  - Step N: Arrival at destination.
  step_title: short imperative ("Entry Point", "Cross-Service Call", "Arrival")
  step_description: explain what happens at this hop and why
Title: "Flow: {entry} -> {destination}"
Node cap: 8-12. Only the direct path, no tangential deps.

### Type 3: Impact Analysis
Trigger: "what breaks if I change X", "blast radius of X"
Nodes: Start from changed node, traverse INCOMING edges transitively. Stop at 3 hops or 15 nodes.
Edges: USES_MODEL, INJECTS, EXPOSES_ENDPOINT, REFERENCES_MODEL — anything pointing TO the changed node.
Styling: Heat map by distance from change:
  - Distance 0 (changed): highlight="#ef4444"
  - Distance 1 (direct): highlight="#f59e0b"
  - Distance 2+ (ripple): highlight="#eab308"
Steps: Multi-step, one per distance level:
  - Step 0 "The Change": highlight changed node + co-affected (e.g. referenced models). Dim all others.
  - Step 1 "Direct Impact": reveal distance-1 nodes, explain WHY each is affected.
  - Step 2 "Ripple Effect": reveal distance-2 nodes, show full blast radius.
  step_description: explain WHY each layer is affected, not just that it is.
Title: "Impact: Changing {node name}"
Node cap: 12-15.

### Type 4: Dependency Map
Trigger: "what does X depend on", "dependencies of X"
Nodes: Subject node + all nodes reachable via outgoing edges (1 hop). Include called endpoints.
Edges: CALLS_SERVICE, CALLS_ENDPOINT, USES_MODEL, PUBLISHES_TOPIC, CONSUMES_TOPIC, INJECTS (if code-level subject)
Styling: Node-type-level — services (#ef4444), models (#8b5cf6), topics (#10b981), endpoints (#10b981)
Steps: Single step. Annotate the subject node with its role.
Title: "Dependencies: {node name}"
Description: Summarize dependency profile — how many services, models, async channels.
Node cap: 10-15. If many endpoints, show only the ones actually called.

### Type 5: Domain Model
Trigger: "show data models", "show entities", "show schema"
Nodes: All data:model nodes. Enums are LOW PRIORITY — only include if user asks or total <10 nodes.
Edges: REFERENCES_MODEL only.
Styling: Node-type-level — models (#8b5cf6), enums (#8b5cf6 with note "enum")
Steps: Single step. Annotate models that span service boundaries.
Title: "Domain: {project name} Data Models"
Description: Summarize count of models and which services own what.
Node cap: All data models (typically <20).

## Cross-cutting Rules (apply to ALL types)

- NEVER exceed 15 nodes. Filter aggressively — prioritize nodes that answer the question.
- Every diagram MUST annotate the focal node(s) explaining WHY they matter.
- Only include edge types specified by the diagram type. No CONTAINS in flow diagrams, no INJECTS in overviews.
- For multi-step: all nodes present from start but dimmed. Steps reveal progressively.
- Step descriptions: write as if narrating to someone unfamiliar with the codebase.
- Node payload must include: node_id, node_key, name, layer, node_type.
- Highlight colors must match the diagram type visual language.
- When in doubt, use single step. Multi-step only when there's a clear sequential narrative.`,
```

- [ ] **Step 3: Build to verify syntax**

Run: `cd /home/alex/personal/depbot && go build ./...`
Expected: Clean build, no errors. The string literal is valid Go.

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/commands.go
git commit -m "feat: replace diagram instructions with full Diagram Type Catalog

Replaces the generic 28-line diagram instructions in CommandInstructions
with the complete Diagram Type Catalog (5 types: System Overview,
Request Flow, Impact Analysis, Dependency Map, Domain Model).

Each type defines: trigger phrases, node/edge selection, styling rules,
step structure, annotations, and node caps."
```

---

### Task 2: Smoke-test System Overview diagram (tom-and-jerry)

**Prerequisites:** Chronicle admin running on port 4200 with tom-and-jerry fixture loaded.

- [ ] **Step 1: Start Chronicle admin for tom-and-jerry**

Run: `cd /home/alex/personal/depbot && go run . admin --dir fixtures/tom-and-jerry --port 4200 &`
Expected: "Chronicle Admin: http://localhost:4200" printed to stderr.

- [ ] **Step 2: Create a System Overview diagram via API**

Run:
```bash
# Create session
curl -s -X POST http://localhost:4200/api/diagram \
  -H 'Content-Type: application/json' \
  -d '{"session_id": "test-overview", "title": "Overview: Tom & Jerry Architecture"}'

# Update with service nodes + topic + edges
curl -s -X PUT http://localhost:4200/api/diagram/test-overview \
  -H 'Content-Type: application/json' \
  -d '{
    "nodes": [
      {"node_id": 1, "node_key": "service:service:tomandjerry:tom-api", "name": "tom-api", "layer": "service", "node_type": "service"},
      {"node_id": 2, "node_key": "service:service:tomandjerry:jerry-api", "name": "jerry-api", "layer": "service", "node_type": "service"},
      {"node_id": 3, "node_key": "service:service:tomandjerry:arena-api", "name": "arena-api", "layer": "service", "node_type": "service"},
      {"node_id": 4, "node_key": "service:service:tomandjerry:spectators-api", "name": "spectators-api", "layer": "service", "node_type": "service"},
      {"node_id": 5, "node_key": "contract:topic:tomandjerry:battle-results", "name": "battle-results", "layer": "contract", "node_type": "topic"}
    ],
    "edges": [
      {"from_node_id": 3, "to_node_id": 1, "edge_type": "CALLS_SERVICE"},
      {"from_node_id": 3, "to_node_id": 2, "edge_type": "CALLS_SERVICE"},
      {"from_node_id": 4, "to_node_id": 1, "edge_type": "CALLS_SERVICE"},
      {"from_node_id": 4, "to_node_id": 2, "edge_type": "CALLS_SERVICE"},
      {"from_node_id": 3, "to_node_id": 5, "edge_type": "PUBLISHES_TOPIC"},
      {"from_node_id": 5, "to_node_id": 4, "edge_type": "CONSUMES_TOPIC"}
    ]
  }'

# Annotate central service
curl -s -X PUT http://localhost:4200/api/diagram/test-overview/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "service:service:tomandjerry:arena-api", "note": "Orchestrates battles between tom-api and jerry-api", "highlight": "#ef4444"}'
```

Expected: Each curl returns `{"status":"ok",...}` or `{"session_id":"test-overview",...}`.

- [ ] **Step 3: Verify in browser**

Open: `http://localhost:4200/diagram/test-overview`
Expected: 5 nodes (4 services + 1 topic), edges showing CALLS_SERVICE and pub/sub, annotation on arena-api.
Verify: services colored red, topic colored green. Single step, no step navigation.

- [ ] **Step 4: Verify rules compliance**

Check against Type 1 rules:
- [ ] Only service + topic nodes (no code, no endpoints) ✓
- [ ] Only CALLS_SERVICE + PUBLISHES/CONSUMES_TOPIC edges ✓
- [ ] Single step ✓
- [ ] 5 nodes (within 5-10 cap) ✓
- [ ] Title format "Overview: ..." ✓
- [ ] Focal node annotated ✓

---

### Task 3: Smoke-test Request Flow diagram (tom-and-jerry)

- [ ] **Step 1: Create a Request Flow diagram**

Run:
```bash
curl -s -X POST http://localhost:4200/api/diagram \
  -H 'Content-Type: application/json' \
  -d '{"session_id": "test-flow", "title": "Flow: POST /arena/attack -> GET /tom/status"}'

curl -s -X PUT http://localhost:4200/api/diagram/test-flow \
  -H 'Content-Type: application/json' \
  -d '{
    "nodes": [
      {"node_id": 1, "node_key": "contract:endpoint:tomandjerry:post:/arena/attack", "name": "POST /arena/attack", "layer": "contract", "node_type": "endpoint"},
      {"node_id": 2, "node_key": "code:controller:tomandjerry:arenacontroller", "name": "ArenaController", "layer": "code", "node_type": "controller"},
      {"node_id": 3, "node_key": "code:provider:tomandjerry:arenaservice", "name": "ArenaService", "layer": "code", "node_type": "provider"},
      {"node_id": 4, "node_key": "code:provider:tomandjerry:tomclient", "name": "TomClient", "layer": "code", "node_type": "provider"},
      {"node_id": 5, "node_key": "contract:endpoint:tomandjerry:get:/tom/status", "name": "GET /tom/status", "layer": "contract", "node_type": "endpoint"},
      {"node_id": 6, "node_key": "service:service:tomandjerry:tom-api", "name": "tom-api", "layer": "service", "node_type": "service"},
      {"node_id": 7, "node_key": "code:controller:tomandjerry:tomcontroller", "name": "TomController", "layer": "code", "node_type": "controller"}
    ],
    "edges": [
      {"from_node_id": 2, "to_node_id": 1, "edge_type": "EXPOSES_ENDPOINT"},
      {"from_node_id": 2, "to_node_id": 3, "edge_type": "INJECTS"},
      {"from_node_id": 3, "to_node_id": 4, "edge_type": "INJECTS"},
      {"from_node_id": 4, "to_node_id": 5, "edge_type": "CALLS_ENDPOINT"},
      {"from_node_id": 4, "to_node_id": 6, "edge_type": "CALLS_SERVICE"},
      {"from_node_id": 7, "to_node_id": 5, "edge_type": "EXPOSES_ENDPOINT"}
    ]
  }'
```

Expected: `{"status":"ok",...}`

- [ ] **Step 2: Add multi-step annotations**

Run:
```bash
# Step 0: Entry point
curl -s -X PUT http://localhost:4200/api/diagram/test-flow/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "code:controller:tomandjerry:arenacontroller", "note": "Request enters here", "highlight": "#f59e0b", "step": 0, "step_title": "Entry Point", "step_description": "POST /arena/attack hits ArenaController. It exposes this endpoint and delegates to ArenaService via dependency injection."}'

curl -s -X PUT http://localhost:4200/api/diagram/test-flow/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "contract:endpoint:tomandjerry:post:/arena/attack", "highlight": "#f59e0b", "step": 0}'

# Step 1: Internal wiring
curl -s -X PUT http://localhost:4200/api/diagram/test-flow/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "code:provider:tomandjerry:arenaservice", "note": "Injected by controller", "highlight": "#f59e0b", "step": 1, "step_title": "Injection Chain", "step_description": "ArenaController injects ArenaService, which injects TomClient. This is the internal wiring inside arena-api before any external call."}'

curl -s -X PUT http://localhost:4200/api/diagram/test-flow/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "code:provider:tomandjerry:tomclient", "highlight": "#f59e0b", "step": 1}'

# Step 2: Cross-service call
curl -s -X PUT http://localhost:4200/api/diagram/test-flow/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "service:service:tomandjerry:tom-api", "note": "Service boundary crossed", "highlight": "#f59e0b", "step": 2, "step_title": "Cross-Service Call", "step_description": "TomClient makes an HTTP GET to /tom/status on tom-api. This is the network hop — the first time a service boundary is crossed."}'

curl -s -X PUT http://localhost:4200/api/diagram/test-flow/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "contract:endpoint:tomandjerry:get:/tom/status", "highlight": "#f59e0b", "step": 2}'

# Step 3: Arrival
curl -s -X PUT http://localhost:4200/api/diagram/test-flow/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "code:controller:tomandjerry:tomcontroller", "note": "Request arrives here", "highlight": "#f59e0b", "step": 3, "step_title": "Arrival", "step_description": "The request lands on TomController, which exposes GET /tom/status. Full path: ArenaController -> ArenaService -> TomClient -> tom-api -> TomController."}'
```

Expected: All return `{"status":"ok"}`.

- [ ] **Step 3: Verify in browser**

Open: `http://localhost:4200/diagram/test-flow`
Expected: 7 nodes, 6 edges. Step navigation (4 steps). Each step highlights relevant nodes.
Verify: multi-step navigation works, descriptions are shown, active nodes are highlighted.

---

### Task 4: Smoke-test Impact Analysis diagram (tom-and-jerry)

- [ ] **Step 1: Create an Impact Analysis diagram**

Run:
```bash
curl -s -X POST http://localhost:4200/api/diagram \
  -H 'Content-Type: application/json' \
  -d '{"session_id": "test-impact", "title": "Impact: Changing Cat model"}'

curl -s -X PUT http://localhost:4200/api/diagram/test-impact \
  -H 'Content-Type: application/json' \
  -d '{
    "nodes": [
      {"node_id": 1, "node_key": "data:model:tomandjerry:cat", "name": "Cat", "layer": "data", "node_type": "model"},
      {"node_id": 2, "node_key": "data:model:tomandjerry:catweapon", "name": "CatWeapon", "layer": "data", "node_type": "model"},
      {"node_id": 3, "node_key": "code:provider:tomandjerry:tomservice", "name": "TomService", "layer": "code", "node_type": "provider"},
      {"node_id": 4, "node_key": "code:provider:tomandjerry:prismaservice-tom", "name": "PrismaService (tom)", "layer": "code", "node_type": "provider"},
      {"node_id": 5, "node_key": "code:controller:tomandjerry:tomcontroller", "name": "TomController", "layer": "code", "node_type": "controller"},
      {"node_id": 6, "node_key": "contract:endpoint:tomandjerry:get:/tom/status", "name": "GET /tom/status", "layer": "contract", "node_type": "endpoint"},
      {"node_id": 7, "node_key": "contract:endpoint:tomandjerry:get:/tom/weapons", "name": "GET /tom/weapons", "layer": "contract", "node_type": "endpoint"},
      {"node_id": 8, "node_key": "contract:endpoint:tomandjerry:post:/tom/arm", "name": "POST /tom/arm", "layer": "contract", "node_type": "endpoint"}
    ],
    "edges": [
      {"from_node_id": 1, "to_node_id": 2, "edge_type": "REFERENCES_MODEL"},
      {"from_node_id": 3, "to_node_id": 1, "edge_type": "USES_MODEL"},
      {"from_node_id": 3, "to_node_id": 2, "edge_type": "USES_MODEL"},
      {"from_node_id": 3, "to_node_id": 4, "edge_type": "INJECTS"},
      {"from_node_id": 5, "to_node_id": 3, "edge_type": "INJECTS"},
      {"from_node_id": 5, "to_node_id": 6, "edge_type": "EXPOSES_ENDPOINT"},
      {"from_node_id": 5, "to_node_id": 7, "edge_type": "EXPOSES_ENDPOINT"},
      {"from_node_id": 5, "to_node_id": 8, "edge_type": "EXPOSES_ENDPOINT"}
    ]
  }'
```

- [ ] **Step 2: Add heat-map step annotations**

Run:
```bash
# Step 0: The Change (red)
curl -s -X PUT http://localhost:4200/api/diagram/test-impact/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "data:model:tomandjerry:cat", "note": "Schema change here", "highlight": "#ef4444", "step": 0, "step_title": "The Change", "step_description": "The Cat model is being modified. It references CatWeapon — any schema change could cascade to related models."}'

curl -s -X PUT http://localhost:4200/api/diagram/test-impact/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "data:model:tomandjerry:catweapon", "highlight": "#ef4444", "step": 0}'

# Step 1: Direct Impact (orange)
curl -s -X PUT http://localhost:4200/api/diagram/test-impact/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "code:provider:tomandjerry:tomservice", "note": "Uses Cat and CatWeapon directly", "highlight": "#f59e0b", "step": 1, "step_title": "Direct Impact", "step_description": "TomService uses both Cat and CatWeapon via USES_MODEL. PrismaService handles DB queries — its generated client will change."}'

curl -s -X PUT http://localhost:4200/api/diagram/test-impact/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "code:provider:tomandjerry:prismaservice-tom", "highlight": "#f59e0b", "step": 1}'

# Step 2: Ripple (yellow)
curl -s -X PUT http://localhost:4200/api/diagram/test-impact/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "code:controller:tomandjerry:tomcontroller", "note": "Injects TomService", "highlight": "#eab308", "step": 2, "step_title": "Ripple Effect", "step_description": "TomController injects TomService — if the service interface changes, the controller adapts. All 3 endpoints could return different shapes."}'

curl -s -X PUT http://localhost:4200/api/diagram/test-impact/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "contract:endpoint:tomandjerry:get:/tom/status", "highlight": "#eab308", "step": 2}'

curl -s -X PUT http://localhost:4200/api/diagram/test-impact/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "contract:endpoint:tomandjerry:get:/tom/weapons", "highlight": "#eab308", "step": 2}'

curl -s -X PUT http://localhost:4200/api/diagram/test-impact/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "contract:endpoint:tomandjerry:post:/tom/arm", "highlight": "#eab308", "step": 2}'
```

- [ ] **Step 3: Verify in browser**

Open: `http://localhost:4200/diagram/test-impact`
Expected: 8 nodes, 8 edges. 3 steps. Heat colors: red → orange → yellow by distance.

---

### Task 5: Smoke-test Dependency Map diagram (tom-and-jerry)

- [ ] **Step 1: Create a Dependency Map diagram**

Run:
```bash
curl -s -X POST http://localhost:4200/api/diagram \
  -H 'Content-Type: application/json' \
  -d '{"session_id": "test-deps", "title": "Dependencies: arena-api"}'

curl -s -X PUT http://localhost:4200/api/diagram/test-deps \
  -H 'Content-Type: application/json' \
  -d '{
    "nodes": [
      {"node_id": 1, "node_key": "service:service:tomandjerry:arena-api", "name": "arena-api", "layer": "service", "node_type": "service"},
      {"node_id": 2, "node_key": "service:service:tomandjerry:tom-api", "name": "tom-api", "layer": "service", "node_type": "service"},
      {"node_id": 3, "node_key": "service:service:tomandjerry:jerry-api", "name": "jerry-api", "layer": "service", "node_type": "service"},
      {"node_id": 4, "node_key": "data:model:tomandjerry:battleevent", "name": "BattleEvent", "layer": "data", "node_type": "model"},
      {"node_id": 5, "node_key": "contract:topic:tomandjerry:battle-results", "name": "battle-results", "layer": "contract", "node_type": "topic"},
      {"node_id": 6, "node_key": "contract:endpoint:tomandjerry:get:/tom/status", "name": "GET /tom/status", "layer": "contract", "node_type": "endpoint"},
      {"node_id": 7, "node_key": "contract:endpoint:tomandjerry:get:/jerry/status", "name": "GET /jerry/status", "layer": "contract", "node_type": "endpoint"},
      {"node_id": 8, "node_key": "contract:endpoint:tomandjerry:get:/jerry/traps", "name": "GET /jerry/traps", "layer": "contract", "node_type": "endpoint"}
    ],
    "edges": [
      {"from_node_id": 1, "to_node_id": 2, "edge_type": "CALLS_SERVICE"},
      {"from_node_id": 1, "to_node_id": 3, "edge_type": "CALLS_SERVICE"},
      {"from_node_id": 1, "to_node_id": 4, "edge_type": "USES_MODEL"},
      {"from_node_id": 1, "to_node_id": 5, "edge_type": "PUBLISHES_TOPIC"},
      {"from_node_id": 1, "to_node_id": 6, "edge_type": "CALLS_ENDPOINT"},
      {"from_node_id": 1, "to_node_id": 7, "edge_type": "CALLS_ENDPOINT"},
      {"from_node_id": 1, "to_node_id": 8, "edge_type": "CALLS_ENDPOINT"}
    ]
  }'

# Annotate subject
curl -s -X PUT http://localhost:4200/api/diagram/test-deps/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "service:service:tomandjerry:arena-api", "note": "Battle orchestrator — depends on 2 services, 1 model, 1 topic", "highlight": "#ef4444"}'
```

- [ ] **Step 2: Verify in browser**

Open: `http://localhost:4200/diagram/test-deps`
Expected: 8 nodes, 7 edges. Single step. arena-api annotated. Dependencies radiate outward.

---

### Task 6: Smoke-test Domain Model diagram (tom-and-jerry)

- [ ] **Step 1: Create a Domain Model diagram**

Run:
```bash
curl -s -X POST http://localhost:4200/api/diagram \
  -H 'Content-Type: application/json' \
  -d '{"session_id": "test-domain", "title": "Domain: Tom & Jerry Data Models"}'

curl -s -X PUT http://localhost:4200/api/diagram/test-domain \
  -H 'Content-Type: application/json' \
  -d '{
    "nodes": [
      {"node_id": 1, "node_key": "data:model:tomandjerry:cat", "name": "Cat", "layer": "data", "node_type": "model"},
      {"node_id": 2, "node_key": "data:model:tomandjerry:catweapon", "name": "CatWeapon", "layer": "data", "node_type": "model"},
      {"node_id": 3, "node_key": "data:model:tomandjerry:mouse", "name": "Mouse", "layer": "data", "node_type": "model"},
      {"node_id": 4, "node_key": "data:model:tomandjerry:trap", "name": "Trap", "layer": "data", "node_type": "model"},
      {"node_id": 5, "node_key": "data:model:tomandjerry:battleevent", "name": "BattleEvent", "layer": "data", "node_type": "model"}
    ],
    "edges": [
      {"from_node_id": 1, "to_node_id": 2, "edge_type": "REFERENCES_MODEL"},
      {"from_node_id": 3, "to_node_id": 4, "edge_type": "REFERENCES_MODEL"}
    ]
  }'

# Annotate cross-service model
curl -s -X PUT http://localhost:4200/api/diagram/test-domain/annotate \
  -H 'Content-Type: application/json' \
  -d '{"node_key": "data:model:tomandjerry:battleevent", "note": "Cross-service event record (arena-api)", "highlight": "#8b5cf6"}'
```

- [ ] **Step 2: Verify in browser**

Open: `http://localhost:4200/diagram/test-domain`
Expected: 5 model nodes (no enums), 2 REFERENCES_MODEL edges. Single step. BattleEvent annotated.
Verify: No enum nodes present (low priority, not requested). Only REFERENCES_MODEL edges.

---

### Task 7: Clean up test diagrams and final commit

- [ ] **Step 1: Stop the test admin server**

Run: `kill %1` (or find and kill the chronicle admin process)

- [ ] **Step 2: Verify the build is clean**

Run: `cd /home/alex/personal/depbot && go build ./... && go vet ./...`
Expected: No errors, no warnings.

- [ ] **Step 3: Final commit if any remaining changes**

Only if there are uncommitted changes beyond Task 1's commit:

```bash
git add -A
git commit -m "test: smoke-test all 5 diagram types against tom-and-jerry fixture"
```

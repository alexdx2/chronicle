# Chronicle Diagram Construction Rules

Rules for Claude to construct proper diagrams in the Chronicle admin dashboard via the Diagram Mode API.

## Scope

When Claude wants to explain, visualize, or answer questions about a codebase's architecture, it creates a DiagramSession using the Chronicle API (POST /api/diagram, PUT /api/diagram/:id, PUT /api/diagram/:id/annotate). The user views the result in the admin dashboard's Diagram tab.

These rules define **what** Claude should put into a diagram — which nodes to select, which edges to include, how to structure steps, and how to annotate — for each diagram type.

## The Diagram Type Catalog

Claude picks the matching type based on the user's question, then follows that type's recipe.

| Type | Trigger phrases | Steps | Styling |
|------|----------------|-------|---------|
| System Overview | "show architecture", "show services", "how does the system work" | Single | Layer-level |
| Request Flow | "how does X flow to Y", "trace a request", "explain how X calls Y" | Multi | Node-type |
| Impact Analysis | "what breaks if I change X", "blast radius of X" | Multi | Heat map |
| Dependency Map | "what does X depend on", "dependencies of X" | Single | Node-type |
| Domain Model | "show data models", "show entities", "show schema" | Single | Node-type |

---

## Type 1: System Overview

**Purpose:** Bird's-eye view of the entire system — services and how they communicate.

**Node selection:**
- All `service` layer nodes
- `contract:topic` nodes (async channels only — no endpoints)

**Edge selection:**
- CALLS_SERVICE — synchronous service-to-service calls
- PUBLISHES_TOPIC / CONSUMES_TOPIC — async messaging

**Styling:**
- Layer-level colors: services use service color (#ef4444), topics use contract color (#10b981)
- Topics rendered as pill shapes (high rx border-radius) with dashed border to distinguish from services
- Services rendered as standard rounded rectangles

**Structure:**
- Single step — the whole picture at once
- Title format: "Overview: {domain/project name} Architecture"
- Description: Summarize the system — how many services, what communication patterns (sync/async)

**Annotations:**
- Annotate the central orchestrating service explaining its role
- If there's an async pattern, annotate the topic with what flows through it

**Node cap:** ~5-10 nodes. If there are more than 10 services, group by subdomain or show only the primary services.

---

## Type 2: Request Flow

**Purpose:** Trace a request path step-by-step from entry to destination, showing each hop.

**Node selection:**
- Only nodes on the actual request path — controllers, providers, endpoints, and services that are crossed
- Optionally include data models if a provider on the path uses one via USES_MODEL (show on the step where that provider is highlighted)

**Edge selection:**
- INJECTS — dependency injection within a service
- CALLS_SERVICE — cross-service hop
- CALLS_ENDPOINT — specific endpoint called
- EXPOSES_ENDPOINT — which controller exposes the target endpoint
- USES_MODEL — optional, only if a data model is relevant on the path

**Styling:**
- Node-type-level colors: controllers/providers use code color (#3b82f6), endpoints use contract color (#10b981), services use service color (#ef4444), models use data color (#8b5cf6)
- Each node shows its type label (controller, provider, endpoint, service, model)

**Structure:**
- Multi-step walkthrough — each step advances the request one hop
- Step 1: Entry point (endpoint + controller that receives the request)
- Middle steps: Each injection or cross-service call
- Final step: Arrival at the destination controller/endpoint
- Title format: "Flow: {entry point} -> {destination}"

**Step behavior:**
- Active nodes on the current step: highlighted (yellow/gold stroke)
- Inactive nodes: dimmed (low opacity)
- Each step description explains what happens at this hop and why

**Node cap:** ~8-12 nodes. Only include nodes on the direct path — no tangential dependencies.

---

## Type 3: Impact Analysis

**Purpose:** Show the blast radius of changing a node — what depends on it, transitively.

**Node selection:**
- Start from the changed node
- Traverse all INCOMING edges — "who depends on this?"
- Continue transitively up to 3 hops or ~15 nodes (whichever comes first)

**Edge selection:**
- All edge types that point TO the changed node, transitively:
  - USES_MODEL (provider uses the changed model)
  - INJECTS (controller injects the affected provider)
  - EXPOSES_ENDPOINT (controller exposes endpoints that may change)
  - REFERENCES_MODEL (another model references the changed model)

**Styling — heat map by distance:**
- Distance 0 (the changed node): Red (#ef4444) — thick border, bold text
- Distance 1 (direct dependents): Orange (#f59e0b)
- Distance 2+ (ripple): Yellow (#eab308)
- Edges colored to match their target's heat level

**Structure:**
- Multi-step — one step per distance level
- Step 1: "The Change" — show the changed node (and any directly co-affected nodes like referenced models)
- Step 2: "Direct Impact" — reveal distance-1 nodes, explain WHY each is affected
- Step 3: "Ripple Effect" — reveal distance-2 nodes, show the full picture
- Title format: "Impact: Changing {node name}"

**Step behavior:**
- Each step reveals the next distance ring
- Previously revealed nodes retain their heat color
- Not-yet-revealed nodes are dimmed
- Final step shows everything — heat colors tell the full story

**Step descriptions:** Explain WHY each layer is affected, not just that it is. E.g., "TomService uses Cat directly via USES_MODEL — any field changes require updating service methods."

---

## Type 4: Dependency Map

**Purpose:** Show everything a given node depends on — all outgoing edges from a subject.

**Node selection:**
- The subject node (centered)
- All nodes reachable via outgoing edges from the subject (1 hop)
- For service-level subjects: include the endpoints it calls (detail nodes)

**Edge selection:**
- CALLS_SERVICE — which services it calls
- CALLS_ENDPOINT — which specific endpoints (shown as dashed detail edges)
- USES_MODEL — which data models it uses
- PUBLISHES_TOPIC / CONSUMES_TOPIC — async dependencies
- INJECTS — internal provider dependencies (only if subject is a code-level node)

**Styling:**
- Node-type-level colors: services (#ef4444), models (#8b5cf6), topics (#10b981 pill + dashed), endpoints (#10b981)
- Subject node: larger, thicker border, centered
- Detail edges (CALLS_ENDPOINT): dashed lines to distinguish from primary edges

**Layout:**
- Subject centered
- Dependencies radiate outward, grouped by category:
  - Service deps (top)
  - Data deps (bottom-left)
  - Async deps (bottom-right)
- Light muted category labels above each cluster

**Structure:**
- Single step — all dependencies visible at once
- Title format: "Dependencies: {node name}"
- Description: Summarize the dependency profile — how many services, models, async channels

**Annotations:**
- Annotate the subject node with a brief description of its role

**Node cap:** ~10-15 nodes. If a service has many endpoints, show only the ones actually called.

---

## Type 5: Domain Model

**Purpose:** Show the data layer — all models and their relationships.

**Node selection:**
- All `data:model` nodes
- `data:enum` nodes are low-priority — only include if the user specifically asks or if total node count is under ~10

**Edge selection:**
- REFERENCES_MODEL — entity relationships (foreign keys, has-many)

**Styling:**
- Models: rounded rectangle cards with name, "model" badge, and key field names listed
- Enums (when shown): dashed pill shape — visually distinct from models
- Group models by owning service inside dashed boundary boxes with the service name as label

**Layout:**
- Models grouped by owning service (from repo_name)
- Within each group: parent model above, referenced child below
- Groups arranged side by side

**Structure:**
- Single step — all models visible at once
- Title format: "Domain: {project name} Data Models"
- Description: Summarize — count of models, which services own what

**Annotations:**
- Annotate models that span service boundaries or have special cross-cutting significance

---

## Cross-cutting Rules

These apply to ALL diagram types:

### Annotation standards
- Every diagram must annotate the focal node(s) explaining WHY they matter
- Annotations use the DiagramNote format: `{note: "text", highlight: "color"}`
- Highlight colors should match the diagram type's visual language (heat colors for impact, layer colors for overview, etc.)

### Title and description
- Title format: "{Type}: {subject}" — e.g., "Overview: Tom & Jerry Architecture"
- Step titles: short imperative — "Entry Point", "Direct Impact", "The Change"
- Step descriptions: explain what to look at AND why — not just labels. Write as if narrating to someone unfamiliar with the codebase.

### Node limits
- Never exceed ~15 nodes in a single diagram
- If the real graph has more nodes than the cap, filter aggressively — prioritize nodes that answer the user's question
- For multi-step diagrams, all nodes are present from the start but dimmed — steps reveal them progressively

### Edge discipline
- Only include edge types relevant to the diagram type — don't show CONTAINS in a flow diagram, don't show INJECTS in an overview
- Edge type selection is defined per diagram type above — follow it strictly
- Edge colors: use the layer color of the target node by default. Exception: Impact Analysis uses heat-map colors based on distance.

### Node metadata
- Always include: node_key, layer, node_type, name
- Include repo_name when grouping by service
- Include file_path when relevant (domain model, impact analysis)

### API call sequence
1. POST /api/diagram — create session with title
2. PUT /api/diagram/:id — set nodes and edges
3. PUT /api/diagram/:id/annotate — add annotations per step (repeat for each step + each annotated node)

### Single-step vs multi-step decision
- Single step: overview, dependency, domain — the whole picture matters equally
- Multi-step: flow, impact — there's a narrative progression (path traversal or distance rings)
- When in doubt, use single step. Multi-step adds complexity — only use it when there's a clear sequential narrative.

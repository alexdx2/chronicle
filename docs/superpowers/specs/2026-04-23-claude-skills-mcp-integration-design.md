# Sub-project 3: Self-Describing Oracle + MCP Integration

## Summary

Make the Oracle MCP server self-describing so Claude Code can analyze any project without per-project CLAUDE.md instructions. Add an `oracle_extraction_guide` tool that returns the full extraction methodology, an `oracle_scan_status` tool for graph state awareness, enhanced tool descriptions, and setup documentation.

## Core Principle

**Zero per-project configuration beyond `oracle init` + MCP config.**

Claude connects to the Oracle MCP server, discovers tools via their descriptions, calls `oracle_extraction_guide` to learn the extraction methodology, and can then analyze any codebase. The intelligence lives in the tool, not in project-specific files.

## New MCP Tools

### oracle_extraction_guide

Returns structured extraction instructions as JSON. Claude calls this before scanning a repo to understand what to extract and how.

**Parameters:** `technology` (optional, string) — filter guide to a specific stack (e.g., "nestjs", "openapi"). If omitted, returns the full guide.

**Response structure:**

```json
{
  "workflow": {
    "steps": [
      "1. Call oracle_scan_status to check current graph state",
      "2. Call oracle_revision_create to start a new scan",
      "3. Read the oracle.domain.yaml to know which repos to scan",
      "4. For each repo: read source files, identify entities and relationships",
      "5. Build an import payload with nodes, edges, and evidence",
      "6. Call oracle_import_all with the payload",
      "7. Call oracle_snapshot_create to record the scan result",
      "8. Call oracle_stale_mark to flag entities not seen in this scan"
    ],
    "key_format": "layer:type:domain:qualified_name (all lowercase, no spaces)",
    "domain_from": "Read domain from oracle.domain.yaml"
  },
  "node_extraction": {
    "typescript_nestjs": {
      "module": {
        "layer": "code",
        "node_type": "module",
        "identify_by": "@Module decorator on a class",
        "key_example": "code:module:orders:ordersmodule",
        "evidence": "file path + line range of the @Module decorator"
      },
      "controller": {
        "layer": "code",
        "node_type": "controller",
        "identify_by": "@Controller decorator on a class",
        "key_example": "code:controller:orders:orderscontroller",
        "evidence": "file path + line range of the class declaration"
      },
      "provider": {
        "layer": "code",
        "node_type": "provider",
        "identify_by": "@Injectable decorator on a class",
        "key_example": "code:provider:orders:ordersservice",
        "evidence": "file path + line range of the class declaration"
      },
      "endpoint": {
        "layer": "contract",
        "node_type": "endpoint",
        "identify_by": "@Get/@Post/@Put/@Delete/@Patch decorators on methods, combined with @Controller prefix",
        "key_example": "contract:endpoint:orders:post:/orders/:id/capture",
        "evidence": "file path + line range of the route decorator"
      },
      "topic": {
        "layer": "contract",
        "node_type": "topic",
        "identify_by": "Kafka producer/consumer references, topic name constants",
        "key_example": "contract:topic:orders:order-created",
        "evidence": "file path + line of topic constant or producer/consumer config"
      }
    },
    "openapi": {
      "endpoint": {
        "layer": "contract",
        "node_type": "endpoint",
        "identify_by": "paths in openapi.yaml/openapi.json — each method+path = one endpoint",
        "key_example": "contract:endpoint:orders:get:/orders/:id",
        "evidence": "openapi file path + path location"
      },
      "http_api": {
        "layer": "contract",
        "node_type": "http_api",
        "identify_by": "top-level info in openapi spec",
        "key_example": "contract:http_api:orders:orders-api",
        "evidence": "openapi file path"
      }
    },
    "service_nodes": {
      "repository": {
        "layer": "code",
        "node_type": "repository",
        "identify_by": "each entry in oracle.domain.yaml repositories",
        "key_example": "code:repository:orders:orders-api"
      },
      "service": {
        "layer": "service",
        "node_type": "service",
        "identify_by": "each deployable service in the domain, typically 1:1 with repositories",
        "key_example": "service:service:orders:orders-api"
      }
    }
  },
  "edge_extraction": {
    "CONTAINS": {
      "meaning": "Structural containment — module contains controllers/providers, repo contains modules",
      "derivation": "hard",
      "from_to": "code:module → code:controller/provider, code:repository → code:module",
      "identify_by": "@Module({ controllers: [...], providers: [...] }) decorator",
      "structural": true,
      "note": "Excluded from dependency path and impact analysis by default"
    },
    "INJECTS": {
      "meaning": "Dependency injection — one class injects another via constructor",
      "derivation": "hard",
      "from_to": "code:controller/provider → code:provider",
      "identify_by": "Constructor parameter with type annotation matching an @Injectable class"
    },
    "EXPOSES_ENDPOINT": {
      "meaning": "Controller exposes an HTTP endpoint via route decorator",
      "derivation": "hard",
      "from_to": "code:controller → contract:endpoint",
      "identify_by": "@Get/@Post/etc decorator on controller method",
      "note": "No reverse impact — endpoint change doesn't impact the controller"
    },
    "CALLS_ENDPOINT": {
      "meaning": "Code calls a specific HTTP endpoint on another service",
      "derivation": "linked",
      "from_to": "code:provider → contract:endpoint",
      "identify_by": "HTTP client call with URL matching a known endpoint path"
    },
    "CALLS_SERVICE": {
      "meaning": "Code depends on an external service (coarse-grained)",
      "derivation": "linked",
      "from_to": "code:provider → service:service",
      "identify_by": "HTTP client configured with service URL, environment variable referencing service"
    },
    "PUBLISHES_TOPIC": {
      "meaning": "Producer publishes messages to a Kafka topic",
      "derivation": "hard",
      "from_to": "code:provider → contract:topic",
      "identify_by": "Kafka producer send() with topic name",
      "note": "No reverse impact — topic change doesn't impact the producer"
    },
    "CONSUMES_TOPIC": {
      "meaning": "Consumer subscribes to a Kafka topic",
      "derivation": "hard",
      "from_to": "code:provider → contract:topic",
      "identify_by": "Kafka consumer subscribe/handler with topic name"
    }
  },
  "evidence_rules": {
    "extractor_id": "claude-code",
    "extractor_version": "1.0",
    "target_kind": "node or edge",
    "required_fields": ["source_kind", "extractor_id", "extractor_version"],
    "source_kinds": {
      "file": "TypeScript/JavaScript source files",
      "openapi": "OpenAPI spec files",
      "asyncapi": "AsyncAPI spec files"
    },
    "best_practice": "Always include file_path + line_start at minimum. Include line_end for multi-line evidence. The evidence should point to the exact code that proves the relationship."
  },
  "confidence_rules": {
    "hard": {
      "range": "0.90-1.00",
      "when": "Directly visible in AST: decorators, imports, type annotations, explicit config"
    },
    "linked": {
      "range": "0.70-0.89",
      "when": "Convention-based: service URL in env var, topic name matching, naming patterns"
    },
    "inferred": {
      "range": "0.40-0.69",
      "when": "Guessed from context: co-located files, naming similarity, comments"
    },
    "unknown": {
      "range": "0.00-0.39",
      "when": "Cannot determine relationship strength"
    }
  },
  "import_payload_format": {
    "nodes": [
      {
        "node_key": "layer:type:domain:name (required, lowercase)",
        "layer": "code|service|contract|flow|ownership|infra|ci (required)",
        "node_type": "from registry (required)",
        "domain_key": "from oracle.domain.yaml (required)",
        "name": "human-readable name (required)",
        "repo_name": "repository name (optional)",
        "file_path": "relative file path within repo (optional)",
        "confidence": "0.0-1.0 (optional, default 1.0)",
        "metadata": "JSON string (optional)"
      }
    ],
    "edges": [
      {
        "from_node_key": "must reference an existing node (required)",
        "to_node_key": "must reference an existing node (required)",
        "edge_type": "from registry (required)",
        "derivation_kind": "hard|linked|inferred|unknown (required)",
        "from_layer": "layer of from node (required for validation)",
        "to_layer": "layer of to node (required for validation)",
        "confidence": "0.0-1.0 (optional, default 1.0)"
      }
    ],
    "evidence": [
      {
        "target_kind": "node|edge (required)",
        "node_key": "for node evidence (required if target_kind=node)",
        "edge_key": "for edge evidence (auto-generated if omitted)",
        "source_kind": "file|openapi|graphql|... (required)",
        "repo_name": "repository name",
        "file_path": "relative path",
        "line_start": "start line number",
        "line_end": "end line number",
        "extractor_id": "claude-code (required)",
        "extractor_version": "1.0 (required)",
        "confidence": "0.0-1.0"
      }
    ]
  }
}
```

### oracle_scan_status

Returns current graph state for a domain.

**Parameters:** `domain` (optional, string) — if omitted, returns status for all domains found in the DB.

**Response:**

```json
{
  "domain": "orders",
  "last_revision": {
    "revision_id": 5,
    "git_after_sha": "abc123",
    "trigger_kind": "manual",
    "mode": "full",
    "created_at": "2026-04-23T10:00:00Z"
  },
  "graph_stats": {
    "node_count": 18,
    "edge_count": 17,
    "active_nodes": 18,
    "stale_nodes": 0,
    "nodes_by_layer": {"code": 9, "service": 2, "contract": 7},
    "edges_by_type": {"CONTAINS": 7, "INJECTS": 3, ...}
  },
  "last_snapshot": {
    "snapshot_id": 3,
    "created_at": "2026-04-23T10:00:05Z"
  }
}
```

### Enhanced Tool Descriptions

All 15 existing MCP tools get improved descriptions explaining context and usage, not just parameter schema. Examples:

- `oracle_import_all`: "Bulk import nodes, edges, and evidence in a single atomic transaction. The payload is a JSON string containing nodes, edges, and evidence arrays. Call oracle_extraction_guide first to understand the payload format. All entries are validated against the type registry — if any validation fails, the entire import is rolled back."
- `oracle_query_deps`: "Query forward dependencies of a node. Returns nodes reachable via outgoing edges. Structural edges (CONTAINS, OWNED_BY, etc.) are traversed by default — these are dependency edges in the code/contract/service layers. Use --derivation to filter by edge confidence level."
- `oracle_impact`: "Analyze the blast radius of a change to a node. Uses reverse dependency traversal with the traversal policy — structural edges (CONTAINS) are excluded, and edges like EXPOSES_ENDPOINT don't propagate reverse impact. Returns scored impact list sorted by proximity."

## Store Additions

### Latest Revision Query

Add `GetLatestRevision(domainKey string) (*Revision, error)` to the store — returns the most recent revision for a domain, needed by `oracle_scan_status`.

### Latest Snapshot Query

Add `GetLatestSnapshot(domainKey string) (*SnapshotRow, error)` — returns the most recent snapshot.

## Setup Documentation

Create `docs/setup.md` with:

1. **Install** — `go build -o oracle ./cmd/oracle`, put binary on PATH
2. **Initialize** — `cd my-project && oracle init`, edit `oracle.domain.yaml`
3. **Configure Claude Code** — MCP settings JSON snippet
4. **First scan** — what to say to Claude Code ("scan this project", "analyze the orders-api repo")
5. **Querying** — example questions ("what depends on PaymentsService?", "show me the path from OrdersController to payments-api", "what would be impacted if I change OrdersService?")

## Files

```
internal/mcp/guide.go            # Extraction guide content
internal/mcp/server.go           # Modified — add new tools, enhance descriptions
internal/store/revisions.go      # Modified — add GetLatestRevision
internal/store/snapshots.go      # Modified — add GetLatestSnapshot
internal/mcp/guide_test.go       # Test guide returns valid JSON
docs/setup.md                    # Setup documentation
```

## Success Criteria

1. `oracle_extraction_guide` returns comprehensive, valid JSON extraction methodology
2. `oracle_scan_status` returns correct graph state
3. All existing MCP tools have enhanced descriptions
4. `docs/setup.md` has complete setup instructions
5. Full loop works: Claude Code connects → calls guide → scans fixture project → imports graph → queries return correct results
6. All existing tests continue to pass

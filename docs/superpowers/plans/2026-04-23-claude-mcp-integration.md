# Self-Describing Oracle + MCP Integration Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Oracle MCP server self-describing so Claude Code can analyze any project without per-project configuration — add extraction guide tool, scan status tool, enhanced tool descriptions, and setup docs.

**Architecture:** Add `internal/mcp/guide.go` with embedded extraction methodology returned as JSON. Add store queries for latest revision/snapshot. Enhance all existing MCP tool descriptions. Create `docs/setup.md` for project setup instructions.

**Tech Stack:** Go 1.22+, existing packages (store, graph, registry, mcp)

---

### Task 1: Store — GetLatestRevision + GetLatestSnapshot

**Files:**
- Modify: `internal/store/revisions.go`
- Modify: `internal/store/snapshots.go`
- Modify: `internal/store/revisions_test.go`
- Modify: `internal/store/snapshots_test.go`

- [ ] **Step 1: Add GetLatestRevision test**

Add to `internal/store/revisions_test.go`:

```go
func TestGetLatestRevision(t *testing.T) {
	s := openTestStore(t)

	// No revisions yet
	_, err := s.GetLatestRevision("orders")
	if err == nil {
		t.Fatal("expected error for no revisions")
	}

	s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	s.CreateRevision("orders", "sha1", "sha2", "manual", "incremental", "{}")
	s.CreateRevision("other", "", "sha3", "manual", "full", "{}")

	rev, err := s.GetLatestRevision("orders")
	if err != nil {
		t.Fatalf("GetLatestRevision: %v", err)
	}
	if rev.GitAfterSHA != "sha2" {
		t.Errorf("after_sha = %q, want sha2", rev.GitAfterSHA)
	}
	if rev.Mode != "incremental" {
		t.Errorf("mode = %q, want incremental", rev.Mode)
	}
}
```

- [ ] **Step 2: Implement GetLatestRevision**

Add to `internal/store/revisions.go`:

```go
// GetLatestRevision returns the most recent revision for a domain.
func (s *Store) GetLatestRevision(domainKey string) (*Revision, error) {
	const q = `
		SELECT revision_id, domain_key, COALESCE(git_before_sha,''), git_after_sha,
		       trigger_kind, mode, created_at, metadata
		FROM graph_revisions
		WHERE domain_key = ?
		ORDER BY revision_id DESC
		LIMIT 1
	`
	r := &Revision{}
	err := s.db.QueryRow(q, domainKey).Scan(
		&r.RevisionID, &r.DomainKey, &r.GitBeforeSHA, &r.GitAfterSHA,
		&r.TriggerKind, &r.Mode, &r.CreatedAt, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetLatestRevision %q: %w", domainKey, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetLatestRevision %q: %w", domainKey, err)
	}
	return r, nil
}
```

- [ ] **Step 3: Add GetLatestSnapshot test**

Add to `internal/store/snapshots_test.go`:

```go
func TestGetLatestSnapshot(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	// No snapshots yet
	_, err := s.GetLatestSnapshot("orders")
	if err == nil {
		t.Fatal("expected error for no snapshots")
	}

	s.CreateSnapshot(SnapshotRow{RevisionID: revID, DomainKey: "orders", Kind: "full", NodeCount: 10, EdgeCount: 20, Summary: "{}"})

	revID2, _ := s.CreateRevision("orders", "sha1", "sha2", "manual", "incremental", "{}")
	s.CreateSnapshot(SnapshotRow{RevisionID: revID2, DomainKey: "orders", Kind: "incremental", NodeCount: 12, EdgeCount: 22, Summary: "{}"})

	snap, err := s.GetLatestSnapshot("orders")
	if err != nil {
		t.Fatalf("GetLatestSnapshot: %v", err)
	}
	if snap.NodeCount != 12 {
		t.Errorf("node_count = %d, want 12", snap.NodeCount)
	}
	if snap.Kind != "incremental" {
		t.Errorf("kind = %q, want incremental", snap.Kind)
	}
}
```

- [ ] **Step 4: Implement GetLatestSnapshot**

Add to `internal/store/snapshots.go`:

```go
// GetLatestSnapshot returns the most recent snapshot for a domain.
func (s *Store) GetLatestSnapshot(domainKey string) (*SnapshotRow, error) {
	const q = `
		SELECT snapshot_id, revision_id, domain_key, snapshot_kind, created_at,
		       node_count, edge_count,
		       changed_file_count, changed_node_count, changed_edge_count, impacted_node_count,
		       summary
		FROM graph_snapshots
		WHERE domain_key = ?
		ORDER BY created_at DESC
		LIMIT 1
	`
	r := &SnapshotRow{}
	err := s.db.QueryRow(q, domainKey).Scan(
		&r.SnapshotID, &r.RevisionID, &r.DomainKey, &r.Kind, &r.CreatedAt,
		&r.NodeCount, &r.EdgeCount,
		&r.ChangedFileCount, &r.ChangedNodeCount, &r.ChangedEdgeCount, &r.ImpactedNodeCount,
		&r.Summary,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetLatestSnapshot %q: %w", domainKey, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetLatestSnapshot %q: %w", domainKey, err)
	}
	return r, nil
}
```

- [ ] **Step 5: Run tests and commit**

```bash
go test ./internal/store/ -v -run "TestGetLatest"
go test ./... -count=1
git add internal/store/ && git commit -m "feat: add GetLatestRevision and GetLatestSnapshot queries"
```

---

### Task 2: Extraction Guide

**Files:**
- Create: `internal/mcp/guide.go`
- Create: `internal/mcp/guide_test.go`

- [ ] **Step 1: Create guide_test.go**

```go
package mcp

import (
	"encoding/json"
	"testing"
)

func TestExtractionGuideReturnsValidJSON(t *testing.T) {
	guide := ExtractionGuide("")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(guide), &parsed); err != nil {
		t.Fatalf("guide is not valid JSON: %v", err)
	}

	// Check top-level sections exist
	for _, key := range []string{"workflow", "node_extraction", "edge_extraction", "evidence_rules", "confidence_rules", "import_payload_format"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing top-level key: %s", key)
		}
	}
}

func TestExtractionGuideFilterByTechnology(t *testing.T) {
	guide := ExtractionGuide("nestjs")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(guide), &parsed); err != nil {
		t.Fatalf("guide is not valid JSON: %v", err)
	}

	nodeExtraction, ok := parsed["node_extraction"].(map[string]any)
	if !ok {
		t.Fatal("node_extraction is not a map")
	}
	if _, ok := nodeExtraction["typescript_nestjs"]; !ok {
		t.Error("nestjs filter should include typescript_nestjs section")
	}
}

func TestExtractionGuideFullHasAllSections(t *testing.T) {
	guide := ExtractionGuide("")
	var parsed map[string]any
	json.Unmarshal([]byte(guide), &parsed)

	nodeExtraction, _ := parsed["node_extraction"].(map[string]any)
	if _, ok := nodeExtraction["typescript_nestjs"]; !ok {
		t.Error("missing typescript_nestjs in node_extraction")
	}
	if _, ok := nodeExtraction["openapi"]; !ok {
		t.Error("missing openapi in node_extraction")
	}
	if _, ok := nodeExtraction["service_nodes"]; !ok {
		t.Error("missing service_nodes in node_extraction")
	}
}
```

- [ ] **Step 2: Create guide.go**

Create `internal/mcp/guide.go` with the `ExtractionGuide(technology string) string` function. This function returns a JSON string with the full extraction methodology. The content matches the spec's `oracle_extraction_guide` response structure.

The function builds a Go map, marshals to JSON. If `technology` is non-empty, it filters `node_extraction` to only include matching sections (e.g., "nestjs" includes "typescript_nestjs", "openapi" includes "openapi").

```go
package mcp

import "encoding/json"

// ExtractionGuide returns the extraction methodology as a JSON string.
// If technology is non-empty, filters node_extraction to matching sections.
func ExtractionGuide(technology string) string {
	guide := map[string]any{
		"workflow": map[string]any{
			"steps": []string{
				"1. Call oracle_scan_status to check current graph state for the domain",
				"2. Call oracle_revision_create to start a new scan (domain from oracle.domain.yaml, after_sha from current git HEAD)",
				"3. Read oracle.domain.yaml to know which repos to scan",
				"4. For each repo: read source files using Read/Glob/Grep tools, identify entities and relationships",
				"5. Build an import payload JSON with nodes, edges, and evidence arrays",
				"6. Call oracle_import_all with the payload and revision_id",
				"7. Call oracle_snapshot_create to record the scan result",
				"8. Call oracle_stale_mark to flag entities not seen in this revision",
			},
			"key_format":  "layer:type:domain:qualified_name (all lowercase, no spaces, colons as separators)",
			"domain_from": "Read 'domain' field from oracle.domain.yaml in the project root",
		},
		"node_extraction": buildNodeExtraction(technology),
		"edge_extraction": map[string]any{
			"CONTAINS": map[string]any{
				"meaning":     "Structural containment — module contains controllers/providers, repo contains modules",
				"derivation":  "hard",
				"from_to":     "code:repository → code:module, code:module → code:controller/provider",
				"identify_by": "@Module({ controllers: [...], providers: [...] }) decorator arrays",
				"structural":  true,
				"note":        "Excluded from dependency path and impact analysis by default",
			},
			"INJECTS": map[string]any{
				"meaning":     "Dependency injection — one class injects another via constructor",
				"derivation":  "hard",
				"from_to":     "code:controller/provider → code:provider",
				"identify_by": "Constructor parameter with type matching an @Injectable class",
			},
			"EXPOSES_ENDPOINT": map[string]any{
				"meaning":     "Controller exposes an HTTP endpoint via route decorator",
				"derivation":  "hard",
				"from_to":     "code:controller → contract:endpoint",
				"identify_by": "@Get/@Post/@Put/@Delete/@Patch decorator on controller method, path = controller prefix + method path",
				"note":        "No reverse impact — endpoint change does not impact the controller",
			},
			"CALLS_ENDPOINT": map[string]any{
				"meaning":     "Code calls a specific HTTP endpoint on another service",
				"derivation":  "linked",
				"from_to":     "code:provider → contract:endpoint",
				"identify_by": "HTTP fetch/axios call with URL path matching a known endpoint",
			},
			"CALLS_SERVICE": map[string]any{
				"meaning":     "Code depends on an external service (coarse-grained)",
				"derivation":  "linked",
				"from_to":     "code:provider → service:service",
				"identify_by": "HTTP client configured with service base URL, env var like SERVICE_NAME_URL",
			},
			"PUBLISHES_TOPIC": map[string]any{
				"meaning":     "Producer publishes messages to a Kafka/message topic",
				"derivation":  "hard",
				"from_to":     "code:provider → contract:topic",
				"identify_by": "Kafka producer.send() or equivalent with topic name",
				"note":        "No reverse impact — topic schema change does not impact the producer",
			},
			"CONSUMES_TOPIC": map[string]any{
				"meaning":     "Consumer subscribes to a Kafka/message topic",
				"derivation":  "hard",
				"from_to":     "code:provider → contract:topic",
				"identify_by": "Kafka consumer.subscribe() or @MessagePattern decorator with topic name",
			},
		},
		"evidence_rules": map[string]any{
			"extractor_id":      "claude-code",
			"extractor_version": "1.0",
			"target_kind":       "node or edge",
			"required_fields":   []string{"source_kind", "extractor_id", "extractor_version"},
			"source_kinds": map[string]string{
				"file":    "TypeScript/JavaScript/Go/Python source files",
				"openapi": "OpenAPI specification files (yaml/json)",
			},
			"best_practice": "Always include file_path and line_start at minimum. The evidence should point to the exact code that proves the entity or relationship exists.",
		},
		"confidence_rules": map[string]any{
			"hard": map[string]any{
				"range": "0.90-1.00",
				"when":  "Directly visible in code: decorators, imports, type annotations, explicit config, spec files",
			},
			"linked": map[string]any{
				"range": "0.70-0.89",
				"when":  "Convention-based: service URL in env var, topic name matching, naming patterns, config references",
			},
			"inferred": map[string]any{
				"range": "0.40-0.69",
				"when":  "Guessed from context: co-located files, naming similarity, comments suggesting relationship",
			},
			"unknown": map[string]any{
				"range": "0.00-0.39",
				"when":  "Cannot reliably determine — flag as unknown for human review",
			},
		},
		"import_payload_format": map[string]any{
			"description": "Pass as JSON string to oracle_import_all's 'payload' parameter",
			"nodes": map[string]any{
				"required": []string{"node_key", "layer", "node_type", "domain_key", "name"},
				"optional": []string{"repo_name", "file_path", "confidence", "metadata"},
				"example": map[string]string{
					"node_key":  "code:controller:orders:orderscontroller",
					"layer":     "code",
					"node_type": "controller",
					"domain_key": "orders",
					"name":      "OrdersController",
					"repo_name": "orders-api",
					"file_path": "src/orders/orders.controller.ts",
				},
			},
			"edges": map[string]any{
				"required": []string{"from_node_key", "to_node_key", "edge_type", "derivation_kind", "from_layer", "to_layer"},
				"optional": []string{"edge_key", "confidence", "metadata"},
				"note":     "from_layer/to_layer must match the layers of the referenced nodes — needed for type registry validation",
			},
			"evidence": map[string]any{
				"required": []string{"target_kind", "source_kind", "extractor_id", "extractor_version"},
				"for_node": []string{"node_key"},
				"for_edge": []string{"edge_key (auto-generated from from->to:type if omitted)"},
				"optional": []string{"repo_name", "file_path", "line_start", "line_end", "confidence"},
			},
		},
	}

	data, _ := json.MarshalIndent(guide, "", "  ")
	return string(data)
}

func buildNodeExtraction(technology string) map[string]any {
	all := map[string]any{
		"typescript_nestjs": map[string]any{
			"module": map[string]any{
				"layer": "code", "node_type": "module",
				"identify_by": "@Module decorator on a class",
				"key_example": "code:module:orders:ordersmodule",
				"evidence":    "file path + line range of the @Module decorator and class",
			},
			"controller": map[string]any{
				"layer": "code", "node_type": "controller",
				"identify_by": "@Controller decorator on a class. The decorator argument is the route prefix.",
				"key_example": "code:controller:orders:orderscontroller",
				"evidence":    "file path + line range of the class declaration",
			},
			"provider": map[string]any{
				"layer": "code", "node_type": "provider",
				"identify_by": "@Injectable decorator on a class. Includes services, repositories, guards, pipes.",
				"key_example": "code:provider:orders:ordersservice",
				"evidence":    "file path + line range of the class declaration",
			},
			"endpoint": map[string]any{
				"layer": "contract", "node_type": "endpoint",
				"identify_by": "@Get/@Post/@Put/@Delete/@Patch on a controller method. Key = method:controller_prefix/method_path",
				"key_example": "contract:endpoint:orders:post:/orders/:id/capture",
				"evidence":    "file path + line of the route decorator",
			},
			"topic": map[string]any{
				"layer": "contract", "node_type": "topic",
				"identify_by": "Kafka topic constants, producer.send() topic, @MessagePattern topic",
				"key_example": "contract:topic:orders:order-created",
				"evidence":    "file path + line of topic constant or producer/consumer config",
			},
		},
		"openapi": map[string]any{
			"endpoint": map[string]any{
				"layer": "contract", "node_type": "endpoint",
				"identify_by": "Each method+path combination under 'paths' in openapi.yaml/json",
				"key_example": "contract:endpoint:orders:get:/orders/:id",
				"evidence":    "openapi file path + path key location",
			},
			"http_api": map[string]any{
				"layer": "contract", "node_type": "http_api",
				"identify_by": "Top-level 'info' section in an OpenAPI spec",
				"key_example": "contract:http_api:orders:orders-api",
				"evidence":    "openapi file path",
			},
		},
		"service_nodes": map[string]any{
			"repository": map[string]any{
				"layer": "code", "node_type": "repository",
				"identify_by": "Each entry in oracle.domain.yaml repositories list",
				"key_example": "code:repository:orders:orders-api",
			},
			"service": map[string]any{
				"layer": "service", "node_type": "service",
				"identify_by": "Each deployable service in the domain — typically 1:1 with repos, or identified by Dockerfile/deployment config",
				"key_example": "service:service:orders:orders-api",
			},
		},
	}

	if technology == "" {
		return all
	}

	filtered := map[string]any{
		"service_nodes": all["service_nodes"],
	}
	switch technology {
	case "nestjs", "typescript", "ts":
		filtered["typescript_nestjs"] = all["typescript_nestjs"]
	case "openapi":
		filtered["openapi"] = all["openapi"]
	default:
		return all // unknown filter = return everything
	}
	return filtered
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/mcp/ -v
go test ./... -count=1
```

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/guide.go internal/mcp/guide_test.go
git commit -m "feat: add extraction guide with embedded methodology"
```

---

### Task 3: Scan Status + Guide MCP Tools

**Files:**
- Modify: `internal/mcp/server.go`

- [ ] **Step 1: Add extraction_guide and scan_status tools to NewServer**

In `NewServer()`, add:

```go
s.AddTool(extractionGuideTool(), extractionGuideHandler())
s.AddTool(scanStatusTool(), scanStatusHandler(g))
```

- [ ] **Step 2: Implement extraction guide tool**

```go
func extractionGuideTool() mcp.Tool {
	return mcp.NewTool("oracle_extraction_guide",
		mcp.WithDescription("Get the extraction methodology guide. Call this before scanning a codebase to understand what entities and relationships to extract, how to structure the import payload, and the recommended workflow. Returns comprehensive JSON instructions for analyzing TypeScript/NestJS, OpenAPI, and other codebases."),
		mcp.WithString("technology", mcp.Description("Filter guide to a specific technology: nestjs, openapi, or omit for full guide")),
	)
}

func extractionGuideHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tech := strParam(req.GetArguments(), "technology")
		guide := ExtractionGuide(tech)
		return mcp.NewToolResultText(guide), nil
	}
}
```

- [ ] **Step 3: Implement scan status tool**

```go
func scanStatusTool() mcp.Tool {
	return mcp.NewTool("oracle_scan_status",
		mcp.WithDescription("Get the current graph state for a domain. Returns last revision, graph statistics (node/edge counts by layer and type), and last snapshot. Use this before scanning to decide whether to do a full or incremental scan."),
		mcp.WithString("domain", mcp.Description("Domain key (from oracle.domain.yaml). If omitted, returns status message to check the manifest.")),
	)
}

func scanStatusHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		domain := strParam(req.GetArguments(), "domain")
		if domain == "" {
			return mcp.NewToolResultText(`{"message": "Provide a domain key. Read oracle.domain.yaml to find the domain."}`), nil
		}

		result := map[string]any{"domain": domain}

		// Latest revision
		rev, err := g.Store().GetLatestRevision(domain)
		if err != nil {
			result["last_revision"] = nil
			result["message"] = "No revisions found. This domain has never been scanned."
		} else {
			result["last_revision"] = rev
		}

		// Graph stats
		stats, err := g.QueryStats(domain)
		if err != nil {
			result["graph_stats"] = nil
		} else {
			result["graph_stats"] = stats
		}

		// Latest snapshot
		snap, err := g.Store().GetLatestSnapshot(domain)
		if err != nil {
			result["last_snapshot"] = nil
		} else {
			result["last_snapshot"] = snap
		}

		return jsonResult(result), nil
	}
}
```

- [ ] **Step 4: Build and verify**

```bash
go build -o oracle ./cmd/oracle
```

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/server.go
git commit -m "feat: add extraction_guide and scan_status MCP tools"
```

---

### Task 4: Enhanced Tool Descriptions

**Files:**
- Modify: `internal/mcp/server.go`

Update the descriptions of all existing MCP tools to be richer and more self-explanatory. This is a text-only change — no logic changes.

- [ ] **Step 1: Update all tool descriptions**

Replace each tool's `mcp.WithDescription(...)` with more detailed text. Here are the updated descriptions for each tool:

**revisionCreateTool:**
```
"Create a new graph revision to track a scan pass. Call this at the start of every scan. Use trigger='manual' for human-initiated scans, 'full_scan' for automated. Mode is 'full' for complete rescan or 'incremental' for partial. Returns revision_id to use in subsequent upsert/import calls."
```

**nodeUpsertTool:**
```
"Create or update a graph node. Upsert by node_key — if the key exists, mutable fields (name, file_path, confidence, metadata) are updated. Immutable fields (layer, node_type, domain) must match or the upsert is rejected. Key format: layer:type:domain:qualified_name (all lowercase)."
```

**nodeListTool:**
```
"List graph nodes with optional filters. Use to browse the current graph state. Filter by layer (code/service/contract/etc), node_type, domain, or status (active/stale/deleted)."
```

**nodeGetTool:**
```
"Get a single node by key with all its evidence entries. Use to inspect a specific entity and see what evidence supports its existence in the graph."
```

**edgeUpsertTool:**
```
"Create or update a graph edge. Upsert by edge_key (auto-generated as from->to:type if not provided). The from/to nodes must already exist. Edge type and from/to layers are validated against the type registry. Derivation kind indicates confidence: hard (AST-level), linked (convention-based), inferred (guessed), unknown."
```

**edgeListTool:**
```
"List graph edges with optional filters. Filter by source node (from_node_key), target node (to_node_key), or edge type. Returns all matching edges with their derivation kind and confidence."
```

**evidenceAddTool:**
```
"Add provenance evidence for a node or edge. Evidence records where in the source code a relationship was found. Include file_path and line_start at minimum. The extractor_id should be 'claude-code' and extractor_version '1.0'. Evidence is deduplicated — same location + extractor = update, different location = new entry."
```

**importAllTool:**
```
"Bulk import nodes, edges, and evidence in a single atomic transaction. The payload is a JSON string with 'nodes', 'edges', and 'evidence' arrays. All entries are validated against the type registry. If ANY entry fails validation, the entire import is rolled back — no partial writes. Call oracle_extraction_guide to understand the payload format. This is the primary tool for loading scan results."
```

**queryDepsTool:**
```
"Query forward dependencies of a node — what does this node depend on? Traverses outgoing edges via BFS up to the specified depth. Structural edges (CONTAINS, OWNED_BY) are included. Use --derivation to filter by confidence level (hard, linked, inferred)."
```

**queryReverseDepsTool:**
```
"Query reverse dependencies — who depends on this node? Traverses incoming edges via BFS. Use to find all consumers of a service, endpoint, or topic. Filter by derivation kind and max depth."
```

**queryStatsTool:**
```
"Get aggregate graph statistics for a domain: total node/edge counts, breakdown by layer, by edge type, by derivation kind, and active vs stale counts. Use to verify scan completeness."
```

**snapshotCreateTool:**
```
"Record a point-in-time snapshot after a scan completes. Captures node and edge counts at the end of a revision. Call after oracle_import_all and oracle_stale_mark to close the scan lifecycle."
```

**staleMarkTool:**
```
"Mark nodes and edges not seen in the current revision as stale. Call after import_all to flag entities that were in the previous scan but not in this one. Stale entities remain queryable but are flagged. Only applies to 'active' entities in the specified domain."
```

**queryPathTool:**
```
"Find paths between two nodes in the graph. Default mode is 'directed' — follows edges in their natural direction, producing meaningful dependency chains. Use 'connected' mode for undirected exploration. Structural edges (CONTAINS) are excluded by default. Returns top-k paths ranked by path score."
```

**impactTool:**
```
"Analyze the blast radius of a change to a node. Performs reverse dependency traversal respecting the traversal policy — structural edges (CONTAINS) are excluded, and edges like EXPOSES_ENDPOINT don't propagate reverse impact. Returns scored impact list sorted by proximity. Use to answer 'what breaks if I change X?'"
```

- [ ] **Step 2: Build to verify compilation**

```bash
go build -o oracle ./cmd/oracle
```

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/server.go
git commit -m "feat: enhance all MCP tool descriptions for self-discovery"
```

---

### Task 5: Setup Documentation

**Files:**
- Create: `docs/setup.md`

- [ ] **Step 1: Create docs/setup.md**

```markdown
# Domain Oracle — Setup Guide

## Install

Build the Oracle CLI from source:

```bash
cd /path/to/depbot
go build -o oracle ./cmd/oracle
```

Move the binary to your PATH or use the full path in the MCP config below.

## Initialize a Project

In any project directory you want to analyze:

```bash
oracle init
```

This creates:
- `oracle.domain.yaml` — domain manifest (edit this)
- `oracle.types.yaml` — type registry (defaults are fine)
- `oracle.db` — SQLite database

Edit `oracle.domain.yaml` to describe your domain:

```yaml
domain: my-domain
description: My project domain
repositories:
  - name: my-api
    path: .
    tags: [nestjs, rest]
owner: my-team
```

## Configure Claude Code

Add the Oracle MCP server to your Claude Code settings. In your project's `.claude/settings.json` or global settings:

```json
{
  "mcpServers": {
    "oracle": {
      "command": "/absolute/path/to/oracle",
      "args": ["mcp", "serve", "--db", "/absolute/path/to/oracle.db"]
    }
  }
}
```

**Important:** Use absolute paths for both the binary and the database file.

## First Scan

Open Claude Code in your project and say:

> "Scan this project and build a knowledge graph"

Claude will:
1. Call `oracle_extraction_guide` to learn the extraction methodology
2. Call `oracle_scan_status` to check if a graph already exists
3. Read your `oracle.domain.yaml` to know the domain and repos
4. Read your source files, identify entities and relationships
5. Import everything via `oracle_import_all`
6. Create a snapshot and mark stale entities

## Querying

Once scanned, ask questions like:

- "What depends on OrdersService?"
- "Show me the path from OrdersController to payments-api"
- "What would be impacted if I change PaymentsService?"
- "What are the stats for the orders domain?"
- "List all endpoints in the graph"

Claude uses the Oracle MCP tools to query the graph and provide evidence-backed answers.

## CLI Usage

You can also use the CLI directly:

```bash
# List nodes
oracle node list --layer code --domain my-domain

# Query dependencies
oracle query deps code:provider:orders:ordersservice --depth 2

# Find paths
oracle query path code:controller:orders:orderscontroller service:service:orders:payments-api

# Analyze impact
oracle impact code:provider:orders:paymentsservice --depth 3

# Check graph stats
oracle query stats --domain orders

# Validate graph integrity
oracle validate graph
```

## Re-scanning

To update the graph after code changes, just tell Claude:

> "Re-scan the orders-api repo"

The Oracle handles idempotent upserts — existing entities are updated, new ones are added, removed ones are marked stale.
```

- [ ] **Step 2: Commit**

```bash
git add docs/setup.md
git commit -m "docs: add setup guide for Oracle CLI and Claude Code integration"
```

---

### Task 6: Integration Verification

**Files:**
- No new files — verify everything works together

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -count=1 -v
```

Expected: all tests pass across all packages including e2e.

- [ ] **Step 2: Build final binary**

```bash
go build -o oracle ./cmd/oracle
./oracle version
```

Expected: `oracle v0.1.0`

- [ ] **Step 3: Test MCP server starts and responds**

```bash
echo '{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | timeout 3 ./oracle mcp serve --db /tmp/verify-test.db 2>/dev/null | head -1
```

Expected: JSON response with server info.

- [ ] **Step 4: Verify new tools are listed**

```bash
echo '{"jsonrpc":"2.0","method":"tools/list","id":2,"params":{}}' | timeout 3 ./oracle mcp serve --db /tmp/verify-test.db 2>/dev/null | python3 -c "import sys,json; tools=[t['name'] for t in json.loads(sys.stdin.readline())['result']['tools']]; print('\n'.join(sorted(tools)))"
```

Expected: should include `oracle_extraction_guide` and `oracle_scan_status` among the tools.

- [ ] **Step 5: Final commit if any fixes needed**

```bash
# Only if fixes were needed
git add -A && git commit -m "fix: integration verification fixes"
```

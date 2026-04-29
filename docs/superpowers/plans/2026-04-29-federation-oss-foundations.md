# Federation OSS Foundations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename module from `github.com/anthropics/depbot` to `github.com/alexdx2/chronicle-core`, then implement the OSS foundation pieces for multi-repo federation: external node status, node aliases table, GraphQuerier/GraphDiscoverer interfaces, and DepNode resolution fields.

**Architecture:** The module rename is a mechanical find-and-replace across all Go files and go.mod. The federation foundations add: (1) `external` node status to registry and schema, (2) `node_aliases` table with CRUD store methods, (3) `GraphQuerier` and `GraphDiscoverer` interfaces that the existing `Graph` struct implements, (4) resolution fields on `DepNode` (unused in OSS, populated by enterprise), (5) `chronicle alias` CLI command.

**Tech Stack:** Go, SQLite, Cobra CLI

---

### Task 1: Rename Go module from anthropics/depbot to alexdx2/core

**Files:**
- Modify: `go.mod:1`
- Modify: All `.go` files containing `github.com/anthropics/depbot`

- [ ] **Step 1: Update go.mod module path**

In `go.mod`, change line 1:
```
module github.com/alexdx2/chronicle-core
```

- [ ] **Step 2: Replace all import paths in Go files**

Run:
```bash
find . -name '*.go' -exec sed -i 's|github.com/anthropics/depbot|github.com/alexdx2/chronicle-core|g' {} +
```

- [ ] **Step 3: Verify build compiles**

Run: `go build ./...`
Expected: clean build, no errors

- [ ] **Step 4: Run tests**

Run: `go test ./internal/...`
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor: rename module github.com/anthropics/depbot -> github.com/alexdx2/chronicle-core"
```

---

### Task 2: Add `external` node status to registry and schema

**Files:**
- Modify: `internal/registry/defaults.yaml:226-230` — add `external` to `node_statuses`
- Modify: `internal/store/store.go:250-251` — add `external` to CHECK constraint
- Modify: `internal/store/store.go` migrate() — ALTER TABLE to add `external` to CHECK (for existing DBs)

- [ ] **Step 1: Add `external` to defaults.yaml**

In `internal/registry/defaults.yaml`, change `node_statuses` to:
```yaml
node_statuses:
  - active
  - stale
  - deleted
  - unknown
  - external
```

- [ ] **Step 2: Update schema CHECK constraint**

In `internal/store/store.go`, update the `graph_nodes` CREATE TABLE `status` CHECK:
```sql
status TEXT NOT NULL DEFAULT 'active'
  CHECK (status IN ('active','stale','deleted','unknown','contradicted','external')),
```

- [ ] **Step 3: Add migration for existing databases**

In `internal/store/store.go` `migrate()`, the CHECK constraint on SQLite columns is only enforced on INSERT/UPDATE, and the CREATE TABLE IF NOT EXISTS won't alter existing tables. For existing databases that already have the table, we need to handle this. SQLite doesn't support ALTER CHECK. Since Chronicle already uses best-effort ALTER TABLE for migrations, and the CHECK is only on new inserts, this is fine — existing DBs will accept `external` status on INSERT because SQLite CHECK constraints are per-statement.

Actually, SQLite CHECK constraints ARE enforced on the existing table definition. We need to recreate the table or just skip — since we use `CREATE TABLE IF NOT EXISTS`, existing tables keep their old CHECK. The simplest approach: the existing `migrate()` pattern already handles this via the schema reset flow. No additional migration needed — new DBs get the updated CHECK, existing DBs will need a reset if they want to use `external` status.

No code change needed beyond the schema string update.

- [ ] **Step 4: Verify build and tests**

Run: `go build ./... && go test ./internal/...`
Expected: all pass

- [ ] **Step 5: Commit**

```bash
git add internal/registry/defaults.yaml internal/store/store.go
git commit -m "feat: add external node status for federation boundary markers"
```

---

### Task 3: Add `node_aliases` table and store CRUD

**Files:**
- Modify: `internal/store/store.go` — add `node_aliases` table to schema, add to ResetDB drop list, add migration
- Create: `internal/store/aliases.go` — AliasRow type, AddAlias, ListAliasesByNode, ListAliasesByNormalized, RemoveAlias
- Create: `internal/store/aliases_test.go` — tests

- [ ] **Step 1: Write failing test for alias CRUD**

Create `internal/store/aliases_test.go`:
```go
package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAliasCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create a revision and node to attach aliases to.
	revID := createTestRevision(t, s)
	nodeID := createTestNode(t, s, revID)

	// Add alias.
	aliasID, err := s.AddAlias(AliasRow{
		NodeID:     nodeID,
		Alias:      "Orders API",
		AliasKind:  "openapi_title",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("AddAlias: %v", err)
	}
	if aliasID == 0 {
		t.Fatal("expected non-zero alias_id")
	}

	// List by node.
	aliases, err := s.ListAliasesByNode(nodeID)
	if err != nil {
		t.Fatalf("ListAliasesByNode: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}
	if aliases[0].Alias != "Orders API" {
		t.Errorf("expected alias 'Orders API', got %q", aliases[0].Alias)
	}
	if aliases[0].NormalizedAlias != "orders api" {
		t.Errorf("expected normalized 'orders api', got %q", aliases[0].NormalizedAlias)
	}

	// Lookup by normalized alias.
	found, err := s.ListAliasesByNormalized("orders api", "openapi_title")
	if err != nil {
		t.Fatalf("ListAliasesByNormalized: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 match, got %d", len(found))
	}

	// Duplicate alias (same node, same normalized, same kind) should fail.
	_, err = s.AddAlias(AliasRow{
		NodeID:     nodeID,
		Alias:      "orders api",
		AliasKind:  "openapi_title",
		Confidence: 0.8,
	})
	if err == nil {
		t.Fatal("expected duplicate alias to fail")
	}

	// Remove alias.
	err = s.RemoveAlias(aliasID)
	if err != nil {
		t.Fatalf("RemoveAlias: %v", err)
	}

	aliases, err = s.ListAliasesByNode(nodeID)
	if err != nil {
		t.Fatalf("ListAliasesByNode after remove: %v", err)
	}
	if len(aliases) != 0 {
		t.Fatalf("expected 0 aliases after remove, got %d", len(aliases))
	}
}

func createTestRevision(t *testing.T, s *Store) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO graph_revisions (domain_key, git_after_sha, trigger_kind, mode) VALUES ('test', 'abc123', 'manual', 'full')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func createTestNode(t *testing.T, s *Store, revID int64) int64 {
	t.Helper()
	id, err := s.UpsertNode(NodeRow{
		NodeKey:             "service:service:orders:orders-service",
		Layer:               "service",
		NodeType:            "service",
		DomainKey:           "orders",
		Name:                "orders-service",
		Status:              "active",
		FirstSeenRevisionID: revID,
		LastSeenRevisionID:  revID,
		Confidence:          1.0,
		Metadata:            "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestAliasCRUD -v`
Expected: FAIL — `AddAlias` not defined

- [ ] **Step 3: Add node_aliases table to schema**

In `internal/store/store.go`, add to the `schema` const (before the closing backtick):
```sql
CREATE TABLE IF NOT EXISTS node_aliases (
    alias_id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id          INTEGER NOT NULL REFERENCES graph_nodes(node_id),
    alias            TEXT    NOT NULL,
    normalized_alias TEXT    NOT NULL,
    alias_kind       TEXT    NOT NULL,
    confidence       REAL    NOT NULL DEFAULT 0.8,
    UNIQUE(node_id, normalized_alias, alias_kind)
);

CREATE INDEX IF NOT EXISTS idx_node_aliases_normalized ON node_aliases(normalized_alias, alias_kind);
CREATE INDEX IF NOT EXISTS idx_node_aliases_node_id ON node_aliases(node_id);
```

Add `"node_aliases"` to the `ResetDB()` drop list (before `"graph_evidence"`).

- [ ] **Step 4: Implement alias store methods**

Create `internal/store/aliases.go`:
```go
package store

import (
	"fmt"
	"strings"
)

// AliasRow represents a row in node_aliases.
type AliasRow struct {
	AliasID         int64   `json:"alias_id"`
	NodeID          int64   `json:"node_id"`
	Alias           string  `json:"alias"`
	NormalizedAlias string  `json:"normalized_alias"`
	AliasKind       string  `json:"alias_kind"`
	Confidence      float64 `json:"confidence"`
}

// AddAlias inserts a new alias for a node. The normalized_alias is computed automatically.
func (s *Store) AddAlias(a AliasRow) (int64, error) {
	normalized := strings.ToLower(strings.TrimSpace(a.Alias))
	if normalized == "" {
		return 0, fmt.Errorf("AddAlias: alias is empty")
	}
	if a.AliasKind == "" {
		return 0, fmt.Errorf("AddAlias: alias_kind is empty")
	}
	confidence := a.Confidence
	if confidence <= 0 {
		confidence = 0.8
	}

	res, err := s.db.Exec(`
		INSERT INTO node_aliases (node_id, alias, normalized_alias, alias_kind, confidence)
		VALUES (?, ?, ?, ?, ?)
	`, a.NodeID, a.Alias, normalized, a.AliasKind, confidence)
	if err != nil {
		return 0, fmt.Errorf("AddAlias: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// ListAliasesByNode returns all aliases for a given node.
func (s *Store) ListAliasesByNode(nodeID int64) ([]AliasRow, error) {
	rows, err := s.db.Query(`
		SELECT alias_id, node_id, alias, normalized_alias, alias_kind, confidence
		FROM node_aliases WHERE node_id = ? ORDER BY alias_kind, normalized_alias
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("ListAliasesByNode: %w", err)
	}
	defer rows.Close()
	return scanAliases(rows)
}

// ListAliasesByNormalized returns aliases matching a normalized value and kind.
func (s *Store) ListAliasesByNormalized(normalized, kind string) ([]AliasRow, error) {
	rows, err := s.db.Query(`
		SELECT alias_id, node_id, alias, normalized_alias, alias_kind, confidence
		FROM node_aliases WHERE normalized_alias = ? AND alias_kind = ?
	`, normalized, kind)
	if err != nil {
		return nil, fmt.Errorf("ListAliasesByNormalized: %w", err)
	}
	defer rows.Close()
	return scanAliases(rows)
}

// RemoveAlias deletes an alias by ID.
func (s *Store) RemoveAlias(aliasID int64) error {
	res, err := s.db.Exec(`DELETE FROM node_aliases WHERE alias_id = ?`, aliasID)
	if err != nil {
		return fmt.Errorf("RemoveAlias: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("RemoveAlias %d: %w", aliasID, ErrNotFound)
	}
	return nil
}

func scanAliases(rows interface{ Next() bool; Scan(...any) error }) ([]AliasRow, error) {
	var out []AliasRow
	for rows.Next() {
		var r AliasRow
		if err := rows.Scan(&r.AliasID, &r.NodeID, &r.Alias, &r.NormalizedAlias, &r.AliasKind, &r.Confidence); err != nil {
			return nil, fmt.Errorf("scanAliases: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/store/ -run TestAliasCRUD -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/aliases.go internal/store/aliases_test.go internal/store/store.go
git commit -m "feat: add node_aliases table and store CRUD"
```

---

### Task 4: Add GraphQuerier and GraphDiscoverer interfaces

**Files:**
- Create: `internal/graph/interfaces.go` — GraphQuerier, GraphDiscoverer interfaces, GraphTarget type
- Modify: `internal/graph/query.go` — add resolution fields to DepNode, update QueryStats signature

- [ ] **Step 1: Create interfaces file**

Create `internal/graph/interfaces.go`:
```go
package graph

// GraphQuerier is the primary query interface. OSS provides a single-repo
// implementation (Graph). Enterprise provides a federated implementation.
type GraphQuerier interface {
	QueryDeps(nodeKey string, maxDepth int, filters []string) ([]DepNode, error)
	QueryReverseDeps(nodeKey string, maxDepth int, filters []string) ([]DepNode, error)
	QueryPath(fromKey, toKey string, opts PathOptions) (*PathResult, error)
	QueryImpact(nodeKey string, opts ImpactOptions) (*ImpactResult, error)
	QueryStats(domainKey string) (*Stats, error)
}

// GraphDiscoverer finds .depbot/ directories and returns openable graph targets.
// OSS provides a single-directory implementation. Enterprise scans children.
type GraphDiscoverer interface {
	Discover(rootDir string) ([]GraphTarget, error)
}

// GraphTarget represents a discovered repo with a .depbot/ directory.
type GraphTarget struct {
	RepoName string `json:"repo_name"`
	Path     string `json:"path"`
	Domain   string `json:"domain,omitempty"`
}

// AmbiguousRef identifies a candidate node in conflict resolution (enterprise).
type AmbiguousRef struct {
	RepoName   string  `json:"repo_name"`
	NodeKey    string  `json:"node_key"`
	TrustScore float64 `json:"trust_score"`
	Status     string  `json:"status"`
}
```

- [ ] **Step 2: Add resolution fields to DepNode**

In `internal/graph/query.go`, update `DepNode`:
```go
type DepNode struct {
	NodeKey             string         `json:"node_key"`
	Name                string         `json:"name"`
	Layer               string         `json:"layer"`
	NodeType            string         `json:"node_type"`
	Depth               int            `json:"depth"`
	TrustScore          float64        `json:"trust_score"`
	Freshness           float64        `json:"freshness"`
	Status              string         `json:"status"`
	SourceRepo          string         `json:"source_repo,omitempty"`
	ResolvedRepo        string         `json:"resolved_repo,omitempty"`
	ResolutionStatus    string         `json:"resolution_status,omitempty"`
	ResolutionMethod    string         `json:"resolution_method,omitempty"`
	ResolutionAliasKind string         `json:"resolution_alias_kind,omitempty"`
	ResolutionAlias     string         `json:"resolution_alias,omitempty"`
	AmbiguousCandidates []AmbiguousRef `json:"ambiguous_candidates,omitempty"`
}
```

- [ ] **Step 3: Verify Graph implements GraphQuerier**

Add a compile-time check at the bottom of `internal/graph/interfaces.go`:
```go
// Compile-time check: Graph implements GraphQuerier.
var _ GraphQuerier = (*Graph)(nil)
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./internal/...`
Expected: all pass (the Graph struct already has all required methods with matching signatures)

- [ ] **Step 5: Commit**

```bash
git add internal/graph/interfaces.go internal/graph/query.go
git commit -m "feat: add GraphQuerier/GraphDiscoverer interfaces and DepNode resolution fields"
```

---

### Task 5: Add `chronicle alias` CLI command

**Files:**
- Create: `internal/cli/alias.go` — add, list, remove subcommands
- Modify: `internal/cli/root.go` — register alias command

- [ ] **Step 1: Create alias CLI command**

Create `internal/cli/alias.go`:
```go
package cli

import (
	"fmt"
	"strconv"

	"github.com/alexdx2/chronicle-core/internal/store"
	"github.com/spf13/cobra"
)

func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage node aliases",
	}
	cmd.AddCommand(
		newAliasAddCmd(),
		newAliasListCmd(),
		newAliasRemoveCmd(),
	)
	return cmd
}

func newAliasAddCmd() *cobra.Command {
	var (
		kind       string
		confidence float64
	)

	cmd := &cobra.Command{
		Use:   "add <node_key> <alias>",
		Short: "Add an alias to a node",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			nodeKey := args[0]
			alias := args[1]

			node, err := g.Store().GetNodeByKey(nodeKey)
			if err != nil {
				outputError(err)
				return
			}

			id, err := g.Store().AddAlias(store.AliasRow{
				NodeID:     node.NodeID,
				Alias:      alias,
				AliasKind:  kind,
				Confidence: confidence,
			})
			if err != nil {
				outputError(err)
				return
			}

			outputJSON(map[string]any{
				"alias_id": id,
				"node_key": nodeKey,
				"alias":    alias,
				"kind":     kind,
			})
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "manual", "Alias kind: dns, package, http_base_url, kafka_topic, openapi_title, manual")
	cmd.Flags().Float64Var(&confidence, "confidence", 0.9, "Confidence score (0-1)")

	return cmd
}

func newAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <node_key>",
		Short: "List aliases for a node",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			node, err := g.Store().GetNodeByKey(args[0])
			if err != nil {
				outputError(err)
				return
			}

			aliases, err := g.Store().ListAliasesByNode(node.NodeID)
			if err != nil {
				outputError(err)
				return
			}

			outputJSON(aliases)
		},
	}
}

func newAliasRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <alias_id>",
		Short: "Remove an alias by ID",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				outputError(fmt.Errorf("invalid alias_id: %w", err))
				return
			}

			if err := g.Store().RemoveAlias(id); err != nil {
				outputError(err)
				return
			}

			outputJSON(map[string]string{"status": "removed"})
		},
	}
}
```

- [ ] **Step 2: Register alias command in root**

In `internal/cli/root.go`, add `newAliasCmd()` to the `root.AddCommand(...)` call.

- [ ] **Step 3: Build and verify**

Run: `go build ./...`
Expected: clean build

- [ ] **Step 4: Commit**

```bash
git add internal/cli/alias.go internal/cli/root.go
git commit -m "feat: add chronicle alias CLI command (add/list/remove)"
```

---

### Task 6: Add SingleRepoDiscoverer (OSS default)

**Files:**
- Create: `internal/graph/discoverer.go` — SingleRepoDiscoverer implementing GraphDiscoverer

- [ ] **Step 1: Implement SingleRepoDiscoverer**

Create `internal/graph/discoverer.go`:
```go
package graph

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SingleRepoDiscoverer is the OSS implementation of GraphDiscoverer.
// It checks the given directory for a .depbot/ subdirectory.
type SingleRepoDiscoverer struct{}

func (d *SingleRepoDiscoverer) Discover(rootDir string) ([]GraphTarget, error) {
	depbotDir := filepath.Join(rootDir, ".depbot")
	dbPath := filepath.Join(depbotDir, "chronicle.db")

	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("no .depbot/chronicle.db found in %s", rootDir)
	}

	target := GraphTarget{
		RepoName: filepath.Base(rootDir),
		Path:     depbotDir,
	}

	// Try to read domain from manifest.
	manifestPath := filepath.Join(depbotDir, "chronicle.domain.yaml")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m struct {
			Domain string `yaml:"domain"`
		}
		if yaml.Unmarshal(data, &m) == nil && m.Domain != "" {
			target.Domain = m.Domain
		}
	}

	return []GraphTarget{target}, nil
}
```

- [ ] **Step 2: Build and test**

Run: `go build ./... && go test ./internal/...`
Expected: all pass

- [ ] **Step 3: Commit**

```bash
git add internal/graph/discoverer.go
git commit -m "feat: add SingleRepoDiscoverer (OSS GraphDiscoverer implementation)"
```

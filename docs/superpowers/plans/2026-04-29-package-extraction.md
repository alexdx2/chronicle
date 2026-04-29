# Package Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move core engine packages from `internal/` to `pkg/` and rename the Go module from `github.com/anthropics/depbot` to `github.com/alexdx2/depbot`, so that the enterprise repo (`github.com/alexdx2/chronicle-pro`) can import them as a normal Go module dependency.

**Architecture:** Five packages move to `pkg/`: store, registry, manifest, validate, graph (in dependency order). Three packages stay internal: cli, mcp, admin. Extension interfaces (`GraphQuerier`, `GraphDiscoverer`) are added to `pkg/graph/`. The module path changes globally.

**Tech Stack:** Go 1.25.5, SQLite, Cobra CLI

---

### Task 1: Rename Go module path

**Files:**
- Modify: `go.mod:1`
- Modify: every `.go` file that imports `github.com/anthropics/depbot` (42 files)

- [ ] **Step 1: Update go.mod module declaration**

In `go.mod`, change line 1:
```
module github.com/alexdx2/depbot
```

- [ ] **Step 2: Replace all import paths across the codebase**

Run:
```bash
find /home/alex/personal/depbot -name "*.go" -exec sed -i 's|github.com/anthropics/depbot|github.com/alexdx2/depbot|g' {} +
```

- [ ] **Step 3: Verify no old import paths remain**

Run:
```bash
grep -r "github.com/anthropics/depbot" --include="*.go" .
```
Expected: no output

- [ ] **Step 4: Run tests to verify everything still compiles**

Run:
```bash
go build ./... && go test ./...
```
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rename module from github.com/anthropics/depbot to github.com/alexdx2/depbot"
```

---

### Task 2: Move store package to pkg/

**Files:**
- Move: `internal/store/*.go` → `pkg/store/*.go`
- Modify: all files that import `github.com/alexdx2/depbot/internal/store` (17 files)

`store` has no internal dependencies — it only imports stdlib and `modernc.org/sqlite`. This is the foundation layer, so it moves first.

- [ ] **Step 1: Create pkg directory and move files**

```bash
mkdir -p pkg
mv internal/store pkg/store
```

- [ ] **Step 2: Update all import paths**

```bash
find /home/alex/personal/depbot -name "*.go" -exec sed -i 's|github.com/alexdx2/depbot/internal/store|github.com/alexdx2/depbot/pkg/store|g' {} +
```

- [ ] **Step 3: Verify no old import paths remain**

```bash
grep -r "depbot/internal/store" --include="*.go" .
```
Expected: no output

- [ ] **Step 4: Run tests**

```bash
go build ./... && go test ./...
```
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move store package to pkg/store for external importability"
```

---

### Task 3: Move registry package to pkg/

**Files:**
- Move: `internal/registry/*.go` → `pkg/registry/*.go`
- Modify: all files that import `github.com/alexdx2/depbot/internal/registry` (9 files)

`registry` has no internal dependencies — only stdlib and `gopkg.in/yaml.v3`.

- [ ] **Step 1: Move files**

```bash
mv internal/registry pkg/registry
```

- [ ] **Step 2: Update all import paths**

```bash
find /home/alex/personal/depbot -name "*.go" -exec sed -i 's|github.com/alexdx2/depbot/internal/registry|github.com/alexdx2/depbot/pkg/registry|g' {} +
```

- [ ] **Step 3: Verify no old import paths remain**

```bash
grep -r "depbot/internal/registry" --include="*.go" .
```
Expected: no output

- [ ] **Step 4: Run tests**

```bash
go build ./... && go test ./...
```
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move registry package to pkg/registry for external importability"
```

---

### Task 4: Move manifest package to pkg/

**Files:**
- Move: `internal/manifest/*.go` → `pkg/manifest/*.go`
- Modify: all files that import `github.com/alexdx2/depbot/internal/manifest` (note: manifest is only imported in `internal/cli/init.go` and possibly `internal/mcp/`)

`manifest` has no internal dependencies.

- [ ] **Step 1: Move files**

```bash
mv internal/manifest pkg/manifest
```

- [ ] **Step 2: Update all import paths**

```bash
find /home/alex/personal/depbot -name "*.go" -exec sed -i 's|github.com/alexdx2/depbot/internal/manifest|github.com/alexdx2/depbot/pkg/manifest|g' {} +
```

- [ ] **Step 3: Verify no old import paths remain**

```bash
grep -r "depbot/internal/manifest" --include="*.go" .
```
Expected: no output

- [ ] **Step 4: Run tests**

```bash
go build ./... && go test ./...
```
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move manifest package to pkg/manifest for external importability"
```

---

### Task 5: Move validate package to pkg/

**Files:**
- Move: `internal/validate/*.go` → `pkg/validate/*.go`
- Modify: all files that import `github.com/alexdx2/depbot/internal/validate` (10 files)

`validate` depends on `registry` — which is already at `pkg/registry` after Task 3. The import inside `validate.go` and `validate_test.go` will be updated by the sed command.

- [ ] **Step 1: Move files**

```bash
mv internal/validate pkg/validate
```

- [ ] **Step 2: Update all import paths**

```bash
find /home/alex/personal/depbot -name "*.go" -exec sed -i 's|github.com/alexdx2/depbot/internal/validate|github.com/alexdx2/depbot/pkg/validate|g' {} +
```

- [ ] **Step 3: Verify no old import paths remain**

```bash
grep -r "depbot/internal/validate" --include="*.go" .
```
Expected: no output

- [ ] **Step 4: Run tests**

```bash
go build ./... && go test ./...
```
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move validate package to pkg/validate for external importability"
```

---

### Task 6: Move graph package to pkg/

**Files:**
- Move: `internal/graph/*.go` → `pkg/graph/*.go`
- Modify: all files that import `github.com/alexdx2/depbot/internal/graph` (13 files)

`graph` depends on `store`, `registry`, and `validate` — all already at `pkg/` after Tasks 2-5.

- [ ] **Step 1: Move files**

```bash
mv internal/graph pkg/graph
```

- [ ] **Step 2: Update all import paths**

```bash
find /home/alex/personal/depbot -name "*.go" -exec sed -i 's|github.com/alexdx2/depbot/internal/graph|github.com/alexdx2/depbot/pkg/graph|g' {} +
```

- [ ] **Step 3: Verify no old import paths remain**

```bash
grep -r "depbot/internal/graph" --include="*.go" .
```
Expected: no output

- [ ] **Step 4: Run tests**

```bash
go build ./... && go test ./...
```
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move graph package to pkg/graph for external importability"
```

---

### Task 7: Add GraphQuerier and GraphDiscoverer interfaces

**Files:**
- Create: `pkg/graph/querier.go`

These interfaces are the extension seams that chronicle-pro will implement.

- [ ] **Step 1: Write the interface file**

Create `pkg/graph/querier.go`:

```go
package graph

// GraphQuerier is the primary query interface for the knowledge graph.
// The OSS Graph type satisfies this interface directly.
// Enterprise provides a FederatedGraph implementation that queries across repos.
type GraphQuerier interface {
	QueryDeps(nodeKey string, maxDepth int, derivationFilter []string) ([]DepNode, error)
	QueryReverseDeps(nodeKey string, maxDepth int, derivationFilter []string) ([]DepNode, error)
	QueryPath(fromKey, toKey string, opts PathOptions) (*PathResult, error)
	QueryImpact(changedNodeKey string, opts ImpactOptions) (*ImpactResult, error)
	QueryStats(domainKey string) (*Stats, error)
}

// GraphDiscoverer finds .depbot/ directories and returns openable graph targets.
// The OSS implementation returns only the current directory.
// Enterprise scans child directories for multi-repo federation.
type GraphDiscoverer interface {
	Discover(rootDir string) ([]GraphTarget, error)
}

// GraphTarget represents a discovered repo with a .depbot/ directory.
type GraphTarget struct {
	RepoName string // directory name
	Path     string // absolute path to the .depbot/ directory
	Domain   string // domain from manifest, if available
}
```

- [ ] **Step 2: Verify Graph already satisfies GraphQuerier**

Run:
```bash
cd /home/alex/personal/depbot && go build ./...
```
Expected: compiles. `Graph` already has the matching method signatures — no adapter needed.

- [ ] **Step 3: Add compile-time interface check**

Add to `pkg/graph/graph.go`, after the `Graph` struct definition:

```go
// Verify Graph implements GraphQuerier at compile time.
var _ GraphQuerier = (*Graph)(nil)
```

- [ ] **Step 4: Run tests**

```bash
go build ./... && go test ./...
```
Expected: all tests pass. If the compile-time check fails, it means a method signature doesn't match — fix it.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: add GraphQuerier and GraphDiscoverer extension interfaces"
```

---

### Task 8: Add default OSS GraphDiscoverer implementation

**Files:**
- Create: `pkg/graph/discoverer.go`
- Create: `pkg/graph/discoverer_test.go`

The OSS discoverer only checks the current directory for `.depbot/`.

- [ ] **Step 1: Write the failing test**

Create `pkg/graph/discoverer_test.go`:

```go
package graph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalDiscoverer_Discover_WithDepbot(t *testing.T) {
	dir := t.TempDir()
	depbotDir := filepath.Join(dir, ".depbot")
	if err := os.MkdirAll(depbotDir, 0755); err != nil {
		t.Fatal(err)
	}

	d := &LocalDiscoverer{}
	targets, err := d.Discover(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].Path != depbotDir {
		t.Errorf("expected path %q, got %q", depbotDir, targets[0].Path)
	}
}

func TestLocalDiscoverer_Discover_NoDepbot(t *testing.T) {
	dir := t.TempDir()

	d := &LocalDiscoverer{}
	targets, err := d.Discover(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected 0 targets, got %d", len(targets))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./pkg/graph/ -run TestLocalDiscoverer -v
```
Expected: FAIL — `LocalDiscoverer` not defined.

- [ ] **Step 3: Write the implementation**

Create `pkg/graph/discoverer.go`:

```go
package graph

import (
	"os"
	"path/filepath"
)

// LocalDiscoverer is the OSS implementation of GraphDiscoverer.
// It checks only the given directory for a .depbot/ subdirectory.
type LocalDiscoverer struct{}

// Verify LocalDiscoverer implements GraphDiscoverer at compile time.
var _ GraphDiscoverer = (*LocalDiscoverer)(nil)

func (d *LocalDiscoverer) Discover(rootDir string) ([]GraphTarget, error) {
	depbotDir := filepath.Join(rootDir, ".depbot")
	info, err := os.Stat(depbotDir)
	if err != nil || !info.IsDir() {
		return nil, nil
	}

	repoName := filepath.Base(rootDir)
	return []GraphTarget{
		{
			RepoName: repoName,
			Path:     depbotDir,
			Domain:   "", // caller can load manifest separately
		},
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./pkg/graph/ -run TestLocalDiscoverer -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: add LocalDiscoverer as OSS GraphDiscoverer implementation"
```

---

### Task 9: Clean up empty internal directories

**Files:**
- Remove: `internal/store/`, `internal/registry/`, `internal/manifest/`, `internal/validate/`, `internal/graph/` (should be empty after moves)

- [ ] **Step 1: Verify directories are empty**

```bash
ls internal/store internal/registry internal/manifest internal/validate internal/graph 2>&1
```
Expected: errors or empty listings for each.

- [ ] **Step 2: Remove empty directories**

```bash
rmdir internal/store internal/registry internal/manifest internal/validate internal/graph 2>/dev/null; true
```

- [ ] **Step 3: Verify remaining internal structure**

```bash
ls internal/
```
Expected: `admin  cli  mcp` — only the three packages that stay internal.

- [ ] **Step 4: Run full test suite one final time**

```bash
go build ./... && go test ./...
```
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: remove empty internal directories after package extraction"
```

---

### Task 10: Verify external importability

This task validates that an external Go module can import the `pkg/` packages.

**Files:**
- Create (temporary, outside repo): `../chronicle-pro-test/go.mod`
- Create (temporary, outside repo): `../chronicle-pro-test/main.go`

- [ ] **Step 1: Create a test consumer module**

```bash
mkdir -p /home/alex/personal/chronicle-pro-test
```

Create `/home/alex/personal/chronicle-pro-test/go.mod`:
```
module github.com/alexdx2/chronicle-pro-test

go 1.25.5

require github.com/alexdx2/depbot v0.0.0
```

Create `/home/alex/personal/chronicle-pro-test/main.go`:
```go
package main

import (
	"fmt"

	"github.com/alexdx2/depbot/pkg/graph"
	"github.com/alexdx2/depbot/pkg/store"
	"github.com/alexdx2/depbot/pkg/registry"
	"github.com/alexdx2/depbot/pkg/manifest"
	"github.com/alexdx2/depbot/pkg/validate"
)

// Verify Graph implements GraphQuerier.
var _ graph.GraphQuerier = (*graph.Graph)(nil)

// Verify LocalDiscoverer implements GraphDiscoverer.
var _ graph.GraphDiscoverer = (*graph.LocalDiscoverer)(nil)

func main() {
	_ = store.NodeRow{}
	_ = registry.Registry{}
	_ = manifest.Manifest{}
	_ = validate.NodeInput{}
	_ = graph.DepNode{}
	fmt.Println("all pkg/ packages importable")
}
```

- [ ] **Step 2: Create go.work for local resolution**

Create `/home/alex/personal/go.work`:
```
go 1.25.5

use (
	./depbot
	./chronicle-pro-test
)
```

- [ ] **Step 3: Build the test consumer**

```bash
cd /home/alex/personal && go build ./chronicle-pro-test/...
```
Expected: compiles successfully, proving all `pkg/` packages are importable.

- [ ] **Step 4: Run the test consumer**

```bash
cd /home/alex/personal && go run ./chronicle-pro-test/main.go
```
Expected: prints `all pkg/ packages importable`

- [ ] **Step 5: Clean up**

```bash
rm -rf /home/alex/personal/chronicle-pro-test /home/alex/personal/go.work
```

No commit — this was a validation step.

---

### Summary of file changes

**Moved (internal/ → pkg/):**
- `internal/store/` → `pkg/store/` (25 files)
- `internal/registry/` → `pkg/registry/` (3 files)
- `internal/manifest/` → `pkg/manifest/` (2 files)
- `internal/validate/` → `pkg/validate/` (3 files)
- `internal/graph/` → `pkg/graph/` (15 files)

**Created:**
- `pkg/graph/querier.go` — GraphQuerier and GraphDiscoverer interfaces
- `pkg/graph/discoverer.go` — LocalDiscoverer (OSS implementation)
- `pkg/graph/discoverer_test.go` — tests for LocalDiscoverer

**Modified (import paths only):**
- `go.mod` — module path
- `cmd/chronicle/main.go`
- `e2e/*.go` (4 files)
- `internal/cli/*.go` (10 files)
- `internal/mcp/*.go` (3 files)
- `internal/admin/*.go` (2 files)
- All moved `pkg/` files that cross-import each other

**Unchanged:**
- `internal/cli/` — stays internal
- `internal/mcp/` — stays internal
- `internal/admin/` — stays internal
- `testdata/` — stays at project root, relative paths unchanged

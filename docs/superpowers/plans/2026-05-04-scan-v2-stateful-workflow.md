# Scan v2 — Stateful Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the instruction-based scan with a server-driven stateful workflow where MCP controls phase transitions and Claude only performs extraction work.

**Architecture:** New `scan_runs` table tracks workflow state. `scan_next_file` becomes the single entry point that returns phase-appropriate actions. Hard blocks prevent skipping steps. Phase 1 (breadth extraction) completes before Phase 2 (flow tracing) begins.

**Tech Stack:** Go, SQLite, MCP-Go (mark3labs/mcp-go)

---

### Task 1: scan_runs Store Layer

**Files:**
- Create: `store/scan_runs.go`
- Modify: `store/store.go` (add CREATE TABLE to schema)
- Test: `store/scan_runs_test.go`

- [ ] **Step 1: Write the failing test**

```go
// store/scan_runs_test.go
package store

import (
	"path/filepath"
	"testing"
)

func TestCreateScanRun(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "test.db"))
	defer s.Close()

	id, err := s.CreateScanRun(1, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Error("expected non-zero run_id")
	}

	run, err := s.GetScanRun(id)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != "setup" {
		t.Errorf("expected phase=setup, got %s", run.Phase)
	}
	if run.Status != "running" {
		t.Errorf("expected status=running, got %s", run.Status)
	}
}

func TestScanRunTransition(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "test.db"))
	defer s.Close()

	id, _ := s.CreateScanRun(1, "myapp")

	// Valid transition: setup → phase1_extract
	err := s.TransitionScanRun(id, "phase1_extract", 42)
	if err != nil {
		t.Fatal(err)
	}

	run, _ := s.GetScanRun(id)
	if run.Phase != "phase1_extract" {
		t.Errorf("expected phase1_extract, got %s", run.Phase)
	}
	if run.TotalFiles != 42 {
		t.Errorf("expected total_files=42, got %d", run.TotalFiles)
	}
}

func TestScanRunIncrementExtracted(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "test.db"))
	defer s.Close()

	id, _ := s.CreateScanRun(1, "myapp")
	s.TransitionScanRun(id, "phase1_extract", 10)

	s.IncrementScanRunExtracted(id)
	s.IncrementScanRunExtracted(id)

	run, _ := s.GetScanRun(id)
	if run.ExtractedFiles != 2 {
		t.Errorf("expected 2 extracted, got %d", run.ExtractedFiles)
	}
}

func TestGetActiveScanRun(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "test.db"))
	defer s.Close()

	s.CreateScanRun(1, "myapp")

	run, err := s.GetActiveScanRun("myapp")
	if err != nil {
		t.Fatal(err)
	}
	if run == nil {
		t.Error("expected active scan run")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./store/ -run "TestCreateScanRun|TestScanRunTransition|TestScanRunIncrementExtracted|TestGetActiveScanRun" -v`
Expected: FAIL — functions don't exist

- [ ] **Step 3: Add schema to store.go**

In `store/store.go`, add to the schema string (after `evidence_verification_runs` table):

```sql
CREATE TABLE IF NOT EXISTS scan_runs (
    run_id          INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id     INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
    domain_key      TEXT NOT NULL,
    phase           TEXT NOT NULL DEFAULT 'setup'
                      CHECK (phase IN ('setup','phase1_extract','phase1_resolve','phase2_select','phase2_extract','phase2_resolve','finalized')),
    status          TEXT NOT NULL DEFAULT 'running'
                      CHECK (status IN ('running','paused','blocked','completed','failed')),
    total_files     INTEGER NOT NULL DEFAULT 0,
    extracted_files INTEGER NOT NULL DEFAULT 0,
    resolved        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at      TEXT
);
```

- [ ] **Step 4: Write scan_runs.go implementation**

```go
// store/scan_runs.go
package store

import (
	"database/sql"
	"errors"
	"fmt"
)

type ScanRunRow struct {
	RunID          int64  `json:"run_id"`
	RevisionID     int64  `json:"revision_id"`
	DomainKey      string `json:"domain_key"`
	Phase          string `json:"phase"`
	Status         string `json:"status"`
	TotalFiles     int    `json:"total_files"`
	ExtractedFiles int    `json:"extracted_files"`
	Resolved       int    `json:"resolved"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

func (s *Store) CreateScanRun(revisionID int64, domainKey string) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO scan_runs (revision_id, domain_key) VALUES (?, ?)
	`, revisionID, domainKey)
	if err != nil {
		return 0, fmt.Errorf("CreateScanRun: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) GetScanRun(runID int64) (*ScanRunRow, error) {
	var r ScanRunRow
	err := s.db.QueryRow(`
		SELECT run_id, revision_id, domain_key, phase, status,
		       total_files, extracted_files, resolved, created_at, COALESCE(updated_at,'')
		FROM scan_runs WHERE run_id = ?
	`, runID).Scan(&r.RunID, &r.RevisionID, &r.DomainKey, &r.Phase, &r.Status,
		&r.TotalFiles, &r.ExtractedFiles, &r.Resolved, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("scan run %d not found", runID)
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetActiveScanRun(domainKey string) (*ScanRunRow, error) {
	var r ScanRunRow
	err := s.db.QueryRow(`
		SELECT run_id, revision_id, domain_key, phase, status,
		       total_files, extracted_files, resolved, created_at, COALESCE(updated_at,'')
		FROM scan_runs
		WHERE domain_key = ? AND phase != 'finalized' AND status NOT IN ('completed','failed')
		ORDER BY run_id DESC LIMIT 1
	`, domainKey).Scan(&r.RunID, &r.RevisionID, &r.DomainKey, &r.Phase, &r.Status,
		&r.TotalFiles, &r.ExtractedFiles, &r.Resolved, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) TransitionScanRun(runID int64, newPhase string, totalFiles int) error {
	_, err := s.db.Exec(`
		UPDATE scan_runs
		SET phase = ?, total_files = CASE WHEN ? > 0 THEN ? ELSE total_files END,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE run_id = ?
	`, newPhase, totalFiles, totalFiles, runID)
	return err
}

func (s *Store) IncrementScanRunExtracted(runID int64) error {
	_, err := s.db.Exec(`
		UPDATE scan_runs SET extracted_files = extracted_files + 1,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE run_id = ?
	`, runID)
	return err
}

func (s *Store) SetScanRunResolved(runID int64, count int) error {
	_, err := s.db.Exec(`
		UPDATE scan_runs SET resolved = ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE run_id = ?
	`, count, runID)
	return err
}

func (s *Store) CompleteScanRun(runID int64) error {
	_, err := s.db.Exec(`
		UPDATE scan_runs SET phase = 'finalized', status = 'completed',
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE run_id = ?
	`, runID)
	return err
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./store/ -run "TestCreateScanRun|TestScanRunTransition|TestScanRunIncrementExtracted|TestGetActiveScanRun" -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add store/scan_runs.go store/scan_runs_test.go store/store.go
git commit -m "feat(store): add scan_runs table for stateful workflow"
```

---

### Task 2: Scan Workflow State Machine

**Files:**
- Create: `graph/scan_workflow.go`
- Test: `graph/scan_workflow_test.go`

- [ ] **Step 1: Write the failing test**

```go
// graph/scan_workflow_test.go
package graph

import (
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func TestScanWorkflowNextAction_Setup(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	action, err := g.ScanNextAction("myapp")
	if err != nil {
		t.Fatal(err)
	}
	// No active scan → should tell Claude to start one
	if action.Action != "start_scan" {
		t.Errorf("expected action=start_scan, got %s", action.Action)
	}
}

func TestScanWorkflowNextAction_Phase1Extract(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")
	runID, _ := s.CreateScanRun(revID, "myapp")
	s.TransitionScanRun(runID, "phase1_extract", 5)

	// Create 5 open obligations
	for i := 0; i < 5; i++ {
		s.CreateObligation(revID, "myapp", "scan_file", "file"+string(rune('a'+i))+".ts", "test")
	}

	action, _ := g.ScanNextAction("myapp")
	if action.Action != "extract_files" {
		t.Errorf("expected action=extract_files, got %s", action.Action)
	}
	if len(action.Files) == 0 {
		t.Error("expected files in action")
	}
}

func TestScanWorkflowNextAction_BlockedResolve(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")
	runID, _ := s.CreateScanRun(revID, "myapp")
	s.TransitionScanRun(runID, "phase1_extract", 2)

	// Create 2 obligations, both satisfied
	s.CreateObligation(revID, "myapp", "scan_file", "a.ts", "test")
	s.CreateObligation(revID, "myapp", "scan_file", "b.ts", "test")
	s.SatisfyObligation(revID, "scan_file", "a.ts")
	s.SatisfyObligation(revID, "scan_file", "b.ts")

	action, _ := g.ScanNextAction("myapp")
	if action.Action != "call_resolve_extractions" {
		t.Errorf("expected action=call_resolve_extractions, got %s", action.Action)
	}
	if !action.Blocked {
		t.Error("expected blocked=true")
	}
}

func TestScanWorkflowNextAction_Done(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")
	runID, _ := s.CreateScanRun(revID, "myapp")
	s.TransitionScanRun(runID, "finalized", 0)
	s.CompleteScanRun(runID)

	action, _ := g.ScanNextAction("myapp")
	// No active run → start_scan or done
	if action.Action != "start_scan" {
		t.Errorf("expected start_scan (no active run), got %s", action.Action)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./graph/ -run "TestScanWorkflow" -v`
Expected: FAIL — ScanNextAction doesn't exist

- [ ] **Step 3: Write scan_workflow.go**

```go
// graph/scan_workflow.go
package graph

import "fmt"

// ScanAction is what chronicle_scan_next_file returns.
type ScanAction struct {
	ScanRunID int64             `json:"scan_run_id,omitempty"`
	Phase     string            `json:"phase"`
	Action    string            `json:"action"` // start_scan, extract_files, call_resolve_extractions, trace_flow, none
	Files     []string          `json:"files,omitempty"`
	Blocked   bool              `json:"blocked,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Done      bool              `json:"done,omitempty"`
	Progress  *ScanProgress     `json:"progress,omitempty"`
	Trigger   map[string]any    `json:"trigger,omitempty"`
	Context   map[string]any    `json:"context,omitempty"`
}

type ScanProgress struct {
	Total     int `json:"total"`
	Extracted int `json:"extracted"`
	Remaining int `json:"remaining"`
}

// ScanNextAction returns what Claude should do next based on scan run state.
func (g *Graph) ScanNextAction(domainKey string) (*ScanAction, error) {
	run, err := g.store.GetActiveScanRun(domainKey)
	if err != nil {
		return nil, err
	}

	// No active scan run
	if run == nil {
		return &ScanAction{
			Action: "start_scan",
			Reason: "No active scan. Call chronicle_discover_files to start a new scan.",
		}, nil
	}

	switch run.Phase {
	case "setup":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "setup",
			Action:    "discover_files",
			Reason:    "Call chronicle_discover_files to discover project files.",
		}, nil

	case "phase1_extract":
		return g.phase1ExtractAction(run)

	case "phase1_resolve":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase1_resolve",
			Action:    "call_resolve_extractions",
			Blocked:   true,
			Reason:    fmt.Sprintf("%d files extracted, need resolution. Call chronicle_resolve_extractions.", run.ExtractedFiles),
		}, nil

	case "phase2_select":
		return g.phase2SelectAction(run)

	case "phase2_extract":
		return g.phase2ExtractAction(run)

	case "phase2_resolve":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase2_resolve",
			Action:    "call_resolve_extractions",
			Blocked:   true,
			Reason:    "Phase 2 flows extracted. Call chronicle_resolve_extractions to add flows to graph.",
		}, nil

	case "finalized":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "finalized",
			Action:    "none",
			Done:      true,
		}, nil
	}

	return &ScanAction{Action: "none", Done: true}, nil
}

func (g *Graph) phase1ExtractAction(run *store.ScanRunRow) (*ScanAction, error) {
	// Get open scan_file obligations
	open, err := g.store.ListOpenObligations(run.RevisionID)
	if err != nil {
		return nil, err
	}

	var pending []string
	for _, ob := range open {
		if ob.ObligationType == "scan_file" {
			pending = append(pending, ob.TargetKey)
		}
	}

	// All files extracted → block until resolve
	if len(pending) == 0 {
		// Transition to phase1_resolve
		g.store.TransitionScanRun(run.RunID, "phase1_resolve", 0)
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase1_resolve",
			Action:    "call_resolve_extractions",
			Blocked:   true,
			Reason:    fmt.Sprintf("All %d files extracted. Call chronicle_resolve_extractions to build the graph.", run.TotalFiles),
			Progress:  &ScanProgress{Total: run.TotalFiles, Extracted: run.ExtractedFiles, Remaining: 0},
		}, nil
	}

	// Return batch of up to 10 files
	batch := pending
	if len(batch) > 10 {
		batch = batch[:10]
	}

	return &ScanAction{
		ScanRunID: run.RunID,
		Phase:     "phase1_extract",
		Action:    "extract_files",
		Files:     batch,
		Progress:  &ScanProgress{Total: run.TotalFiles, Extracted: run.ExtractedFiles, Remaining: len(pending)},
	}, nil
}

func (g *Graph) phase2SelectAction(run *store.ScanRunRow) (*ScanAction, error) {
	// TODO: implement trigger detection + scoring in a later task
	// For now, skip phase 2 and finalize
	g.store.TransitionScanRun(run.RunID, "finalized", 0)
	g.store.CompleteScanRun(run.RunID)
	return &ScanAction{
		ScanRunID: run.RunID,
		Phase:     "finalized",
		Action:    "none",
		Done:      true,
		Reason:    "Scan complete. Phase 2 (flow tracing) not yet implemented.",
	}, nil
}

func (g *Graph) phase2ExtractAction(run *store.ScanRunRow) (*ScanAction, error) {
	// TODO: implement in later task
	return &ScanAction{
		ScanRunID: run.RunID,
		Phase:     "phase2_extract",
		Action:    "none",
		Done:      true,
	}, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./graph/ -run "TestScanWorkflow" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add graph/scan_workflow.go graph/scan_workflow_test.go
git commit -m "feat(graph): scan workflow state machine with phase transitions"
```

---

### Task 3: Phase-Aware scan_next_file MCP Tool

**Files:**
- Modify: `internal/mcp/server.go` (rewrite scanNextBatchTool + handler)
- Modify: `internal/mcp/middleware.go` (same registration)
- Modify: `internal/mcp/commands.go` (simplify scan command)

- [ ] **Step 1: Rewrite scan_next_file tool and handler in server.go**

Replace the existing `scanNextBatchTool`/`scanNextBatchHandler` with:

```go
func scanNextFileTool() mcp.Tool {
	return mcp.NewTool("chronicle_scan_next_file",
		mcp.WithDescription("Get the next scan action. Returns what to do: extract files, resolve, trace flows, or done. Call in a loop until done=true. MCP controls the workflow — just follow the action."),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
	)
}

func scanNextFileHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		domain := strParam(req.GetArguments(), "domain")
		if domain == "" {
			return errorResult(fmt.Errorf("domain is required")), nil
		}

		action, err := g.ScanNextAction(domain)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(action), nil
	}
}
```

- [ ] **Step 2: Update tool registration in both server.go and middleware.go**

Replace `scanNextBatchTool()` → `scanNextFileTool()` and `scanNextBatchHandler` → `scanNextFileHandler` in both files.

- [ ] **Step 3: Simplify scan command in commands.go**

```go
"scan": `Scan this project:
1. Call chronicle_scan_next_file(domain) in a loop
2. Follow the action it returns:
   - "start_scan": call chronicle_discover_files first
   - "extract_files": spawn parallel subagents to read and extract each file
   - "call_resolve_extractions": call chronicle_resolve_extractions
   - "trace_flow": read the trigger file and trace the business flow
   - "none" with done=true: scan complete
3. For file extraction, spawn one subagent per file. Each subagent:
   - Reads the file
   - Extracts facts as JSON array
   - Calls chronicle_file_extracted(file_path, status, facts, revision_id, domain)
4. Keep calling chronicle_scan_next_file until done=true

MCP controls the workflow. You just execute what it tells you.`,
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./... -count=1`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/server.go internal/mcp/middleware.go internal/mcp/commands.go
git commit -m "feat(mcp): phase-aware scan_next_file — MCP drives workflow"
```

---

### Task 4: Wire discover_files into Scan Run

**Files:**
- Modify: `internal/mcp/server.go` (discoverFilesHandler creates scan run + transitions)
- Modify: `graph/discover.go` (accept run_id parameter)

- [ ] **Step 1: Update discoverFilesHandler to create/transition scan run**

```go
func discoverFilesHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		revisionID := int64Param(args, "revision_id")
		domain := strParam(args, "domain")
		if revisionID == 0 || domain == "" {
			return errorResult(fmt.Errorf("revision_id and domain are required")), nil
		}

		rootDir, _ := os.Getwd()
		var scanCfg *manifest.ScanConfig
		if m, err := manifest.LoadFile(filepath.Join(rootDir, ".depbot", "chronicle.domain.yaml")); err == nil {
			scanCfg = &m.Scan
		}

		result, err := g.DiscoverFiles(rootDir, domain, revisionID, scanCfg)
		if err != nil {
			return errorResult(err), nil
		}

		// Create or get scan run and transition to phase1_extract
		run, _ := g.Store().GetActiveScanRun(domain)
		if run == nil {
			runID, _ := g.Store().CreateScanRun(revisionID, domain)
			g.Store().TransitionScanRun(runID, "phase1_extract", result.TotalFiles)
		} else {
			g.Store().TransitionScanRun(run.RunID, "phase1_extract", result.TotalFiles)
		}

		return jsonResult(result), nil
	}
}
```

- [ ] **Step 2: Update fileExtractedHandler to increment scan run counter**

After saving extraction and satisfying obligation, add:
```go
// Increment scan run progress
if run, _ := g.Store().GetActiveScanRun(domain); run != nil {
    g.Store().IncrementScanRunExtracted(run.RunID)
}
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./... -count=1`
Expected: All pass

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/server.go graph/discover.go
git commit -m "feat: discover_files creates scan run, file_extracted tracks progress"
```

---

### Task 5: Hard Blocks on resolve and finalize

**Files:**
- Modify: `internal/mcp/server.go` (resolveExtractionsHandler, finalizeHandler)

- [ ] **Step 1: Add resolve check — transition scan run after resolve**

In `resolveExtractionsHandler`, after successful resolve, transition the scan run:
```go
// After resolve succeeds:
if run, _ := g.Store().GetActiveScanRun(domain); run != nil && run.Phase == "phase1_resolve" {
    g.Store().SetScanRunResolved(run.RunID, result.NodesCreated+result.EdgesCreated)
    g.Store().TransitionScanRun(run.RunID, "phase2_select", 0)
}
```

- [ ] **Step 2: Add finalize hard block — reject if unresolved extractions exist**

In `finalizeIncrementalScanHandler`, before calling finalize:
```go
// Check for unresolved extractions
unresolved, _ := g.Store().ListUnresolvedExtractions(revisionID, domain)
if len(unresolved) > 0 {
    return jsonResult(map[string]any{
        "error":   "Cannot finalize: unresolved extractions exist",
        "blocked": true,
        "unresolved_count": len(unresolved),
        "required_action": "Call chronicle_resolve_extractions first.",
    }), nil
}
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./... -count=1`
Expected: All pass

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/server.go
git commit -m "feat: hard blocks — finalize rejects unresolved, resolve transitions scan run"
```

---

### Task 6: setup Command with Directory Grouping

**Files:**
- Create: `graph/file_groups.go`
- Modify: `internal/mcp/commands.go` (add setup command)

- [ ] **Step 1: Write GroupFilesByDirectory**

```go
// graph/file_groups.go
package graph

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type DirectoryGroup struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// GroupFilesByDirectory runs git ls-files and groups results by parent directory.
func GroupFilesByDirectory(rootDir string) ([]DirectoryGroup, int, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return nil, 0, err
	}

	dirCounts := make(map[string]int)
	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		total++
		dir := filepath.Dir(line)
		// Collapse to 2-3 levels deep
		parts := strings.Split(dir, "/")
		if len(parts) > 3 {
			parts = parts[:3]
		}
		key := strings.Join(parts, "/")
		dirCounts[key]++
	}

	var groups []DirectoryGroup
	for path, count := range dirCounts {
		groups = append(groups, DirectoryGroup{Path: path, Count: count})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Count > groups[j].Count
	})

	return groups, total, nil
}
```

- [ ] **Step 2: Add setup command to commands.go**

```go
"setup": `Project setup — configure which files to scan:
1. List directories with file counts (grouped by path)
2. Show the user which directories contain what
3. Ask which directories to EXCLUDE from scanning
4. Default excludes: presentation layer (components/, hooks/, pages/, screens/, assets/, styles/), tests, generated
5. Write the scan.include and scan.exclude to the manifest
6. Call chronicle_discover_files to validate total count
7. Show final: "X files will be scanned. Ready?"

Use chronicle_file_groups to get directory listing with counts.
User makes directory-level decisions — not file-level.`,
```

- [ ] **Step 3: Add MCP tool for file groups**

```go
func fileGroupsTool() mcp.Tool {
	return mcp.NewTool("chronicle_file_groups",
		mcp.WithDescription("List git-tracked files grouped by directory with counts. Use during setup to show user which directories to include/exclude."),
	)
}

func fileGroupsHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rootDir, _ := os.Getwd()
		groups, total, err := GroupFilesByDirectory(rootDir)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{
			"groups":      groups,
			"total_files": total,
		}), nil
	}
}
```

Register in both server.go and middleware.go.

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./... -count=1`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add graph/file_groups.go internal/mcp/server.go internal/mcp/middleware.go internal/mcp/commands.go
git commit -m "feat: setup command with directory grouping for interactive manifest"
```

---

### Task 7: Integration Test — tom-and-jerry Full Scan

**Files:**
- Create: `graph/scan_workflow_integration_test.go`

- [ ] **Step 1: Write integration test**

```go
// graph/scan_workflow_integration_test.go
package graph

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/manifest"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func TestScanWorkflowFullCycle(t *testing.T) {
	fixtureDir := "../chronicle-pro/fixtures/tom-and-jerry"
	if _, err := os.Stat(fixtureDir); err != nil {
		t.Skip("tom-and-jerry fixture not available")
	}

	dbPath := t.TempDir() + "/test.db"
	s, _ := store.Open(dbPath)
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	// Step 1: ScanNextAction — no active run
	action, _ := g.ScanNextAction("tomandjerry")
	if action.Action != "start_scan" {
		t.Fatalf("expected start_scan, got %s", action.Action)
	}

	// Step 2: Create revision + discover files
	revID, _ := s.CreateRevision("tomandjerry", "test", "", "manual", "full", "{}")
	scanCfg := &manifest.ScanConfig{
		Include: []string{"**/*.ts", "**/*.prisma", "**/package.json"},
		Exclude: []string{"**/*.test.*", "**/*.spec.*"},
	}
	result, _ := g.DiscoverFiles(fixtureDir, "tomandjerry", revID, scanCfg)
	t.Logf("Discovered: %d files", result.TotalFiles)

	if result.TotalFiles < 30 {
		t.Errorf("expected at least 30 files, got %d", result.TotalFiles)
	}

	// Create scan run + transition
	runID, _ := s.CreateScanRun(revID, "tomandjerry")
	s.TransitionScanRun(runID, "phase1_extract", result.TotalFiles)

	// Step 3: Extract all files
	extractedCount := 0
	for {
		action, _ = g.ScanNextAction("tomandjerry")
		if action.Action == "extract_files" {
			for _, f := range action.Files {
				// Simulate extraction
				facts := []Fact{{Kind: "import", To: "./something", Symbols: []string{"Something"}}}
				factsJSON, _ := json.Marshal(facts)
				g.SaveFileExtraction(revID, "tomandjerry", f, "extracted", string(factsJSON), "")
				s.SatisfyObligation(revID, "scan_file", f)
				s.IncrementScanRunExtracted(runID)
				extractedCount++
			}
		} else if action.Action == "call_resolve_extractions" {
			break
		} else {
			t.Fatalf("unexpected action: %s", action.Action)
		}
	}

	t.Logf("Extracted: %d files", extractedCount)
	if extractedCount != result.TotalFiles {
		t.Errorf("expected %d extractions, got %d", result.TotalFiles, extractedCount)
	}

	// Step 4: Resolve
	if !action.Blocked {
		t.Error("expected blocked=true before resolve")
	}
	resolveResult, _ := g.ResolveExtractions("tomandjerry", revID)
	t.Logf("Resolved: nodes=%d edges=%d", resolveResult.NodesCreated, resolveResult.EdgesCreated)
	s.TransitionScanRun(runID, "phase2_select", 0)
	s.SetScanRunResolved(runID, resolveResult.NodesCreated+resolveResult.EdgesCreated)

	// Step 5: Phase 2 (skipped for now, goes to finalized)
	action, _ = g.ScanNextAction("tomandjerry")
	if !action.Done {
		t.Errorf("expected done=true after phase2_select (auto-finalize), got action=%s", action.Action)
	}

	// Verify final state
	run, _ := s.GetScanRun(runID)
	if run.Phase != "finalized" {
		t.Errorf("expected finalized, got %s", run.Phase)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./graph/ -run TestScanWorkflowFullCycle -v -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add graph/scan_workflow_integration_test.go
git commit -m "test: full scan workflow cycle on tom-and-jerry fixture"
```

---

### Task 8: Update file_extracted statuses

**Files:**
- Modify: `store/store.go` (update scan_extractions CHECK constraint)
- Modify: `store/extractions.go` (update allowed statuses)

- [ ] **Step 1: Update CHECK constraint in schema**

Change the scan_extractions status CHECK:
```sql
CHECK (status IN ('extracted','no_runtime_architecture','config_only','type_only','generated','skipped','failed','resolved'))
```

- [ ] **Step 2: Build and test**

Run: `go build ./... && go test ./... -count=1`
Expected: All pass (existing tests use 'extracted', 'no_architecture', 'skipped' which need updating)

- [ ] **Step 3: Update existing tests that use old statuses**

Change `"no_architecture"` → `"no_runtime_architecture"` in test files.

- [ ] **Step 4: Commit**

```bash
git add store/store.go store/extractions.go graph/extraction_integration_test.go
git commit -m "feat: richer file processing statuses (config_only, type_only, etc)"
```

---

### Task 9: Delegates Fact Kind in resolve_extractions

**Files:**
- Modify: `graph/resolve_extractions.go` (add "delegates" case)
- Modify: `graph/extraction_integration_test.go` (add test)

- [ ] **Step 1: Add delegates handling**

In `resolveOneFact`, add a new case:

```go
case "delegates":
    // Record delegation — creates obligation for delegated file if not already scanned
    if fact.To == "" {
        return counts, nil
    }
    // Check if delegated file was extracted
    delegatedFile := fact.To
    if !strings.HasPrefix(delegatedFile, "/") {
        // Relative path — resolve relative to current file
        delegatedFile = filepath.Join(filepath.Dir(filePath), fact.To)
    }
    // Create obligation if not already satisfied
    if revisionID > 0 {
        g.store.CreateObligation(revisionID, domainKey, "scan_file", delegatedFile, "delegation from "+filePath)
    }
    return counts, &UnresolvedRef{
        FromFile: filePath,
        Kind:     "delegation",
        Target:   delegatedFile,
        Reason:   fmt.Sprintf("delegates to %s via %s — ensure this file is also scanned", delegatedFile, fact.Method),
    }
```

- [ ] **Step 2: Test with delegation fact**

Add to extraction_integration_test.go:
```go
func TestExtractionWithDelegation(t *testing.T) {
    // ... setup ...
    facts, _ := json.Marshal([]Fact{
        {Kind: "delegates", To: "./handler.factory.ts", Method: "registerHandlers"},
    })
    g.SaveFileExtraction(revID, "myapp", "src/gateway.ts", "extracted", string(facts), "")
    result, _ := g.ResolveExtractions("myapp", revID)

    if len(result.Unresolved) == 0 {
        t.Error("expected delegation to be reported as unresolved")
    }
    // Check obligation was created for delegated file
    open, _ := s.ListOpenObligations(revID)
    found := false
    for _, ob := range open {
        if strings.Contains(ob.TargetKey, "handler.factory.ts") {
            found = true
        }
    }
    if !found {
        t.Error("expected obligation created for delegated file")
    }
}
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./graph/ -run TestExtractionWithDelegation -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add graph/resolve_extractions.go graph/extraction_integration_test.go
git commit -m "feat: delegates fact kind — creates obligation for delegated files"
```

---

### Task 10: Final — Build, Full Test Suite, Version Bump

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -count=1
```
Expected: All pass

- [ ] **Step 2: Bump version**

```go
// version/version.go
const Version = "0.6.0"
```

- [ ] **Step 3: Build binary**

```bash
go build -o tmp/chronicle ./cmd/chronicle/
```

- [ ] **Step 4: Commit and tag**

```bash
git add version/version.go
git commit -m "release: v0.6.0 — stateful scan workflow, phase transitions, hard blocks"
git tag v0.6.0
git push origin main v0.6.0
```

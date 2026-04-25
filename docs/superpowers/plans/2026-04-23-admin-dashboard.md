# Admin Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a localhost admin dashboard (`oracle admin`) with real-time MCP request logging, graph stats, layered/force graph visualization, and validation — all embedded in the Go binary.

**Architecture:** Go HTTP server with embedded static assets (Alpine.js + htmx + D3.js SPA). MCP request logging via SQLite table + middleware wrapper. WebSocket for real-time updates. Two views: Overview (stats + request log + panels) and Graph (layered/force diagram with click-to-inspect).

**Tech Stack:** Go 1.22+, gorilla/websocket, Alpine.js (CDN inline), htmx (CDN inline), D3.js (CDN inline), embedded HTML/CSS/JS via `//go:embed`

---

### Task 1: MCP Request Log — Store Layer

**Files:**
- Modify: `internal/store/store.go` (add table to schema)
- Create: `internal/store/requestlog.go`
- Create: `internal/store/requestlog_test.go`

- [ ] **Step 1: Add mcp_request_log table to schema**

In `internal/store/store.go`, add to the `schema` const, after the `graph_snapshots` table:

```sql
CREATE TABLE IF NOT EXISTS mcp_request_log (
  request_id     INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  tool_name      TEXT NOT NULL,
  params_json    TEXT NOT NULL DEFAULT '{}',
  result_json    TEXT,
  error_message  TEXT,
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  summary        TEXT
);

CREATE INDEX IF NOT EXISTS idx_mcp_request_log_timestamp
  ON mcp_request_log(timestamp DESC);
```

- [ ] **Step 2: Write failing tests**

Create `internal/store/requestlog_test.go`:

```go
package store

import (
	"testing"
)

func TestLogRequest(t *testing.T) {
	s := openTestStore(t)

	id, err := s.LogRequest(RequestLogEntry{
		ToolName:    "oracle_import_all",
		ParamsJSON:  `{"revision_id": 5}`,
		ResultJSON:  `{"nodes_created": 18}`,
		DurationMs:  142,
		Summary:     "18n 17e 7ev",
	})
	if err != nil {
		t.Fatalf("LogRequest: %v", err)
	}
	if id <= 0 {
		t.Errorf("request_id = %d, want > 0", id)
	}
}

func TestLogRequestError(t *testing.T) {
	s := openTestStore(t)

	id, err := s.LogRequest(RequestLogEntry{
		ToolName:     "oracle_query_reverse_deps",
		ParamsJSON:   `{"node_key": "code:provider:orders:unknown"}`,
		ErrorMessage: `node "code:provider:orders:unknown" not found`,
		DurationMs:   5,
		Summary:      `node not found`,
	})
	if err != nil {
		t.Fatalf("LogRequest: %v", err)
	}
	if id <= 0 {
		t.Errorf("request_id = %d, want > 0", id)
	}
}

func TestListRecentRequests(t *testing.T) {
	s := openTestStore(t)

	s.LogRequest(RequestLogEntry{ToolName: "oracle_scan_status", ParamsJSON: "{}", DurationMs: 2, Summary: "domain: orders"})
	s.LogRequest(RequestLogEntry{ToolName: "oracle_extraction_guide", ParamsJSON: "{}", DurationMs: 1, Summary: "full guide"})
	s.LogRequest(RequestLogEntry{ToolName: "oracle_import_all", ParamsJSON: "{}", DurationMs: 142, Summary: "18n 17e"})

	entries, err := s.ListRecentRequests(10)
	if err != nil {
		t.Fatalf("ListRecentRequests: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("count = %d, want 3", len(entries))
	}
	// Most recent first
	if entries[0].ToolName != "oracle_import_all" {
		t.Errorf("first = %q, want oracle_import_all", entries[0].ToolName)
	}
}

func TestListRequestsSince(t *testing.T) {
	s := openTestStore(t)

	s.LogRequest(RequestLogEntry{ToolName: "tool_a", ParamsJSON: "{}", DurationMs: 1})
	id2, _ := s.LogRequest(RequestLogEntry{ToolName: "tool_b", ParamsJSON: "{}", DurationMs: 1})
	s.LogRequest(RequestLogEntry{ToolName: "tool_c", ParamsJSON: "{}", DurationMs: 1})

	entries, err := s.ListRequestsSince(id2)
	if err != nil {
		t.Fatalf("ListRequestsSince: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("count = %d, want 1", len(entries))
	}
	if entries[0].ToolName != "tool_c" {
		t.Errorf("entry = %q, want tool_c", entries[0].ToolName)
	}
}

func TestRequestStats(t *testing.T) {
	s := openTestStore(t)

	s.LogRequest(RequestLogEntry{ToolName: "a", ParamsJSON: "{}", DurationMs: 10})
	s.LogRequest(RequestLogEntry{ToolName: "b", ParamsJSON: "{}", DurationMs: 20, ErrorMessage: "fail"})
	s.LogRequest(RequestLogEntry{ToolName: "c", ParamsJSON: "{}", DurationMs: 30})

	stats, err := s.RequestStats()
	if err != nil {
		t.Fatalf("RequestStats: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("total = %d, want 3", stats.Total)
	}
	if stats.Errors != 1 {
		t.Errorf("errors = %d, want 1", stats.Errors)
	}
	if stats.AvgDurationMs != 20 {
		t.Errorf("avg = %d, want 20", stats.AvgDurationMs)
	}
}
```

- [ ] **Step 3: Implement requestlog.go**

Create `internal/store/requestlog.go`:

```go
package store

import (
	"fmt"
)

type RequestLogEntry struct {
	RequestID    int64  `json:"request_id"`
	Timestamp    string `json:"timestamp"`
	ToolName     string `json:"tool_name"`
	ParamsJSON   string `json:"params_json"`
	ResultJSON   string `json:"result_json,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	DurationMs   int    `json:"duration_ms"`
	Summary      string `json:"summary,omitempty"`
}

type RequestLogStats struct {
	Total         int `json:"total"`
	Errors        int `json:"errors"`
	AvgDurationMs int `json:"avg_duration_ms"`
}

func (s *Store) LogRequest(e RequestLogEntry) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO mcp_request_log (tool_name, params_json, result_json, error_message, duration_ms, summary)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.ToolName, e.ParamsJSON, e.ResultJSON, e.ErrorMessage, e.DurationMs, e.Summary,
	)
	if err != nil {
		return 0, fmt.Errorf("LogRequest: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) ListRecentRequests(limit int) ([]RequestLogEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT request_id, timestamp, tool_name, params_json,
		 COALESCE(result_json,''), COALESCE(error_message,''), duration_ms, COALESCE(summary,'')
		 FROM mcp_request_log ORDER BY request_id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("ListRecentRequests: %w", err)
	}
	defer rows.Close()
	var out []RequestLogEntry
	for rows.Next() {
		var e RequestLogEntry
		if err := rows.Scan(&e.RequestID, &e.Timestamp, &e.ToolName, &e.ParamsJSON,
			&e.ResultJSON, &e.ErrorMessage, &e.DurationMs, &e.Summary); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ListRequestsSince(afterID int64) ([]RequestLogEntry, error) {
	rows, err := s.db.Query(
		`SELECT request_id, timestamp, tool_name, params_json,
		 COALESCE(result_json,''), COALESCE(error_message,''), duration_ms, COALESCE(summary,'')
		 FROM mcp_request_log WHERE request_id > ? ORDER BY request_id ASC`, afterID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListRequestsSince: %w", err)
	}
	defer rows.Close()
	var out []RequestLogEntry
	for rows.Next() {
		var e RequestLogEntry
		if err := rows.Scan(&e.RequestID, &e.Timestamp, &e.ToolName, &e.ParamsJSON,
			&e.ResultJSON, &e.ErrorMessage, &e.DurationMs, &e.Summary); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) RequestStats() (*RequestLogStats, error) {
	var stats RequestLogStats
	err := s.db.QueryRow(
		`SELECT COUNT(*), COUNT(CASE WHEN error_message != '' AND error_message IS NOT NULL THEN 1 END),
		 COALESCE(AVG(duration_ms), 0) FROM mcp_request_log`,
	).Scan(&stats.Total, &stats.Errors, &stats.AvgDurationMs)
	if err != nil {
		return nil, fmt.Errorf("RequestStats: %w", err)
	}
	return &stats, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/store/ -v -run "TestLog|TestList|TestRequest"
go test ./... -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat: add MCP request log table and store CRUD"
```

---

### Task 2: MCP Logging Middleware

**Files:**
- Create: `internal/mcp/middleware.go`
- Create: `internal/mcp/middleware_test.go`
- Modify: `internal/mcp/server.go` (add WithLogging option)

- [ ] **Step 1: Create middleware.go**

This wraps the MCP server's tool handler to log every request to the store.

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/depbot/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// LoggingMiddleware wraps a tool handler to log requests to the store.
func LoggingMiddleware(s *store.Store, toolName string, next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		result, err := next(ctx, req)

		duration := time.Since(start).Milliseconds()

		// Build log entry
		entry := store.RequestLogEntry{
			ToolName:   toolName,
			DurationMs: int(duration),
		}

		// Serialize params
		paramsBytes, _ := json.Marshal(req.GetArguments())
		entry.ParamsJSON = string(paramsBytes)

		// Check for errors
		if err != nil {
			entry.ErrorMessage = err.Error()
			entry.Summary = truncate(err.Error(), 80)
		} else if result != nil && result.IsError {
			// MCP-level error (returned in result, not Go error)
			for _, c := range result.Content {
				if tc, ok := c.(mcp.TextContent); ok {
					entry.ErrorMessage = tc.Text
					entry.Summary = truncate(tc.Text, 80)
					break
				}
			}
		} else if result != nil {
			// Extract result text for logging
			for _, c := range result.Content {
				if tc, ok := c.(mcp.TextContent); ok {
					entry.ResultJSON = tc.Text
					entry.Summary = generateSummary(toolName, tc.Text)
					break
				}
			}
		}

		s.LogRequest(entry)
		return result, err
	}
}

func generateSummary(toolName, resultJSON string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &data); err != nil {
		return ""
	}

	switch toolName {
	case "oracle_import_all":
		n, _ := data["nodes_created"].(float64)
		e, _ := data["edges_created"].(float64)
		ev, _ := data["evidence_created"].(float64)
		return fmt.Sprintf("%.0fn %.0fe %.0fev", n, e, ev)
	case "oracle_revision_create":
		id, _ := data["revision_id"].(float64)
		return fmt.Sprintf("rev #%.0f", id)
	case "oracle_node_upsert":
		id, _ := data["node_id"].(float64)
		return fmt.Sprintf("node_id: %.0f", id)
	case "oracle_edge_upsert":
		id, _ := data["edge_id"].(float64)
		return fmt.Sprintf("edge_id: %.0f", id)
	case "oracle_evidence_add":
		id, _ := data["evidence_id"].(float64)
		return fmt.Sprintf("evidence #%.0f", id)
	case "oracle_snapshot_create":
		id, _ := data["snapshot_id"].(float64)
		return fmt.Sprintf("snapshot #%.0f", id)
	case "oracle_stale_mark":
		n, _ := data["stale_nodes"].(float64)
		e, _ := data["stale_edges"].(float64)
		return fmt.Sprintf("%.0f nodes, %.0f edges stale", n, e)
	case "oracle_query_deps":
		if arr, ok := data[""].([]any); ok {
			return fmt.Sprintf("%d deps", len(arr))
		}
		// Try as array directly
		var arr []any
		json.Unmarshal([]byte(resultJSON), &arr)
		return fmt.Sprintf("%d deps", len(arr))
	case "oracle_query_reverse_deps":
		var arr []any
		json.Unmarshal([]byte(resultJSON), &arr)
		return fmt.Sprintf("%d reverse deps", len(arr))
	case "oracle_query_path":
		paths, _ := data["paths"].([]any)
		if len(paths) == 0 {
			return "0 paths"
		}
		return fmt.Sprintf("%d path(s)", len(paths))
	case "oracle_impact":
		total, _ := data["total_impacted"].(float64)
		return fmt.Sprintf("%.0f impacted", total)
	case "oracle_query_stats":
		n, _ := data["node_count"].(float64)
		e, _ := data["edge_count"].(float64)
		return fmt.Sprintf("%.0fn %.0fe", n, e)
	case "oracle_extraction_guide":
		return "guide returned"
	case "oracle_scan_status":
		domain, _ := data["domain"].(string)
		if domain != "" {
			return fmt.Sprintf("domain: %s", domain)
		}
		return "status returned"
	case "oracle_node_list":
		var arr []any
		json.Unmarshal([]byte(resultJSON), &arr)
		return fmt.Sprintf("%d nodes", len(arr))
	case "oracle_edge_list":
		var arr []any
		json.Unmarshal([]byte(resultJSON), &arr)
		return fmt.Sprintf("%d edges", len(arr))
	case "oracle_node_get":
		return "node details"
	case "oracle_validate_graph":
		if issues, ok := data["issues"].([]any); ok {
			return fmt.Sprintf("%d issues", len(issues))
		}
		return "validated"
	default:
		return ""
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// NewServerWithLogging creates an MCP server with request logging enabled.
func NewServerWithLogging(g *graph.Graph, logStore *store.Store) *server.MCPServer {
	s := server.NewMCPServer("oracle", "0.1.0")

	addLogged := func(tool mcp.Tool, handler server.ToolHandlerFunc) {
		s.AddTool(tool, LoggingMiddleware(logStore, tool.Name, handler))
	}

	addLogged(revisionCreateTool(), revisionCreateHandler(g))
	addLogged(nodeUpsertTool(), nodeUpsertHandler(g))
	addLogged(nodeListTool(), nodeListHandler(g))
	addLogged(nodeGetTool(), nodeGetHandler(g))
	addLogged(edgeUpsertTool(), edgeUpsertHandler(g))
	addLogged(edgeListTool(), edgeListHandler(g))
	addLogged(evidenceAddTool(), evidenceAddHandler(g))
	addLogged(importAllTool(), importAllHandler(g))
	addLogged(queryDepsTool(), queryDepsHandler(g))
	addLogged(queryReverseDepsTool(), queryReverseDepsHandler(g))
	addLogged(queryStatsTool(), queryStatsHandler(g))
	addLogged(snapshotCreateTool(), snapshotCreateHandler(g))
	addLogged(staleMarkTool(), staleMarkHandler(g))
	addLogged(queryPathTool(), queryPathHandler(g))
	addLogged(impactTool(), impactHandler(g))
	addLogged(extractionGuideTool(), extractionGuideHandler())
	addLogged(scanStatusTool(), scanStatusHandler(g))

	return s
}
```

Note: The `graph` import is needed — add `"github.com/anthropics/depbot/internal/graph"` to imports.

- [ ] **Step 2: Update MCP serve command to use logging**

In `internal/cli/mcp.go`, modify the serve command's `Run` function to use `NewServerWithLogging` when `--log` flag is set (default true):

```go
Run: func(cmd *cobra.Command, args []string) {
	g := openGraph()
	var s *server.MCPServer
	enableLog, _ := cmd.Flags().GetBool("log")
	if enableLog {
		s = mcpserver.NewServerWithLogging(g, g.Store())
	} else {
		s = mcpserver.NewServer(g)
	}
	if err := server.ServeStdio(s); err != nil {
		outputError(err)
	}
},
```

Add flag: `serveCmd.Flags().Bool("log", true, "Log MCP requests to SQLite")`

- [ ] **Step 3: Run tests and build**

```bash
go build ./internal/mcp/
go build -o oracle ./cmd/oracle
go test ./... -count=1
```

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/middleware.go internal/cli/mcp.go
git commit -m "feat: add MCP request logging middleware"
```

---

### Task 3: Admin HTTP Server + API Endpoints

**Files:**
- Create: `internal/admin/server.go`
- Create: `internal/admin/handlers.go`
- Create: `internal/admin/server_test.go`

- [ ] **Step 1: Create server.go**

```go
package admin

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/store"
)

//go:embed static/*
var staticFS embed.FS

type Server struct {
	graph  *graph.Graph
	store  *store.Store
	hub    *Hub
	port   int
}

func NewServer(g *graph.Graph, s *store.Store, port int) *Server {
	return &Server{
		graph: g,
		store: s,
		hub:   NewHub(),
		port:  port,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/requests", s.handleRequests)
	mux.HandleFunc("/api/low-confidence", s.handleLowConfidence)
	mux.HandleFunc("/api/scans", s.handleScans)
	mux.HandleFunc("/api/validate", s.handleValidate)
	mux.HandleFunc("/api/graph", s.handleGraph)

	// WebSocket
	mux.HandleFunc("/ws", s.handleWebSocket)

	// Static files
	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("static fs: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticContent)))

	// Start WebSocket hub
	go s.hub.Run()

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	fmt.Printf("Oracle Admin: http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) Hub() *Hub {
	return s.hub
}
```

- [ ] **Step 2: Create handlers.go**

```go
package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = "orders" // fallback
	}

	stats, err := s.graph.QueryStats(domain)
	if err != nil {
		httpError(w, err, 500)
		return
	}

	reqStats, _ := s.store.RequestStats()

	result := map[string]any{
		"graph":    stats,
		"requests": reqStats,
	}
	httpJSON(w, result)
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	if sinceStr != "" {
		sinceID, err := strconv.ParseInt(sinceStr, 10, 64)
		if err != nil {
			httpError(w, err, 400)
			return
		}
		entries, err := s.store.ListRequestsSince(sinceID)
		if err != nil {
			httpError(w, err, 500)
			return
		}
		httpJSON(w, entries)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	entries, err := s.store.ListRecentRequests(limit)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, entries)
}

func (s *Server) handleLowConfidence(w http.ResponseWriter, r *http.Request) {
	thresholdStr := r.URL.Query().Get("threshold")
	threshold := 0.80
	if thresholdStr != "" {
		if t, err := strconv.ParseFloat(thresholdStr, 64); err == nil {
			threshold = t
		}
	}

	edges, err := s.store.ListEdges(store.EdgeFilter{})
	if err != nil {
		httpError(w, err, 500)
		return
	}

	var lowConf []map[string]any
	for _, e := range edges {
		if e.Confidence < threshold && e.Active {
			fromNode, _ := s.store.GetNodeByID(e.FromNodeID)
			toNode, _ := s.store.GetNodeByID(e.ToNodeID)
			fromName, toName := "", ""
			if fromNode != nil {
				fromName = fromNode.Name
			}
			if toNode != nil {
				toName = toNode.Name
			}
			lowConf = append(lowConf, map[string]any{
				"edge_key":    e.EdgeKey,
				"from_name":   fromName,
				"to_name":     toName,
				"edge_type":   e.EdgeType,
				"derivation":  e.DerivationKind,
				"confidence":  e.Confidence,
			})
		}
	}
	httpJSON(w, lowConf)
}

func (s *Server) handleScans(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = "orders"
	}
	snaps, err := s.store.ListSnapshots(domain)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, snaps)
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var issues []map[string]string

	nodes, _ := s.store.ListNodes(store.NodeFilter{})
	edges, _ := s.store.ListEdges(store.EdgeFilter{})

	for _, n := range nodes {
		if _, err := validate.NormalizeNodeKey(n.NodeKey); err != nil {
			issues = append(issues, map[string]string{
				"kind": "malformed_key", "target": n.NodeKey, "message": err.Error(),
			})
		}
		if n.Confidence < 0 || n.Confidence > 1 {
			issues = append(issues, map[string]string{
				"kind": "confidence_range", "target": n.NodeKey,
				"message": fmt.Sprintf("confidence %f out of [0,1]", n.Confidence),
			})
		}
	}
	for _, e := range edges {
		if _, err := validate.NormalizeEdgeKey(e.EdgeKey); err != nil {
			issues = append(issues, map[string]string{
				"kind": "malformed_key", "target": e.EdgeKey, "message": err.Error(),
			})
		}
	}

	httpJSON(w, map[string]any{
		"issues":        issues,
		"nodes_checked": len(nodes),
		"edges_checked": len(edges),
		"valid":         len(issues) == 0,
	})
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = "orders"
	}

	nodes, _ := s.store.ListNodes(store.NodeFilter{Domain: domain})
	edges, _ := s.store.ListEdges(store.EdgeFilter{})

	// Build node ID set for domain filtering of edges
	nodeIDSet := map[int64]bool{}
	nodeList := []map[string]any{}
	for _, n := range nodes {
		nodeIDSet[n.NodeID] = true
		nodeList = append(nodeList, map[string]any{
			"node_id":   n.NodeID,
			"node_key":  n.NodeKey,
			"layer":     n.Layer,
			"node_type": n.NodeType,
			"name":      n.Name,
			"repo_name": n.RepoName,
			"file_path": n.FilePath,
			"status":    n.Status,
			"confidence": n.Confidence,
		})
	}

	edgeList := []map[string]any{}
	for _, e := range edges {
		if nodeIDSet[e.FromNodeID] || nodeIDSet[e.ToNodeID] {
			edgeList = append(edgeList, map[string]any{
				"edge_id":     e.EdgeID,
				"edge_key":    e.EdgeKey,
				"from_node_id": e.FromNodeID,
				"to_node_id":  e.ToNodeID,
				"edge_type":   e.EdgeType,
				"derivation":  e.DerivationKind,
				"confidence":  e.Confidence,
				"active":      e.Active,
			})
		}
	}

	httpJSON(w, map[string]any{
		"nodes": nodeList,
		"edges": edgeList,
	})
}

func httpJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
```

Add `"fmt"` to imports.

- [ ] **Step 3: Create server_test.go**

```go
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	g := graph.New(s, reg)
	return NewServer(g, s, 0)
}

func TestHandleStats(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/stats?domain=orders", nil)
	w := httptest.NewRecorder()
	srv.handleStats(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	if result["graph"] == nil {
		t.Error("missing graph stats")
	}
}

func TestHandleRequests(t *testing.T) {
	srv := setupTestServer(t)
	srv.store.LogRequest(store.RequestLogEntry{ToolName: "test_tool", ParamsJSON: "{}", DurationMs: 5, Summary: "test"})

	req := httptest.NewRequest("GET", "/api/requests", nil)
	w := httptest.NewRecorder()
	srv.handleRequests(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var entries []store.RequestLogEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 1 {
		t.Errorf("count = %d, want 1", len(entries))
	}
}

func TestHandleValidate(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/validate", nil)
	w := httptest.NewRecorder()
	srv.handleValidate(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	if result["valid"] != true {
		t.Error("expected valid = true for empty graph")
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/admin/ -v
go test ./... -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/admin/
git commit -m "feat: add admin HTTP server with API endpoints"
```

---

### Task 4: WebSocket Hub

**Files:**
- Create: `internal/admin/websocket.go`

- [ ] **Step 1: Add gorilla/websocket dependency**

```bash
go get github.com/gorilla/websocket
```

- [ ] **Step 2: Create websocket.go**

```go
package admin

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // localhost only
}

type Hub struct {
	clients    map[*websocket.Conn]bool
	mu         sync.Mutex
	broadcast  chan []byte
}

func NewHub() *Hub {
	return &Hub{
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan []byte, 256),
	}
}

func (h *Hub) Run() {
	for msg := range h.broadcast {
		h.mu.Lock()
		for conn := range h.clients {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				conn.Close()
				delete(h.clients, conn)
			}
		}
		h.mu.Unlock()
	}
}

func (h *Hub) Send(msgType string, data any) {
	msg, err := json.Marshal(map[string]any{"type": msgType, "data": data})
	if err != nil {
		return
	}
	select {
	case h.broadcast <- msg:
	default:
		// Drop if buffer full
	}
}

func (h *Hub) AddClient(conn *websocket.Conn) {
	h.mu.Lock()
	h.clients[conn] = true
	h.mu.Unlock()
}

func (h *Hub) RemoveClient(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, conn)
	h.mu.Unlock()
}

func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade: %v", err)
		return
	}
	s.hub.AddClient(conn)
	defer func() {
		s.hub.RemoveClient(conn)
		conn.Close()
	}()

	// Read pump — just keep connection alive, discard client messages
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}
```

- [ ] **Step 3: Build to verify**

```bash
go build ./internal/admin/
```

- [ ] **Step 4: Commit**

```bash
git add internal/admin/websocket.go go.mod go.sum
git commit -m "feat: add WebSocket hub for real-time admin updates"
```

---

### Task 5: Frontend SPA — Overview Tab

**Files:**
- Create: `internal/admin/static/index.html`
- Create: `internal/admin/static/style.css`
- Create: `internal/admin/static/app.js`

This is the main frontend. Uses Alpine.js for reactivity, htmx could be used for polling but WebSocket is primary. Alpine.js and D3.js loaded from CDN URLs embedded in the HTML.

- [ ] **Step 1: Create index.html**

Create `internal/admin/static/index.html` — the full SPA. This is a single HTML file with:
- Alpine.js CDN script tag
- D3.js CDN script tag
- Link to style.css and app.js
- Two tabs: Overview and Graph
- Overview: stat cards, MCP request log, graph breakdown, low confidence, scan history, validation
- Graph: toolbar + SVG container for diagram + node detail panel
- WebSocket connection in app.js

The HTML should match the approved light-theme mockup exactly. Use Alpine.js `x-data`, `x-init`, `x-for`, `x-show` for all dynamic content. WebSocket messages update the Alpine data reactively.

The implementer should create the full HTML file matching the mockup design from the spec. Key elements:
- Header: domain, rev info, last scan, Overview/Graph tabs
- 6 stat cards in a grid
- MCP request log panel (left, scrollable, entries prepend on WS message)
- Graph breakdown panel
- Low confidence panel
- Scan history panel
- Validation bar
- Graph tab with layered SVG + force D3 toggle

- [ ] **Step 2: Create style.css**

Light theme CSS matching the approved mockup:
- Background: #f0f1f3
- Cards: #ffffff with box-shadow: 0 1px 3px rgba(0,0,0,0.06)
- Primary: #4361ee
- Success: #2ecc71
- Warning: #f39c12
- Error: #e74c3c
- Contract color: #27ae60
- Service color: #e74c3c
- Topic color: #1abc9c
- Font: system-ui, monospace for code
- Request log entries with left border color indicating status

- [ ] **Step 3: Create app.js**

Alpine.js application logic:
- `x-data` object with: `tab` (overview/graph), `stats`, `requests`, `lowConf`, `scans`, `validation`, `graphData`, `graphMode` (layered/force), `selectedNode`
- `init()`: fetch all API endpoints, open WebSocket
- WebSocket `onmessage`: handle `mcp_request` (prepend to requests array) and `stats_update` (refresh stats)
- Methods: `fetchStats()`, `fetchRequests()`, `fetchLowConf()`, `fetchScans()`, `runValidation()`, `fetchGraph()`, `selectNode(node)`
- Auto-refresh stats every 5 seconds as fallback

- [ ] **Step 4: Build and verify**

```bash
go build -o oracle ./cmd/oracle
```

- [ ] **Step 5: Commit**

```bash
git add internal/admin/static/
git commit -m "feat: add admin dashboard frontend SPA"
```

---

### Task 6: Graph Visualization — Layered + Force

**Files:**
- Create: `internal/admin/static/graph.js`

- [ ] **Step 1: Create graph.js**

Two rendering functions:

**`renderLayeredGraph(container, nodes, edges, options)`**
- Groups nodes by layer into horizontal bands
- Draws SVG with:
  - Layer labels and dashed separators
  - Nodes as rounded rects colored by layer
  - Edge arrows between nodes (curved paths)
  - Low-confidence nodes/edges in amber
  - Click handler on nodes → calls `options.onNodeClick(node)`

**`renderForceGraph(container, nodes, edges, options)`**
- D3 force simulation
- Nodes as circles colored by layer, sized by edge count
- Edges as lines, solid for hard, dashed for linked
- Drag, zoom, pan
- Tooltip on hover showing node name
- Click handler → `options.onNodeClick(node)`

Both functions:
- Accept a filter for hiding structural edges (CONTAINS etc.)
- Accept a flag for highlighting low-confidence items
- Clear the container before rendering

- [ ] **Step 2: Integrate with app.js**

In app.js, when Graph tab is active, call the appropriate render function after `fetchGraph()`. Wire up the `selectNode()` callback to populate the node detail panel.

- [ ] **Step 3: Build and verify**

```bash
go build -o oracle ./cmd/oracle
```

- [ ] **Step 4: Commit**

```bash
git add internal/admin/static/graph.js
git commit -m "feat: add layered and force-directed graph rendering"
```

---

### Task 7: CLI Command + Integration

**Files:**
- Create: `internal/cli/admin.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Create admin.go**

```go
package cli

import (
	"github.com/anthropics/depbot/internal/admin"
	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Start the admin dashboard (localhost only)",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			port, _ := cmd.Flags().GetInt("port")
			srv := admin.NewServer(g, g.Store(), port)
			if err := srv.Start(); err != nil {
				outputError(err)
			}
		},
	}

	cmd.Flags().Int("port", 4200, "HTTP port")
	return cmd
}
```

- [ ] **Step 2: Register in root.go**

Add `newAdminCmd()` to `root.AddCommand(...)`.

- [ ] **Step 3: Build and test**

```bash
go build -o oracle ./cmd/oracle
./oracle admin --help
```

Expected output shows `--port` flag with default 4200.

- [ ] **Step 4: Quick smoke test**

```bash
# Start admin in background, verify it responds, then kill
./oracle admin --port 4201 --db /tmp/admin-test.db &
ADMIN_PID=$!
sleep 1
curl -s http://localhost:4201/api/stats | head -1
kill $ADMIN_PID 2>/dev/null
```

Expected: JSON response with stats.

- [ ] **Step 5: Run all tests**

```bash
go test ./... -count=1
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli/admin.go internal/cli/root.go
git commit -m "feat: add oracle admin CLI command"
```

---

### Task 8: MCP Middleware → WebSocket Bridge

**Files:**
- Modify: `internal/mcp/middleware.go`
- Modify: `internal/cli/mcp.go`

The middleware needs to broadcast MCP requests to the admin WebSocket hub so connected browsers get real-time updates. Since the MCP server and admin server may run in the same process, we need a notification callback.

- [ ] **Step 1: Add notification callback to middleware**

Add a `Notifier` interface and update `LoggingMiddleware`:

```go
// Notifier is called after each MCP request is logged.
type Notifier interface {
	OnMCPRequest(entry store.RequestLogEntry)
}

func LoggingMiddlewareWithNotify(s *store.Store, toolName string, next server.ToolHandlerFunc, notifier Notifier) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// ... same as LoggingMiddleware ...
		// After s.LogRequest(entry):
		if notifier != nil {
			entry.RequestID = id // set the ID from LogRequest
			notifier.OnMCPRequest(entry)
		}
		return result, err
	}
}
```

Update `NewServerWithLogging` to accept optional `Notifier`:

```go
func NewServerWithLogging(g *graph.Graph, logStore *store.Store, notifier Notifier) *server.MCPServer {
	// ... use LoggingMiddlewareWithNotify with notifier ...
}
```

- [ ] **Step 2: Implement Notifier in admin server**

In `internal/admin/server.go`, add:

```go
// OnMCPRequest broadcasts the request to all connected WebSocket clients.
func (s *Server) OnMCPRequest(entry store.RequestLogEntry) {
	s.hub.Send("mcp_request", entry)
}
```

This makes `*Server` satisfy the `Notifier` interface.

- [ ] **Step 3: Update CLI to connect MCP logging with admin hub**

In the `mcp serve` command, if admin is running in the same process (future), pass the admin server as notifier. For now, the admin reads from SQLite on its own polling cycle, and the WebSocket bridge works when both are in the same process.

Alternatively, the simpler approach: the admin server polls `ListRequestsSince` every second and pushes new entries to WebSocket. This works across processes (MCP and admin can be separate).

Add a polling goroutine in `admin/server.go` `Start()`:

```go
// Poll for new MCP requests and broadcast via WebSocket
go func() {
	var lastID int64
	for {
		time.Sleep(1 * time.Second)
		entries, err := s.store.ListRequestsSince(lastID)
		if err != nil || len(entries) == 0 {
			continue
		}
		for _, e := range entries {
			s.hub.Send("mcp_request", e)
			if e.RequestID > lastID {
				lastID = e.RequestID
			}
		}
		// Also send stats update if any mutation happened
		for _, e := range entries {
			if isMutation(e.ToolName) {
				stats, _ := s.graph.QueryStats("")
				s.hub.Send("stats_update", stats)
				break
			}
		}
	}
}()
```

```go
func isMutation(toolName string) bool {
	switch toolName {
	case "oracle_import_all", "oracle_node_upsert", "oracle_edge_upsert",
		"oracle_evidence_add", "oracle_stale_mark", "oracle_snapshot_create",
		"oracle_revision_create":
		return true
	}
	return false
}
```

Add `"time"` to imports.

- [ ] **Step 4: Build and test**

```bash
go build -o oracle ./cmd/oracle
go test ./... -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/middleware.go internal/admin/server.go internal/cli/mcp.go
git commit -m "feat: bridge MCP request log to admin WebSocket via polling"
```

---

### Task 9: Final Integration + Smoke Test

**Files:**
- No new files — verify everything works end-to-end

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -count=1 -v
```

Expected: all tests pass.

- [ ] **Step 2: Build final binary**

```bash
go build -o oracle ./cmd/oracle
./oracle version
```

- [ ] **Step 3: Verify admin starts and serves content**

```bash
./oracle admin --port 4201 --db /tmp/smoke-test.db &
ADMIN_PID=$!
sleep 1

# Check API endpoints
curl -s http://localhost:4201/api/stats
curl -s http://localhost:4201/api/requests
curl -s http://localhost:4201/api/validate
curl -s http://localhost:4201/api/graph?domain=orders
curl -s http://localhost:4201/ | head -5  # Should return HTML

kill $ADMIN_PID
```

- [ ] **Step 4: Import fixture data and verify dashboard shows it**

```bash
./oracle init --db /tmp/smoke-test.db
./oracle revision create --domain orders --after-sha abc123 --trigger manual --mode full --db /tmp/smoke-test.db
./oracle import all --file fixtures/orders-domain/expected-graph.json --revision 1 --db /tmp/smoke-test.db

./oracle admin --port 4201 --db /tmp/smoke-test.db &
ADMIN_PID=$!
sleep 1

# Stats should show 18 nodes, 17 edges
curl -s http://localhost:4201/api/stats | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'nodes={d[\"graph\"][\"node_count\"]}, edges={d[\"graph\"][\"edge_count\"]}')"

# Graph should return nodes and edges
curl -s http://localhost:4201/api/graph?domain=orders | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'graph: {len(d[\"nodes\"])} nodes, {len(d[\"edges\"])} edges')"

# Low confidence should show 2+ edges
curl -s http://localhost:4201/api/low-confidence | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'low conf: {len(d)} edges')"

kill $ADMIN_PID
```

- [ ] **Step 5: Commit any fixes**

```bash
# Only if fixes needed
git add -A && git commit -m "fix: admin dashboard integration fixes"
```

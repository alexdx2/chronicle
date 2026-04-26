# Live Diagrams Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Claude push live diagrams to the dashboard — 3 new MCP tools, in-memory session store, WebSocket broadcast, path-based routing, diagram tab.

**Architecture:** MCP tools call admin server HTTP endpoints to create/update diagram sessions stored in memory. Admin server broadcasts updates via WebSocket. Dashboard reads `window.location.pathname` for routing, renders diagram tab using the existing workspace canvas renderer. Annotations (highlight + notes) rendered as colored glows and text labels on nodes.

**Tech Stack:** Go (MCP tools + admin server), Alpine.js + D3.js (dashboard), WebSocket, JSON.

---

## File Structure

- **Modify:** `internal/admin/server.go` — Add DiagramSession struct, session map, 4 new endpoints (`/api/diagram` CRUD), SPA catch-all for path routing
- **Modify:** `internal/mcp/server.go` — Add 3 tool definitions + handlers (diagram_create, diagram_update, diagram_annotate), `net/http` import for calling admin
- **Modify:** `internal/mcp/middleware.go` — Register 3 new tools in NewServerWithLogging
- **Modify:** `internal/mcp/guide.go` — Add "diagrams" section to compact guide
- **Modify:** `internal/mcp/commands.go` — Add "diagram" command instructions
- **Modify:** `internal/admin/static/index.html` — Path-based routing, diagram tab, WebSocket diagram_update handler, annotation rendering

---

### Task 1: Admin Server — DiagramSession State & Endpoints

**Files:**
- Modify: `internal/admin/server.go`

- [ ] **Step 1: Add DiagramSession types and session map to Server struct**

Add after the `Server` struct definition (after line 62):

```go
// DiagramSession holds an in-memory diagram pushed by Claude via MCP.
type DiagramSession struct {
	ID          string                    `json:"id"`
	Title       string                    `json:"title"`
	Nodes       []map[string]any          `json:"nodes"`
	Edges       []map[string]any          `json:"edges"`
	Annotations map[string]DiagramNote    `json:"annotations"`
	CreatedAt   string                    `json:"created_at"`
	UpdatedAt   string                    `json:"updated_at"`
}

// DiagramNote is an annotation on a specific node.
type DiagramNote struct {
	Note      string `json:"note,omitempty"`
	Highlight string `json:"highlight,omitempty"`
}
```

Add `diagrams` field to the Server struct:

```go
type Server struct {
	mu           sync.RWMutex
	graph        *graph.Graph
	store        *store.Store
	hub          *Hub
	port         int
	manifestPath string
	devMode      bool
	projectPath  string
	originalPath string
	diagrams     map[string]*DiagramSession // session_id → session
}
```

Initialize the map in `NewServer()` — find the line `return &Server{` and add `diagrams: make(map[string]*DiagramSession),`.

- [ ] **Step 2: Add diagram HTTP handlers**

Add these handler methods after the existing handlers (e.g., after `handleGraph`):

```go
func (s *Server) handleDiagram(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: /api/diagram/{id} or /api/diagram/{id}/annotate
	path := strings.TrimPrefix(r.URL.Path, "/api/diagram")
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 2)
	sessionID := parts[0]

	switch {
	case r.Method == "POST" && sessionID == "":
		// Create new session
		s.handleDiagramCreate(w, r)
	case r.Method == "GET" && sessionID != "":
		s.handleDiagramGet(w, r, sessionID)
	case r.Method == "PUT" && len(parts) == 2 && parts[1] == "annotate":
		s.handleDiagramAnnotate(w, r, sessionID)
	case r.Method == "PUT" && sessionID != "":
		s.handleDiagramUpdate(w, r, sessionID)
	default:
		http.Error(w, "not found", 404)
	}
}

func (s *Server) handleDiagramCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	session := &DiagramSession{
		ID:          body.SessionID,
		Title:       body.Title,
		Annotations: make(map[string]DiagramNote),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.mu.Lock()
	s.diagrams[body.SessionID] = session
	s.mu.Unlock()

	url := fmt.Sprintf("http://localhost:%d/diagram/%s", s.port, body.SessionID)
	httpJSON(w, map[string]any{"session_id": body.SessionID, "url": url})
}

func (s *Server) handleDiagramGet(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	session, ok := s.diagrams[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", 404)
		return
	}
	httpJSON(w, session)
}

func (s *Server) handleDiagramUpdate(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	session, ok := s.diagrams[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", 404)
		return
	}
	var body struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	s.mu.Lock()
	session.Nodes = body.Nodes
	session.Edges = body.Edges
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Unlock()

	// Broadcast via WebSocket
	s.hub.Send("diagram_update", map[string]any{
		"session_id":  id,
		"nodes":       session.Nodes,
		"edges":       session.Edges,
		"annotations": session.Annotations,
	})

	httpJSON(w, map[string]any{"status": "ok", "node_count": len(body.Nodes), "edge_count": len(body.Edges)})
}

func (s *Server) handleDiagramAnnotate(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	session, ok := s.diagrams[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", 404)
		return
	}
	var body struct {
		NodeKey   string `json:"node_key"`
		Note      string `json:"note"`
		Highlight string `json:"highlight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	s.mu.Lock()
	session.Annotations[body.NodeKey] = DiagramNote{Note: body.Note, Highlight: body.Highlight}
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Unlock()

	// Broadcast via WebSocket
	s.hub.Send("diagram_update", map[string]any{
		"session_id":  id,
		"nodes":       session.Nodes,
		"edges":       session.Edges,
		"annotations": session.Annotations,
	})

	httpJSON(w, map[string]any{"status": "ok"})
}
```

- [ ] **Step 3: Register the diagram endpoint and add SPA catch-all**

In `Start()`, add the diagram handler before the static file handler:

```go
mux.HandleFunc("/api/diagram/", s.handleDiagram)
mux.HandleFunc("/api/diagram", s.handleDiagram)
```

Replace the static file serving (both dev and prod branches) with an SPA catch-all that serves `index.html` for unknown paths:

```go
if s.devMode {
    staticDir := filepath.Join(findModuleRoot(), "internal", "admin", "static")
    fmt.Fprintf(os.Stderr, "Dev mode: serving static files from %s\n", staticDir)
    fileServer := http.FileServer(http.Dir(staticDir))
    mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // If the file exists, serve it. Otherwise serve index.html (SPA routing).
        path := filepath.Join(staticDir, r.URL.Path)
        if _, err := os.Stat(path); err == nil {
            fileServer.ServeHTTP(w, r)
            return
        }
        http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
    }))
} else {
    staticContent, err := fs.Sub(staticFS, "static")
    if err != nil {
        return fmt.Errorf("static fs: %w", err)
    }
    fsys := http.FS(staticContent)
    fileServer := http.FileServer(fsys)
    mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Try to open the file in the embedded FS
        f, err := staticContent.Open(strings.TrimPrefix(r.URL.Path, "/"))
        if err == nil {
            f.Close()
            fileServer.ServeHTTP(w, r)
            return
        }
        // Fallback to index.html for SPA routing
        r.URL.Path = "/"
        fileServer.ServeHTTP(w, r)
    }))
}
```

Make sure `"strings"` and `"time"` are in the imports.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/server.go
git commit -m "feat: diagram session endpoints — create, get, update, annotate + SPA routing"
```

---

### Task 2: MCP Tools — diagram_create, diagram_update, diagram_annotate

**Files:**
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/middleware.go`

- [ ] **Step 1: Add 3 tool definitions and handlers in server.go**

Add these at the end of `server.go`, before the closing of the file (after the existing `commandHandler`):

```go
// ---------------------------------------------------------------------------
// oracle_diagram_create
// ---------------------------------------------------------------------------

func diagramCreateTool() mcp.Tool {
	return mcp.NewTool("oracle_diagram_create",
		mcp.WithDescription("Create a live diagram session. Returns a URL the user can open to see the diagram. Use oracle_diagram_update to push content."),
		mcp.WithString("title", mcp.Description("Human-readable title for the diagram, e.g. 'Auth Flow' or 'Order Dependencies'")),
	)
}

func diagramCreateHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title := strParam(req.GetArguments(), "title")
		if title == "" {
			title = "Diagram"
		}
		// Generate 8-char session ID
		sessionID := fmt.Sprintf("%x", time.Now().UnixNano())[len(fmt.Sprintf("%x", time.Now().UnixNano()))-8:]

		port := adminPortValue
		if port == 0 {
			port = 4200
		}

		// Call admin server to create session
		body, _ := json.Marshal(map[string]string{"session_id": sessionID, "title": title})
		resp, err := http.Post(fmt.Sprintf("http://localhost:%d/api/diagram", port), "application/json", bytes.NewReader(body))
		if err != nil {
			return errorResult(fmt.Errorf("failed to create diagram session: %w", err)), nil
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// oracle_diagram_update
// ---------------------------------------------------------------------------

func diagramUpdateTool() mcp.Tool {
	return mcp.NewTool("oracle_diagram_update",
		mcp.WithDescription("Push graph data to a live diagram. The dashboard updates in real-time. Can be called repeatedly to evolve the diagram. Payload uses standard Oracle graph format: {nodes: [...], edges: [...]}"),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID from oracle_diagram_create")),
		mcp.WithString("payload", mcp.Required(), mcp.Description("JSON string: {\"nodes\": [{\"node_id\": 1, \"node_key\": \"...\", \"name\": \"...\", \"layer\": \"...\", \"node_type\": \"...\", ...}], \"edges\": [{\"from_node_id\": 1, \"to_node_id\": 2, \"edge_type\": \"...\", ...}]}")),
	)
}

func diagramUpdateHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID := strParam(req.GetArguments(), "session_id")
		payloadStr := strParam(req.GetArguments(), "payload")
		if sessionID == "" || payloadStr == "" {
			return errorResult(fmt.Errorf("session_id and payload are required")), nil
		}

		port := adminPortValue
		if port == 0 {
			port = 4200
		}

		url := fmt.Sprintf("http://localhost:%d/api/diagram/%s", port, sessionID)
		httpReq, _ := http.NewRequest("PUT", url, strings.NewReader(payloadStr))
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return errorResult(fmt.Errorf("failed to update diagram: %w", err)), nil
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return jsonResult(result), nil
	}
}

// ---------------------------------------------------------------------------
// oracle_diagram_annotate
// ---------------------------------------------------------------------------

func diagramAnnotateTool() mcp.Tool {
	return mcp.NewTool("oracle_diagram_annotate",
		mcp.WithDescription("Add a highlight or text note to a node in a live diagram. The node gets a colored glow and/or a text label."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID from oracle_diagram_create")),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("node_key of the node to annotate")),
		mcp.WithString("note", mcp.Description("Text note shown near the node, e.g. 'This is the bottleneck'")),
		mcp.WithString("highlight", mcp.Description("Highlight color — name or hex, e.g. 'red', '#ff6600', 'green'")),
	)
}

func diagramAnnotateHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID := strParam(req.GetArguments(), "session_id")
		nodeKey := strParam(req.GetArguments(), "node_key")
		note := strParam(req.GetArguments(), "note")
		highlight := strParam(req.GetArguments(), "highlight")
		if sessionID == "" || nodeKey == "" {
			return errorResult(fmt.Errorf("session_id and node_key are required")), nil
		}
		if note == "" && highlight == "" {
			return errorResult(fmt.Errorf("at least one of note or highlight is required")), nil
		}

		port := adminPortValue
		if port == 0 {
			port = 4200
		}

		body, _ := json.Marshal(map[string]string{"node_key": nodeKey, "note": note, "highlight": highlight})
		url := fmt.Sprintf("http://localhost:%d/api/diagram/%s/annotate", port, sessionID)
		httpReq, _ := http.NewRequest("PUT", url, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return errorResult(fmt.Errorf("failed to annotate diagram: %w", err)), nil
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return jsonResult(result), nil
	}
}
```

Make sure to add `"bytes"`, `"net/http"`, and `"time"` to the imports at the top of `server.go`.

- [ ] **Step 2: Register tools in NewServer()**

In `NewServer()` (around line 50), add:

```go
s.AddTool(diagramCreateTool(), diagramCreateHandler())
s.AddTool(diagramUpdateTool(), diagramUpdateHandler())
s.AddTool(diagramAnnotateTool(), diagramAnnotateHandler())
```

- [ ] **Step 3: Register tools in NewServerWithLogging()**

In `internal/mcp/middleware.go`, in `NewServerWithLogging()` (around line 357), add:

```go
add(diagramCreateTool(), diagramCreateHandler())
add(diagramUpdateTool(), diagramUpdateHandler())
add(diagramAnnotateTool(), diagramAnnotateHandler())
```

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/server.go internal/mcp/middleware.go
git commit -m "feat: 3 MCP tools — diagram_create, diagram_update, diagram_annotate"
```

---

### Task 3: Guide & Command Integration

**Files:**
- Modify: `internal/mcp/guide.go`
- Modify: `internal/mcp/commands.go`

- [ ] **Step 1: Add diagrams section to compact guide**

In `guide.go`, in the `ExtractionGuide()` function, find the `guide := map[string]any{` block. Before the closing `}` of the map (before the custom instructions check), add:

```go
"diagrams": map[string]any{
    "when": "When explaining architecture, dependencies, impact, or flows to the user, offer to show a live diagram",
    "how":  "Call oracle_diagram_create() to get a URL, share it, then oracle_diagram_update() with {nodes, edges} payload",
    "tips": []string{
        "Start simple — 3-5 key nodes, add detail incrementally",
        "Use oracle_diagram_annotate to highlight what you're talking about",
        "Pull nodes from oracle_node_list or oracle_query_deps results",
        "Update the diagram as the conversation evolves",
        "For custom explanatory diagrams, invent node_keys like custom:box:explain:name",
        "Use layer to control color: service=red, data=purple, code=blue, flow=pink, contract=green",
    },
},
```

- [ ] **Step 2: Add diagram command to CommandInstructions**

In `commands.go`, add a new entry to the `CommandInstructions` map:

```go
"diagram": `Live diagram for the user:
1. Ask user: "Want me to show this as a diagram?"
2. Call oracle_diagram_create(title="descriptive name") — get session_id and URL
3. Share URL with user: "Open {url} to see the diagram"
4. Query the graph to build the payload:
   - Dependency diagram: oracle_query_deps(node_key, depth=2)
   - Impact diagram: oracle_impact(node_key)
   - Path diagram: oracle_query_path(from, to)
   - Service map: oracle_node_list(layer='service') + oracle_edge_list
   - Custom: invent nodes with layer for color (service=red, data=purple, code=blue)
5. Build payload: {nodes: [{node_id: 1, node_key: "...", name: "...", layer: "...", node_type: "..."}, ...], edges: [{from_node_id: 1, to_node_id: 2, edge_type: "CALLS_SERVICE"}, ...]}
6. Call oracle_diagram_update(session_id, payload) — dashboard updates live
7. Call oracle_diagram_annotate(session_id, node_key, note="explanation", highlight="red") — highlight key nodes
8. As conversation evolves, call oracle_diagram_update again to add/remove/change nodes
Diagram types: dependency, impact, path, service map, custom (explanatory)`,
```

Also add `"diagram"` to the `UserCommands` map:

```go
"diagram": "Show a live diagram to explain architecture",
```

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/guide.go internal/mcp/commands.go
git commit -m "feat: diagram guidance in extraction guide + oracle_command('diagram')"
```

---

### Task 4: Dashboard — Path-Based Routing & Diagram Tab

**Files:**
- Modify: `internal/admin/static/index.html`

- [ ] **Step 1: Add diagram state variables**

In the Alpine.js data object (around line 952, near the other workspace state), add:

```javascript
    // Diagram (live from Claude)
    diagramSessionId: null,
    diagramData: null,  // {id, title, nodes, edges, annotations}
```

- [ ] **Step 2: Add path-based routing in init()**

Find the `async init()` method. At the very beginning (after the opening `{`), add routing logic:

```javascript
      // Path-based routing
      const path = window.location.pathname;
      const diagramMatch = path.match(/^\/diagram\/([a-zA-Z0-9]+)$/);
      if (diagramMatch) {
        this.diagramSessionId = diagramMatch[1];
        this.tab = 'diagram';
      } else if (path === '/workspace') {
        this.tab = 'workspace';
      } else if (path === '/graph') {
        this.tab = 'graph';
      } else if (path === '/language') {
        this.tab = 'language';
      } else if (path === '/settings') {
        this.tab = 'settings';
      }
```

- [ ] **Step 3: Update tab buttons to use pushState**

Find the tab bar buttons. Update them to also push the URL:

```html
<button class="tab-btn" :class="{active: tab==='overview'}" @click="tab='overview';history.pushState(null,'','/')">Overview</button>
```

Do the same for graph (`/graph`), language (`/language`), settings (`/settings`). For the workspace button, push `/workspace`.

- [ ] **Step 4: Add diagram tab button (conditional)**

After the existing tab buttons, add:

```html
<template x-if="diagramSessionId">
  <button class="tab-btn" :class="{active: tab==='diagram'}" @click="tab='diagram';history.pushState(null,'','/diagram/'+diagramSessionId)">
    Diagram<template x-if="diagramData?.title"><span x-text="': '+diagramData.title"></span></template>
  </button>
</template>
```

- [ ] **Step 5: Add diagram tab content section**

After the existing tab content sections (after the settings container), add:

```html
<!-- Diagram tab (live from Claude) -->
<div class="container" x-show="tab==='diagram'" x-cloak :style="graphFullscreen ? 'height:calc(100vh - 100px)' : ''">
  <div class="graph-container" style="min-height:500px">
    <div class="graph-toolbar">
      <span style="font-weight:600;font-size:13px" x-text="diagramData?.title || 'Diagram'"></span>
      <div style="margin-left:auto;display:flex;gap:8px;align-items:center">
        <span style="font-size:10px;color:var(--text-muted)" x-text="diagramData ? (diagramData.nodes?.length || 0) + ' nodes, ' + (diagramData.edges?.length || 0) + ' edges' : 'Waiting for data...'"></span>
        <button class="mode-btn" @click="graphFullscreen=!graphFullscreen" x-text="graphFullscreen ? '⊟' : '⊞'" style="font-size:14px;padding:2px 6px"></button>
      </div>
    </div>
    <div style="display:flex;flex:1;overflow:hidden">
      <div id="diagram-canvas" style="flex:1;overflow:auto">
        <template x-if="!diagramData">
          <div style="padding:80px;text-align:center;color:var(--text-muted)">
            <div style="font-size:18px;margin-bottom:8px">Waiting for Claude...</div>
            <div style="font-size:12px">Claude will push a diagram here via MCP tools.</div>
          </div>
        </template>
      </div>
      <!-- Side panel for selected node -->
      <div x-show="selectedNode" x-cloak
           style="width:320px;border-left:1px solid var(--border);overflow-y:auto;background:#fafbfc;flex-shrink:0;padding:12px;font-size:11px">
        <template x-if="selectedNode">
          <div>
            <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
              <strong style="font-size:13px" x-text="selectedNode.name"></strong>
              <button @click="selectedNode=null" style="border:none;background:none;cursor:pointer;color:#999;font-size:16px">&times;</button>
            </div>
            <div style="margin-bottom:6px">
              <span style="font-size:9px;padding:1px 6px;border-radius:8px;font-weight:500"
                    :style="{background: layerColor(selectedNode.layer)+'15', color: layerColor(selectedNode.layer)}"
                    x-text="selectedNode.node_type"></span>
              <span style="font-size:9px;padding:1px 6px;border-radius:8px;background:#f0f0f0;color:#666;margin-left:4px" x-text="selectedNode.layer"></span>
            </div>
            <div style="font-family:monospace;font-size:9px;color:#999;word-break:break-all" x-text="selectedNode.node_key"></div>
          </div>
        </template>
      </div>
    </div>
  </div>
</div>
```

- [ ] **Step 6: Add WebSocket handler for diagram_update**

In the `ws.onmessage` handler, after the existing `if (msg.type === 'mcp_request')` block, add:

```javascript
          if (msg.type === 'diagram_update' && msg.data?.session_id === this.diagramSessionId) {
            this.diagramData = {
              id: msg.data.session_id,
              title: this.diagramData?.title || '',
              nodes: msg.data.nodes || [],
              edges: msg.data.edges || [],
              annotations: msg.data.annotations || {}
            };
            this.$nextTick(() => this.renderDiagram());
          }
```

- [ ] **Step 7: Add fetchDiagram() and renderDiagram() methods**

Add after the existing workspace-related methods:

```javascript
    async fetchDiagram() {
      if (!this.diagramSessionId) return;
      try {
        const r = await fetch('/api/diagram/' + this.diagramSessionId);
        if (!r.ok) return;
        this.diagramData = await r.json();
        this.$nextTick(() => this.renderDiagram());
      } catch(e) { console.error('diagram fetch:', e); }
    },

    renderDiagram() {
      const container = document.getElementById('diagram-canvas');
      if (!container || !this.diagramData) return;
      container.innerHTML = '';

      const nodes = this.diagramData.nodes || [];
      const edges = this.diagramData.edges || [];
      const annotations = this.diagramData.annotations || {};
      if (nodes.length === 0) return;

      const self = this;
      const width = container.clientWidth || 900;
      const height = Math.max(500, container.clientHeight || 500);
      const groupField = 'domain_key';

      // Group by domain
      const groupMap = {};
      nodes.forEach(n => {
        const gk = n[groupField] || null;
        if (gk) { if (!groupMap[gk]) groupMap[gk] = []; groupMap[gk].push(n); }
      });
      const groupKeys = Object.keys(groupMap).sort();

      // Group centers
      const groupCenters = {};
      if (groupKeys.length > 0) {
        const cols = Math.max(2, Math.ceil(Math.sqrt(groupKeys.length)));
        const cellW = Math.max(450, width / (cols + 0.5));
        const cellH = 400;
        groupKeys.forEach((gk, i) => {
          groupCenters[gk] = { x: cellW * ((i % cols) + 0.75), y: cellH * (Math.floor(i / cols) + 0.75) };
        });
      }

      // Build sim nodes
      const simNodes = nodes.map(n => {
        const gk = n[groupField] || null;
        const center = gk && groupCenters[gk] ? groupCenters[gk] : { x: width / 2, y: height / 2 };
        return {
          ...n, id: n.node_id, _group: gk,
          x: center.x + (Math.random() - 0.5) * 100,
          y: center.y + (Math.random() - 0.5) * 100
        };
      });
      const simNodeMap = {};
      simNodes.forEach(n => { simNodeMap[n.id] = n; });

      const simEdges = edges.filter(e => simNodeMap[e.from_node_id] && simNodeMap[e.to_node_id])
        .map(e => ({ source: e.from_node_id, target: e.to_node_id, ...e }));

      // SVG
      const svg = d3.select(container).append('svg')
        .attr('class', 'graph-svg').attr('width', width).attr('height', height);
      const defs = svg.append('defs');
      defs.append('marker').attr('id', 'dia-arrow').attr('viewBox', '0 0 10 10')
        .attr('refX', 22).attr('refY', 5).attr('markerWidth', 6).attr('markerHeight', 6)
        .attr('orient', 'auto-start-reverse')
        .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('class', 'arrow-head');

      const gRoot = svg.append('g');
      svg.call(d3.zoom().scaleExtent([0.1, 4]).on('zoom', (event) => {
        gRoot.attr('transform', event.transform);
      }));

      const boxGroup = gRoot.append('g').attr('class', 'domain-boxes');

      // Edges
      const link = gRoot.selectAll('.edge-line')
        .data(simEdges).join('line')
        .attr('class', d => {
          let cls = 'edge-line';
          if (d.derivation === 'linked' || d.derivation === 'inferred') cls += ' dashed';
          return cls;
        })
        .attr('marker-end', 'url(#dia-arrow)');

      // Nodes
      const node = gRoot.selectAll('.node-g')
        .data(simNodes).join('g')
        .attr('class', 'node-g')
        .call(d3.drag()
          .on('start', (event, d) => { if (!event.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
          .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y; })
          .on('end', (event, d) => { if (!event.active) sim.alphaTarget(0); d.fx = null; d.fy = null; })
        );

      node.append('rect')
        .attr('class', 'node-rect')
        .attr('width', 100).attr('height', 40).attr('x', -50).attr('y', -20)
        .attr('fill', d => self.layerColor(d.layer) + '20')
        .attr('stroke', d => {
          const ann = annotations[d.node_key];
          return ann?.highlight || self.layerColor(d.layer);
        })
        .attr('stroke-width', d => annotations[d.node_key]?.highlight ? 3 : 1.5)
        .style('filter', d => {
          const ann = annotations[d.node_key];
          return ann?.highlight ? 'drop-shadow(0 0 6px ' + ann.highlight + ')' : 'none';
        });

      node.append('text')
        .attr('text-anchor', 'middle').attr('dy', -2).attr('font-size', '10px')
        .text(d => d.name.length > 14 ? d.name.slice(0, 13) + '\u2026' : d.name);
      node.append('text')
        .attr('text-anchor', 'middle').attr('dy', 12).attr('font-size', '7px').attr('fill', 'var(--text-muted)')
        .text(d => d.node_type);

      // Annotation notes
      node.filter(d => annotations[d.node_key]?.note).append('text')
        .attr('text-anchor', 'middle').attr('dy', 32).attr('font-size', '8px')
        .attr('fill', d => annotations[d.node_key]?.highlight || 'var(--primary)')
        .attr('font-style', 'italic')
        .text(d => {
          const n = annotations[d.node_key].note;
          return n.length > 30 ? n.slice(0, 29) + '\u2026' : n;
        });

      // Click
      node.on('click', (event, d) => { event.stopPropagation(); self.selectedNode = d; });
      svg.on('click', () => { self.selectedNode = null; });

      // Force sim
      const sim = d3.forceSimulation(simNodes)
        .force('link', d3.forceLink(simEdges).id(d => d.id).distance(140).strength(0.15))
        .force('charge', d3.forceManyBody().strength(-300))
        .force('collision', d3.forceCollide().radius(60));

      if (groupKeys.length > 0) {
        sim.force('x', d3.forceX(d => d._group && groupCenters[d._group] ? groupCenters[d._group].x : width / 2).strength(d => d._group ? 0.15 : 0.03));
        sim.force('y', d3.forceY(d => d._group && groupCenters[d._group] ? groupCenters[d._group].y : height / 2).strength(d => d._group ? 0.15 : 0.03));
      }

      sim.on('tick', () => {
        link.attr('x1', d => d.source.x).attr('y1', d => d.source.y)
          .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
        node.attr('transform', d => `translate(${d.x},${d.y})`);

        if (groupKeys.length > 0) {
          const padding = 25, labelH = 16;
          const boxData = groupKeys.map(gk => {
            const members = (groupMap[gk] || []).filter(n => simNodeMap[n.node_id || n.id]);
            if (members.length === 0) return null;
            const sn = members.map(m => simNodeMap[m.node_id || m.id]).filter(Boolean);
            if (sn.length === 0) return null;
            const minX = d3.min(sn, d => d.x) - 50 - padding;
            const maxX = d3.max(sn, d => d.x) + 50 + padding;
            const minY = d3.min(sn, d => d.y) - 20 - padding - labelH;
            const maxY = d3.max(sn, d => d.y) + 20 + padding;
            return { gk, x: minX, y: minY, w: maxX - minX, h: maxY - minY };
          }).filter(Boolean);
          const boxes = boxGroup.selectAll('.domain-g').data(boxData, d => d.gk);
          const enter = boxes.enter().append('g').attr('class', 'domain-g');
          enter.append('rect').attr('class', 'domain-box');
          enter.append('text').attr('class', 'domain-label');
          const merged = enter.merge(boxes);
          merged.select('.domain-box').attr('x', d => d.x).attr('y', d => d.y).attr('width', d => d.w).attr('height', d => d.h);
          merged.select('.domain-label').attr('x', d => d.x + 10).attr('y', d => d.y + 14).text(d => d.gk.toUpperCase());
          boxes.exit().remove();
        }
      });
    },
```

- [ ] **Step 8: Trigger fetchDiagram on init when diagram tab**

In the `init()` method, after the routing logic added in Step 2, add:

```javascript
      if (this.tab === 'diagram') {
        this.fetchDiagram();
      }
```

- [ ] **Step 9: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: diagram tab with path routing, WebSocket live updates, annotation rendering"
```

---

### Task 5: Integration Test & Polish

- [ ] **Step 1: Build and verify**

```bash
cd /home/alex/personal/depbot
go build ./...
```

Ensure no compilation errors.

- [ ] **Step 2: Manual end-to-end test**

Start the admin server and MCP server. Then test the flow:

1. Call `oracle_diagram_create(title="Test Diagram")` → get session_id + URL
2. Open the URL in browser → should show "Waiting for Claude..." message
3. Call `oracle_diagram_update(session_id, '{"nodes":[{"node_id":1,"node_key":"service:myapp:auth","name":"AuthService","layer":"service","node_type":"service"}],"edges":[]}')` → dashboard should update live
4. Call `oracle_diagram_annotate(session_id, "service:myapp:auth", note="Start here", highlight="green")` → node should glow green with "Start here" text

- [ ] **Step 3: Verify path routing works**

- Navigate to `localhost:{port}/diagram/test123` → should show diagram tab
- Navigate to `localhost:{port}/` → should show overview
- Click tabs → URL should update via pushState
- Refresh page on `/diagram/test123` → should load diagram tab (not 404)

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat: live diagrams complete — Claude pushes diagrams to dashboard in real-time"
```

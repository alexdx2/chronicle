package admin

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sync"

	dashboard "github.com/alexdx2/chronicle-core/admin"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/internal/mcp"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
	"github.com/alexdx2/chronicle-core/version"
)

// findModuleRoot walks up from the current executable (or working dir) to find go.mod.
func findModuleRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

func portFromPath(dir string) int {
	abs, _ := filepath.Abs(dir)
	h := fnv.New32a()
	h.Write([]byte(abs))
	return 4200 + int(h.Sum32()%800)
}


type DiagramNode struct {
	Key       string            `json:"key"`
	Label     string            `json:"label"`
	Kind      string            `json:"kind"`
	SourceKey string            `json:"source_key,omitempty"`
	Domain    string            `json:"domain,omitempty"`
	Layer     string            `json:"layer,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type DiagramEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

type DiagramStep struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Highlights  map[string]string `json:"highlights"`
	Notes       map[string]string `json:"notes,omitempty"`
}

type DiagramGroups struct {
	Field string `json:"field"`
}

type HideEdgeRule struct {
	From     string `json:"from"`
	To       string `json:"to"`
	EdgeType string `json:"edge_type,omitempty"`
}

type DiagramBuildRequest struct {
	Title       string                 `json:"title"`
	Nodes       []DiagramNode          `json:"nodes,omitempty"`
	Edges       []DiagramEdge          `json:"edges,omitempty"`
	NodeKeys    []string               `json:"node_keys,omitempty"`
	Groups      *DiagramGroups         `json:"groups,omitempty"`
	Steps       []DiagramStep          `json:"steps,omitempty"`
	Annotations map[string]DiagramNote `json:"annotations,omitempty"`
	HideEdges   []HideEdgeRule         `json:"hide_edges,omitempty"`
}

type DiagramSession struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Nodes       []DiagramNode          `json:"nodes"`
	Edges       []DiagramEdge          `json:"edges"`
	Groups      *DiagramGroups         `json:"groups,omitempty"`
	Annotations map[string]DiagramNote `json:"annotations,omitempty"`
	Steps       []DiagramStep          `json:"steps,omitempty"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
}

type DiagramNote struct {
	Note      string `json:"note,omitempty"`
	Highlight string `json:"highlight,omitempty"`
}

// EdgeCategory defines visual encoding for a group of edge types.
type EdgeCategory struct {
	Color string            `json:"color"`
	Types map[string]string `json:"types"` // edge_type → short label
}

// DefaultEdgeCategories is the built-in category map for edge visual encoding.
var DefaultEdgeCategories = map[string]EdgeCategory{
	"code": {
		Color: "#6b9bd2",
		Types: map[string]string{
			"IMPORTS":      "imp",
			"EXPORTS":      "exp",
			"CALLS_SYMBOL": "call",
			"DECLARES":     "decl",
			"INJECTS":      "inj",
		},
	},
	"service": {
		Color: "#7db87d",
		Types: map[string]string{
			"CALLS_ENDPOINT":    "calls",
			"CALLS_SERVICE":     "calls",
			"EXPOSES_ENDPOINT":  "exposes",
			"HANDLES_OPERATION": "handles",
		},
	},
	"async": {
		Color: "#d4915e",
		Types: map[string]string{
			"PUBLISHES_TOPIC": "pub",
			"CONSUMES_TOPIC":  "sub",
			"USES_QUEUE":      "queue",
		},
	},
	"data": {
		Color: "#a87dc2",
		Types: map[string]string{
			"READS_DB":          "reads",
			"WRITES_DB":         "writes",
			"USES_SCHEMA":       "schema",
			"RETURNS_TYPE":      "returns",
			"REGISTERS_SUBJECT": "reg",
			"REFERENCES_MODEL":  "ref",
			"USES_MODEL":        "uses",
		},
	},
	"flow": {
		Color: "#d4735e",
		Types: map[string]string{
			"TRIGGERS_FLOW": "triggers",
			"REQUIRES":      "requires",
		},
	},
	"structural": {
		Color: "#b8a898",
		Types: map[string]string{
			"CONTAINS":          "contains",
			"DEPLOYS_AS":        "deploys",
			"USES_INFRA":        "infra",
			"DEPENDS_ON":        "dep",
			"OWNED_BY":          "owns",
			"MAINTAINED_BY":     "maintains",
			"PART_OF_FLOW":      "flow",
			"SELECTS_PODS":      "selects",
			"BUILDS_ARTIFACT":   "builds",
			"DEPLOYS_RESOURCE":  "deploys",
			"DEPENDS_ON_DOMAIN": "dep",
			"PRECEDES":          "precedes",
			"EMITS_AFTER":       "emits",
			"TRIGGERS_ANALYSIS": "triggers",
			"READS_OUTPUT":      "reads",
			"ROUTES_TO":         "routes",
			"TARGETS_SERVICE":   "targets",
		},
	},
}

// mergedEdgeCategories returns DefaultEdgeCategories merged with project overrides.
func (s *Server) mergedEdgeCategories() map[string]EdgeCategory {
	result := make(map[string]EdgeCategory, len(DefaultEdgeCategories))
	for k, v := range DefaultEdgeCategories {
		copyTypes := make(map[string]string, len(v.Types))
		for tk, tv := range v.Types {
			copyTypes[tk] = tv
		}
		result[k] = EdgeCategory{Color: v.Color, Types: copyTypes}
	}

	raw, err := s.getStore().GetSetting("edge_categories")
	if err != nil || raw == "" {
		return result
	}
	var overrides map[string]EdgeCategory
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		return result
	}
	for k, v := range overrides {
		result[k] = v
	}
	return result
}

// Server is the admin dashboard HTTP server.
type Server struct {
	mu            sync.RWMutex
	graph         *graph.Graph
	store         *store.Store
	hub           *Hub
	port          int
	manifestPath  string
	devMode       bool
	projectPath   string
	originalPath  string
	diagrams      map[string]*DiagramSession
	dashboardHTML string // rendered dashboard HTML (from slots)
}

// NewServer creates a new admin Server.
func NewServer(g *graph.Graph, s *store.Store, port int, manifestPath string, devMode bool, projectDir string) *Server {
	return NewServerWithSlots(g, s, port, manifestPath, devMode, projectDir, dashboard.DashboardSlots{})
}

// NewServerWithSlots creates a server with custom dashboard slots (used by Pro).
func NewServerWithSlots(g *graph.Graph, s *store.Store, port int, manifestPath string, devMode bool, projectDir string, slots dashboard.DashboardSlots) *Server {
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	} else {
		projectDir, _ = filepath.Abs(projectDir)
	}
	srv := &Server{
		graph: g, store: s, hub: NewHub(), port: port,
		manifestPath: manifestPath, devMode: devMode,
		projectPath: projectDir, originalPath: projectDir,
		diagrams:      make(map[string]*DiagramSession),
		dashboardHTML: dashboard.RenderDashboard(slots),
	}
	srv.loadDiagramSessions()
	g.SetEventEmitter(&hubEmitter{hub: srv.hub})
	return srv
}

// hubEmitter bridges graph.EventEmitter to the WebSocket Hub.
type hubEmitter struct {
	hub *Hub
}

func (h *hubEmitter) Emit(event graph.ScanEvent) {
	h.hub.Send("scan_event", event)
}

// HandleDiagramForTest exposes the diagram handler for use by e2e tests.
func (s *Server) HandleDiagramForTest(w http.ResponseWriter, r *http.Request) {
	s.handleDiagram(w, r)
}

// loadDiagramSessions restores diagram sessions from SQLite on startup.
func (s *Server) loadDiagramSessions() {
	sessions, err := s.getStore().ListDiagramSessions()
	if err != nil {
		return
	}
	for _, row := range sessions {
		_, dataJSON, err := s.getStore().GetDiagramSession(row["session_id"])
		if err != nil {
			continue
		}
		var session DiagramSession
		if err := json.Unmarshal([]byte(dataJSON), &session); err != nil {
			continue
		}
		session.ID = row["session_id"]
		if session.Annotations == nil {
			session.Annotations = make(map[string]DiagramNote)
		}
		s.diagrams[session.ID] = &session
	}
}

// persistDiagramSession saves a diagram session to SQLite.
func (s *Server) persistDiagramSession(session *DiagramSession) {
	data, err := json.Marshal(session)
	if err != nil {
		return
	}
	s.getStore().SaveDiagramSession(session.ID, session.Title, string(data))
}

// switchTo swaps the server's backing store/graph to a different project.
// Creates .depbot/chronicle.db if the directory exists but the DB doesn't.
func (s *Server) switchTo(projectPath string) error {
	if _, err := os.Stat(projectPath); err != nil {
		return fmt.Errorf("directory not found: %s", projectPath)
	}
	dbDir := filepath.Join(projectPath, ".depbot")
	os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "chronicle.db")
	newStore, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	reg, _ := registry.LoadDefaults()
	newGraph := graph.New(newStore, reg)

	// Find manifest: prefer project root, fall back to .depbot/
	manifestPath := filepath.Join(projectPath, "chronicle.domain.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		manifestPath = filepath.Join(projectPath, ".depbot", "chronicle.domain.yaml")
	}

	s.mu.Lock()
	oldStore := s.store
	s.store = newStore
	s.graph = newGraph
	s.projectPath = projectPath
	s.manifestPath = manifestPath
	s.mu.Unlock()

	oldStore.Close()
	return nil
}

func (s *Server) getGraph() *graph.Graph {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.graph
}

func (s *Server) getStore() *store.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store
}

// domainFromManifest reads the domain key from the manifest YAML file.
// Supports both "domain: xxx" and "domains: [{key: xxx}]" formats.
// Returns empty string if not found — no fallbacks.
func (s *Server) domainFromManifest() string {
	data, err := os.ReadFile(s.manifestPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "domain:") && !strings.HasPrefix(line, "domains:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "domain:"))
			val = strings.Trim(val, "\"'")
			if val != "" {
				return val
			}
		}
		if strings.HasPrefix(line, "key:") || strings.HasPrefix(line, "- key:") {
			val := line
			val = strings.TrimPrefix(val, "- ")
			val = strings.TrimPrefix(val, "key:")
			val = strings.TrimSpace(val)
			val = strings.Trim(val, "\"'")
			if val != "" {
				return val
			}
		}
	}
	return ""
}

func (s *Server) getDomain(r *http.Request) string {
	if d := r.URL.Query().Get("domain"); d != "" {
		return d
	}
	return s.domainFromManifest()
}

// registerAPIRoutes registers all API routes on the given mux.
func (s *Server) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/api/projects/discover", s.handleDiscoverProjects)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/scan-progress", s.handleScanProgress)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/requests", s.handleRequests)
	mux.HandleFunc("/api/low-confidence", s.handleLowConfidence)
	mux.HandleFunc("/api/scans", s.handleScans)
	mux.HandleFunc("/api/validate", s.handleValidate)
	mux.HandleFunc("/api/graph/domains", s.handleGraphDomains)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/manifest", s.handleManifest)
	mux.HandleFunc("/api/settings/prompt", s.handlePromptSetting)
	mux.HandleFunc("/api/settings/default-guide", s.handleDefaultGuide)
	mux.HandleFunc("/api/settings/edge-categories", s.handleEdgeCategorySettings)
	mux.HandleFunc("/api/discoveries", s.handleDiscoveries)
	mux.HandleFunc("/api/glossary", s.handleGlossary)
	mux.HandleFunc("/api/language-check", s.handleLanguageCheck)
	mux.HandleFunc("/api/glossary/delete", s.handleDeleteTerm)
	mux.HandleFunc("/api/glossary/dismiss", s.handleDismissViolation)
	mux.HandleFunc("/api/glossary/save", s.handleSaveTerm)
	mux.HandleFunc("/api/registry", s.handleRegistry)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWebSocket(s.hub, w, r)
	})
	mux.HandleFunc("/api/diagram/", s.handleDiagram)
	mux.HandleFunc("/api/diagram", s.handleDiagram)
	mux.HandleFunc("/api/evidence", s.handleEvidence)
}

// ServeHTTP implements http.Handler for testing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	mux.ServeHTTP(w, r)
}

// Start begins serving the admin dashboard on localhost.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)

	dashboardBytes := []byte(s.dashboardHTML)

	if s.devMode {
		staticDir := filepath.Join(findModuleRoot(), "admin", "static")
		fmt.Fprintf(os.Stderr, "Dev mode: serving static files from %s\n", staticDir)
		fileServer := http.FileServer(http.Dir(staticDir))
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			path := filepath.Join(staticDir, r.URL.Path)
			if _, err := os.Stat(path); err == nil {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				fileServer.ServeHTTP(w, r)
				return
			}
			http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
		}))
	} else {
		staticContent, err := fs.Sub(dashboard.StaticFS(), "static")
		if err != nil {
			return fmt.Errorf("static fs: %w", err)
		}
		fileServer := http.FileServer(http.FS(staticContent))
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			// Serve static assets (favicon, logo) from embed
			f, err := staticContent.Open(strings.TrimPrefix(r.URL.Path, "/"))
			if err == nil {
				f.Close()
				if r.URL.Path != "/" && r.URL.Path != "/index.html" {
					fileServer.ServeHTTP(w, r)
					return
				}
			}
			// SPA: serve rendered dashboard HTML for all routes
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(dashboardBytes)
		}))
	}

	go s.hub.Run()
	go s.pollRequests()

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	fmt.Fprintf(os.Stderr, "Chronicle Admin: http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) pollRequests() {
	// Initialize lastID from existing data so we only broadcast NEW requests.
	existing, _ := s.getStore().ListRecentRequests(1)
	var lastID int64
	if len(existing) > 0 {
		lastID = existing[0].RequestID
	}
	for {
		time.Sleep(1 * time.Second)
		entries, err := s.getStore().ListRequestsSince(lastID)
		if err != nil || len(entries) == 0 {
			continue
		}
		for _, e := range entries {
			s.hub.Send("mcp_request", e)
			if e.RequestID > lastID {
				lastID = e.RequestID
			}
		}
	}
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	cwd, _ := os.Getwd()
	domain := s.domainFromManifest()
	httpJSON(w, map[string]any{
		"project_dir":   cwd,
		"domain":        domain,
		"manifest_path": s.manifestPath,
		"port":          s.port,
		"version":       version.Version,
	})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projectPath := r.URL.Query().Get("path")
	if projectPath == "" {
		s.mu.RLock()
		cur := s.projectPath
		s.mu.RUnlock()
		httpJSON(w, map[string]any{
			"current":  cur,
			"name":     filepath.Base(cur),
			"original": s.originalPath,
		})
		return
	}

	if err := s.switchTo(projectPath); err != nil {
		httpJSON(w, map[string]any{"error": err.Error()})
		return
	}

	st := s.getStore()
	nodes, _ := st.ListNodes(store.NodeFilter{})

	httpJSON(w, map[string]any{
		"name":       filepath.Base(projectPath),
		"path":       projectPath,
		"node_count": len(nodes),
		"switched":   true,
	})
}

func (s *Server) handleDiscoverProjects(w http.ResponseWriter, r *http.Request) {
	searchRoot := filepath.Dir(s.originalPath)
	grandparent := filepath.Dir(searchRoot)

	found := []map[string]string{}
	seen := map[string]bool{}
	skip := map[string]bool{"node_modules": true, ".git": true, ".cache": true, ".npm": true, ".nvm": true}

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 4 || dir == "" || dir == "/" {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name()[0] == '.' || skip[e.Name()] {
				continue
			}
			candidate := filepath.Join(dir, e.Name())
			depbotDir := filepath.Join(candidate, ".depbot")
			if _, err := os.Stat(depbotDir); err == nil {
				abs, _ := filepath.Abs(candidate)
				if !seen[abs] {
					seen[abs] = true
					found = append(found, map[string]string{
						"name": e.Name(),
						"path": abs,
					})
				}
			}
			walk(candidate, depth+1)
		}
	}

	// Also check the root itself
	if _, err := os.Stat(filepath.Join(searchRoot, ".depbot")); err == nil {
		abs, _ := filepath.Abs(searchRoot)
		seen[abs] = true
		found = append(found, map[string]string{
			"name": filepath.Base(searchRoot),
			"path": abs,
		})
	}

	walk(searchRoot, 0)
	if grandparent != searchRoot {
		if _, err := os.Stat(filepath.Join(grandparent, ".depbot")); err == nil {
			abs, _ := filepath.Abs(grandparent)
			if !seen[abs] {
				seen[abs] = true
				found = append(found, map[string]string{
					"name": filepath.Base(grandparent),
					"path": abs,
				})
			}
		}
		walk(grandparent, 0)
	}

	httpJSON(w, found)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	domain := s.getDomain(r)
	stats, err := s.getGraph().QueryStats(domain)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	reqStats, _ := s.getStore().RequestStats()
	httpJSON(w, map[string]any{"domain": domain, "graph": stats, "requests": reqStats})
}

func (s *Server) handleScanProgress(w http.ResponseWriter, r *http.Request) {
	domain := s.getDomain(r)
	progress, err := s.getStore().GetScanProgress(domain)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, progress)
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	if sinceStr != "" {
		sinceID, err := strconv.ParseInt(sinceStr, 10, 64)
		if err != nil {
			httpError(w, err, 400)
			return
		}
		entries, err := s.getStore().ListRequestsSince(sinceID)
		if err != nil {
			httpError(w, err, 500)
			return
		}
		httpJSON(w, entries)
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n > 0 {
			offset = n
		}
	}
	entries, err := s.getStore().ListRecentRequestsOffset(limit, offset)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, entries)
}

func (s *Server) handleLowConfidence(w http.ResponseWriter, r *http.Request) {
	threshold := 0.80
	if t := r.URL.Query().Get("threshold"); t != "" {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			threshold = v
		}
	}
	edges, err := s.getStore().ListEdges(store.EdgeFilter{})
	if err != nil {
		httpError(w, err, 500)
		return
	}
	var result []map[string]any
	for _, e := range edges {
		if e.Confidence < threshold && e.Active {
			fromName, toName := "", ""
			if n, _ := s.getStore().GetNodeByID(e.FromNodeID); n != nil {
				fromName = n.Name
			}
			if n, _ := s.getStore().GetNodeByID(e.ToNodeID); n != nil {
				toName = n.Name
			}
			result = append(result, map[string]any{
				"edge_key": e.EdgeKey, "from_name": fromName, "to_name": toName,
				"edge_type": e.EdgeType, "derivation": e.DerivationKind, "confidence": e.Confidence,
			})
		}
	}
	httpJSON(w, result)
}

func (s *Server) handleScans(w http.ResponseWriter, r *http.Request) {
	domain := s.getDomain(r)
	snaps, _ := s.getStore().ListSnapshots(domain)
	httpJSON(w, snaps)
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var issues []map[string]string
	nodes, _ := s.getStore().ListNodes(store.NodeFilter{})
	edges, _ := s.getStore().ListEdges(store.EdgeFilter{})
	for _, n := range nodes {
		if _, err := validate.NormalizeNodeKey(n.NodeKey); err != nil {
			issues = append(issues, map[string]string{"kind": "malformed_key", "target": n.NodeKey, "message": err.Error()})
		}
		if n.Confidence < 0 || n.Confidence > 1 {
			issues = append(issues, map[string]string{"kind": "confidence_range", "target": n.NodeKey, "message": fmt.Sprintf("confidence %f out of [0,1]", n.Confidence)})
		}
	}
	for _, e := range edges {
		if _, err := validate.NormalizeEdgeKey(e.EdgeKey); err != nil {
			issues = append(issues, map[string]string{"kind": "malformed_key", "target": e.EdgeKey, "message": err.Error()})
		}
	}
	httpJSON(w, map[string]any{"issues": issues, "nodes_checked": len(nodes), "edges_checked": len(edges), "valid": len(issues) == 0})
}

func buildHierarchy(nodeList []map[string]any, edgeList []map[string]any) map[string]any {
	type hlink struct {
		ParentID   string  `json:"parentId"`
		ChildID    string  `json:"childId"`
		Kind       string  `json:"kind"`
		Reason     string  `json:"reason"`
		Confidence float64 `json:"confidence"`
	}

	nodeByID := map[string]map[string]any{}
	for _, n := range nodeList {
		id := fmt.Sprintf("%v", n["node_id"])
		nodeByID[id] = n
	}

	rootTypes := map[string]bool{
		"container": true, "external_system": true,
		"broker": true, "database": true, "cache": true,
		"queue": true, "infrastructure": true,
	}

	var roots []string
	for _, n := range nodeList {
		nt, _ := n["node_type"].(string)
		if rootTypes[nt] {
			roots = append(roots, fmt.Sprintf("%v", n["node_id"]))
		}
	}

	links := []hlink{}
	placed := map[string]bool{}

	// 1. Real CONTAINS edges from DB
	for _, e := range edgeList {
		et, _ := e["edge_type"].(string)
		if et != "CONTAINS" { continue }
		deriv, _ := e["derivation"].(string)
		if deriv == "rollup" || deriv == "virtual" { continue }
		fromID := fmt.Sprintf("%v", e["from_node_id"])
		toID := fmt.Sprintf("%v", e["to_node_id"])
		if placed[toID] { continue }
		placed[toID] = true
		links = append(links, hlink{
			ParentID: fromID, ChildID: toID,
			Kind: "real", Reason: "db_contains", Confidence: 1.0,
		})
	}

	// 2. container → service/external_system by domain_key
	containerByDomain := map[string]string{}
	for _, n := range nodeList {
		nt, _ := n["node_type"].(string)
		if nt != "container" { continue }
		dk, _ := n["domain_key"].(string)
		if dk != "" { containerByDomain[dk] = fmt.Sprintf("%v", n["node_id"]) }
	}
	for _, n := range nodeList {
		id := fmt.Sprintf("%v", n["node_id"])
		if placed[id] { continue }
		nt, _ := n["node_type"].(string)
		if nt != "service" && nt != "external_system" { continue }
		dk, _ := n["domain_key"].(string)
		cID, ok := containerByDomain[dk]
		if !ok { continue }
		placed[id] = true
		links = append(links, hlink{
			ParentID: cID, ChildID: id,
			Kind: "virtual", Reason: "domain_key", Confidence: 0.9,
		})
	}

	// 3. service → module by domain_key + name prefix
	serviceByDomain := map[string][]map[string]any{}
	for _, n := range nodeList {
		nt, _ := n["node_type"].(string)
		if nt != "service" { continue }
		dk, _ := n["domain_key"].(string)
		serviceByDomain[dk] = append(serviceByDomain[dk], n)
	}
	for _, n := range nodeList {
		id := fmt.Sprintf("%v", n["node_id"])
		if placed[id] { continue }
		nt, _ := n["node_type"].(string)
		if nt != "module" { continue }
		dk, _ := n["domain_key"].(string)
		name, _ := n["name"].(string)
		modPrefix := strings.TrimSuffix(name, ".module")
		for _, svc := range serviceByDomain[dk] {
			svcName, _ := svc["name"].(string)
			svcPrefix := strings.TrimSuffix(svcName, "-api")
			if modPrefix == svcPrefix || modPrefix+"s" == svcPrefix || svcPrefix+"s" == modPrefix {
				placed[id] = true
				links = append(links, hlink{
					ParentID: fmt.Sprintf("%v", svc["node_id"]), ChildID: id,
					Kind: "virtual", Reason: "name_prefix", Confidence: 0.8,
				})
				break
			}
		}
	}

	// 4. controller/provider → endpoint from EXPOSES_ENDPOINT edges
	for _, e := range edgeList {
		et, _ := e["edge_type"].(string)
		if et != "EXPOSES_ENDPOINT" { continue }
		toID := fmt.Sprintf("%v", e["to_node_id"])
		if placed[toID] { continue }
		fromID := fmt.Sprintf("%v", e["from_node_id"])
		placed[toID] = true
		links = append(links, hlink{
			ParentID: fromID, ChildID: toID,
			Kind: "virtual", Reason: "exposes_endpoint", Confidence: 1.0,
		})
	}

	// 5. provider → topic from PUBLISHES_TOPIC (only if single publisher)
	topicPublishers := map[string][]string{}
	for _, e := range edgeList {
		et, _ := e["edge_type"].(string)
		if et != "PUBLISHES_TOPIC" { continue }
		toID := fmt.Sprintf("%v", e["to_node_id"])
		fromID := fmt.Sprintf("%v", e["from_node_id"])
		topicPublishers[toID] = append(topicPublishers[toID], fromID)
	}
	for topicID, publishers := range topicPublishers {
		if placed[topicID] { continue }
		if len(publishers) != 1 { continue }
		placed[topicID] = true
		links = append(links, hlink{
			ParentID: publishers[0], ChildID: topicID,
			Kind: "virtual", Reason: "topic_edge", Confidence: 0.8,
		})
	}

	// Fallback: if no roots found (no containers), promote services/external/infra
	if len(roots) == 0 {
		fallbackTypes := map[string]bool{"service": true, "external_system": true, "broker": true, "database": true, "cache": true, "queue": true, "infrastructure": true}
		for _, n := range nodeList {
			nt, _ := n["node_type"].(string)
			if fallbackTypes[nt] {
				roots = append(roots, fmt.Sprintf("%v", n["node_id"]))
			}
		}
	}

	// Remove placed nodes from roots (they're children now)
	filteredRoots := []string{}
	for _, r := range roots {
		if !placed[r] {
			filteredRoots = append(filteredRoots, r)
		}
	}

	_ = nodeByID
	return map[string]any{
		"roots": filteredRoots,
		"links": links,
	}
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	domain := s.getDomain(r)
	nodes, _ := s.getStore().ListNodes(store.NodeFilter{Domain: domain})
	edges, _ := s.getStore().ListEdges(store.EdgeFilter{})

	nodeIDSet := map[int64]bool{}
	var nodeList []map[string]any
	for _, n := range nodes {
		nodeIDSet[n.NodeID] = true
		nodeList = append(nodeList, map[string]any{
			"node_id": n.NodeID, "node_key": n.NodeKey, "layer": n.Layer,
			"node_type": n.NodeType, "domain_key": n.DomainKey, "name": n.Name,
			"repo_name": n.RepoName, "file_path": n.FilePath, "status": n.Status,
			"confidence": n.Confidence,
		})
	}

	// Inject manifest domains and infra as virtual nodes for C1/C2 zoom views.
	// These come directly from the manifest file, not the graph DB.
	if m, err := manifest.LoadFile(s.manifestPath); err == nil {
		virtualID := int64(-1)
		virtualEdgeID := int64(-1)
		containerIDs := map[string]int64{}

		// Domain stats from graph
		domainStats := map[string]struct{ nodes, endpoints, modules, models int }{}
		for _, n := range nodes {
			dk := n.DomainKey
			if dk == "" { continue }
			st := domainStats[dk]
			st.nodes++
			switch n.NodeType {
			case "endpoint": st.endpoints++
			case "module", "controller", "provider": st.modules++
			case "model", "enum": st.models++
			}
			domainStats[dk] = st
		}

		// Find the actual graph domain_key for each manifest domain.
		// Manifest may say "tom-and-jerry" but graph uses "tomandjerry".
		manifestToGraphDK := map[string]string{}
		graphDomainKeys := map[string]bool{}
		for _, n := range nodes { if n.DomainKey != "" { graphDomainKeys[n.DomainKey] = true } }
		for _, d := range m.Domains {
			if graphDomainKeys[d.Name] {
				manifestToGraphDK[d.Name] = d.Name
			} else if len(graphDomainKeys) == 1 {
				// Single domain in graph — map manifest domain to it
				for dk := range graphDomainKeys { manifestToGraphDK[d.Name] = dk }
			}
		}

		for _, d := range m.Domains {
			graphDK := manifestToGraphDK[d.Name]
			if graphDK == "" { graphDK = d.Name }
			st := domainStats[graphDK]
			desc := d.Description
			if desc == "" {
				desc = fmt.Sprintf("%d nodes, %d endpoints, %d components, %d models", st.nodes, st.endpoints, st.modules, st.models)
			}
			nodeList = append(nodeList, map[string]any{
				"node_id": virtualID, "node_key": "service:container:" + d.Name,
				"layer": "service", "node_type": "container",
				"domain_key": graphDK, "name": d.Name,
				"repo_name": "", "file_path": "", "status": "active",
				"confidence": 1.0, "description": desc,
			})
			nodeIDSet[virtualID] = true
			containerIDs[graphDK] = virtualID
			virtualID--
		}

		// Ensure all graph domain_keys have a container mapping
		for dk := range graphDomainKeys {
			if _, ok := containerIDs[dk]; ok { continue }
			if len(containerIDs) == 1 {
				for _, cID := range containerIDs { containerIDs[dk] = cID }
			}
		}

		// Virtual edges: container→module CONTAINS + cross-domain
		var edgeList []map[string]any
		for _, n := range nodes {
			if n.NodeType != "module" { continue }
			cID, ok := containerIDs[n.DomainKey]
			if !ok { continue }
			edgeList = append(edgeList, map[string]any{
				"edge_id": virtualEdgeID, "edge_key": "service:container:" + n.DomainKey + "->" + n.NodeKey + ":CONTAINS",
				"from_node_id": cID, "to_node_id": n.NodeID,
				"edge_type": "CONTAINS", "derivation": "virtual",
				"confidence": 1.0, "active": true,
			})
			virtualEdgeID--
		}

		// Cross-domain edges between containers (from real graph edges)
		nodeDomain := map[int64]string{}
		for _, n := range nodes { nodeDomain[n.NodeID] = n.DomainKey }
		domainEdgeTypes := map[string]map[string]bool{}
		for _, e := range edges {
			fromDK, toDK := nodeDomain[e.FromNodeID], nodeDomain[e.ToNodeID]
			if fromDK != "" && toDK != "" && fromDK != toDK {
				key := fromDK + "→" + toDK
				if domainEdgeTypes[key] == nil { domainEdgeTypes[key] = map[string]bool{} }
				domainEdgeTypes[key][e.EdgeType] = true
			}
		}
		for key, types := range domainEdgeTypes {
			parts := strings.SplitN(key, "→", 2)
			fromCID, ok1 := containerIDs[parts[0]]
			toCID, ok2 := containerIDs[parts[1]]
			if !ok1 || !ok2 { continue }
			et := "DEPENDS_ON"
			for _, t := range []string{"CALLS_SERVICE", "CALLS_ENDPOINT", "PUBLISHES_TOPIC", "CONSUMES_TOPIC"} {
				if types[t] { et = t; break }
			}
			edgeList = append(edgeList, map[string]any{
				"edge_id": virtualEdgeID,
				"edge_key": "service:container:" + parts[0] + "->service:container:" + parts[1] + ":" + et,
				"from_node_id": fromCID, "to_node_id": toCID,
				"edge_type": et, "derivation": "hard",
				"confidence": 1.0, "active": true,
			})
			virtualEdgeID--
		}

		// Infra edges: backend containers → infra nodes (from manifest)
		infraIDs := map[string]int64{}
		for _, n := range nodes {
			if n.Layer == "infra" {
				infraIDs[n.NodeKey] = n.NodeID
			}
		}
		// Backend domains that use infra (have controllers — unambiguous server-side marker)
		backendDomains := map[string]bool{}
		for _, n := range nodes {
			if n.NodeType == "controller" {
				backendDomains[n.DomainKey] = true
			}
		}
		for _, infra := range m.Infrastructure {
			infraID, ok := infraIDs[infra.InfraNodeKey()]
			if !ok { continue }
			for dk := range backendDomains {
				cID, ok2 := containerIDs[dk]
				if !ok2 { continue }
				et := "USES_INFRA"
				edgeList = append(edgeList, map[string]any{
					"edge_id": virtualEdgeID,
					"edge_key": "service:container:" + dk + "->" + infra.InfraNodeKey() + ":" + et,
					"from_node_id": cID, "to_node_id": infraID,
					"edge_type": et, "derivation": "hard",
					"confidence": 1.0, "active": true,
				})
				virtualEdgeID--
			}
		}

		// Frontend→Backend edges: non-backend containers → first backend container
		// (architecturally, frontends call the API backend)
		var primaryBackend int64
		for dk := range backendDomains {
			if cID, ok := containerIDs[dk]; ok {
				primaryBackend = cID
				break
			}
		}
		if primaryBackend != 0 {
			for dk := range containerIDs {
				if backendDomains[dk] { continue }
				fromCID := containerIDs[dk]
				edgeList = append(edgeList, map[string]any{
					"edge_id": virtualEdgeID,
					"edge_key": "service:container:" + dk + "->service:container:api:CALLS_ENDPOINT",
					"from_node_id": fromCID, "to_node_id": primaryBackend,
					"edge_type": "CALLS_ENDPOINT", "derivation": "hard",
					"confidence": 0.8, "active": true,
				})
				virtualEdgeID--
			}
		}

		// --- Rollup / derived edges for C1/C2 projection ---
		// Trace code-level edges up to their owning service via CONTAINS.
		// e.g. provider→endpoint becomes service→endpoint (EXPOSES/CALLS_ENDPOINT)
		//      provider→topic becomes service→topic (PUBLISHES/CONSUMES_TOPIC)
		//      provider→provider across services becomes service→service (CALLS_SERVICE)

		// Build ownership map: nodeID → service nodeID (via CONTAINS chain)
		nodeByID := map[int64]store.NodeRow{}
		for _, n := range nodes { nodeByID[n.NodeID] = n }

		// CONTAINS: module→controller/provider. Service→module doesn't exist in DB,
		// so we match module to service by domain_key + name convention.
		moduleToService := map[int64]int64{} // module nodeID → service nodeID
		serviceByDomain := map[string][]store.NodeRow{}
		for _, n := range nodes {
			if n.Layer == "service" && (n.NodeType == "service" || n.NodeType == "external_system") {
				serviceByDomain[n.DomainKey] = append(serviceByDomain[n.DomainKey], n)
			}
		}
		for _, n := range nodes {
			if n.NodeType != "module" { continue }
			// Match module to service: arena.module → arena-api, tom.module → tom-api
			modPrefix := strings.TrimSuffix(n.Name, ".module")
			for _, svc := range serviceByDomain[n.DomainKey] {
				svcPrefix := strings.TrimSuffix(svc.Name, "-api")
				if modPrefix == svcPrefix {
					moduleToService[n.NodeID] = svc.NodeID
					break
				}
			}
		}

		// Code node → owning service (via CONTAINS to module, then module→service)
		codeToService := map[int64]int64{}
		for _, e := range edges {
			if e.EdgeType != "CONTAINS" { continue }
			parentNode := nodeByID[e.FromNodeID]
			if parentNode.NodeType != "module" { continue }
			svcID, ok := moduleToService[e.FromNodeID]
			if !ok { continue }
			codeToService[e.ToNodeID] = svcID
			codeToService[e.FromNodeID] = svcID // module itself belongs to service
		}

		// Fallback 1: if a code node shares a name with a mapped node, use the same service.
		// Handles duplicates like stats.controller (controller, id=21) vs stats.controller (provider, id=50).
		nameToService := map[string]int64{}
		for id, svcID := range codeToService {
			n := nodeByID[id]
			if n.Name != "" { nameToService[n.Name] = svcID }
		}
		for _, n := range nodes {
			if n.Layer != "code" { continue }
			if _, ok := codeToService[n.NodeID]; ok { continue }
			if svcID, ok := nameToService[n.Name]; ok {
				codeToService[n.NodeID] = svcID
			}
		}

		// Fallback 2: orphan code nodes — match by name prefix to service name.
		for _, n := range nodes {
			if n.Layer != "code" { continue }
			if _, ok := codeToService[n.NodeID]; ok { continue }
			svcs := serviceByDomain[n.DomainKey]
			if len(svcs) == 0 { continue }
			if len(svcs) == 1 {
				codeToService[n.NodeID] = svcs[0].NodeID
				continue
			}
			codeName := strings.Split(n.Name, ".")[0]
			for _, svc := range svcs {
				svcPrefix := strings.TrimSuffix(svc.Name, "-api")
				if codeName == svcPrefix || codeName+"s" == svcPrefix || svcPrefix+"s" == codeName {
					codeToService[n.NodeID] = svc.NodeID
					break
				}
			}
		}

		// Fallback 3: still unmapped nodes — check INJECTS edges to find what module they belong to.
		// If node A INJECTS node B, and B is mapped, A likely belongs to the same service.
		for changed := true; changed; {
			changed = false
			for _, e := range edges {
				if e.EdgeType != "INJECTS" { continue }
				_, fromMapped := codeToService[e.FromNodeID]
				_, toMapped := codeToService[e.ToNodeID]
				if fromMapped && !toMapped {
					codeToService[e.ToNodeID] = codeToService[e.FromNodeID]
					changed = true
				} else if !fromMapped && toMapped {
					codeToService[e.FromNodeID] = codeToService[e.ToNodeID]
					changed = true
				}
			}
		}

		// Build name→serviceID map for resolving provider targets that shadow service names
		// e.g. provider "tom-api" (code) → service "tom-api" (service layer)
		serviceByName := map[string]int64{}
		for _, n := range nodes {
			if n.Layer == "service" && (n.NodeType == "service" || n.NodeType == "external_system") {
				serviceByName[n.Name] = n.NodeID
			}
		}

		// Generate rollup edges
		rollupSeen := map[string]bool{}
		rollupEdgeTypes := map[string]bool{
			"EXPOSES_ENDPOINT": true, "CALLS_ENDPOINT": true,
			"PUBLISHES_TOPIC": true, "CONSUMES_TOPIC": true,
			"CALLS_SERVICE": true,
		}
		for _, e := range edges {
			if !rollupEdgeTypes[e.EdgeType] { continue }
			fromSvc, fromHas := codeToService[e.FromNodeID]
			if !fromHas { continue }

			var toID int64
			var toNode store.NodeRow
			toNode = nodeByID[e.ToNodeID]

			switch e.EdgeType {
			case "EXPOSES_ENDPOINT", "CALLS_ENDPOINT", "PUBLISHES_TOPIC", "CONSUMES_TOPIC":
				// code→contract: rollup to service→contract
				toID = e.ToNodeID
			case "CALLS_SERVICE":
				// code→code/service: rollup to service→service
				if toNode.Layer == "service" {
					// Direct call to service-layer node (e.g. external_system)
					toID = e.ToNodeID
				} else if svcID, ok := serviceByName[toNode.Name]; ok {
					// Provider shadows a service name (e.g. provider "jerry-api" → service "jerry-api")
					if fromSvc == svcID { continue } // skip internal
					toID = svcID
				} else if toSvc, ok := codeToService[e.ToNodeID]; ok {
					if fromSvc == toSvc { continue } // skip internal
					toID = toSvc
				} else {
					continue
				}
			default:
				continue
			}

			if fromSvc == toID { continue } // skip self-loops
			key := fmt.Sprintf("%d->%d:%s", fromSvc, toID, e.EdgeType)
			if rollupSeen[key] { continue }
			rollupSeen[key] = true

			edgeList = append(edgeList, map[string]any{
				"edge_id": virtualEdgeID,
				"edge_key": key,
				"from_node_id": fromSvc, "to_node_id": toID,
				"edge_type": e.EdgeType, "derivation": "rollup",
				"confidence": e.Confidence, "active": true,
				"virtual": true, "derived": true,
			})
			virtualEdgeID--
		}

		// Service→infra rollup: if a domain's code uses infra, its services should connect
		infraNodes := map[int64]bool{}
		for _, n := range nodes {
			if n.Layer == "infra" { infraNodes[n.NodeID] = true }
		}
		// For each service, check if any code in that service connects to infra
		svcInfraEdges := map[string]bool{}
		for codeID, svcID := range codeToService {
			neighbors := map[int64]bool{}
			for _, e := range edges {
				if e.FromNodeID == codeID && infraNodes[e.ToNodeID] { neighbors[e.ToNodeID] = true }
				if e.ToNodeID == codeID && infraNodes[e.FromNodeID] { neighbors[e.FromNodeID] = true }
			}
			for infraID := range neighbors {
				key := fmt.Sprintf("%d->%d:USES_INFRA", svcID, infraID)
				if svcInfraEdges[key] || rollupSeen[key] { continue }
				svcInfraEdges[key] = true
				edgeList = append(edgeList, map[string]any{
					"edge_id": virtualEdgeID, "edge_key": key,
					"from_node_id": svcID, "to_node_id": infraID,
					"edge_type": "USES_INFRA", "derivation": "rollup",
					"confidence": 0.9, "active": true,
					"virtual": true, "derived": true,
				})
				virtualEdgeID--
			}
		}
		// Also: all backend services use shared infra from manifest
		for _, infra := range m.Infrastructure {
			infraID, ok := infraIDs[infra.InfraNodeKey()]
			if !ok { continue }
			for dk := range backendDomains {
				for _, svc := range serviceByDomain[dk] {
					key := fmt.Sprintf("%d->%d:USES_INFRA", svc.NodeID, infraID)
					if svcInfraEdges[key] || rollupSeen[key] { continue }
					svcInfraEdges[key] = true
					edgeList = append(edgeList, map[string]any{
						"edge_id": virtualEdgeID, "edge_key": key,
						"from_node_id": svc.NodeID, "to_node_id": infraID,
						"edge_type": "USES_INFRA", "derivation": "rollup",
						"confidence": 0.8, "active": true,
						"virtual": true, "derived": true,
					})
					virtualEdgeID--
				}
			}
		}

		// Container-level rollup for C1: if any service in a domain connects to
		// an external_system or infra node, create container→target edge
		serviceToContainer := map[int64]int64{} // serviceNodeID → containerNodeID
		for _, n := range nodes {
			if n.Layer == "service" && n.NodeType == "service" {
				if cID, ok := containerIDs[n.DomainKey]; ok {
					serviceToContainer[n.NodeID] = cID
				}
			}
		}
		// Also map code nodes to containers via codeToService→serviceToContainer
		for codeID, svcID := range codeToService {
			if cID, ok := serviceToContainer[svcID]; ok {
				_ = cID
				_ = codeID
			}
		}

		// Scan rollup edges + real edges for service→external/infra connections
		containerRollupSeen := map[string]bool{}
		for _, e := range edgeList {
			fromID, _ := e["from_node_id"].(int64)
			toID, _ := e["to_node_id"].(int64)
			if fromID == 0 || toID == 0 { continue }
			cID, fromIsService := serviceToContainer[fromID]
			if !fromIsService { continue }
			if cID == toID { continue } // skip self-loops
			toNode := nodeByID[toID]
			if toNode.NodeType != "external_system" { continue }
			et := "CALLS_SERVICE"
			key := fmt.Sprintf("%d->%d:%s", cID, toID, et)
			if containerRollupSeen[key] { continue }
			containerRollupSeen[key] = true
			edgeList = append(edgeList, map[string]any{
				"edge_id": virtualEdgeID, "edge_key": key,
				"from_node_id": cID, "to_node_id": toID,
				"edge_type": et, "derivation": "rollup",
				"confidence": 0.9, "active": true,
				"virtual": true, "derived": true,
			})
			virtualEdgeID--
		}
		// Also check raw edges: code→external_system (for C1 container→external)
		for _, e := range edges {
			if e.EdgeType != "CALLS_SERVICE" { continue }
			toNode := nodeByID[e.ToNodeID]
			if toNode.NodeType != "external_system" { continue }
			fromNode := nodeByID[e.FromNodeID]
			// Find container by service mapping or direct domain
			var cID int64
			if svcID, ok := codeToService[e.FromNodeID]; ok {
				if c, ok2 := serviceToContainer[svcID]; ok2 {
					cID = c
				}
			}
			if cID == 0 {
				if c, ok := containerIDs[fromNode.DomainKey]; ok {
					cID = c
				}
			}
			if cID == 0 { continue }
			et := "CALLS_SERVICE"
			key := fmt.Sprintf("%d->%d:%s", cID, e.ToNodeID, et)
			if containerRollupSeen[key] { continue }
			containerRollupSeen[key] = true
			edgeList = append(edgeList, map[string]any{
				"edge_id": virtualEdgeID, "edge_key": key,
				"from_node_id": cID, "to_node_id": e.ToNodeID,
				"edge_type": et, "derivation": "rollup",
				"confidence": e.Confidence, "active": true,
				"virtual": true, "derived": true,
			})
			virtualEdgeID--
		}

		// Append real edges
		for _, e := range edges {
			if nodeIDSet[e.FromNodeID] || nodeIDSet[e.ToNodeID] {
				edgeList = append(edgeList, map[string]any{
					"edge_id": e.EdgeID, "edge_key": e.EdgeKey, "from_node_id": e.FromNodeID,
					"to_node_id": e.ToNodeID, "edge_type": e.EdgeType, "derivation": e.DerivationKind,
					"confidence": e.Confidence, "active": e.Active,
				})
			}
		}
		hierarchy := buildHierarchy(nodeList, edgeList)
		httpJSON(w, map[string]any{"nodes": nodeList, "edges": edgeList, "edgeCategories": s.mergedEdgeCategories(), "hierarchy": hierarchy})
		return
	}

	// Fallback: no manifest — just return graph as-is
	var edgeList []map[string]any
	for _, e := range edges {
		if nodeIDSet[e.FromNodeID] || nodeIDSet[e.ToNodeID] {
			edgeList = append(edgeList, map[string]any{
				"edge_id": e.EdgeID, "edge_key": e.EdgeKey, "from_node_id": e.FromNodeID,
				"to_node_id": e.ToNodeID, "edge_type": e.EdgeType, "derivation": e.DerivationKind,
				"confidence": e.Confidence, "active": e.Active,
			})
		}
	}
	hierarchy := buildHierarchy(nodeList, edgeList)
	httpJSON(w, map[string]any{"nodes": nodeList, "edges": edgeList, "edgeCategories": s.mergedEdgeCategories(), "hierarchy": hierarchy})
}

func (s *Server) handleGraphDomains(w http.ResponseWriter, r *http.Request) {
	nodes, _ := s.getStore().ListNodes(store.NodeFilter{})

	type domainInfo struct {
		DomainKey string         `json:"domain_key"`
		NodeCount int            `json:"node_count"`
		ByLayer   map[string]int `json:"by_layer"`
		ByType    map[string]int `json:"by_type"`
	}

	dm := map[string]*domainInfo{}
	for _, n := range nodes {
		dk := n.DomainKey
		if dk == "" {
			dk = "_unassigned"
		}
		d, ok := dm[dk]
		if !ok {
			d = &domainInfo{DomainKey: dk, ByLayer: map[string]int{}, ByType: map[string]int{}}
			dm[dk] = d
		}
		d.NodeCount++
		d.ByLayer[n.Layer]++
		d.ByType[n.NodeType]++
	}

	var out []domainInfo
	for _, d := range dm {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeCount > out[j].NodeCount })
	httpJSON(w, map[string]any{"domains": out})
}

func (s *Server) handleEdgeCategorySettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		httpJSON(w, s.mergedEdgeCategories())
		return
	}
	if r.Method == "PUT" || r.Method == "POST" {
		var cats map[string]EdgeCategory
		if err := json.NewDecoder(r.Body).Decode(&cats); err != nil {
			httpError(w, err, 400)
			return
		}
		data, _ := json.Marshal(cats)
		s.getStore().SetSetting("edge_categories", string(data))
		httpJSON(w, s.mergedEdgeCategories())
		return
	}
	if r.Method == "DELETE" {
		s.getStore().SetSetting("edge_categories", "")
		httpJSON(w, s.mergedEdgeCategories())
		return
	}
	http.Error(w, "method not allowed", 405)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics, err := s.getStore().GetScanMetrics()
	if err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, metrics)
}

func (s *Server) handleDiscoveries(w http.ResponseWriter, r *http.Request) {
	domain := s.getDomain(r)
	category := r.URL.Query().Get("category")
	discoveries, err := s.getStore().ListDiscoveries(domain, category)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	if discoveries == nil {
		discoveries = []store.Discovery{}
	}
	httpJSON(w, discoveries)
}

func (s *Server) handleGlossary(w http.ResponseWriter, r *http.Request) {
	domain := s.getDomain(r)
	terms, err := s.getStore().GetGlossary(domain)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	if terms == nil {
		terms = []store.DomainTerm{}
	}
	httpJSON(w, terms)
}

func (s *Server) handleLanguageCheck(w http.ResponseWriter, r *http.Request) {
	domain := s.getDomain(r)
	violations, err := s.getStore().CheckLanguage(domain)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	if violations == nil {
		violations = []store.LanguageViolation{}
	}
	httpJSON(w, map[string]any{"violations": violations, "total": len(violations)})
}

func (s *Server) handleDeleteTerm(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "DELETE" {
		w.WriteHeader(405)
		return
	}
	domain := s.getDomain(r)
	term := r.URL.Query().Get("term")
	if term == "" {
		httpError(w, fmt.Errorf("term parameter required"), 400)
		return
	}
	if err := s.getStore().DeleteTerm(domain, term); err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, map[string]string{"status": "deleted", "term": term})
}

func (s *Server) handleDismissViolation(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	domain := s.getDomain(r)
	term := r.URL.Query().Get("term")
	anti := r.URL.Query().Get("anti")
	if term == "" || anti == "" {
		httpError(w, fmt.Errorf("term and anti parameters required"), 400)
		return
	}
	if err := s.getStore().RemoveAntiPattern(domain, term, anti); err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, map[string]string{"status": "dismissed", "term": term, "removed_anti_pattern": anti})
}

func (s *Server) handleSaveTerm(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Term         string `json:"term"`
		Description  string `json:"description"`
		Context      string `json:"context"`
		Aliases      string `json:"aliases"`
		AntiPatterns string `json:"anti_patterns"`
		Examples     string `json:"examples"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, err, 400)
		return
	}
	domain := s.getDomain(r)

	// Parse comma-separated strings into arrays
	parseCSV := func(s string) []string {
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		var out []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	t := store.DomainTerm{
		DomainKey:    domain,
		Term:         req.Term,
		Description:  req.Description,
		Context:      req.Context,
		Aliases:      parseCSV(req.Aliases),
		AntiPatterns: parseCSV(req.AntiPatterns),
		Examples:     parseCSV(req.Examples),
	}
	id, err := s.getStore().UpsertTerm(t)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, map[string]any{"term_id": id, "term": t.Term})
}

func (s *Server) handlePromptSetting(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	guideDefaults := prompts.Defaults()

	if r.Method == "GET" {
		// Return all command prompts — defaults merged with custom overrides
		result := map[string]string{}
		for cmd, defaultPrompt := range mcp.CommandInstructions {
			custom, err := st.GetSetting("prompt_" + cmd)
			if err != nil || custom == "" {
				result[cmd] = defaultPrompt
			} else {
				result[cmd] = custom
			}
		}
		// Include extraction guide prompts (fact_schema, enrichment, flow_tracing)
		for key, defaultGuide := range guideDefaults {
			custom, err := st.GetSetting(key)
			if err != nil || custom == "" {
				result[key] = defaultGuide
			} else {
				result[key] = custom
			}
		}
		// Legacy extraction guide custom prompt
		guideCustom, _ := st.GetSetting("extraction_prompt")
		result["_extraction_guide"] = guideCustom
		httpJSON(w, result)
		return
	}
	if r.Method == "PUT" || r.Method == "POST" {
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, err, 400)
			return
		}
		for key, val := range req {
			if key == "_extraction_guide" {
				st.SetSetting("extraction_prompt", val)
			} else if _, isGuide := guideDefaults[key]; isGuide {
				// Guide prompt override — clear if same as default
				if val == guideDefaults[key] {
					st.SetSetting(key, "")
				} else {
					st.SetSetting(key, val)
				}
			} else {
				// Command prompt override
				if defaultVal, ok := mcp.CommandInstructions[key]; ok && val == defaultVal {
					st.SetSetting("prompt_"+key, "")
				} else {
					st.SetSetting("prompt_"+key, val)
				}
			}
		}
		httpJSON(w, map[string]string{"status": "saved"})
		return
	}
	w.WriteHeader(405)
}

func (s *Server) handleDefaultGuide(w http.ResponseWriter, r *http.Request) {
	guide := mcp.ExtractionGuide("")
	httpJSON(w, map[string]string{"guide": guide})
}

func (s *Server) handleRegistry(w http.ResponseWriter, r *http.Request) {
	f, err := registry.AsFile()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(f)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		data, err := os.ReadFile(s.manifestPath)
		if err != nil {
			httpError(w, fmt.Errorf("reading manifest: %w", err), 500)
			return
		}
		httpJSON(w, map[string]any{"path": s.manifestPath, "content": string(data)})
		return
	}
	if r.Method == "PUT" || r.Method == "POST" {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpError(w, err, 400)
			return
		}
		var req struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			httpError(w, err, 400)
			return
		}
		if err := os.WriteFile(s.manifestPath, []byte(req.Content), 0644); err != nil {
			httpError(w, fmt.Errorf("writing manifest: %w", err), 500)
			return
		}
		httpJSON(w, map[string]string{"status": "saved", "path": s.manifestPath})
		return
	}
	w.WriteHeader(405)
}

func (s *Server) handleDiagram(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/diagram")
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 2)
	sessionID := parts[0]

	switch {
	case r.Method == "POST" && sessionID == "build":
		s.handleDiagramBuild(w, r)
	case r.Method == "GET" && sessionID == "latest":
		s.handleDiagramLatest(w, r)
	case r.Method == "GET" && sessionID == "list":
		s.handleDiagramList(w, r)
	case r.Method == "GET" && sessionID == "":
		s.handleDiagramList(w, r)
	case r.Method == "GET" && sessionID != "":
		s.handleDiagramGet(w, r, sessionID)
	case r.Method == "DELETE" && sessionID != "":
		s.handleDiagramDelete(w, r, sessionID)
	default:
		http.Error(w, "not found", 404)
	}
}

func (s *Server) handleDiagramBuild(w http.ResponseWriter, r *http.Request) {
	var req DiagramBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		req.Title = "Diagram"
	}
	if len(req.Nodes) == 0 && len(req.NodeKeys) == 0 {
		http.Error(w, "at least one of 'nodes' or 'node_keys' is required", http.StatusBadRequest)
		return
	}

	// Start with explicit nodes/edges
	diagramNodes := append([]DiagramNode{}, req.Nodes...)
	diagramEdges := append([]DiagramEdge{}, req.Edges...)
	var errors []string

	// Resolve node_keys from graph DB
	if len(req.NodeKeys) > 0 && s.getStore() != nil {
		foundNodes, missingKeys, err := s.getStore().GetNodesByKeys(req.NodeKeys)
		if err != nil {
			http.Error(w, "store error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		errors = missingKeys

		existingKeys := make(map[string]bool, len(diagramNodes))
		for _, n := range diagramNodes {
			existingKeys[n.Key] = true
		}

		resolvedIDs := make([]int64, 0, len(foundNodes))
		for _, gn := range foundNodes {
			if existingKeys[gn.NodeKey] {
				continue
			}
			diagramNodes = append(diagramNodes, DiagramNode{
				Key:       gn.NodeKey,
				Label:     gn.Name,
				Kind:      gn.NodeType,
				SourceKey: gn.NodeKey,
				Domain:    gn.DomainKey,
				Layer:     gn.Layer,
			})
			resolvedIDs = append(resolvedIDs, gn.NodeID)
		}

		// Auto-discover edges
		if len(resolvedIDs) > 0 {
			discoveredEdges, err := s.getStore().GetEdgesBetweenNodes(resolvedIDs)
			if err == nil {
				idToKey := make(map[int64]string, len(foundNodes))
				for _, gn := range foundNodes {
					idToKey[gn.NodeID] = gn.NodeKey
				}
				for _, ge := range discoveredEdges {
					fromKey := idToKey[ge.FromNodeID]
					toKey := idToKey[ge.ToNodeID]
					if fromKey == "" || toKey == "" {
						continue
					}
					hidden := false
					for _, h := range req.HideEdges {
						if h.From == fromKey && h.To == toKey {
							if h.EdgeType == "" || h.EdgeType == ge.EdgeType {
								hidden = true
								break
							}
						}
					}
					if hidden {
						continue
					}
					diagramEdges = append(diagramEdges, DiagramEdge{
						From:  fromKey,
						To:    toKey,
						Label: ge.EdgeType,
						Kind:  edgeTypeToKind(ge.EdgeType),
					})
				}
			}
		}
	}

	// Create session
	sessionID := fmt.Sprintf("%x", time.Now().UnixNano())
	if len(sessionID) > 8 {
		sessionID = sessionID[len(sessionID)-8:]
	}

	now := time.Now().UTC().Format(time.RFC3339)
	session := &DiagramSession{
		ID:          sessionID,
		Title:       req.Title,
		Nodes:       diagramNodes,
		Edges:       diagramEdges,
		Groups:      req.Groups,
		Annotations: req.Annotations,
		Steps:       req.Steps,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.mu.Lock()
	s.diagrams[sessionID] = session
	s.mu.Unlock()
	s.persistDiagramSession(session)

	sessionJSON, _ := json.Marshal(session)
	s.hub.Send("diagram", map[string]any{"session": json.RawMessage(sessionJSON)})

	port := s.port
	if port == 0 {
		port = 4200
	}

	resp := map[string]any{
		"session_id": sessionID,
		"url":        fmt.Sprintf("http://localhost:%d/diagram", port),
		"node_count": len(diagramNodes),
		"edge_count": len(diagramEdges),
	}
	if len(errors) > 0 {
		resp["errors"] = errors
	}
	httpJSON(w, resp)
}

func edgeTypeToKind(edgeType string) string {
	switch edgeType {
	case "CALLS_ENDPOINT", "CALLS_SERVICE", "EXPOSES_ENDPOINT", "HANDLES_OPERATION":
		return "http"
	case "PUBLISHES_TOPIC", "CONSUMES_TOPIC", "CONSUMED_BY", "USES_QUEUE":
		return "async"
	case "USES_MODEL", "DEFINES_MODEL", "REFERENCES_MODEL", "READS_DB", "WRITES_DB", "STORED_IN", "HAS_FIELD":
		return "data"
	default:
		return "structural"
	}
}

func (s *Server) handleDiagramList(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	sessions := make([]map[string]any, 0, len(s.diagrams))
	for _, session := range s.diagrams {
		sessions = append(sessions, map[string]any{
			"id":         session.ID,
			"title":      session.Title,
			"node_count": len(session.Nodes),
			"edge_count": len(session.Edges),
			"created_at": session.CreatedAt,
		})
	}
	s.mu.RUnlock()
	httpJSON(w, map[string]any{"sessions": sessions})
}

func (s *Server) handleDiagramDelete(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	delete(s.diagrams, id)
	s.mu.Unlock()
	s.getStore().DeleteDiagramSession(id)
	httpJSON(w, map[string]any{"deleted": id})
}

func (s *Server) handleDiagramLatest(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	var latest *DiagramSession
	for _, session := range s.diagrams {
		if latest == nil || session.UpdatedAt > latest.UpdatedAt {
			latest = session
		}
	}
	s.mu.RUnlock()
	if latest == nil {
		http.Error(w, "no diagrams", 404)
		return
	}
	httpJSON(w, latest)
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


func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	nodeKey := r.URL.Query().Get("node_key")
	if nodeKey == "" {
		httpJSON(w, []any{})
		return
	}
	node, err := s.getStore().GetNodeByKey(nodeKey)
	if err != nil {
		httpJSON(w, []any{})
		return
	}
	nodeEvidence, _ := s.getStore().ListEvidenceByNode(node.NodeID)
	edges, _ := s.getStore().ListEdges(store.EdgeFilter{})
	var edgeEvidence []store.EvidenceRow
	for _, e := range edges {
		if e.FromNodeID == node.NodeID || e.ToNodeID == node.NodeID {
			ev, _ := s.getStore().ListEvidenceByEdge(e.EdgeID)
			edgeEvidence = append(edgeEvidence, ev...)
		}
	}
	httpJSON(w, map[string]any{
		"node":           node,
		"node_evidence":  nodeEvidence,
		"edge_evidence":  edgeEvidence,
		"total_evidence": len(nodeEvidence) + len(edgeEvidence),
	})
}

func httpJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

package admin

import (
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sync"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/mcp"
	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
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

//go:embed static/*
var staticFS embed.FS

type DiagramSession struct {
	ID          string                            `json:"id"`
	Title       string                            `json:"title"`
	Nodes       []map[string]any                  `json:"nodes"`
	Edges       []map[string]any                  `json:"edges"`
	Annotations map[string]DiagramNote            `json:"annotations"`
	Steps            map[int]map[string]DiagramNote `json:"steps"`             // step_number → {node_key → annotation}
	StepTitles       map[int]string                `json:"step_titles"`       // step_number → title
	StepDescriptions map[int]string                `json:"step_descriptions"` // step_number → description text
	TotalSteps       int                           `json:"total_steps"`
	CreatedAt   string                            `json:"created_at"`
	UpdatedAt   string                            `json:"updated_at"`
}

type DiagramNote struct {
	Note      string `json:"note,omitempty"`
	Highlight string `json:"highlight,omitempty"`
}

// Server is the admin dashboard HTTP server.
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
	diagrams     map[string]*DiagramSession
}

// NewServer creates a new admin Server.
func NewServer(g *graph.Graph, s *store.Store, port int, manifestPath string, devMode bool, projectDir string) *Server {
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	} else {
		projectDir, _ = filepath.Abs(projectDir)
	}
	srv := &Server{graph: g, store: s, hub: NewHub(), port: port, manifestPath: manifestPath, devMode: devMode, projectPath: projectDir, originalPath: projectDir, diagrams: make(map[string]*DiagramSession)}
	srv.loadDiagramSessions()
	return srv
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
		if session.Steps == nil {
			session.Steps = make(map[int]map[string]DiagramNote)
		}
		if session.StepTitles == nil {
			session.StepTitles = make(map[int]string)
		}
		if session.StepDescriptions == nil {
			session.StepDescriptions = make(map[int]string)
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

// Start begins serving the admin dashboard on localhost.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/requests", s.handleRequests)
	mux.HandleFunc("/api/low-confidence", s.handleLowConfidence)
	mux.HandleFunc("/api/scans", s.handleScans)
	mux.HandleFunc("/api/validate", s.handleValidate)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/manifest", s.handleManifest)
	mux.HandleFunc("/api/settings/prompt", s.handlePromptSetting)
	mux.HandleFunc("/api/settings/default-guide", s.handleDefaultGuide)
	mux.HandleFunc("/api/discoveries", s.handleDiscoveries)
	mux.HandleFunc("/api/glossary", s.handleGlossary)
	mux.HandleFunc("/api/language-check", s.handleLanguageCheck)
	mux.HandleFunc("/api/glossary/delete", s.handleDeleteTerm)
	mux.HandleFunc("/api/glossary/dismiss", s.handleDismissViolation)
	mux.HandleFunc("/api/glossary/save", s.handleSaveTerm)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWebSocket(s.hub, w, r)
	})

	mux.HandleFunc("/api/diagram/", s.handleDiagram)
	mux.HandleFunc("/api/diagram", s.handleDiagram)

	if s.devMode {
		staticDir := filepath.Join(findModuleRoot(), "internal", "admin", "static")
		fmt.Fprintf(os.Stderr, "Dev mode: serving static files from %s\n", staticDir)
		fileServer := http.FileServer(http.Dir(staticDir))
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		fileServer := http.FileServer(http.FS(staticContent))
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f, err := staticContent.Open(strings.TrimPrefix(r.URL.Path, "/"))
			if err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
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
	httpJSON(w, map[string]any{"nodes": nodeList, "edges": edgeList})
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
		// Also include the extraction guide custom prompt
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
			} else {
				// Only save if different from default
				if defaultVal, ok := mcp.CommandInstructions[key]; ok && val == defaultVal {
					st.SetSetting("prompt_"+key, "") // clear override, use default
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
	case r.Method == "POST" && sessionID == "":
		s.handleDiagramCreate(w, r)
	case r.Method == "GET" && sessionID == "latest":
		s.handleDiagramLatest(w, r)
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
		Steps:            make(map[int]map[string]DiagramNote),
		StepTitles:       make(map[int]string),
		StepDescriptions: make(map[int]string),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.mu.Lock()
	s.diagrams[body.SessionID] = session
	s.mu.Unlock()
	s.persistDiagramSession(session)
	url := fmt.Sprintf("http://localhost:%d/diagram/%s", s.port, body.SessionID)
	httpJSON(w, map[string]any{"session_id": body.SessionID, "url": url})
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
	s.persistDiagramSession(session)
	s.hub.Send("diagram_update", map[string]any{
		"session_id": id, "nodes": session.Nodes, "edges": session.Edges, "annotations": session.Annotations,
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
		Step            *int   `json:"step"`              // nil = global annotation, number = step-specific
		StepTitle       string `json:"step_title"`        // optional title for this step
		StepDescription string `json:"step_description"`  // optional description text for this step
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	s.mu.Lock()
	if body.Step != nil {
		step := *body.Step
		if session.Steps[step] == nil {
			session.Steps[step] = make(map[string]DiagramNote)
		}
		session.Steps[step][body.NodeKey] = DiagramNote{Note: body.Note, Highlight: body.Highlight}
		if body.StepTitle != "" {
			session.StepTitles[step] = body.StepTitle
		}
		if body.StepDescription != "" {
			session.StepDescriptions[step] = body.StepDescription
		}
		if step+1 > session.TotalSteps {
			session.TotalSteps = step + 1
		}
	} else {
		session.Annotations[body.NodeKey] = DiagramNote{Note: body.Note, Highlight: body.Highlight}
	}
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Unlock()
	s.persistDiagramSession(session)
	s.hub.Send("diagram_update", map[string]any{
		"session_id": id, "nodes": session.Nodes, "edges": session.Edges,
		"annotations": session.Annotations, "steps": session.Steps,
		"step_titles": session.StepTitles, "step_descriptions": session.StepDescriptions, "total_steps": session.TotalSteps,
	})
	httpJSON(w, map[string]any{"status": "ok"})
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

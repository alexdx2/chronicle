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
}

// NewServer creates a new admin Server.
func NewServer(g *graph.Graph, s *store.Store, port int, manifestPath string, devMode bool) *Server {
	cwd, _ := os.Getwd()
	return &Server{graph: g, store: s, hub: NewHub(), port: port, manifestPath: manifestPath, devMode: devMode, projectPath: cwd, originalPath: cwd}
}

// switchTo swaps the server's backing store/graph to a different project.
// Creates .depbot/oracle.db if the directory exists but the DB doesn't.
func (s *Server) switchTo(projectPath string) error {
	if _, err := os.Stat(projectPath); err != nil {
		return fmt.Errorf("directory not found: %s", projectPath)
	}
	dbDir := filepath.Join(projectPath, ".depbot")
	os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "oracle.db")
	newStore, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	reg, _ := registry.LoadDefaults()
	newGraph := graph.New(newStore, reg)

	// Find manifest: prefer project root, fall back to .depbot/
	manifestPath := filepath.Join(projectPath, "oracle.domain.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		manifestPath = filepath.Join(projectPath, ".depbot", "oracle.domain.yaml")
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

	if s.devMode {
		staticDir := filepath.Join(findModuleRoot(), "internal", "admin", "static")
		fmt.Fprintf(os.Stderr, "Dev mode: serving static files from %s\n", staticDir)
		mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	} else {
		staticContent, err := fs.Sub(staticFS, "static")
		if err != nil {
			return fmt.Errorf("static fs: %w", err)
		}
		mux.Handle("/", http.FileServer(http.FS(staticContent)))
	}

	go s.hub.Run()
	go s.pollRequests()

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	fmt.Fprintf(os.Stderr, "Oracle Admin: http://%s\n", addr)
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

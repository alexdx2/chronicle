package graph

import (
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// ExtractionStats aggregates extraction rows by status for scan reporting.
type ExtractionStats struct {
	Total                  int `json:"total"`
	Extracted              int `json:"extracted"`
	Resolved               int `json:"resolved"`
	TypeOnly               int `json:"type_only"`
	NoRuntimeArchitecture  int `json:"no_runtime_architecture"`
	ConfigOnly             int `json:"config_only"`
	Generated              int `json:"generated"`
	Skipped                int `json:"skipped"`
	Failed                 int `json:"failed"`
}

// GraphStats summarizes active graph nodes and edges for a domain.
type GraphStats struct {
	NodesTotal    int            `json:"nodes_total"`
	EdgesTotal    int            `json:"edges_total"`
	NodesByLayer  map[string]int `json:"nodes_by_layer"`
	NodesByType   map[string]int `json:"nodes_by_type"`
	EdgesByDeriv  map[string]int `json:"edges_by_derivation"`
}

// ScanRunStatus is the full scan status payload for pool_status and checkpoints.
type ScanRunStatus struct {
	Phase            string           `json:"phase"`
	ScanMode         string           `json:"scan_mode"`
	ScanRunID        int64            `json:"scan_run_id"`
	RevisionID       int64            `json:"revision_id"`
	VotesNeeded      int              `json:"votes_needed"`
	Obligations      *store.PoolStatus `json:"obligations,omitempty"`
	Extractions      ExtractionStats  `json:"extractions"`
	Graph            GraphStats       `json:"graph"`
	QualityWarnings  []QualityWarning `json:"quality_warnings"`
	ReviewCandidates []ReviewCandidate `json:"review_candidates,omitempty"`
	ReadyToResolve   bool             `json:"ready_to_resolve"`
	WaveComplete     bool             `json:"wave_complete"`
}

// BuildExtractionStats counts extraction rows by status.
func BuildExtractionStats(rows []store.ExtractionRow) ExtractionStats {
	var s ExtractionStats
	for _, r := range rows {
		s.Total++
		switch r.Status {
		case "extracted":
			s.Extracted++
		case "resolved":
			s.Resolved++
		case "type_only":
			s.TypeOnly++
		case "no_runtime_architecture":
			s.NoRuntimeArchitecture++
		case "config_only":
			s.ConfigOnly++
		case "generated":
			s.Generated++
		case "skipped":
			s.Skipped++
		case "failed":
			s.Failed++
		}
	}
	return s
}

// BuildGraphStats summarizes the graph for a domain.
func (g *Graph) BuildGraphStats(domainKey string) GraphStats {
	stats := GraphStats{
		NodesByLayer: make(map[string]int),
		NodesByType:  make(map[string]int),
		EdgesByDeriv: make(map[string]int),
	}

	nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	for _, n := range nodes {
		stats.NodesTotal++
		stats.NodesByLayer[n.Layer]++
		stats.NodesByType[n.NodeType]++
	}

	edges, _ := g.store.ListEdges(store.EdgeFilter{})
	for _, e := range edges {
		if !e.Active {
			continue
		}
		from, _ := g.store.GetNodeByID(e.FromNodeID)
		if from.DomainKey != domainKey {
			continue
		}
		stats.EdgesTotal++
		stats.EdgesByDeriv[e.DerivationKind]++
	}
	return stats
}

// BuildScanQualityReport inspects the graph for invariant violations and noise.
func (g *Graph) BuildScanQualityReport(domainKey string) []QualityWarning {
	var warnings []QualityWarning

	topicIDs := g.buildTopicIdentitySet(domainKey)
	endpoints, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "endpoint", Status: "active"})
	for _, ep := range endpoints {
		id := endpointIdentity(ep)
		if id != "" && topicIDs[id] {
			warnings = append(warnings, QualityWarning{
				Category: "topic",
				Severity: "critical",
				Message:  "endpoint duplicates a Kafka/event topic name — should be suppressed",
				NodeKey:  ep.NodeKey,
			})
		}
	}

	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	for _, e := range edges {
		if !e.Active {
			continue
		}
		from, err1 := g.store.GetNodeByID(e.FromNodeID)
		to, err2 := g.store.GetNodeByID(e.ToNodeID)
		if err1 != nil || err2 != nil || from.DomainKey != domainKey {
			continue
		}
		if from.NodeType == "controller" && to.NodeType == "endpoint" {
			warnings = append(warnings, QualityWarning{
				Category: "endpoint",
				Severity: "warning",
				Message:  "controller CONTAINS endpoint — use EXPOSES_ENDPOINT instead",
				NodeKey:  to.NodeKey,
			})
		}
	}

	services, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "service", Status: "active"})
	externals, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "external_system", Status: "active"})
	byTail := make(map[string]bool)
	for _, svc := range services {
		byTail[serviceKeyTail(svc.NodeKey)] = true
	}
	for _, ext := range externals {
		if byTail[serviceKeyTail(ext.NodeKey)] {
			warnings = append(warnings, QualityWarning{
				Category: "service",
				Severity: "warning",
				Message:  "external_system duplicates an in-repo service node",
				NodeKey:  ext.NodeKey,
			})
		}
	}

	controllers, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "controller", Status: "active"})
	modules, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "module", Status: "active"})
	if len(controllers) > 0 && len(modules) == 0 {
		warnings = append(warnings, QualityWarning{
			Category: "noise",
			Severity: "info",
			Message:  "controllers exist but no module nodes — emit provides/parent facts from @Module or Program.cs",
		})
	}

	providers, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "provider", Status: "active"})
	support := 0
	for _, p := range providers {
		name := strings.ToLower(p.Name)
		if strings.Contains(name, "logger") || strings.Contains(name, "config") ||
			strings.Contains(name, "mapper") || strings.Contains(name, "cache") {
			support++
		}
	}
	warnings = append(warnings, CheckNoiseRatio(len(providers), support)...)

	return warnings
}

// BuildScanRunStatus assembles the full scan status for MCP responses.
func (g *Graph) BuildScanRunStatus(run *store.ScanRunRow, obligationType string) (*ScanRunStatus, error) {
	pool, err := g.store.ObligationPoolStatus(run.RevisionID, obligationType)
	if err != nil {
		return nil, err
	}

	exts, err := g.store.ListExtractions(run.RevisionID, run.DomainKey)
	if err != nil {
		return nil, err
	}

	status := &ScanRunStatus{
		Phase:       run.Phase,
		ScanMode:    "artifact-pool",
		ScanRunID:   run.RunID,
		RevisionID:  run.RevisionID,
		VotesNeeded: run.VotesNeeded,
		Obligations: pool,
		Extractions: BuildExtractionStats(exts),
		Graph:       g.BuildGraphStats(run.DomainKey),
		ReadyToResolve: pool.ClaimableNow == 0 && pool.InProgress == 0 && pool.Failed == 0,
		WaveComplete:   pool.ClaimableNow == 0 && pool.InProgress == 0,
	}

	if run.Phase == "phase1_review" || run.Phase == "phase2_confirm" || run.Phase == "endpoint_reconcile" || run.Phase == "finalized" {
		status.QualityWarnings = g.BuildScanQualityReport(run.DomainKey)
		candidates, _ := g.ScanReviewCandidates(run.DomainKey, run.RevisionID)
		status.ReviewCandidates = candidates
	}

	return status, nil
}

// CountResolvedExtractions returns rows marked resolved after graph build.
func (g *Graph) CountResolvedExtractions(revisionID int64, domainKey string) (int, error) {
	exts, err := g.store.ListExtractions(revisionID, domainKey)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range exts {
		if e.Status == "resolved" {
			n++
		}
	}
	return n, nil
}

package graph

import "github.com/alexdx2/chronicle-core/store"

// ScanAction is what chronicle_scan_next_file returns to Claude.
type ScanAction struct {
	ScanRunID    int64         `json:"scan_run_id,omitempty"`
	Phase        string        `json:"phase"`
	Action       string        `json:"action"` // start_scan, extract_files, call_resolve_extractions, discover_files, wait, trace_flow, none
	Files        []string      `json:"files,omitempty"`
	Blocked      bool          `json:"blocked,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	Done         bool          `json:"done,omitempty"`
	Progress     *ScanProgress `json:"progress,omitempty"`
	FactSchema   string        `json:"fact_schema,omitempty"` // included with extract_files/trace_flow to guide agents
}

const factSchemaGuide = `EXTRACTION RULES — follow every step, skip nothing.

Read the file. Determine what role this file plays by reading its code (not filename):
- Does it define HTTP/WS/GraphQL endpoints? → from_type="controller"
- Does it wire together other components (DI container, module registry)? → from_type="module"
- Everything else (services, clients, producers, consumers, helpers) → from_type="provider"
- Schema/manifest files (prisma, protobuf, openapi, package.json) → extract models/deps directly

Extract ALL of the following that appear in the code:
1. EVERY import/require → {"kind":"import","to":"./path","symbols":["Name"],"from_type":"...","to_type":"controller|provider|module"}
2. EVERY HTTP/WS/RPC endpoint definition → {"kind":"endpoint","method":"GET|POST|PUT|DELETE|WS","target":"/path","from_type":"..."}
3. EVERY outbound HTTP/RPC call → {"kind":"http_call","method":"GET|POST","target":"http://host:port/path","from_type":"provider"}
4. EVERY database model/table access → {"kind":"model","to":"ModelName","from_type":"provider"}
5. EVERY event/message publish → {"kind":"produces","to":"topic-name","method":"methodName","from_type":"provider"}
6. EVERY event/message subscribe/consume → {"kind":"consumes","to":"topic-name","method":"handlerName","from_type":"provider"}
7. EVERY package dependency (from manifest) → {"kind":"dependency","to":"package-name"}
8. EVERY schema model/entity definition → {"kind":"model","to":"Name"}
9. EVERY schema enum definition → {"kind":"enum","to":"Name"}
10. EVERY schema relation/FK → {"kind":"model_relation","from":"ModelA","to":"ModelB"}

Status: "extracted" if any facts, "no_runtime_architecture" if file has no endpoints/imports/calls, "type_only" for pure types/interfaces/constants, "config_only" for config files, "generated" for auto-generated.

CRITICAL: facts MUST be a JSON array of objects. Every object MUST have a "kind" field. NEVER use plain strings.`

// ScanProgress tracks extraction progress within a scan run.
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

	// No active run — tell Claude to start one.
	if run == nil {
		return &ScanAction{
			Phase:  "",
			Action: "start_scan",
		}, nil
	}

	switch run.Phase {
	case "setup":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "setup",
			Action:    "discover_files",
		}, nil

	case "phase1_extract":
		// Atomically claim up to 10 unclaimed files (reclaims expired leases too)
		batch, err := g.store.ClaimObligations(run.RevisionID, "scan_file", 10)
		if err != nil {
			return nil, err
		}

		if len(batch) > 0 {
			pending, _ := g.store.CountPendingObligations(run.RevisionID, "scan_file")
			return &ScanAction{
				ScanRunID:  run.RunID,
				Phase:      "phase1_extract",
				Action:     "extract_files",
				Files:      batch,
				FactSchema: factSchemaGuide,
				Progress: &ScanProgress{
					Total:     run.TotalFiles,
					Extracted: run.TotalFiles - pending,
					Remaining: pending,
				},
			}, nil
		}

		// No unclaimed files. Check if other agents still have claimed (in-flight) files.
		pending, _ := g.store.CountPendingObligations(run.RevisionID, "scan_file")
		if pending > 0 {
			return &ScanAction{
				ScanRunID: run.RunID,
				Phase:     "phase1_extract",
				Action:    "wait",
				Reason:    "All files claimed by other agents. Retry in a few seconds.",
				Progress: &ScanProgress{
					Total:     run.TotalFiles,
					Extracted: run.TotalFiles - pending,
					Remaining: pending,
				},
			}, nil
		}

		// All files done — transition to phase1_resolve.
		if err := g.store.TransitionScanRun(run.RunID, "phase1_resolve", 0); err != nil {
			return nil, err
		}
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase1_resolve",
			Action:    "call_resolve_extractions",
			Blocked:   true,
		}, nil

	case "phase1_resolve":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase1_resolve",
			Action:    "call_resolve_extractions",
			Blocked:   true,
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
			Reason:    "Flow facts extracted. Call chronicle_resolve_extractions to add flows to graph.",
		}, nil

	case "finalized":
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "finalized",
			Action:    "none",
			Done:      true,
		}, nil

	default:
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     run.Phase,
			Action:    "none",
			Reason:    "unknown phase: " + run.Phase,
		}, nil
	}
}

// phase2SelectAction finds trigger files (endpoints, consumers) and creates trace_flow obligations.
func (g *Graph) phase2SelectAction(run *store.ScanRunRow) (*ScanAction, error) {
	// Find trigger files: nodes that expose endpoints or consume topics
	triggerFiles := make(map[string]bool)

	for _, edgeType := range []string{"EXPOSES_ENDPOINT", "CONSUMES_TOPIC"} {
		active := true
		edges, err := g.store.ListEdges(store.EdgeFilter{EdgeType: edgeType, Active: &active})
		if err != nil {
			continue
		}
		for _, e := range edges {
			node, err := g.store.GetNodeByKey(e.FromNodeKey)
			if err != nil || node == nil || node.FilePath == "" {
				continue
			}
			triggerFiles[node.FilePath] = true
		}
	}

	if len(triggerFiles) == 0 {
		g.store.CompleteScanRun(run.RunID)
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "finalized",
			Action:    "none",
			Done:      true,
			Reason:    "No trigger files found for flow tracing.",
		}, nil
	}

	for fp := range triggerFiles {
		g.store.CreateObligation(run.RevisionID, run.DomainKey, "trace_flow", fp, "trigger file — trace business flow")
	}

	g.store.TransitionScanRun(run.RunID, "phase2_extract", len(triggerFiles))
	return &ScanAction{
		ScanRunID:  run.RunID,
		Phase:      "phase2_extract",
		Action:     "trace_flow",
		Files:      mapKeys(triggerFiles),
		FactSchema: factSchemaGuide,
		Reason:     "Read each trigger file, follow the call chain, and extract flow facts. For each file call chronicle_file_extracted with flow facts.",
		Progress:   &ScanProgress{Total: len(triggerFiles), Extracted: 0, Remaining: len(triggerFiles)},
	}, nil
}

// phase2ExtractAction returns pending trace_flow files using atomic claim.
func (g *Graph) phase2ExtractAction(run *store.ScanRunRow) (*ScanAction, error) {
	batch, err := g.store.ClaimObligations(run.RevisionID, "trace_flow", 5)
	if err != nil {
		return nil, err
	}

	if len(batch) > 0 {
		pending, _ := g.store.CountPendingObligations(run.RevisionID, "trace_flow")
		return &ScanAction{
			ScanRunID:  run.RunID,
			Phase:      "phase2_extract",
			Action:     "trace_flow",
			Files:      batch,
			FactSchema: factSchemaGuide,
			Reason:     "Read each file, trace the business flow through service calls, and extract flow facts.",
			Progress:   &ScanProgress{Total: run.TotalFiles, Extracted: run.TotalFiles - pending, Remaining: pending},
		}, nil
	}

	pending, _ := g.store.CountPendingObligations(run.RevisionID, "trace_flow")
	if pending > 0 {
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase2_extract",
			Action:    "wait",
			Reason:    "All flow files claimed by other agents. Retry in a few seconds.",
			Progress:  &ScanProgress{Total: run.TotalFiles, Extracted: run.TotalFiles - pending, Remaining: pending},
		}, nil
	}

	// All flows traced — transition to resolve
	g.store.TransitionScanRun(run.RunID, "phase2_resolve", 0)
	return &ScanAction{
		ScanRunID: run.RunID,
		Phase:     "phase2_resolve",
		Action:    "call_resolve_extractions",
		Blocked:   true,
		Reason:    "All flows traced. Call chronicle_resolve_extractions to add flows to graph.",
	}, nil
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

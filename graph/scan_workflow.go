package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/ast"
	"github.com/alexdx2/chronicle-core/extract/prisma"
	"github.com/alexdx2/chronicle-core/extract/rules"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
)

// FileWithAST is a file path paired with pre-extracted AST facts and enrichment candidates.
type FileWithAST struct {
	Path         string `json:"path"`
	DomainKey    string `json:"domain_key,omitempty"`    // which domain this file belongs to (from manifest)
	ASTFacts     string `json:"ast_facts"`               // JSON array of semantic facts, or "[]"
	FromType     string `json:"from_type"`               // detected by AST: controller, module, or ""
	Candidates   string `json:"candidates"`              // JSON array of enrichment candidates for LLM classification
	ObligationID int64  `json:"obligation_id,omitempty"` // obligation that claimed this file
	VoteGroup    string `json:"vote_group,omitempty"`    // vote group for deduplication
	VoteIndex    int    `json:"vote_index,omitempty"`    // which vote pass this is (1-based)
}

// ScanAction is what chronicle_scan_next_file returns to Claude.
type ScanAction struct {
	Domain       string         `json:"domain,omitempty"`        // ALWAYS included — agents MUST use this exact domain
	ScanRunID    int64          `json:"scan_run_id,omitempty"`
	Phase        string         `json:"phase"`
	Action       string         `json:"action"` // start_scan, extract_files, call_resolve_extractions, discover_files, wait, trace_flow, none
	Files        []string       `json:"files,omitempty"`         // simple file list (backward compat)
	FilesWithAST []FileWithAST  `json:"files_with_ast,omitempty"` // files + pre-extracted AST facts
	Blocked      bool           `json:"blocked,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Done         bool           `json:"done,omitempty"`
	Progress     *ScanProgress  `json:"progress,omitempty"`
	FactSchema   string         `json:"fact_schema,omitempty"`  // included with extract_files/trace_flow to guide agents
	VotesNeeded  int            `json:"votes_needed,omitempty"` // how many LLM enrichment runs per file (0 or 1 = no voting)
	GraphContext         *GraphContext          `json:"graph_context,omitempty"`      // phase 2 select — flat list of known entities
	FlowContext          *FlowContext           `json:"flow_context,omitempty"`       // phase 2 extract — per-trigger enriched context
	EndpointReconcile    []UnmatchedHTTPCall    `json:"endpoint_reconcile,omitempty"` // unmatched http_calls + candidate endpoints for LLM matching
	InstructionPacks *prompts.PackSelection `json:"instruction_packs,omitempty"` // loaded + available instruction packs
	Infrastructure   []manifest.InfraEntry  `json:"infrastructure,omitempty"`    // from manifest — agents use to link topics to brokers
	CandidateBoundaries []string            `json:"candidate_boundaries,omitempty"` // from manifest include patterns — hints, not truth
	// Checkpoint — when set, Claude MUST show this to user and call chronicle_scan_confirm
	Checkpoint       *ScanCheckpoint        `json:"checkpoint,omitempty"`
}

// ScanCheckpoint is a user-facing question that blocks scan progress.
// The orchestrator MUST show this to the user and pass the answer
// back via chronicle_scan_confirm before scan_next_file will return files.
type ScanCheckpoint struct {
	ID       string         `json:"id"`       // checkpoint identifier
	Question string         `json:"question"` // what to ask the user
	Context  map[string]any `json:"context"`  // data to show (file counts, packs, etc.)
	Options  []string       `json:"options"`  // suggested answers
}

// GraphContext provides phase 1 graph data to help flow tracing agents.
type GraphContext struct {
	Endpoints []string `json:"endpoints,omitempty"` // known endpoint names
	Services  []string `json:"services,omitempty"`  // known service/provider nodes
	Topics    []string `json:"topics,omitempty"`    // known event/message topics
}

// FlowContext provides per-trigger-file enriched context for flow tracing.
type FlowContext struct {
	TriggerFile string           `json:"trigger_file"`
	TriggerNode string           `json:"trigger_node"`            // node key
	TriggerType string           `json:"trigger_type"`            // controller, provider
	FilesToRead []string         `json:"files_to_read"`           // trigger + reachable files
	Reachable   []ReachableNode  `json:"reachable"`               // graph neighborhood
	ModelHint   string           `json:"model_hint,omitempty"`    // "use sonnet or better for flow tracing"
}

// ReachableNode is a node reachable from the trigger via INJECTS/CALLS edges.
type ReachableNode struct {
	Name     string   `json:"name"`
	NodeType string   `json:"type"`
	File     string   `json:"file,omitempty"`
	Depth    int      `json:"depth"`
	Edges    []string `json:"edges"` // compact edge descriptions: "INJECTS→ServiceName", "EXPOSES→POST /path"
}

// Guides are loaded from graph/prompts/*.md via go:embed.
// Per-project overrides can be set via the admin dashboard (stored in settings table).

// getGuide returns a guide with per-project override support.
// If tech is provided, technology adapters are appended to the core guide.
func (g *Graph) getGuide(key string, tech ...string) string {
	custom, err := g.store.GetSetting(key)
	if err == nil && custom != "" {
		return custom // custom overrides include their own tech hints
	}
	defaults := prompts.Defaults()
	def, ok := defaults[key]
	if !ok {
		return ""
	}
	guide := prompts.Compose(def, tech)

	// Also append project-level packs from .depbot/packs/
	for _, t := range tech {
		content, err := g.store.LoadPackFile(t)
		if err == nil && content != "" {
			guide += "\n\n---\n\n" + content
		}
	}
	return guide
}

// ScanProgress tracks extraction progress within a scan run.
type ScanProgress struct {
	Total     int `json:"total"`
	Extracted int `json:"extracted"`
	Remaining int `json:"remaining"`
}

// ScanNextAction returns what Claude should do next based on scan run state.
// tech is the list of frameworks from the manifest (e.g. ["nestjs", "graphql"]).
func (g *Graph) ScanNextAction(domainKey string, tech ...string) (*ScanAction, error) {
	// Wrap to always set Domain on the returned action
	action, err := g.scanNextAction(domainKey, tech...)
	if err == nil && action != nil {
		action.Domain = domainKey
	}
	return action, err
}

func (g *Graph) scanNextAction(domainKey string, tech ...string) (*ScanAction, error) {
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

	case "confirm_scope":
		// Checkpoint: show user what was discovered, ask for confirmation
		// Blocked until chronicle_scan_confirm is called
		pending, _ := g.store.CountPendingObligations(run.RevisionID, "scan_file")
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "confirm_scope",
			Action:    "confirm",
			Blocked:   true,
			Checkpoint: &ScanCheckpoint{
				ID:       "scope",
				Question: "Ready to scan? Review the file count and confirm.",
				Context: map[string]any{
					"total_files":  run.TotalFiles,
					"pending":     pending,
					"votes_needed": run.VotesNeeded,
					"estimated_reads": run.TotalFiles * run.VotesNeeded,
				},
				Options: []string{"yes", "adjust scope", "change votes"},
			},
			Reason: "STOP: Show the checkpoint to the user. Call chronicle_scan_confirm(scan_run_id, confirmed=true) to proceed.",
		}, nil

	case "phase1_extract":
		// Atomically claim up to 10 unclaimed files (reclaims expired leases too)
		claimed, err := g.store.ClaimObligations(run.RevisionID, "scan_file", 8)
		if err != nil {
			return nil, err
		}

		if len(claimed) > 0 {
			pending, _ := g.store.CountPendingObligations(run.RevisionID, "scan_file")

			// Build metadata lookup from claimed obligations
			type claimMeta struct {
				domain       string
				obligationID int64
				voteGroup    string
				voteIndex    int
			}
			metaByFile := make(map[string]claimMeta, len(claimed))
			batch := make([]string, 0, len(claimed))
			for _, c := range claimed {
				batch = append(batch, c.TargetKey)
				metaByFile[c.TargetKey] = claimMeta{
					domain:       c.DomainKey,
					obligationID: c.ObligationID,
					voteGroup:    c.VoteGroup,
					voteIndex:    c.VoteIndex,
				}
			}

			// Run tree-sitter AST + rules on each file
			// AST extracts raw syntax → rules apply framework meaning → semantic facts saved
			ruleRegistry := rules.NewRegistry(rules.RulesetsForTech(tech)...)
			filesWithAST := make([]FileWithAST, 0, len(batch))
			for _, filePath := range batch {
				meta := metaByFile[filePath]
				fwa := FileWithAST{
					Path:         filePath,
					DomainKey:    meta.domain,
					ASTFacts:     "[]",
					Candidates:   "[]",
					ObligationID: meta.obligationID,
					VoteGroup:    meta.voteGroup,
					VoteIndex:    meta.voteIndex,
				}
				if isTypeScriptFile(filePath) {
					if content := readFileContent(filePath); content != nil {
						rawResult := ast.ExtractTypeScript(content)
						semantic := ruleRegistry.Apply(rawResult)
						fwa.ASTFacts = semantic.FactsJSON()
						fwa.FromType = semantic.FromType
						fmt.Fprintf(os.Stderr, "AST: %s → %d facts, %d candidates\n", filePath, len(rawResult.Facts), len(rawResult.Candidates))
						// Serialize candidates for LLM classification
						if len(rawResult.Candidates) > 0 {
							if cb, err := json.Marshal(rawResult.Candidates); err == nil {
								fwa.Candidates = string(cb)
							}
						}
					}
				}
				if strings.HasSuffix(filePath, ".prisma") {
					if content := readFileContent(filePath); content != nil {
						prismaResult := prisma.Extract(content)
						var facts []map[string]any
						for _, m := range prismaResult.Models {
							facts = append(facts, map[string]any{
								"kind": "model", "name": m.Name,
								"file_path": filePath, "line": m.Line,
							})
						}
						for _, e := range prismaResult.Enums {
							facts = append(facts, map[string]any{
								"kind": "enum", "name": e.Name,
								"file_path": filePath, "line": e.Line,
							})
						}
						for _, r := range prismaResult.Relations {
							facts = append(facts, map[string]any{
								"kind": "model_relation", "from": r.From,
								"to": r.To, "field_name": r.FieldName,
							})
						}
						if len(facts) > 0 {
							factsJSON, _ := json.Marshal(facts)
							fwa.ASTFacts = string(factsJSON)
							fwa.FromType = "schema"
							fmt.Fprintf(os.Stderr, "Prisma: %s → %d models, %d enums, %d relations\n",
								filePath, len(prismaResult.Models), len(prismaResult.Enums), len(prismaResult.Relations))
						}
					}
				}
				filesWithAST = append(filesWithAST, fwa)
			}

			// Save semantic facts directly — high-confidence deterministic facts
			for _, fwa := range filesWithAST {
				if fwa.ASTFacts != "[]" {
					domain := fwa.DomainKey
					if domain == "" {
						domain = run.DomainKey
					}
					g.SaveFileExtraction(run.RevisionID, domain, fwa.Path, "extracted", fwa.FromType, fwa.ASTFacts, "")
				}
			}

			// Pick guide: enrichment if AST found facts, full extraction if not
			guide := g.getGuide("guide_enrichment", tech...)
			hasASTFacts := false
			for _, fwa := range filesWithAST {
				if fwa.ASTFacts != "[]" {
					hasASTFacts = true
					break
				}
			}
			if !hasASTFacts {
				guide = g.getGuide("guide_fact_schema", tech...)
				filesWithAST = nil // don't confuse agent with empty AST
			}

			// Core packs — tech packs are agent-selected
			packSel := g.buildPackSelection()

			g.emitter.Emit(ScanEvent{
				Kind:  EventBatchExtracted,
				Phase: "phase1_extract",
				Data: map[string]any{
					"batch_size": len(batch),
					"ast_facts":  countASTFacts(filesWithAST),
				},
				Progress: &ScanProgress{
					Total:     run.TotalFiles,
					Extracted: run.TotalFiles - pending,
					Remaining: pending,
				},
			})

			return &ScanAction{
				ScanRunID:        run.RunID,
				Phase:            "phase1_extract",
				Action:           "extract_files",
				Files:            batch,
				FilesWithAST:     filesWithAST,
				VotesNeeded:      run.VotesNeeded,
				FactSchema:       guide,
				InstructionPacks: packSel,
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
		g.emitter.Emit(ScanEvent{
			Kind:  EventPhaseChanged,
			Phase: "phase1_resolve",
			Data: map[string]any{
				"message": "All files extracted. Resolving facts into graph...",
			},
		})
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

	case "phase1_review":
		status, err := g.BuildScanRunStatus(run, "scan_file")
		if err != nil {
			return nil, err
		}
		summary := g.buildScanSummary(run.DomainKey)
		summary["quality_warnings"] = status.QualityWarnings
		summary["review_candidates"] = status.ReviewCandidates
		summary["extractions"] = status.Extractions
		reviewCount := len(status.ReviewCandidates)
		criticalWarnings := 0
		for _, w := range status.QualityWarnings {
			if w.Severity == "critical" {
				criticalWarnings++
			}
		}
		question := "Phase 1 resolved. Review graph quality before endpoint reconciliation."
		options := []string{"proceed", "re-review files"}
		if reviewCount == 0 && criticalWarnings == 0 {
			// Auto-skip review gate when graph is clean
			if err := g.store.TransitionScanRun(run.RunID, "endpoint_reconcile", 0); err != nil {
				return nil, err
			}
			return g.endpointReconcileAction(run)
		}
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase1_review",
			Action:    "confirm",
			Blocked:   true,
			Checkpoint: &ScanCheckpoint{
				ID:       "phase1_review",
				Question: question,
				Context:  summary,
				Options:  options,
			},
			Reason: "STOP: Show quality_warnings and review_candidates. Call chronicle_scan_confirm(checkpoint_id='phase1_review', answer='proceed' or 're-review files').",
		}, nil

	case "endpoint_reconcile":
		return g.endpointReconcileAction(run)

	case "phase2_select":
		return g.phase2SelectAction(run)

	case "phase2_confirm":
		summary := g.buildScanSummary(run.DomainKey)
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "phase2_confirm",
			Action:    "confirm",
			Blocked:   true,
			Checkpoint: &ScanCheckpoint{
				ID:       "phase1_summary",
				Question: "Phase 1 complete. Here's what Chronicle found. Continue with flow tracing?",
				Context:  summary,
				Options:  []string{"yes", "skip flows"},
			},
			Reason: "STOP: Show the checkpoint to the user. Call chronicle_scan_confirm(scan_run_id, checkpoint_id='phase1_summary', answer='yes') to proceed.",
		}, nil

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
		summary := g.buildScanSummary(run.DomainKey)
		g.emitter.Emit(ScanEvent{
			Kind:  EventScanComplete,
			Phase: "finalized",
			Data:  map[string]any{"message": "Scan complete."},
		})
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "finalized",
			Action:    "none",
			Done:      true,
			Reason:    formatScanSummary(summary),
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

// endpointReconcileAction presents unmatched HTTP calls to the LLM
// along with the list of known endpoints so it can emit calls_endpoint facts.
func (g *Graph) endpointReconcileAction(run *store.ScanRunRow) (*ScanAction, error) {
	unmatched := g.FindUnmatchedHTTPCalls(run.DomainKey)

	if len(unmatched) == 0 {
		// Nothing to reconcile — skip to phase2
		g.store.TransitionScanRun(run.RunID, "phase2_select", 0)
		return g.phase2SelectAction(run)
	}

	return &ScanAction{
		Domain:    run.DomainKey,
		ScanRunID: run.RunID,
		Phase:     "endpoint_reconcile",
		Action:    "reconcile_endpoints",
		Reason: "Some HTTP calls could not be automatically matched to known endpoints. " +
			"Review each unmatched call and emit calls_endpoint facts for matches you can identify. " +
			"For example, if a client calls /users/123 and the known endpoint is GET /users/:id, " +
			"emit: {\"kind\": \"calls_endpoint\", \"from\": \"ClientName\", \"from_type\": \"provider\", \"target\": \"/users/:id\", \"method\": \"GET\"}. " +
			"Then call chronicle_resolve_extractions to apply your matches.",
		EndpointReconcile: unmatched,
		Blocked:           true,
	}, nil
}

// phase2SelectAction finds trigger files (endpoints, consumers) and creates trace_flow obligations.
func (g *Graph) phase2SelectAction(run *store.ScanRunRow) (*ScanAction, error) {
	// Find trigger files: nodes that expose endpoints or consume topics
	triggerFiles := make(map[string]bool)
	var skippedNoFilePath int

	for _, edgeType := range []string{"EXPOSES_ENDPOINT", "CONSUMES_TOPIC"} {
		active := true
		edges, err := g.store.ListEdges(store.EdgeFilter{EdgeType: edgeType, Active: &active})
		if err != nil {
			continue
		}
		for _, e := range edges {
			node, err := g.store.GetNodeByKey(e.FromNodeKey)
			if err != nil || node == nil {
				continue
			}
			if node.DomainKey != "" && node.DomainKey != run.DomainKey {
				continue
			}
			if node.FilePath == "" {
				skippedNoFilePath++
				continue
			}
			triggerFiles[node.FilePath] = true
		}
	}

	// Fallback: if primary lookup found edges but all had empty FilePath,
	// scan all code-layer nodes with non-empty file_path that are connected
	// to endpoint/topic nodes via EXPOSES_ENDPOINT or CONSUMES_TOPIC.
	if len(triggerFiles) == 0 && skippedNoFilePath > 0 {
		// Build set of node IDs that are sources of trigger edges
		triggerNodeIDs := make(map[int64]bool)
		for _, edgeType := range []string{"EXPOSES_ENDPOINT", "CONSUMES_TOPIC"} {
			active := true
			edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: edgeType, Active: &active})
			for _, e := range edges {
				triggerNodeIDs[e.FromNodeID] = true
			}
		}
		// Search all nodes with file_path set; if they match a trigger node ID, use them
		allNodes, _ := g.store.ListNodes(store.NodeFilter{Domain: run.DomainKey})
		for _, n := range allNodes {
			if n.FilePath != "" && triggerNodeIDs[n.NodeID] {
				triggerFiles[n.FilePath] = true
			}
		}
		// Still empty? Last resort: find files via CONTAINS edges to trigger source nodes
		if len(triggerFiles) == 0 {
			allEdges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
			parentFiles := make(map[int64]string) // child node ID → parent file_path
			for _, e := range allEdges {
				if triggerNodeIDs[e.ToNodeID] {
					pNode, err := g.store.GetNodeByKey(e.FromNodeKey)
					if err == nil && pNode != nil && pNode.FilePath != "" {
						parentFiles[e.ToNodeID] = pNode.FilePath
					}
				}
			}
			for _, fp := range parentFiles {
				triggerFiles[fp] = true
			}
		}
	}

	if len(triggerFiles) == 0 {
		reason := "No trigger files found for flow tracing."
		if skippedNoFilePath > 0 {
			reason = fmt.Sprintf("Phase 2 skipped: found %d EXPOSES_ENDPOINT/CONSUMES_TOPIC edges but all source nodes have empty file_path. This usually means the controller/consumer nodes were created by import facts before endpoint facts could set the file path.", skippedNoFilePath)
		}
		g.store.CompleteScanRun(run.RunID)
		return &ScanAction{
			ScanRunID: run.RunID,
			Phase:     "finalized",
			Action:    "none",
			Done:      true,
			Reason:    reason,
		}, nil
	}

	for fp := range triggerFiles {
		g.store.CreateObligation(run.RevisionID, run.DomainKey, "trace_flow", fp, "trigger file — trace business flow")
	}

	// Build graph context from phase 1 results
	ctx := g.buildGraphContext(run.DomainKey)

	if err := g.store.TransitionScanRun(run.RunID, "phase2_confirm", len(triggerFiles)); err != nil {
		return nil, err
	}
	g.emitter.Emit(ScanEvent{
		Kind:  EventPhaseChanged,
		Phase: "phase2_confirm",
		Data: map[string]any{
			"trigger_files": len(triggerFiles),
			"message":       fmt.Sprintf("Found %d trigger files for flow tracing. Awaiting user confirmation.",
				len(triggerFiles)),
		},
	})
	summary := g.buildScanSummary(run.DomainKey)
	return &ScanAction{
		ScanRunID:    run.RunID,
		Phase:        "phase2_confirm",
		Action:       "confirm",
		Files:        mapKeys(triggerFiles),
		FactSchema:   g.getGuide("guide_flow"),
		GraphContext: ctx,
		Reason:       "STOP: Show phase 1 summary to the user. Call chronicle_scan_confirm before flow tracing.",
		Progress:     &ScanProgress{Total: len(triggerFiles), Extracted: 0, Remaining: len(triggerFiles)},
		Blocked:      true,
		Checkpoint: &ScanCheckpoint{
			ID:       "phase1_summary",
			Question: "Phase 1 complete. Here's what Chronicle found. Continue with flow tracing?",
			Context:  summary,
			Options:  []string{"yes", "skip flows"},
		},
	}, nil
}

// phase2ExtractAction claims ONE trigger file, builds enriched per-file context.
func (g *Graph) phase2ExtractAction(run *store.ScanRunRow) (*ScanAction, error) {
	// Claim 1 trigger file (not a batch — flow tracing needs focus)
	claimed, err := g.store.ClaimObligations(run.RevisionID, "trace_flow", 1)
	if err != nil {
		return nil, err
	}

	if len(claimed) > 0 {
		triggerFile := claimed[0].TargetKey
		flowCtx := g.buildFlowContext(run.DomainKey, triggerFile)
		pending, _ := g.store.CountPendingObligations(run.RevisionID, "trace_flow")

		g.emitter.Emit(ScanEvent{
			Kind:  EventFlowTraced,
			Phase: "phase2_extract",
			Data: map[string]any{
				"trigger_file": flowCtx.TriggerFile,
				"trigger_node": flowCtx.TriggerNode,
			},
		})

		return &ScanAction{
			ScanRunID:   run.RunID,
			Phase:       "phase2_extract",
			Action:      "trace_flow",
			Files:       []string{triggerFile},
			FactSchema:  g.getGuide("guide_flow"),
			FlowContext: flowCtx,
			Reason:      "Read trigger_file and files_to_read from flow_context. Use reachable graph as navigation. Emit one flow fact per endpoint/consumer.",
			Progress:    &ScanProgress{Total: run.TotalFiles, Extracted: run.TotalFiles - pending, Remaining: pending},
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

	g.store.TransitionScanRun(run.RunID, "phase2_resolve", 0)
	return &ScanAction{
		ScanRunID: run.RunID,
		Phase:     "phase2_resolve",
		Action:    "call_resolve_extractions",
		Blocked:   true,
		Reason:    "All flows traced. Call chronicle_resolve_extractions to add flows to graph.",
	}, nil
}

// buildPackSelection returns core packs that are always loaded.
// Technology packs are selected by the agent based on match sections.
func (g *Graph) buildPackSelection() *prompts.PackSelection {
	sel := prompts.PackSelection{}
	for _, p := range prompts.CorePacks() {
		sel.Loaded = append(sel.Loaded, prompts.PackMeta{
			ID: p.ID, Type: p.Type, Description: p.Description,
		})
	}
	return &sel
}

// buildFlowContext builds enriched per-trigger context by walking the phase 1 graph.
// BFS from trigger node, depth 2, following INJECTS/CALLS_SERVICE/USES_MODEL/PUBLISHES/CONSUMES edges.
func (g *Graph) buildFlowContext(domainKey, triggerFilePath string) *FlowContext {
	ctx := &FlowContext{
		TriggerFile: triggerFilePath,
		ModelHint:   "use sonnet or better model for flow tracing",
	}

	// Find trigger node by file_path
	nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	var rootKey string
	for _, n := range nodes {
		if n.FilePath == triggerFilePath {
			rootKey = n.NodeKey
			ctx.TriggerNode = n.NodeKey
			ctx.TriggerType = n.NodeType
			break
		}
	}
	if rootKey == "" {
		ctx.FilesToRead = []string{triggerFilePath}
		return ctx
	}

	// BFS from root, depth 2
	active := true
	allEdges, _ := g.store.ListEdges(store.EdgeFilter{Active: &active})

	// Build adjacency: from_key → [(edge_type, to_key)]
	adj := make(map[string][]struct{ edgeType, toKey string })
	for _, e := range allEdges {
		adj[e.FromNodeKey] = append(adj[e.FromNodeKey], struct{ edgeType, toKey string }{e.EdgeType, e.ToNodeKey})
	}

	// Node lookup
	nodeMap := make(map[string]store.NodeRow)
	for _, n := range nodes {
		nodeMap[n.NodeKey] = n
	}

	// BFS
	type bfsItem struct {
		key   string
		depth int
	}
	visited := make(map[string]int) // key → depth
	queue := []bfsItem{{rootKey, 0}}
	visited[rootKey] = 0

	followEdges := map[string]bool{
		"INJECTS": true, "CONTAINS": true, "CALLS_SERVICE": true,
		"USES_MODEL": true, "PUBLISHES_TOPIC": true, "CONSUMES_TOPIC": true,
		"EXPOSES_ENDPOINT": true, "CALLS_ENDPOINT": true,
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		// Collect edges for this node
		var edgeDescs []string
		for _, e := range adj[item.key] {
			toName := e.toKey
			if n, ok := nodeMap[e.toKey]; ok {
				toName = n.Name
			}
			edgeDescs = append(edgeDescs, e.edgeType+"→"+toName)

			// Continue BFS for structural edges
			if item.depth < 2 && followEdges[e.edgeType] {
				if _, seen := visited[e.toKey]; !seen {
					visited[e.toKey] = item.depth + 1
					queue = append(queue, bfsItem{e.toKey, item.depth + 1})
				}
			}
		}

		n := nodeMap[item.key]
		ctx.Reachable = append(ctx.Reachable, ReachableNode{
			Name:     n.Name,
			NodeType: n.NodeType,
			File:     n.FilePath,
			Depth:    item.depth,
			Edges:    edgeDescs,
		})
	}

	// Collect files_to_read (unique, max 8)
	seen := make(map[string]bool)
	for _, r := range ctx.Reachable {
		if r.File != "" && !seen[r.File] && len(ctx.FilesToRead) < 8 {
			ctx.FilesToRead = append(ctx.FilesToRead, r.File)
			seen[r.File] = true
		}
	}

	return ctx
}

// buildGraphContext extracts known entities from the phase 1 graph for flow tracing agents.
func (g *Graph) buildGraphContext(domainKey string) *GraphContext {
	ctx := &GraphContext{}

	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	if err != nil {
		return ctx
	}

	for _, n := range nodes {
		switch {
		case n.Layer == "contract" && n.NodeType == "endpoint":
			ctx.Endpoints = append(ctx.Endpoints, n.Name)
		case n.Layer == "code" && (n.NodeType == "provider" || n.NodeType == "controller"):
			ctx.Services = append(ctx.Services, n.Name)
		case n.Layer == "contract" && n.NodeType == "topic":
			ctx.Topics = append(ctx.Topics, n.Name)
		}
	}
	return ctx
}

// buildScanSummary returns a summary of the current graph state for the given domain.
func (g *Graph) buildScanSummary(domainKey string) map[string]any {
	summary := map[string]any{}

	gstats := g.BuildGraphStats(domainKey)
	summary["nodes_total"] = gstats.NodesTotal
	summary["nodes_by_layer"] = gstats.NodesByLayer
	summary["nodes_by_type"] = gstats.NodesByType
	summary["edges_total"] = gstats.EdgesTotal
	summary["edges_by_derivation"] = gstats.EdgesByDeriv
	summary["quality_warnings"] = g.BuildScanQualityReport(domainKey)

	if run, _ := g.store.GetActiveScanRun(domainKey); run != nil {
		if status, err := g.BuildScanRunStatus(run, "scan_file"); err == nil {
			summary["extractions"] = status.Extractions
			if len(status.ReviewCandidates) > 0 {
				summary["review_candidates"] = status.ReviewCandidates
			}
		}
	}

	return summary
}

// formatScanSummary formats a scan summary map as a human-readable string.
func formatScanSummary(summary map[string]any) string {
	nodes, _ := summary["nodes_total"].(int)
	edges, _ := summary["edges_total"].(int)

	byType, _ := summary["nodes_by_type"].(map[string]int)
	byDeriv, _ := summary["edges_by_derivation"].(map[string]int)

	parts := []string{fmt.Sprintf("Scan complete. %d nodes, %d edges.", nodes, edges)}

	if len(byType) > 0 {
		parts = append(parts, "\nFound:")
		for t, c := range byType {
			if c > 0 {
				parts = append(parts, fmt.Sprintf("  %d %s", c, t))
			}
		}
	}

	if len(byDeriv) > 0 {
		parts = append(parts, "\nConfidence:")
		total := 0
		for _, c := range byDeriv {
			total += c
		}
		for d, c := range byDeriv {
			if total > 0 {
				pct := c * 100 / total
				parts = append(parts, fmt.Sprintf("  %d%% %s (%d edges)", pct, d, c))
			}
		}
	}

	return strings.Join(parts, "\n")
}

// readFileContent tries to read a file relative to cwd, parent dirs, or monorepo subprojects.
func readFileContent(filePath string) []byte {
	tryRead := func(base string) []byte {
		candidate := filePath
		if base != "" {
			candidate = filepath.Join(base, filePath)
		}
		if content, err := os.ReadFile(candidate); err == nil {
			return content
		}
		return nil
	}

	if content := tryRead(""); content != nil {
		return content
	}

	dir, _ := os.Getwd()
	for depth := 0; depth < 6; depth++ {
		if content := tryRead(dir); content != nil {
			return content
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(dir, e.Name())
			if _, err := os.Stat(filepath.Join(sub, ".depbot")); err != nil {
				continue
			}
			if content := tryRead(sub); content != nil {
				return content
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

func isTypeScriptFile(path string) bool {
	return strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// countASTFacts counts total pre-extracted AST facts across a batch.
func countASTFacts(files []FileWithAST) int {
	count := 0
	for _, f := range files {
		if f.ASTFacts == "" || f.ASTFacts == "[]" {
			continue
		}
		var facts []json.RawMessage
		if err := json.Unmarshal([]byte(f.ASTFacts), &facts); err == nil {
			count += len(facts)
		}
	}
	return count
}

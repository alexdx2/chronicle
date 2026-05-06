package graph

import "github.com/alexdx2/chronicle-core/store"

// ScanAction is what chronicle_scan_next_file returns to Claude.
type ScanAction struct {
	ScanRunID    int64          `json:"scan_run_id,omitempty"`
	Phase        string         `json:"phase"`
	Action       string         `json:"action"` // start_scan, extract_files, call_resolve_extractions, discover_files, wait, trace_flow, none
	Files        []string       `json:"files,omitempty"`
	Blocked      bool           `json:"blocked,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Done         bool           `json:"done,omitempty"`
	Progress     *ScanProgress  `json:"progress,omitempty"`
	FactSchema   string         `json:"fact_schema,omitempty"`  // included with extract_files/trace_flow to guide agents
	GraphContext *GraphContext  `json:"graph_context,omitempty"` // phase 2 only — known nodes/edges from phase 1
}

// GraphContext provides phase 1 graph data to help flow tracing agents.
type GraphContext struct {
	Endpoints []string `json:"endpoints,omitempty"` // known endpoint names
	Services  []string `json:"services,omitempty"`  // known service/provider nodes
	Topics    []string `json:"topics,omitempty"`    // known event/message topics
}

const factSchemaGuide = `OUTPUT FORMAT: JSON array of objects. Use EXACTLY these field names — no aliases.

EXAMPLES (copy the field names exactly):

import { OrderService } from './order.service';
→ {"kind":"import","to":"./order.service","symbols":["OrderService"]}

@Controller('orders')
@Get('/:id')
→ {"kind":"endpoint","method":"GET","target":"/orders/:id"}

await fetch('http://user-api:3000/users/me')
→ {"kind":"http_call","method":"GET","target":"http://user-api:3000/users/me"}

constructor(private orderService: OrderService)
→ {"kind":"injects","to":"OrderService"}

this.orderService.create(dto)
→ {"kind":"call","to":"OrderService","method":"create"}

this.prisma.order.create(...)
→ {"kind":"model","to":"Order"}

this.eventBus.emit('order.created', data)
→ {"kind":"produces","to":"order.created","method":"emit"}

@OnEvent('order.created')
handleOrderCreated(data) { ... }
→ {"kind":"consumes","to":"order.created","method":"handleOrderCreated"}

"dependencies": { "express": "^4.0" }
→ {"kind":"dependency","to":"express"}

model User { id Int @id }
→ {"kind":"model","to":"User"}

enum Role { ADMIN USER }
→ {"kind":"enum","to":"Role"}

model Post { author User @relation(...) }
→ {"kind":"model_relation","from":"Post","to":"User"}

FIELD NAMES: kind, to, symbols, method, target, from (for relations only). NO other field names.

from_type: set as a SEPARATE PARAMETER on chronicle_file_extracted (not inside facts).
- File wires components together (DI, module registry) → from_type="module"
- File defines endpoints/routes → from_type="controller"
- Everything else → omit (defaults to provider)

Do NOT emit:
- local helper/utility function calls within the same file
- type-only imports (import type { X }) unless the file is pure types
- test mocks, stubs, or test-only dependencies
- UI component imports (Button, Modal) unless they cross package boundaries
- comments, TODOs, or documentation
- framework boilerplate decorators (@Injectable, @Module) as standalone facts

Status: "extracted" if any facts found, "no_runtime_architecture" if no architecture, "type_only" for pure types, "config_only" for config, "generated" for generated code.`

const flowFactGuide = `Phase 2: FLOW TRACING ONLY. Do NOT re-extract imports, endpoints, or dependencies (already done in phase 1).

For each trigger file, identify the business use case it starts, then trace the call chain:
1. What triggers this flow? (HTTP endpoint, message consumer, event handler, cron)
2. What services does it call?
3. What's the sequence of steps?

Emit ONLY flow facts:
{"kind":"flow","flow_name":"Human-readable use case name","trigger":"POST /path OR topic-name","method":"entryMethodName","requires":["ServiceA","ServiceB"],"steps":["step 1 description","step 2 description"]}

One flow fact per business use case. A controller with 3 endpoints = 3 flow facts.
Status: "extracted" with flow facts array.

CRITICAL: facts MUST be a JSON array of objects with "kind":"flow". Do NOT re-emit import/endpoint/dependency facts.`

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

	// Build graph context from phase 1 results
	ctx := g.buildGraphContext(run.DomainKey)

	g.store.TransitionScanRun(run.RunID, "phase2_extract", len(triggerFiles))
	return &ScanAction{
		ScanRunID:    run.RunID,
		Phase:        "phase2_extract",
		Action:       "trace_flow",
		Files:        mapKeys(triggerFiles),
		FactSchema:   flowFactGuide,
		GraphContext: ctx,
		Reason:       "Phase 2: trace business flows. Use graph_context to understand the full call chain. Emit ONLY flow facts.",
		Progress:     &ScanProgress{Total: len(triggerFiles), Extracted: 0, Remaining: len(triggerFiles)},
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
			FactSchema: flowFactGuide,
			Reason:     "Phase 2: trace business flows ONLY. Do NOT re-extract imports/endpoints. Emit ONLY flow facts.",
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

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

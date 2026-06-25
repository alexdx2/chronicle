package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

// writeTempTS writes TS source to a temp file and returns its absolute path.
func writeTempTS(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "svc.ts")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write temp ts: %v", err)
	}
	return p
}

// TestComputeASTComplexityWritesMetricsAndEvidence proves the Tier-A pass reads
// the node's source file, stamps exact metrics (cyclomatic/loop_count/loop_depth)
// into Metadata with metric_sources=ast, and emits one complexity-ast evidence
// row carrying file_path + line span + confidence 1.0 — idempotently.
func TestComputeASTComplexityWritesMetricsAndEvidence(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	tsPath := writeTempTS(t, `
class OrderService {
  process(items: number[]) {
    for (const i of items) {
      while (i > 0) {
        if (i % 2 === 0) { doThing(); }
      }
    }
  }
}
`)
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "code:provider:orders:order-service", Layer: "code", NodeType: "provider",
		DomainKey: "orders", Name: "OrderService", FilePath: tsPath,
	}, revID); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	if err := g.ComputeASTComplexity(revID); err != nil {
		t.Fatalf("ComputeASTComplexity: %v", err)
	}

	n, err := g.store.GetNodeByKey("code:provider:orders:order-service")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	m, ok := complexityFromMetadata(n.Metadata)
	if !ok {
		t.Fatalf("node has no complexity metadata: %q", n.Metadata)
	}
	// process(): for-of + while + if = 3 decisions -> cyclomatic 4; 2 loops; depth 2.
	if m.Cyclomatic != 4 {
		t.Errorf("cyclomatic = %d, want 4", m.Cyclomatic)
	}
	if m.LoopCount != 2 {
		t.Errorf("loop_count = %d, want 2", m.LoopCount)
	}
	if m.LoopDepth != 2 {
		t.Errorf("loop_depth = %d, want 2", m.LoopDepth)
	}
	if !strings.Contains(n.Metadata, `"loop_depth":"ast"`) {
		t.Errorf("metric_sources should mark loop_depth as ast: %q", n.Metadata)
	}

	nodeID, _ := g.store.GetNodeIDByKey("code:provider:orders:order-service")
	evs, err := g.store.ListEvidenceByNode(nodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	astRows := 0
	for _, e := range evs {
		if e.ExtractorID == "complexity-ast" && e.SourceKind == "ast" {
			astRows++
			if e.FilePath != tsPath {
				t.Errorf("evidence file_path = %q, want %q", e.FilePath, tsPath)
			}
			if e.Confidence != 1.0 {
				t.Errorf("exact AST evidence confidence = %v, want 1.0", e.Confidence)
			}
		}
	}
	if astRows != 1 {
		t.Fatalf("want exactly 1 complexity-ast evidence row, got %d", astRows)
	}

	// Idempotent: re-run must not duplicate the row.
	if err := g.ComputeASTComplexity(revID); err != nil {
		t.Fatalf("ComputeASTComplexity (2nd): %v", err)
	}
	evs2, _ := g.store.ListEvidenceByNode(nodeID)
	rows2 := 0
	for _, e := range evs2 {
		if e.ExtractorID == "complexity-ast" && e.SourceKind == "ast" {
			rows2++
		}
	}
	if rows2 != 1 {
		t.Fatalf("re-run duplicated AST evidence: got %d rows", rows2)
	}
}

// TestFinalizeASTThenGraphChain proves the end-to-end real-scan flow: Tier-A
// metrics are extracted from source files (NOT pre-seeded in metadata), then the
// Tier-B graph pass propagates the AST-derived loop_depth across a CALLS_SYMBOL
// edge — closing the gap where transitive_loop_depth was always 0 on live scans.
func TestFinalizeASTThenGraphChain(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	// Upstream caller: no loops of its own.
	callerPath := writeTempTS(t, `
class Caller {
  run(svc) { return svc.work(); }
}
`)
	// Downstream callee: a single loop (loop_depth 1) — discovered from source.
	calleePath := writeTempTS(t, `
class Worker {
  work(items) { for (const i of items) { doThing(); } }
}
`)
	for _, n := range []validate.NodeInput{
		{NodeKey: "code:provider:orders:caller", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Caller", FilePath: callerPath},
		{NodeKey: "code:provider:orders:worker", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Worker", FilePath: calleePath},
	} {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:provider:orders:caller", ToNodeKey: "code:provider:orders:worker",
		EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code",
	}, revID); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	if _, err := g.FinalizeIncrementalScan("orders", revID); err != nil {
		t.Fatalf("FinalizeIncrementalScan: %v", err)
	}

	caller, err := g.store.GetNodeByKey("code:provider:orders:caller")
	if err != nil {
		t.Fatalf("GetNodeByKey caller: %v", err)
	}
	m, ok := complexityFromMetadata(caller.Metadata)
	if !ok {
		t.Fatalf("caller has no complexity metadata: %q", caller.Metadata)
	}
	// caller's own loop_depth is 0, callee's is 1 -> transitive_loop_depth 1.
	if m.TransitiveLoopDepth != 1 {
		t.Fatalf("caller transitive_loop_depth = %d, want 1 (AST-seeded then graph-propagated)", m.TransitiveLoopDepth)
	}
}

// TestInsightsVerificationWeightedByComplexity proves a higher-complexity source
// outranks a lower-complexity one even when degree and trust are equal and the
// EdgeKey tiebreak would otherwise put it last.
func TestInsightsVerificationWeightedByComplexity(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	nodes := []validate.NodeInput{
		// Low-complexity source: no complexity metadata. Edge key sorts first.
		{NodeKey: "code:provider:orders:aaa-cold", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "AaaCold"},
		// High-complexity source: transitive_loop_depth 5 -> normComplexity 0.6.
		{NodeKey: "code:provider:orders:zzz-hot", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "ZzzHot",
			Metadata: `{"complexity":{"transitive_loop_depth":5,"cyclomatic":0}}`},
		{NodeKey: "code:provider:orders:t-a", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Ta"},
		{NodeKey: "code:provider:orders:t-z", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Tz"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}
	// Two low-trust (inferred) edges, equal source degree (1) and equal trust.
	edges := []validate.EdgeInput{
		{FromNodeKey: "code:provider:orders:aaa-cold", ToNodeKey: "code:provider:orders:t-a", EdgeType: "CALLS_SYMBOL", DerivationKind: "inferred", FromLayer: "code", ToLayer: "code"},
		{FromNodeKey: "code:provider:orders:zzz-hot", ToNodeKey: "code:provider:orders:t-z", EdgeType: "CALLS_SYMBOL", DerivationKind: "inferred", FromLayer: "code", ToLayer: "code"},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge %s->%s: %v", e.FromNodeKey, e.ToNodeKey, err)
		}
	}

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if len(ins.VerificationTargets) != 2 {
		t.Fatalf("want 2 verification targets, got %d", len(ins.VerificationTargets))
	}
	if ins.VerificationTargets[0].From != "code:provider:orders:zzz-hot" {
		t.Fatalf("high-complexity source should rank first, got %q", ins.VerificationTargets[0].From)
	}
	if !strings.Contains(ins.VerificationTargets[0].Reason, "src cx=") {
		t.Fatalf("reason should expose source complexity, got %q", ins.VerificationTargets[0].Reason)
	}
}

// TestComputeGraphComplexityWritesMetricsAndEvidence proves the keystone graph
// pass writes derived metrics into node Metadata (preserving Tier-A) and emits a
// graph-derived evidence row.
func TestComputeGraphComplexityWritesMetricsAndEvidence(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	nodes := []validate.NodeInput{
		{NodeKey: "code:provider:orders:a", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "A",
			Metadata: `{"complexity":{"cyclomatic":3,"loop_count":1,"loop_depth":1,"metric_sources":{"loop_depth":"ast"}}}`},
		{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B",
			Metadata: `{"complexity":{"cyclomatic":2,"loop_count":1,"loop_depth":1,"metric_sources":{"loop_depth":"ast"}}}`},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:provider:orders:a", ToNodeKey: "code:provider:orders:b",
		EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code",
	}, revID); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	if err := g.ComputeGraphComplexity(revID); err != nil {
		t.Fatalf("ComputeGraphComplexity: %v", err)
	}

	na, err := g.store.GetNodeByKey("code:provider:orders:a")
	if err != nil {
		t.Fatalf("GetNodeByKey A: %v", err)
	}
	m, ok := complexityFromMetadata(na.Metadata)
	if !ok {
		t.Fatalf("A has no complexity metadata: %q", na.Metadata)
	}
	if m.TransitiveLoopDepth != 2 {
		t.Fatalf("A transitive_loop_depth = %d, want 2", m.TransitiveLoopDepth)
	}
	if m.Cyclomatic != 3 {
		t.Fatalf("A cyclomatic should be preserved (3), got %d", m.Cyclomatic)
	}
	if m.Recursive {
		t.Fatalf("A should not be recursive")
	}

	aID, _ := g.store.GetNodeIDByKey("code:provider:orders:a")
	evs, err := g.store.ListEvidenceByNode(aID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	graphRows := 0
	for _, e := range evs {
		if e.ExtractorID == "complexity-graph" && e.SourceKind == "graph" {
			graphRows++
		}
	}
	if graphRows != 1 {
		t.Fatalf("want exactly 1 complexity-graph evidence row on A, got %d", graphRows)
	}

	// Idempotent: a second pass must not duplicate the evidence row.
	if err := g.ComputeGraphComplexity(revID); err != nil {
		t.Fatalf("ComputeGraphComplexity (2nd): %v", err)
	}
	evs2, _ := g.store.ListEvidenceByNode(aID)
	graphRows2 := 0
	for _, e := range evs2 {
		if e.ExtractorID == "complexity-graph" && e.SourceKind == "graph" {
			graphRows2++
		}
	}
	if graphRows2 != 1 {
		t.Fatalf("re-run must not duplicate evidence: got %d complexity-graph rows", graphRows2)
	}
}

// TestFinalizeRunsComplexityPass proves the complexity pass is wired into scan
// finalize (runs automatically, not only via explicit call).
func TestFinalizeRunsComplexityPass(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	for _, n := range []validate.NodeInput{
		{NodeKey: "code:provider:orders:a", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "A",
			Metadata: `{"complexity":{"loop_depth":1,"metric_sources":{"loop_depth":"ast"}}}`},
		{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B",
			Metadata: `{"complexity":{"loop_depth":1,"metric_sources":{"loop_depth":"ast"}}}`},
	} {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:provider:orders:a", ToNodeKey: "code:provider:orders:b",
		EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code",
	}, revID); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	if _, err := g.FinalizeIncrementalScan("orders", revID); err != nil {
		t.Fatalf("FinalizeIncrementalScan: %v", err)
	}

	na, err := g.store.GetNodeByKey("code:provider:orders:a")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	m, ok := complexityFromMetadata(na.Metadata)
	if !ok || m.TransitiveLoopDepth != 2 {
		t.Fatalf("finalize should have derived transitive_loop_depth=2, got ok=%v %+v", ok, m)
	}
}

// TestInsightsHotPathTargets proves a complex, highly-connected, TRUSTED function
// (invisible to verification targets because its edges are high-trust) surfaces in
// the new hot-path section with a reason tag.
func TestInsightsHotPathTargets(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	nodes := []validate.NodeInput{
		{NodeKey: "code:provider:orders:hot", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Hot",
			Metadata: `{"complexity":{"cyclomatic":10,"transitive_loop_depth":6,"recursive":true}}`},
		{NodeKey: "code:provider:orders:c1", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C1"},
		{NodeKey: "code:provider:orders:c2", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C2"},
		{NodeKey: "code:provider:orders:c3", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C3"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}
	// High-trust (hard) edges: hot is highly connected but NOT a verification target.
	for _, to := range []string{"c1", "c2", "c3"} {
		e := validate.EdgeInput{FromNodeKey: "code:provider:orders:hot", ToNodeKey: "code:provider:orders:" + to,
			EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge ->%s: %v", to, err)
		}
	}

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if len(ins.HotPathTargets) == 0 {
		t.Fatalf("hot node should surface in hot-path targets")
	}
	if ins.HotPathTargets[0].NodeKey != "code:provider:orders:hot" {
		t.Fatalf("top hot-path target = %q, want hot", ins.HotPathTargets[0].NodeKey)
	}
	if !strings.Contains(ins.HotPathTargets[0].Reason, "complex") {
		t.Fatalf("reason should tag the dominant factor, got %q", ins.HotPathTargets[0].Reason)
	}
	// Hot-path is complexity-driven: a node with no complexity metadata is absent.
	for _, n := range ins.HotPathTargets {
		if n.NodeKey == "code:provider:orders:c1" {
			t.Fatalf("non-complex node c1 must not appear in hot-path targets")
		}
	}
}

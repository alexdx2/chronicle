package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

func TestIsTestFilePath(t *testing.T) {
	yes := []string{
		"src/a.spec.ts", "src/a.test.ts", "pkg/x_test.go",
		"src/__tests__/a.ts", "tests/a.py", "src/test_util.py",
	}
	no := []string{"src/a.ts", "pkg/x.go", "src/testimonials.ts", "src/contest.ts"}
	for _, p := range yes {
		if !isTestFilePath(p) {
			t.Errorf("%s should be a test file", p)
		}
	}
	for _, p := range no {
		if isTestFilePath(p) {
			t.Errorf("%s should NOT be a test file", p)
		}
	}
}

// TestFindTestFile pins the naming-convention lookup: sibling .spec/.test,
// __tests__/ subdir, and Go's _test.go.
func TestFindTestFile(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel string) string {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	src := mk("svc/orders.ts")
	if got := findTestFile(src); got != "" {
		t.Fatalf("no test file yet, got %q", got)
	}
	spec := mk("svc/orders.spec.ts")
	if got := findTestFile(src); got != spec {
		t.Fatalf("sibling spec: got %q, want %q", got, spec)
	}

	src2 := mk("svc/billing.ts")
	nested := mk("svc/__tests__/billing.test.ts")
	if got := findTestFile(src2); got != nested {
		t.Fatalf("__tests__ lookup: got %q, want %q", got, nested)
	}

	goSrc := mk("pkg/store.go")
	goTest := mk("pkg/store_test.go")
	if got := findTestFile(goSrc); got != goTest {
		t.Fatalf("go convention: got %q, want %q", got, goTest)
	}
}

// TestComputeTestSignalsEndToEnd proves the pass stamps tests metadata + a
// heuristic evidence row, and skips test files themselves.
func TestComputeTestSignalsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tested := write("orders.ts", "export class Orders {}\n")
	write("orders.spec.ts", "import { Orders } from './orders';\n")
	untested := write("billing.ts", "export class Billing {}\n")

	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	for _, n := range []validate.NodeInput{
		{NodeKey: "code:provider:orders:orders", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Orders", FilePath: tested},
		{NodeKey: "code:provider:orders:billing", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Billing", FilePath: untested},
	} {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}

	if err := g.ComputeTestSignals(revID); err != nil {
		t.Fatalf("ComputeTestSignals: %v", err)
	}

	no, err := g.store.GetNodeByKey("code:provider:orders:orders")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if !strings.Contains(no.Metadata, `"has_test_file":true`) {
		t.Errorf("tested node metadata: %q", no.Metadata)
	}
	nb, _ := g.store.GetNodeByKey("code:provider:orders:billing")
	if !strings.Contains(nb.Metadata, `"has_test_file":false`) {
		t.Errorf("untested node metadata should record the looked-but-absent fact: %q", nb.Metadata)
	}

	oID, _ := g.store.GetNodeIDByKey("code:provider:orders:orders")
	evs, _ := g.store.ListEvidenceByNode(oID)
	rows := 0
	for _, e := range evs {
		if e.ExtractorID == "test-link" {
			rows++
			if e.SourceKind != "file" {
				t.Errorf("test-link source_kind = %q, want file", e.SourceKind)
			}
			if e.Confidence >= 1.0 {
				t.Errorf("naming-convention linkage is heuristic, confidence = %v, want < 1.0", e.Confidence)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("want 1 test-link evidence row, got %d", rows)
	}
}

// TestInsightsHotPathFlagsUntested proves an untested complex node outranks an
// otherwise-equal tested one and the reason says "untested" — but only when the
// test pass actually looked (no fabricated claims for unscanned nodes).
func TestInsightsHotPathFlagsUntested(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	nodes := []validate.NodeInput{
		{NodeKey: "code:provider:orders:covered", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Covered",
			Metadata: `{"complexity":{"cyclomatic":10},"tests":{"has_test_file":true}}`},
		{NodeKey: "code:provider:orders:naked", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Naked",
			Metadata: `{"complexity":{"cyclomatic":10},"tests":{"has_test_file":false}}`},
		{NodeKey: "code:provider:orders:v1", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "V1"},
		{NodeKey: "code:provider:orders:v2", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "V2"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	for _, e := range []validate.EdgeInput{
		{FromNodeKey: "code:provider:orders:covered", ToNodeKey: "code:provider:orders:v1", EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		{FromNodeKey: "code:provider:orders:naked", ToNodeKey: "code:provider:orders:v2", EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
	} {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge: %v", err)
		}
	}

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if len(ins.HotPathTargets) < 2 {
		t.Fatalf("want both nodes, got %d", len(ins.HotPathTargets))
	}
	if ins.HotPathTargets[0].NodeKey != "code:provider:orders:naked" {
		t.Fatalf("untested node should rank first, got %q", ins.HotPathTargets[0].NodeKey)
	}
	if !strings.Contains(ins.HotPathTargets[0].Reason, "untested") {
		t.Fatalf("reason should say untested, got %q", ins.HotPathTargets[0].Reason)
	}
	// The tested node must NOT be called untested.
	for _, h := range ins.HotPathTargets {
		if h.NodeKey == "code:provider:orders:covered" && strings.Contains(h.Reason, "untested") {
			t.Fatalf("covered node wrongly labeled untested: %q", h.Reason)
		}
	}
}

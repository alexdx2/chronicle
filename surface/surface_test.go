package surface

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

const fixturePath = "../testdata/surface/mini.surface.json"

// setupSeededGraph builds a graph holding exactly the code-side nodes the
// surface extract expects to find: the two Shop fields it edits, the mutation
// its controls write through, and the page endpoint that serves its screen.
// Key spellings are the ones a real scan produces (verified against a live DB).
func setupSeededGraph(t *testing.T) *graph.Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	g := graph.New(s, reg)

	revID, err := s.CreateRevision("mini", "", "seed0000", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	payload := graph.ImportPayload{Nodes: []graph.ImportNode{
		{NodeKey: "data:model:mini:shop", Layer: "data", NodeType: "model", DomainKey: "mini", Name: "Shop"},
		{NodeKey: "data:field:mini:shop/quiet-from-hour", Layer: "data", NodeType: "field", DomainKey: "mini", Name: "quietFromHour"},
		{NodeKey: "data:field:mini:shop/sms-sender-id", Layer: "data", NodeType: "field", DomainKey: "mini", Name: "smsSenderId"},
		{NodeKey: "contract:endpoint:mini:mutation:/saveshopsettings", Layer: "contract", NodeType: "endpoint", DomainKey: "mini", Name: "saveShopSettings"},
		{NodeKey: "contract:endpoint:mini:get:/panel/ustawienia", Layer: "contract", NodeType: "endpoint", DomainKey: "mini", Name: "GET /panel/ustawienia"},
	}}
	res, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("seed ImportAll: %v", err)
	}
	if len(res.Rejected) > 0 {
		t.Fatalf("seed rejected: %+v", res.Rejected)
	}
	return g
}

func loadFixture(t *testing.T) *File {
	t.Helper()
	f, err := Load(fixturePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

func TestLoadFixture(t *testing.T) {
	f := loadFixture(t)
	if f.ContentHash == "" {
		t.Fatal("ContentHash empty")
	}
	if f.SchemaVersion != 1 || f.Product != "mini" || f.Repo != "mini" {
		t.Fatalf("header wrong: %+v", f.Header())
	}
	if f.Commit != "0000000000000000000000000000000000000001" {
		t.Fatalf("commit %q", f.Commit)
	}
	counts := map[string]int{}
	for _, n := range f.Nodes {
		counts[n.Kind]++
	}
	// 5 ui nodes (product + screen + panel + 2 controls) and 2 field
	// references. Field nodes are NOT ui nodes: they point at data:field
	// nodes the scan already made.
	want := map[string]int{"product": 1, "screen": 1, "panel": 1, "control": 2, "field": 2}
	for k, v := range want {
		if counts[k] != v {
			t.Fatalf("kind %s: got %d want %d", k, counts[k], v)
		}
	}
	if len(f.UINodes()) != 4+1 {
		t.Fatalf("UINodes = %d, want 5", len(f.UINodes()))
	}
	if len(f.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(f.Edges))
	}
	// decisions: the controls group is understood, the homes group survives
	// parsing without exploding.
	if got := f.Decisions["controls"]["panel.ustawienia.komunikacja.smsSenderId"].Verdict; got != "ask" {
		t.Fatalf("control decision verdict = %q", got)
	}
}

func TestLoadRejectsUnknownSchemaVersion(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	bumped := strings.Replace(string(raw), `"schemaVersion": 1`, `"schemaVersion": 7`, 1)
	p := filepath.Join(t.TempDir(), "v7.surface.json")
	if err := os.WriteFile(p, []byte(bumped), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(p)
	if err == nil {
		t.Fatal("expected error for schemaVersion 7")
	}
	if !strings.Contains(err.Error(), "unsupported schemaVersion 7 (supported: 1)") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRequiresSchemaVersion(t *testing.T) {
	raw, _ := os.ReadFile(fixturePath)
	stripped := strings.Replace(string(raw), `"schemaVersion": 1,`, ``, 1)
	p := filepath.Join(t.TempDir(), "v0.surface.json")
	if err := os.WriteFile(p, []byte(stripped), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for missing schemaVersion")
	}
}

func TestResolver(t *testing.T) {
	g := setupSeededGraph(t)
	r := &Resolver{Store: g.Store(), Domain: "mini"}

	got, err := r.FieldKey("Shop.quietFromHour")
	if err != nil {
		t.Fatalf("FieldKey: %v", err)
	}
	if got != "data:field:mini:shop/quiet-from-hour" {
		t.Fatalf("FieldKey = %q", got)
	}
	if _, err := r.FieldKey("Shop.noSuchColumn"); err == nil {
		t.Fatal("expected FieldKey error for absent field")
	}

	got, err = r.MutationKey("saveShopSettings")
	if err != nil {
		t.Fatalf("MutationKey: %v", err)
	}
	if got != "contract:endpoint:mini:mutation:/saveshopsettings" {
		t.Fatalf("MutationKey = %q", got)
	}
	if _, err := r.MutationKey("noSuchMutation"); err == nil {
		t.Fatal("expected MutationKey error for absent mutation")
	}

	got, err = r.RouteKey("/panel/ustawienia")
	if err != nil {
		t.Fatalf("RouteKey: %v", err)
	}
	if got != "contract:endpoint:mini:get:/panel/ustawienia" {
		t.Fatalf("RouteKey = %q", got)
	}
	if _, err := r.RouteKey("/nowhere"); err == nil {
		t.Fatal("expected RouteKey error for absent route")
	}
}

// TestMutationKeyFallback: the direct key is absent, but one endpoint's NAME
// matches the mutation once punctuation and case are dropped.
func TestMutationKeyFallback(t *testing.T) {
	g := setupSeededGraph(t)
	revID, _ := g.Store().CreateRevision("mini", "", "seed0001", "manual", "incremental", "{}")
	_, err := g.ImportAll(graph.ImportPayload{Nodes: []graph.ImportNode{
		{NodeKey: "contract:endpoint:mini:post:/graphql/save-voice-config", Layer: "contract", NodeType: "endpoint", DomainKey: "mini", Name: "saveVoiceConfig"},
	}}, revID)
	if err != nil {
		t.Fatal(err)
	}
	r := &Resolver{Store: g.Store(), Domain: "mini"}
	got, err := r.MutationKey("saveVoiceConfig")
	if err != nil {
		t.Fatalf("MutationKey fallback: %v", err)
	}
	if got != "contract:endpoint:mini:post:/graphql/save-voice-config" {
		t.Fatalf("MutationKey fallback = %q", got)
	}
}

func TestPlan(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	r := &Resolver{Store: g.Store(), Domain: "mini"}

	payload, keys, unresolved, err := Plan(f, r, Options{Domain: "mini"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(unresolved) != 0 {
		t.Fatalf("unresolved: %+v", unresolved)
	}

	// --- nodes ---------------------------------------------------------
	if len(payload.Nodes) != 5 {
		t.Fatalf("nodes = %d, want 5", len(payload.Nodes))
	}
	byKey := map[string]graph.ImportNode{}
	for _, n := range payload.Nodes {
		if n.Layer != "ui" {
			t.Fatalf("node %s layer %q", n.NodeKey, n.Layer)
		}
		byKey[n.NodeKey] = n
	}
	for _, want := range []struct{ key, nodeType, name, qname string }{
		{"ui:product:mini:mini", "product", "mini", "mini"},
		{"ui:screen:mini:panel-ustawienia", "screen", "ustawienia", "panel.ustawienia"},
		{"ui:panel:mini:panel-ustawienia-komunikacja", "panel", "komunikacja", "panel.ustawienia.komunikacja"},
		{"ui:control:mini:panel-ustawienia-komunikacja-sms-sender-id", "control", "smsSenderId", "panel.ustawienia.komunikacja.smsSenderId"},
		{"ui:control:mini:panel-ustawienia-komunikacja-quiet-from-hour", "control", "quietFromHour", "panel.ustawienia.komunikacja.quietFromHour"},
	} {
		n, ok := byKey[want.key]
		if !ok {
			t.Fatalf("missing node %s (have %v)", want.key, keysOf(byKey))
		}
		if n.NodeType != want.nodeType || n.Name != want.name || n.QualifiedName != want.qname {
			t.Fatalf("node %s = type %q name %q qname %q", want.key, n.NodeType, n.Name, n.QualifiedName)
		}
		var md struct{ Product, Address, Kind string }
		if err := json.Unmarshal([]byte(n.Metadata), &md); err != nil {
			t.Fatalf("node %s metadata %q: %v", want.key, n.Metadata, err)
		}
		if md.Product != "mini" || md.Address != want.qname || md.Kind != want.nodeType {
			t.Fatalf("node %s metadata = %+v", want.key, md)
		}
		if n.FilePath == "" {
			t.Fatalf("node %s has no file_path", want.key)
		}
	}
	if len(keys) != 5 {
		t.Fatalf("ui keys = %d, want 5", len(keys))
	}

	// --- edges ---------------------------------------------------------
	type pair struct{ from, to, kind string }
	got := map[pair]bool{}
	for _, e := range payload.Edges {
		got[pair{e.FromNodeKey, e.ToNodeKey, e.EdgeType}] = true
	}
	for _, want := range []pair{
		// CONTAINS chain product → screen → panel → control
		{"ui:product:mini:mini", "ui:screen:mini:panel-ustawienia", "CONTAINS"},
		{"ui:screen:mini:panel-ustawienia", "ui:panel:mini:panel-ustawienia-komunikacja", "CONTAINS"},
		{"ui:panel:mini:panel-ustawienia-komunikacja", "ui:control:mini:panel-ustawienia-komunikacja-sms-sender-id", "CONTAINS"},
		{"ui:panel:mini:panel-ustawienia-komunikacja", "ui:control:mini:panel-ustawienia-komunikacja-quiet-from-hour", "CONTAINS"},
		// the screen is served by its page endpoint
		{"ui:screen:mini:panel-ustawienia", "contract:endpoint:mini:get:/panel/ustawienia", "SERVED_BY"},
		// both controls write through the same mutation
		{"ui:control:mini:panel-ustawienia-komunikacja-sms-sender-id", "contract:endpoint:mini:mutation:/saveshopsettings", "WRITES_VIA"},
		{"ui:control:mini:panel-ustawienia-komunikacja-quiet-from-hour", "contract:endpoint:mini:mutation:/saveshopsettings", "WRITES_VIA"},
		// edits edges become WRITES_FIELD onto the real data:field nodes
		{"ui:control:mini:panel-ustawienia-komunikacja-sms-sender-id", "data:field:mini:shop/sms-sender-id", "WRITES_FIELD"},
		{"ui:control:mini:panel-ustawienia-komunikacja-quiet-from-hour", "data:field:mini:shop/quiet-from-hour", "WRITES_FIELD"},
	} {
		if !got[want] {
			t.Fatalf("missing edge %+v", want)
		}
	}
	if len(payload.Edges) != 9 {
		t.Fatalf("edges = %d, want 9", len(payload.Edges))
	}

	// --- evidence ------------------------------------------------------
	var extract, declared int
	var decisionEv *graph.ImportEvidence
	for i, ev := range payload.Evidence {
		switch ev.SourceKind {
		case "surface_extract":
			extract++
			if ev.ExtractorID != "okeep-surface" || ev.ExtractorVersion != "1" {
				t.Fatalf("evidence %d extractor = %s/%s", i, ev.ExtractorID, ev.ExtractorVersion)
			}
			if ev.CommitSHA != f.Commit {
				t.Fatalf("evidence %d commit = %q", i, ev.CommitSHA)
			}
		case "declared":
			declared++
			decisionEv = &payload.Evidence[i]
		default:
			t.Fatalf("evidence %d source_kind = %q", i, ev.SourceKind)
		}
	}
	if extract != len(payload.Nodes)+len(payload.Edges) {
		t.Fatalf("surface_extract evidence = %d, want %d", extract, len(payload.Nodes)+len(payload.Edges))
	}
	if declared != 1 || decisionEv == nil {
		t.Fatalf("declared evidence = %d", declared)
	}
	if decisionEv.AssertionKind != "decision" {
		t.Fatalf("decision assertion_kind = %q", decisionEv.AssertionKind)
	}
	if decisionEv.NodeKey != "ui:control:mini:panel-ustawienia-komunikacja-sms-sender-id" {
		t.Fatalf("decision target = %q", decisionEv.NodeKey)
	}
	var d Decision
	if err := json.Unmarshal([]byte(decisionEv.Assertion), &d); err != nil {
		t.Fatalf("decision assertion %q: %v", decisionEv.Assertion, err)
	}
	if d.Verdict != "ask" || d.Placement != "settings" || d.Owner != "Shop" {
		t.Fatalf("decision assertion = %+v", d)
	}
	if d.Evidence != nil {
		t.Fatal("decision assertion must not carry its own evidence locator")
	}
	if decisionEv.FilePath != "src/surface/decisions.ts" || int(decisionEv.LineStart) != 12 {
		t.Fatalf("decision evidence loc = %s:%d", decisionEv.FilePath, decisionEv.LineStart)
	}
}

// TestPlanUnresolvedVia: a control points at a mutation the graph has never
// seen. Strict mode refuses the whole plan; AllowUnresolved drops that one
// edge and reports it.
func TestPlanUnresolvedVia(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	for i := range f.Nodes {
		if f.Nodes[i].Kind == "control" && strings.HasSuffix(f.Nodes[i].Address, "smsSenderId") {
			f.Nodes[i].Via = []string{"ghostMutation"}
		}
	}
	r := &Resolver{Store: g.Store(), Domain: "mini"}

	if _, _, _, err := Plan(f, r, Options{Domain: "mini"}); err == nil {
		t.Fatal("expected Plan to fail on unresolved via")
	}

	payload, _, unresolved, err := Plan(f, r, Options{Domain: "mini", AllowUnresolved: true})
	if err != nil {
		t.Fatalf("Plan(AllowUnresolved): %v", err)
	}
	if len(unresolved) != 1 {
		t.Fatalf("unresolved = %+v", unresolved)
	}
	if unresolved[0].Kind != "mutation" || unresolved[0].Name != "ghostMutation" {
		t.Fatalf("unresolved[0] = %+v", unresolved[0])
	}
	if unresolved[0].Owner != "ui:control:mini:panel-ustawienia-komunikacja-sms-sender-id" {
		t.Fatalf("unresolved owner = %q", unresolved[0].Owner)
	}
	for _, e := range payload.Edges {
		if e.EdgeType == "WRITES_VIA" && strings.Contains(e.FromNodeKey, "sms-sender-id") {
			t.Fatal("unresolved WRITES_VIA edge must not be planned")
		}
	}
	if len(payload.Edges) != 8 {
		t.Fatalf("edges = %d, want 8", len(payload.Edges))
	}
}

// TestPlanImportsCleanly is the contract that matters most: everything Plan
// builds must survive the registry unchanged.
func TestPlanImportsCleanly(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	r := &Resolver{Store: g.Store(), Domain: "mini"}
	payload, _, _, err := Plan(f, r, Options{Domain: "mini"})
	if err != nil {
		t.Fatal(err)
	}
	revID, err := g.Store().CreateRevision("mini", "", f.Commit, "manual", "incremental", `{"layer":"ui"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if len(res.Rejected) > 0 {
		t.Fatalf("rejected: %+v", res.Rejected)
	}
	if res.NodesCreated != 5 || res.EdgesCreated != 9 {
		t.Fatalf("created %d nodes / %d edges", res.NodesCreated, res.EdgesCreated)
	}
}

func keysOf(m map[string]graph.ImportNode) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

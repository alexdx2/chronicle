package viewmodel

import (
	"os"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// BuildC3 now selects components via salience (registry-driven) instead of a
// hardcoded controller/provider list, and annotates each with render_mode/tier.
func TestBuildC3_SalienceAnnotatesComponents(t *testing.T) {
	st := openLiveDBCopy(t)
	c3, err := BuildC3(st, "tom-and-jerry", "arena-api")
	if err != nil {
		t.Fatalf("BuildC3: %v", err)
	}
	if len(c3.Components) == 0 {
		t.Fatal("expected components, got none")
	}

	sawProvider := false
	for _, c := range c3.Components {
		// Every component in the list is a box (the selection invariant).
		if c.RenderMode != "box" {
			t.Errorf("component %s: render_mode=%q want box", c.Key, c.RenderMode)
		}
		if c.Tier == "" {
			t.Errorf("component %s: tier not annotated", c.Key)
		}
		if c.Type == "provider" {
			sawProvider = true
		}
	}
	// Providers must still appear as boxes at c3 (the level-aware policy override).
	if !sawProvider {
		t.Error("expected at least one provider component at c3 level")
	}
}

// BuildC2 annotates service containers with salience (uniformly box/primary at c2).
func TestBuildC2_SalienceAnnotatesServices(t *testing.T) {
	st := openLiveDBCopy(t)
	c2, err := BuildC2(st, "tom-and-jerry")
	if err != nil {
		t.Fatalf("BuildC2: %v", err)
	}
	if len(c2.Services) == 0 {
		t.Fatal("expected services")
	}
	for _, s := range c2.Services {
		if s.RenderMode != "box" {
			t.Errorf("service %s: render_mode=%q want box", s.Key, s.RenderMode)
		}
		if s.Tier != "primary" {
			t.Errorf("service %s: tier=%q want primary", s.Key, s.Tier)
		}
	}
}

// End-to-end proof on a constructed graph: role-tagged code nodes drive C3
// buckets. Demonstrates the marquee behavior (DTO/helper hidden, entity-role
// promoted to a box) that the tom-and-jerry fixture cannot show (it has no
// role-tagged nodes). C3 owns code-layer nodes, so the demo uses code nodes.
func TestBuildC3_RolesDriveBuckets(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	mk := func(key, layer, ntype, name, file, meta string) {
		if _, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d",
			Name: name, FilePath: file, Status: "active",
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: meta,
		}); err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
	}
	mk("service:service:d:app", "service", "service", "app", "app/main.ts", "{}")
	mk("code:controller:d:ctl", "code", "controller", "AppController", "app/app.controller.ts", "{}")
	// A normally-hidden code symbol promoted to a box because its role is entity.
	mk("code:symbol:d:order", "code", "symbol", "OrderEntity", "app/order.entity.ts", `{"role":"entity"}`)
	// request_dto and helper are scaffolding → hidden.
	mk("code:symbol:d:dto", "code", "symbol", "CreateOrderDto", "app/create-order.dto.ts", `{"role":"request_dto"}`)
	mk("code:symbol:d:util", "code", "symbol", "fmt", "app/util.ts", `{"role":"helper"}`)

	c3, err := BuildC3(st, "d", "app")
	if err != nil {
		t.Fatalf("BuildC3: %v", err)
	}

	boxes := map[string]string{} // name -> render_mode
	for _, c := range c3.Components {
		boxes[c.Name] = c.RenderMode
	}

	// entity-role symbol and the controller are boxes.
	if boxes["OrderEntity"] != "box" {
		t.Errorf("entity role: want OrderEntity box, got %q (components=%v)", boxes["OrderEntity"], boxes)
	}
	if boxes["AppController"] != "box" {
		t.Errorf("controller: want box, got %q", boxes["AppController"])
	}
	// request_dto must NOT be a box.
	if _, ok := boxes["CreateOrderDto"]; ok {
		t.Errorf("request_dto must not be a box component")
	}
	// helper symbol + dto are hidden.
	if c3.HiddenCount < 2 {
		t.Errorf("HiddenCount=%d want >=2 (dto + helper)", c3.HiddenCount)
	}
}

// The demote confidence gate must flow through the viewmodel: a low-confidence
// LLM "helper" claim may not hide a controller the type policy shows as a box,
// while a high-confidence claim may.
func TestBuildC3_LowConfidenceRoleClaimCannotHide(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	mk := func(key, layer, ntype, name, file, meta string) {
		if _, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d",
			Name: name, FilePath: file, Status: "active",
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: meta,
		}); err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
	}
	mk("service:service:d:app", "service", "service", "app", "app/main.ts", "{}")
	mk("code:controller:d:low", "code", "controller", "LowConfCtl", "app/low.ts", `{"role":"helper","role_confidence":0.4}`)
	mk("code:controller:d:high", "code", "controller", "HighConfCtl", "app/high.ts", `{"role":"helper","role_confidence":0.9}`)

	c3, err := BuildC3(st, "d", "app")
	if err != nil {
		t.Fatalf("BuildC3: %v", err)
	}
	boxes := map[string]bool{}
	for _, c := range c3.Components {
		boxes[c.Name] = true
	}
	if !boxes["LowConfCtl"] {
		t.Errorf("low-confidence helper claim must NOT hide a controller (components=%v)", boxes)
	}
	if boxes["HighConfCtl"] {
		t.Errorf("high-confidence helper claim must hide the controller")
	}
}

// Deterministic path-based noise: nodes whose file paths match noise_paths
// patterns (generated/test/vendor) are demoted regardless of role claims —
// no LLM involved, so no confidence gate.
func TestBuildC3_PathNoiseDemotes(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	mk := func(key, layer, ntype, name, file string) {
		if _, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d",
			Name: name, FilePath: file, Status: "active",
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
		}); err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
	}
	mk("service:service:d:app", "service", "service", "app", "app/main.ts")
	mk("code:controller:d:real", "code", "controller", "RealCtl", "app/app.controller.ts")
	mk("code:controller:d:gen", "code", "controller", "GenCtl", "app/generated/gen.controller.ts")
	mk("code:controller:d:spec", "code", "controller", "SpecCtl", "app/app.controller.spec.ts")

	c3, err := BuildC3(st, "d", "app")
	if err != nil {
		t.Fatalf("BuildC3: %v", err)
	}
	boxes := map[string]bool{}
	for _, c := range c3.Components {
		boxes[c.Name] = true
	}
	if !boxes["RealCtl"] {
		t.Errorf("real controller must stay a box (components=%v)", boxes)
	}
	if boxes["GenCtl"] {
		t.Errorf("generated-path controller must be demoted")
	}
	if boxes["SpecCtl"] {
		t.Errorf("spec-file controller must be demoted")
	}
}

// Bounded topology promotion, wired: a promotable-role node referenced from
// code owned by ANOTHER service crosses the boundary and surfaces as a box;
// its twin without cross-service edges stays hidden. Resolves spec open
// question #2 as degree-in-the-view-subgraph.
func TestBuildC3_BoundaryCrossingPromotes(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	mk := func(key, layer, ntype, name, file, meta string) int64 {
		id, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d",
			Name: name, FilePath: file, Status: "active",
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: meta,
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	mk("service:service:d:app", "service", "service", "app", "app/main.ts", "{}")
	mk("service:service:d:other", "service", "service", "other", "other/main.ts", "{}")
	// Two repository-role symbols in app: hidden by type, promotable by role.
	sharedID := mk("code:symbol:d:shared", "code", "symbol", "SharedRepo", "app/shared.repo.ts", `{"role":"repository"}`)
	mk("code:symbol:d:local", "code", "symbol", "LocalRepo", "app/local.repo.ts", `{"role":"repository"}`)
	// A controller in the OTHER service injects the shared repo.
	octlID := mk("code:controller:d:octl", "code", "controller", "OtherCtl", "other/x.controller.ts", "{}")
	if _, err := st.UpsertEdge(store.EdgeRow{
		EdgeKey:    "code:controller:d:octl->code:symbol:d:shared:INJECTS",
		FromNodeID: octlID, ToNodeID: sharedID, EdgeType: "INJECTS", DerivationKind: "hard",
		Active: true, Metadata: "{}",
		FromNodeKey: "code:controller:d:octl", ToNodeKey: "code:symbol:d:shared",
	}); err != nil {
		t.Fatalf("edge: %v", err)
	}

	c3, err := BuildC3(st, "d", "app")
	if err != nil {
		t.Fatalf("BuildC3: %v", err)
	}
	boxes := map[string]bool{}
	for _, c := range c3.Components {
		boxes[c.Name] = true
	}
	if !boxes["SharedRepo"] {
		t.Errorf("boundary-crossing repository must be promoted to a box (components=%v)", boxes)
	}
	if boxes["LocalRepo"] {
		t.Errorf("local repository without cross-service edges must stay hidden")
	}
}

// No silent drops: an owned node that resolves to a mode C3 does not draw
// (badge/attached_detail/...) must still be accounted for in HiddenCount —
// under a custom policy a node must never vanish without a trace.
func TestBuildC3_NonBoxModesAreCounted(t *testing.T) {
	dir := t.TempDir()
	// Project policy: controllers render as badges at c3 (not boxes).
	types := []byte(`
version: "1"
layers: [code]
node_types:
  code: [controller]
salience:
  render_policy:
    "type:code.controller": { c3: { render_mode: badge } }
`)
	if err := os.WriteFile(dir+"/chronicle.types.yaml", types, 0o644); err != nil {
		t.Fatalf("write types.yaml: %v", err)
	}
	st, err := store.Open(dir + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	mk := func(key, layer, ntype, name, file string) {
		if _, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d",
			Name: name, FilePath: file, Status: "active",
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
		}); err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
	}
	mk("service:service:d:app", "service", "service", "app", "app/main.ts")
	mk("code:controller:d:ctl", "code", "controller", "BadgeCtl", "app/app.controller.ts")

	c3, err := BuildC3(st, "d", "app")
	if err != nil {
		t.Fatalf("BuildC3: %v", err)
	}
	for _, c := range c3.Components {
		if c.Name == "BadgeCtl" {
			t.Fatalf("badge-mode node must not be a component")
		}
	}
	if c3.HiddenCount != 1 {
		t.Errorf("badge-mode node must be counted (HiddenCount=%d want 1)", c3.HiddenCount)
	}
}

// ViewSpec-level salience overrides: the user's last word, session-scoped
// (spec open question #3 resolved: overrides live in the ViewSpec, not the DB).
func TestBuildView_SalienceOverridePins(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	mk := func(key, layer, ntype, name, file string) {
		if _, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d",
			Name: name, FilePath: file, Status: "active",
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
		}); err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
	}
	mk("service:service:d:app", "service", "service", "app", "app/main.ts")
	mk("data:dto:d:x", "data", "dto", "XDto", "app/x.dto.ts")

	spec := ViewSpec{
		Scope: ScopeSpec{Domain: "d"},
		Group: GroupSpec{By: "none"},
		SalienceOverrides: map[string]SalienceOverrideSpec{
			"data:dto:d:x": {RenderMode: "box"},
		},
	}
	view, err := BuildView(st, spec)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	var got string
	for _, n := range view.Nodes {
		if n.Key == "data:dto:d:x" {
			got = n.RenderMode
		}
	}
	if got != "box" {
		t.Errorf("salience override must pin dto to box, got %q", got)
	}

	// Closed vocab holds for overrides too.
	spec.SalienceOverrides["data:dto:d:x"] = SalienceOverrideSpec{RenderMode: "boxx"}
	if _, err := BuildView(st, spec); err == nil {
		t.Error("invalid override render_mode must fail BuildView")
	}
}

// BuildView (the dashboard's data path) annotates each VNode with salience so
// the frontend can render by render_mode.
func TestBuildPresetC3_VNodesCarrySalience(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c3", "tom-and-jerry", "arena-api")
	if err != nil {
		t.Fatalf("BuildPreset(c3): %v", err)
	}
	annotated := 0
	for _, n := range view.Nodes {
		if n.Boundary {
			continue // boundary targets are intentionally not salience-annotated
		}
		if n.RenderMode == "" || n.Tier == "" {
			t.Errorf("vnode %s missing salience (tier=%q mode=%q)", n.Key, n.Tier, n.RenderMode)
		}
		annotated++
	}
	if annotated == 0 {
		t.Fatal("no in-view nodes to annotate")
	}
}

// Role-as-evidence: conflicting role_classification evidence rows resolve to the
// highest-confidence winner, which drives C3 salience — even with NO role in the
// node's metadata. Proves the evidence-backed winning_role path end-to-end.
func TestBuildC3_RoleEvidenceResolvesWinner(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	mkNode := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d",
			Name: name, FilePath: file, Status: "active",
			Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	mkNode("service:service:d:app", "service", "service", "app", "app/main.ts")
	symID := mkNode("code:symbol:d:order", "code", "symbol", "OrderEntity", "app/order.ts")

	// Distinct file_path per claim so AddEvidence keeps them as two separate
	// claims (it dedups by target/node/source/repo/file) — mirrors two extractors.
	addRole := func(role string, conf float64, file string) {
		if _, err := st.AddEvidence(store.EvidenceRow{
			TargetKind: "node", NodeID: symID, SourceKind: "role_classification",
			FilePath: file, ExtractorID: "test", ExtractorVersion: "1",
			Confidence: conf, EvidenceStatus: "valid", EvidencePolarity: "positive",
			Assertion: `{"role":"` + role + `","role_reason":"test"}`, AssertionKind: "semantic_role",
		}); err != nil {
			t.Fatalf("add role %s: %v", role, err)
		}
	}
	// Two conflicting claims: helper (0.6) and entity (0.9). entity must win.
	addRole("helper", 0.6, "app/order.ts")
	addRole("entity", 0.9, "app/order_alt.ts")

	c3, err := BuildC3(st, "d", "app")
	if err != nil {
		t.Fatalf("BuildC3: %v", err)
	}
	var found string
	for _, c := range c3.Components {
		if c.Name == "OrderEntity" {
			found = c.RenderMode
		}
	}
	if found != "box" {
		t.Fatalf("entity-role (evidence winner) symbol should be a box; got render_mode=%q, components=%v", found, c3.Components)
	}
}

// ExplainSalience surfaces the resolve trace on demand — the "why is this node
// hidden" answer the spec promises, without embedding traces in every payload.
func TestExplainSalience(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertNode(store.NodeRow{
		NodeKey: "data:dto:d:x", Layer: "data", NodeType: "dto", DomainKey: "d",
		Name: "XDto", FilePath: "app/x.dto.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1,
		Metadata: `{"role":"request_dto","role_confidence":0.9}`,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	ex, err := ExplainSalience(st, "data:dto:d:x", "")
	if err != nil {
		t.Fatalf("ExplainSalience: %v", err)
	}
	if ex.RenderMode != "hidden" || ex.Tier != "detail" {
		t.Errorf("dto at default: got %s/%s want detail/hidden", ex.Tier, ex.RenderMode)
	}
	if ex.Role != "request_dto" || ex.RoleConfidence != 0.9 {
		t.Errorf("role claim not surfaced: %q %v", ex.Role, ex.RoleConfidence)
	}
	if len(ex.Trace) == 0 {
		t.Error("trace must not be empty")
	}
	if ex.Level != "default" {
		t.Errorf("empty level must normalize to default, got %q", ex.Level)
	}

	ex, err = ExplainSalience(st, "data:dto:d:x", "focus")
	if err != nil {
		t.Fatalf("ExplainSalience(focus): %v", err)
	}
	if ex.RenderMode != "attached_detail" {
		t.Errorf("dto at focus: got %q want attached_detail (trace=%v)", ex.RenderMode, ex.Trace)
	}

	if _, err := ExplainSalience(st, "data:dto:d:missing", ""); err == nil {
		t.Error("missing node must error")
	}
}

// Lens-specific salience: the Data lens promotes models to primary boxes, whereas
// the same models are collapsed background in C2/C3. Proves the lens dimension
// works via the existing data preset (layout.preset reaches salience as level).
func TestBuildPresetData_LensPromotesModels(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "data", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(data): %v", err)
	}
	models := 0
	for _, n := range view.Nodes {
		if n.Type == "model" {
			models++
			if n.RenderMode != "box" || n.Tier != "primary" {
				t.Errorf("model %s in data lens: want primary/box, got %s/%s", n.Key, n.Tier, n.RenderMode)
			}
		}
	}
	if models == 0 {
		t.Fatal("expected model nodes in the data lens")
	}
}

package viewmodel

import (
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

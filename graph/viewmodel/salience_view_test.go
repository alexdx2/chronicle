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

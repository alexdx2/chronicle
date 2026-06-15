package viewmodel

import "testing"

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

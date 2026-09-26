package review

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestRenderJSON_RoundTrips: RenderJSON -> json.Unmarshal into a fresh
// ReviewResult reproduces every field of the original, using the same
// mockup-derived fixture render_markdown_test.go builds (RenderJSON has no
// business logic of its own beyond json.MarshalIndent — see render_json.go's
// own doc comment — so this fixture is exercised for real richness: nested
// Facts/Evidence/Downstream, not just a couple of scalar fields).
func TestRenderJSON_RoundTrips(t *testing.T) {
	r := mockupFixtureResult()

	data, err := RenderJSON(r)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	var got ReviewResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, data)
	}

	if !reflect.DeepEqual(&got, r) {
		t.Errorf("round-tripped ReviewResult != original\n--- got ---\n%+v\n--- want ---\n%+v", got, *r)
	}
}

// TestRenderJSON_Indent: a 2-space indent, per the brief.
func TestRenderJSON_Indent(t *testing.T) {
	r := &ReviewResult{SchemaVersion: 1}
	data, err := RenderJSON(r)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	want := "{\n  \"SchemaVersion\": 1,\n"
	if string(data[:len(want)]) != want {
		t.Errorf("RenderJSON indent = %q..., want prefix %q", data[:len(want)], want)
	}
}

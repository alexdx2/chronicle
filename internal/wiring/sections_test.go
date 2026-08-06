package wiring

import (
	"strings"
	"testing"
)

// ─── PlanMarkedSection ──────────────────────────────────────────────────────

func TestPlanMarkedSectionCreate(t *testing.T) {
	action, content := PlanMarkedSection(nil, false, "Hello")
	if action != ActionCreate || !strings.Contains(string(content), AgentsMarkerStart) ||
		!strings.Contains(string(content), "Hello") {
		t.Fatalf("action=%s content=%s", action, content)
	}
}

func TestPlanMarkedSectionPreservesSurroundingContent(t *testing.T) {
	existing := []byte("# Mine\n\n" + AgentsMarkerStart + "\nold\n" + AgentsMarkerEnd + "\n\n# Tail\n")
	action, content := PlanMarkedSection(existing, true, "new")
	if action != ActionUpdate {
		t.Fatalf("action=%s", action)
	}
	s := string(content)
	if !strings.Contains(s, "# Mine") || !strings.Contains(s, "# Tail") || strings.Contains(s, "old") {
		t.Fatalf("content=%s", s)
	}
}

func TestPlanMarkedSectionUnchanged(t *testing.T) {
	_, first := PlanMarkedSection(nil, false, "same")
	action, content := PlanMarkedSection(first, true, "same")
	if action != ActionUnchanged || content != nil {
		t.Fatalf("action=%s content=%v", action, content)
	}
}

// TestPlanMarkedSectionCreatesFileWhenMissing ports
// TestUpsertMarkedSection_CreatesFileWhenMissing.
func TestPlanMarkedSectionCreatesFileWhenMissing(t *testing.T) {
	action, content := PlanMarkedSection(nil, false, "hello section")
	if action != ActionCreate {
		t.Errorf("action = %q; want %q", action, ActionCreate)
	}

	got := string(content)
	if !strings.Contains(got, AgentsMarkerStart) || !strings.Contains(got, AgentsMarkerEnd) {
		t.Errorf("missing markers in:\n%s", got)
	}
	if !strings.Contains(got, "hello section") {
		t.Errorf("missing content in:\n%s", got)
	}
}

// TestPlanMarkedSectionAppendsPreservingUserContent ports
// TestUpsertMarkedSection_AppendsPreservingUserContent.
func TestPlanMarkedSectionAppendsPreservingUserContent(t *testing.T) {
	userContent := "# My project rules\n\nAlways use tabs.\n"

	action, content := PlanMarkedSection([]byte(userContent), true, "chronicle stuff")
	if action != ActionUpdate {
		t.Errorf("action = %q; want %q", action, ActionUpdate)
	}

	got := string(content)
	if !strings.Contains(got, "Always use tabs.") {
		t.Errorf("user content lost:\n%s", got)
	}
	if !strings.Contains(got, "chronicle stuff") {
		t.Errorf("section not appended:\n%s", got)
	}
	if strings.Index(got, "Always use tabs.") > strings.Index(got, AgentsMarkerStart) {
		t.Errorf("section should be appended after user content:\n%s", got)
	}
}

// TestPlanMarkedSectionReplacesExistingSection ports
// TestUpsertMarkedSection_ReplacesExistingSection.
func TestPlanMarkedSectionReplacesExistingSection(t *testing.T) {
	existing := "# Before\n\n" + AgentsMarkerStart + "\nOLD CONTENT\n" + AgentsMarkerEnd + "\n\n# After\n"

	action, content := PlanMarkedSection([]byte(existing), true, "NEW CONTENT")
	if action != ActionUpdate {
		t.Errorf("action = %q; want %q", action, ActionUpdate)
	}

	got := string(content)
	if strings.Contains(got, "OLD CONTENT") {
		t.Errorf("old section not replaced:\n%s", got)
	}
	if !strings.Contains(got, "NEW CONTENT") {
		t.Errorf("new section missing:\n%s", got)
	}
	if !strings.Contains(got, "# Before") || !strings.Contains(got, "# After") {
		t.Errorf("surrounding user content lost:\n%s", got)
	}
	if strings.Count(got, AgentsMarkerStart) != 1 {
		t.Errorf("want exactly one start marker:\n%s", got)
	}
}

// TestPlanMarkedSectionIdempotentWhenUnchanged ports
// TestUpsertMarkedSection_IdempotentWhenUnchanged.
func TestPlanMarkedSectionIdempotentWhenUnchanged(t *testing.T) {
	_, first := PlanMarkedSection(nil, false, "same content")

	action, content := PlanMarkedSection(first, true, "same content")
	if action != ActionUnchanged {
		t.Errorf("action = %q; want %q", action, ActionUnchanged)
	}
	if content != nil {
		t.Errorf("content = %v; want nil on unchanged", content)
	}
}

// ─── RemoveMarkedSection ────────────────────────────────────────────────────

func TestRemoveMarkedSection(t *testing.T) {
	existing := []byte("keep\n\n" + AgentsMarkerStart + "\nx\n" + AgentsMarkerEnd + "\nkeep2\n")
	action, content, found := RemoveMarkedSection(existing)
	if !found || action != ActionUpdate {
		t.Fatalf("found=%v action=%s", found, action)
	}
	s := string(content)
	if strings.Contains(s, AgentsMarkerStart) || !strings.Contains(s, "keep2") {
		t.Fatalf("content=%s", s)
	}
	_, _, found2 := RemoveMarkedSection([]byte("no markers"))
	if found2 {
		t.Fatal("false positive")
	}
}

// ─── content: ProjectAgentsSection / GlobalAgentsSection ──────────────────

func TestProjectAgentsSection_CoversEntryPointAndScanGating(t *testing.T) {
	content := ProjectAgentsSection()

	for _, want := range []string{
		"chronicle_command",     // the workflow entry point
		"chronicle_node_search", // query surface
		"chronicle_impact",      // query surface
		"chronicle_query_deps",  // query surface
		"chronicle_scan_",       // scan pipeline warning must name the prefix
		"Claude Code",           // scan gating: where scans run today
	} {
		if !strings.Contains(content, want) {
			t.Errorf("project AGENTS section missing %q", want)
		}
	}
	if strings.Contains(content, AgentsMarkerStart) {
		t.Error("section body must not embed its own markers")
	}
}

func TestGlobalAgentsSection_IsProjectAgnostic(t *testing.T) {
	content := GlobalAgentsSection()
	if !strings.Contains(content, "chronicle_command") {
		t.Error("global AGENTS section must name chronicle_command as entry point")
	}
	if !strings.Contains(content, ".depbot") {
		t.Error("global AGENTS section should tell agents how to recognize a Chronicle project (.depbot)")
	}
}

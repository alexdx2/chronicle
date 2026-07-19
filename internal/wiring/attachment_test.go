package wiring

import (
	"strings"
	"testing"
)

func TestProjectIDStable(t *testing.T) {
	a := ProjectID("/repos/otopoint", "git@github.com:x/otopoint.git")
	b := ProjectID("/repos/otopoint", "git@github.com:x/otopoint.git")
	c := ProjectID("/repos/other", "")
	if a != b || a == c || len(a) != 24 {
		t.Fatalf("ids: %s %s %s", a, b, c)
	}
}

func TestAttachmentRoundtripAndDelete(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	id := ProjectID("/p", "")
	if r, err := LoadAttachment(id); err != nil || r != nil {
		t.Fatalf("absent: %v %v", r, err)
	}
	rec := &AttachmentRecord{SchemaVersion: 1, ProjectPath: "/p",
		Changes: AttachmentChanges{AgentsSectionAdded: true, HookEntryAdded: true}}
	if err := SaveAttachment(id, rec); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAttachment(id)
	if err != nil || got == nil || !got.Changes.HookEntryAdded {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	if err := DeleteAttachment(id); err != nil {
		t.Fatal(err)
	}
	if r, _ := LoadAttachment(id); r != nil {
		t.Fatal("not deleted")
	}
}

func TestPlanClaudeBridgeCases(t *testing.T) {
	// no file → create with sentinel-wrapped import
	action, content, created, added := PlanClaudeBridge(nil, false)
	if action != ActionCreate || !created || !added ||
		!strings.Contains(string(content), "@AGENTS.md") ||
		!strings.Contains(string(content), ClaudeMarkerStart) {
		t.Fatalf("create: %s %v %v %s", action, created, added, content)
	}
	// file already importing (however written) → unchanged
	action2, _, _, added2 := PlanClaudeBridge([]byte("# Mine\n@AGENTS.md\n"), true)
	if action2 != ActionUnchanged || added2 {
		t.Fatalf("existing import: %s", action2)
	}
	// file without import → sentinel block appended, user content preserved
	action3, content3, created3, added3 := PlanClaudeBridge([]byte("# Mine\n"), true)
	if action3 != ActionUpdate || created3 || !added3 ||
		!strings.Contains(string(content3), "# Mine") ||
		!strings.Contains(string(content3), "@AGENTS.md") {
		t.Fatalf("append: %s %s", action3, content3)
	}
}

func TestPlanClaudeBridgeRemoval(t *testing.T) {
	_, createdContent, _, _ := PlanClaudeBridge(nil, false)
	rec := AttachmentChanges{ClaudeFileCreated: true, ClaudeImportAdded: true}
	// untouched chronicle-created file → delete
	action, _ := PlanClaudeBridgeRemoval(createdContent, true, rec)
	if action != ActionDelete {
		t.Fatalf("pristine: %s", action)
	}
	// user added content → strip block only
	modified := append([]byte("# User notes\n"), createdContent...)
	action2, content2 := PlanClaudeBridgeRemoval(modified, true, rec)
	if action2 != ActionUpdate || strings.Contains(string(content2), "@AGENTS.md") ||
		!strings.Contains(string(content2), "# User notes") {
		t.Fatalf("modified: %s %s", action2, content2)
	}
	// we never added anything → untouched
	action3, _ := PlanClaudeBridgeRemoval([]byte("# Mine\n@AGENTS.md\n"), true, AttachmentChanges{})
	if action3 != ActionUnchanged {
		t.Fatalf("no-op: %s", action3)
	}
}

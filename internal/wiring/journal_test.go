package wiring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPlanCommitsAndCleansBackups(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	dir := t.TempDir()
	existing := filepath.Join(dir, "config.toml")
	os.WriteFile(existing, []byte("old"), 0644)

	p := &Plan{Agent: "codex", Changes: []PlannedChange{
		{Target: existing, Ownership: OwnershipUser, Action: ActionUpdate, Summary: "update config", NewContent: []byte("new")},
		{Target: filepath.Join(dir, "prompts", "a.md"), Ownership: OwnershipChronicle, Action: ActionCreate, Summary: "create prompt", NewContent: []byte("p")},
		{Target: filepath.Join(dir, "untouched"), Ownership: OwnershipUser, Action: ActionUnchanged, Summary: "noop"},
	}}
	res, err := ApplyPlan(p)
	if err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}
	if res.Status != "committed" || res.Applied != 2 {
		t.Fatalf("res = %+v", res)
	}
	if got, _ := os.ReadFile(existing); string(got) != "new" {
		t.Fatalf("config = %q", got)
	}
	if _, err := os.Stat(filepath.Join(Home(), "backups", res.OperationID)); !os.IsNotExist(err) {
		t.Fatal("backups not cleaned after commit")
	}
	jdata, err := os.ReadFile(filepath.Join(Home(), "journal", res.OperationID+".json"))
	if err != nil || !strings.Contains(string(jdata), `"committed"`) {
		t.Fatalf("journal not committed: %v %s", err, jdata)
	}
}

func TestApplyPlanRollsBackOnFailure(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	dir := t.TempDir()
	existing := filepath.Join(dir, "config.toml")
	os.WriteFile(existing, []byte("old"), 0644)
	// Second change fails deterministically: its parent "dir" is a FILE.
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0644)

	p := &Plan{Agent: "codex", Changes: []PlannedChange{
		{Target: existing, Action: ActionUpdate, Ownership: OwnershipUser, Summary: "u", NewContent: []byte("new")},
		{Target: filepath.Join(blocker, "impossible.md"), Action: ActionCreate, Ownership: OwnershipChronicle, Summary: "c", NewContent: []byte("p")},
	}}
	res, err := ApplyPlan(p)
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil || res.Status != "rolled-back" {
		t.Fatalf("res = %+v", res)
	}
	if got, _ := os.ReadFile(existing); string(got) != "old" {
		t.Fatalf("rollback failed, config = %q", got)
	}
}

func TestApplyPlanRollsBackFailedCreate(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	dir := t.TempDir()
	created := filepath.Join(dir, "prompts", "a.md")
	// Second change fails deterministically: its parent "blocker" is a FILE.
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0644)

	p := &Plan{Agent: "codex", Changes: []PlannedChange{
		{Target: created, Action: ActionCreate, Ownership: OwnershipChronicle, Summary: "c1", NewContent: []byte("p")},
		{Target: filepath.Join(blocker, "impossible.md"), Action: ActionCreate, Ownership: OwnershipChronicle, Summary: "c2", NewContent: []byte("p")},
	}}
	res, err := ApplyPlan(p)
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil || res.Status != "rolled-back" {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatal("created file from change 1 should be gone after rollback")
	}
}

func TestApplyPlanRollsBackFailedDeleteRestoresContentAndMode(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	dir := t.TempDir()
	doomed := filepath.Join(dir, "gone.md")
	if err := os.WriteFile(doomed, []byte("bye"), 0600); err != nil {
		t.Fatal(err)
	}
	// Second change fails deterministically: its parent "blocker" is a FILE.
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0644)

	p := &Plan{Agent: "codex", Changes: []PlannedChange{
		{Target: doomed, Action: ActionDelete, Ownership: OwnershipChronicle, Summary: "d"},
		{Target: filepath.Join(blocker, "impossible.md"), Action: ActionCreate, Ownership: OwnershipChronicle, Summary: "c", NewContent: []byte("p")},
	}}
	res, err := ApplyPlan(p)
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil || res.Status != "rolled-back" {
		t.Fatalf("res = %+v", res)
	}
	got, rerr := os.ReadFile(doomed)
	if rerr != nil {
		t.Fatalf("restored file missing: %v", rerr)
	}
	if string(got) != "bye" {
		t.Fatalf("restored content = %q, want %q", got, "bye")
	}
	info, serr := os.Stat(doomed)
	if serr != nil {
		t.Fatalf("stat restored file: %v", serr)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("restored mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestApplyPlanDeleteAndRestore(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	dir := t.TempDir()
	doomed := filepath.Join(dir, "gone.md")
	os.WriteFile(doomed, []byte("bye"), 0644)
	p := &Plan{Agent: "codex", Changes: []PlannedChange{
		{Target: doomed, Action: ActionDelete, Ownership: OwnershipChronicle, Summary: "d"},
	}}
	res, err := ApplyPlan(p)
	if err != nil || res.Status != "committed" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(doomed); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}
}

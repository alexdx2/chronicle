package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/wiring"
)

func TestDoctorReportsPartialJournals(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	jdir := filepath.Join(wiring.Home(), "journal")
	os.MkdirAll(jdir, 0755)
	os.WriteFile(filepath.Join(jdir, "codex-123.json"),
		[]byte(`{"operationId":"codex-123","agent":"codex","status":"partial","steps":[]}`), 0644)

	var out bytes.Buffer
	if err := runDoctor(nil, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "partial") || !strings.Contains(s, "codex-123") {
		t.Fatalf("doctor must surface partial ops:\n%s", s)
	}
}

func TestDoctorPartialAttachJournalSuggestsAttachNotSetup(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	jdir := filepath.Join(wiring.Home(), "journal")
	os.MkdirAll(jdir, 0755)
	os.WriteFile(filepath.Join(jdir, "project-attach-123.json"),
		[]byte(`{"operationId":"project-attach-123","agent":"project-attach","status":"partial","steps":[]}`), 0644)

	var out bytes.Buffer
	if err := runDoctor(nil, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "chronicle attach <dir>") {
		t.Fatalf("doctor should suggest re-running attach:\n%s", s)
	}
	if strings.Contains(s, "chronicle setup --agent project-attach") {
		t.Fatalf("doctor must not suggest the non-existent 'chronicle setup --agent project-attach':\n%s", s)
	}
}

func TestDoctorPartialDetachJournalSuggestsDetachNotSetup(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	jdir := filepath.Join(wiring.Home(), "journal")
	os.MkdirAll(jdir, 0755)
	os.WriteFile(filepath.Join(jdir, "project-detach-123.json"),
		[]byte(`{"operationId":"project-detach-123","agent":"project-detach","status":"partial","steps":[]}`), 0644)

	var out bytes.Buffer
	if err := runDoctor(nil, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "chronicle detach <dir>") {
		t.Fatalf("doctor should suggest re-running detach:\n%s", s)
	}
	if strings.Contains(s, "chronicle setup --agent project-detach") {
		t.Fatalf("doctor must not suggest the non-existent 'chronicle setup --agent project-detach':\n%s", s)
	}
}

func TestDoctorPartialJournalHintNamesJournalPathForManualDeletion(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	jdir := filepath.Join(wiring.Home(), "journal")
	os.MkdirAll(jdir, 0755)
	journalFile := filepath.Join(jdir, "codex-123.json")
	os.WriteFile(journalFile, []byte(`{"operationId":"codex-123","agent":"codex","status":"partial","steps":[]}`), 0644)

	var out bytes.Buffer
	if err := runDoctor(nil, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "deleting "+journalFile) {
		t.Fatalf("recovery hint should name the journal path for manual deletion:\n%s", s)
	}
}

func TestDoctorPartialJournalResolvedAfterSuccessfulReapplyIsNotReported(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	jdir := filepath.Join(wiring.Home(), "journal")
	os.MkdirAll(jdir, 0755)
	os.WriteFile(filepath.Join(jdir, "codex-111.json"),
		[]byte(`{"operationId":"codex-111","agent":"codex","status":"partial","steps":[]}`), 0644)

	// Recovery: a fresh, successful apply for the SAME agent.
	dir := t.TempDir()
	p := &wiring.Plan{Agent: "codex", Changes: []wiring.PlannedChange{
		{Target: filepath.Join(dir, "a.md"), Action: wiring.ActionCreate, Ownership: wiring.OwnershipChronicle, Summary: "c", NewContent: []byte("p")},
	}}
	if _, err := wiring.ApplyPlan(p); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runDoctor(nil, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "codex-111") {
		t.Fatalf("doctor should stop reporting the resolved journal:\n%s", out.String())
	}
}

func TestDoctorCleanState(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	var out bytes.Buffer
	if err := runDoctor(nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "codex") {
		t.Fatalf("doctor must list registered agents:\n%s", out.String())
	}
}

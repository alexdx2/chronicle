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

func TestPublicationReport_NoPublishersWarnsOnce(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	writePlainFile(t, repo, "f", "1")
	c := commitAllCLI(t, repo, "c")
	gitRun(t, repo, "update-ref", "refs/heads/chronicle/graphs/main", c)
	var out bytes.Buffer
	publicationReport(repo, &out)
	s := out.String()
	if !strings.Contains(s, "no publishers configured") {
		t.Fatalf("doctor must warn about missing publishers: %q", s)
	}
	if n := strings.Count(s, "trusting every"); n != 1 {
		t.Fatalf("TrustAll warning must print exactly once, got %d: %q", n, s)
	}
}

func TestPublicationReport_NoRefsSkipsTrustList(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	writePlainFile(t, repo, "f", "1")
	commitAllCLI(t, repo, "c")
	gitRun(t, repo, "remote", "add", "origin", "/dev/null") // origin, but no default branch known
	var out bytes.Buffer
	publicationReport(repo, &out)
	s := out.String()
	if !strings.Contains(s, "no chronicle/graphs/* publications") {
		t.Fatalf("expected the no-publications line: %q", s)
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "  !") {
			t.Fatalf("no refs must mean no trust-list errors: %q", s)
		}
	}
}

func TestPublicationReport_RefsWithUnknownDefaultBranchWarns(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	writePlainFile(t, repo, "f", "1")
	c := commitAllCLI(t, repo, "c")
	gitRun(t, repo, "remote", "add", "origin", "/dev/null")
	gitRun(t, repo, "update-ref", "refs/heads/chronicle/graphs/main", c)
	var out bytes.Buffer
	publicationReport(repo, &out)
	s := out.String()
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "  ! ") {
			n++
			if !strings.Contains(line, "cannot determine origin default branch") {
				t.Fatalf("unexpected warning line: %q", line)
			}
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one '  ! ' line, got %d: %q", n, s)
	}
}

func TestPublicationReport_NotAGitRepoIsSilent(t *testing.T) {
	var out bytes.Buffer
	publicationReport(t.TempDir(), &out)
	if out.Len() != 0 {
		t.Fatalf("non-git directory must print nothing: %q", out.String())
	}
}

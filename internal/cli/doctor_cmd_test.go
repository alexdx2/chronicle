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

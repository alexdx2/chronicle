package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/wiring"
)

func TestWiringNoticeOnlyWhenMarkedAndOutdated(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer

	// no AGENTS.md → silent
	maybePrintWiringNotice(dir, &buf)
	if buf.Len() != 0 {
		t.Fatalf("noise without AGENTS.md: %s", buf.String())
	}

	// AGENTS.md without marker → silent (unwired project stays untouched)
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Mine\n"), 0644)
	maybePrintWiringNotice(dir, &buf)
	if buf.Len() != 0 {
		t.Fatalf("noise without marker: %s", buf.String())
	}

	// outdated chronicle section → one notice line
	stale := wiring.AgentsMarkerStart + "\nold content\n" + wiring.AgentsMarkerEnd + "\n"
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(stale), 0644)
	maybePrintWiringNotice(dir, &buf)
	if !strings.Contains(buf.String(), "chronicle attach --upgrade") {
		t.Fatalf("missing notice: %q", buf.String())
	}

	// current section → silent
	buf.Reset()
	_, current := wiring.PlanMarkedSection(nil, false, wiring.ProjectAgentsSection())
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), current, 0644)
	maybePrintWiringNotice(dir, &buf)
	if buf.Len() != 0 {
		t.Fatalf("noise when current: %s", buf.String())
	}
}

func TestEnsureDepbotDirDoesNotCreateWiringFiles(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)
	projectPath = ""
	chronicleDir = ".depbot"
	resolveDefaults()
	ensureDepbotDir()
	if _, err := os.Stat("CLAUDE.md"); !os.IsNotExist(err) {
		t.Fatal("ensureDepbotDir must not create CLAUDE.md anymore")
	}
	if _, err := os.Stat("AGENTS.md"); !os.IsNotExist(err) {
		t.Fatal("ensureDepbotDir must not create AGENTS.md anymore")
	}
}

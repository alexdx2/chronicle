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
	stamps := filepath.Join(dir, ".depbot")
	var buf bytes.Buffer

	// no AGENTS.md → silent
	printWiringNotice(dir, stamps, &buf, true)
	if buf.Len() != 0 {
		t.Fatalf("noise without AGENTS.md: %s", buf.String())
	}

	// AGENTS.md without marker → silent (unwired project stays untouched)
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Mine\n"), 0644)
	printWiringNotice(dir, stamps, &buf, true)
	if buf.Len() != 0 {
		t.Fatalf("noise without marker: %s", buf.String())
	}

	// outdated chronicle section → one notice line
	stale := wiring.AgentsMarkerStart + "\nold content\n" + wiring.AgentsMarkerEnd + "\n"
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(stale), 0644)
	printWiringNotice(dir, stamps, &buf, true)
	if !strings.Contains(buf.String(), "chronicle attach --upgrade") {
		t.Fatalf("missing notice: %q", buf.String())
	}

	// current section → silent
	buf.Reset()
	_, current := wiring.PlanMarkedSection(nil, false, wiring.ProjectAgentsSection())
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), current, 0644)
	printWiringNotice(dir, stamps, &buf, true)
	if buf.Len() != 0 {
		t.Fatalf("noise when current: %s", buf.String())
	}
}

func TestWiringNoticeOncePerTemplateAndNeverInCI(t *testing.T) {
	dir := t.TempDir()
	stamps := filepath.Join(dir, ".depbot")
	stale := wiring.AgentsMarkerStart + "\nold content\n" + wiring.AgentsMarkerEnd + "\n"
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(stale), 0644)

	// Not interactive (stdout not a TTY / CI set): silent, and no stamp.
	var buf bytes.Buffer
	printWiringNotice(dir, stamps, &buf, false)
	if buf.Len() != 0 {
		t.Fatalf("notice in a non-interactive run: %q", buf.String())
	}
	if m, _ := filepath.Glob(filepath.Join(stamps, "wiring-notice.*")); len(m) != 0 {
		t.Fatalf("stamp written without printing: %v", m)
	}

	// First interactive run prints and stamps; the second is silent.
	printWiringNotice(dir, stamps, &buf, true)
	if strings.Count(buf.String(), "chronicle attach --upgrade") != 1 {
		t.Fatalf("first run: %q", buf.String())
	}
	if m, _ := filepath.Glob(filepath.Join(stamps, "wiring-notice.*")); len(m) != 1 {
		t.Fatalf("want one stamp, got %v", m)
	}
	buf.Reset()
	printWiringNotice(dir, stamps, &buf, true)
	if buf.Len() != 0 {
		t.Fatalf("repeated notice for the same template: %q", buf.String())
	}

	// A different template hash (stamp for another version only) prints again.
	m, _ := filepath.Glob(filepath.Join(stamps, "wiring-notice.*"))
	os.Rename(m[0], filepath.Join(stamps, "wiring-notice.000000000000"))
	printWiringNotice(dir, stamps, &buf, true)
	if buf.Len() == 0 {
		t.Fatal("a new template version must print once more")
	}

	// The public entry point honours CI even when interactive otherwise.
	t.Setenv("CI", "1")
	buf.Reset()
	os.RemoveAll(stamps)
	maybePrintWiringNotice(dir, &buf)
	if buf.Len() != 0 {
		t.Fatalf("notice with CI set: %q", buf.String())
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

func TestWiringNoticeInteractive(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		tty  bool
		env  map[string]string
		want bool
	}{
		{"terminal, no CI", true, nil, true},
		{"terminal, CI set", true, map[string]string{"CI": "true"}, false},
		{"pipe, no CI", false, nil, false},
		{"pipe, CI set", false, map[string]string{"CI": "1"}, false},
	}
	for _, c := range cases {
		if got := wiringNoticeInteractive(c.tty, env(c.env)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

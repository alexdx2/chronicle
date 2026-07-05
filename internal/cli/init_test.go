package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/paths"
)

// TestAddToGitignoreLineMatch guards against the substring false-positive
// where a .gitignore entry like ".depbot-exp/" would satisfy a
// strings.Contains check for the default ".depbot" entry, causing
// addToGitignore to skip appending ".depbot/" entirely.
func TestAddToGitignoreLineMatch(t *testing.T) {
	dir := t.TempDir()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir: %v", err)
	}

	paths.SetChronicleDir("")
	t.Cleanup(func() { paths.SetChronicleDir("") })

	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".depbot-exp/\n"), 0644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	addToGitignore()

	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, ".depbot/") {
		t.Errorf(".gitignore = %q, want .depbot/ appended despite .depbot-exp/ present", got)
	}

	// Second call with the entry already present as an exact line must not
	// duplicate it.
	before := got
	addToGitignore()
	after, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore (2nd): %v", err)
	}
	if string(after) != before {
		t.Errorf(".gitignore changed on repeat call:\nbefore=%q\nafter=%q", before, string(after))
	}
	if strings.Count(string(after), ".depbot/") != 1 {
		t.Errorf(".gitignore = %q, want exactly one .depbot/ entry", string(after))
	}
}

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
)

// TestManifestSkeleton_ParsesToDomainWithScanConfig guards the starter
// chronicle.domain.yaml content `chronicle init` and ensureDepbotDir write to
// disk on a fresh project. The manifest parser (manifest/manifest.go) only
// ever recognizes a "domains:" key (map or list) — it has no knowledge of
// "domain:" (singular) or "repositories:". A skeleton spelled with those dead
// keys parses into a completely empty Manifest, so `chronicle_save_manifest`
// later reports {"status":"saved"} for a manifest with zero domains and every
// dashboard goes blank. This test feeds the literal skeleton constant through
// manifest.Load and requires it to yield a real, scannable domain.
func TestManifestSkeleton_ParsesToDomainWithScanConfig(t *testing.T) {
	m, err := manifest.Load([]byte(manifestSkeleton))
	if err != nil {
		t.Fatalf("manifest.Load(manifestSkeleton) = %v, want a manifest with at least one domain", err)
	}
	if len(m.Domains) == 0 {
		t.Fatal("manifestSkeleton parsed with 0 domains, want >= 1")
	}
	d := m.Domains[0]
	if len(d.Scan.Include) == 0 {
		t.Errorf("domain %q has empty scan.include, want at least one pattern so files actually get discovered", d.Key)
	}
}

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

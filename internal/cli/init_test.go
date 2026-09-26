package cli

import (
	"os"
	"os/exec"
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

// containsLine reports whether want appears as its own (trimmed) line in
// lines — a stricter check than strings.Contains, since ".depbot" is a
// substring of ".depbot-exp/" and a naive Contains check would treat the
// unrelated entry as a match.
func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

// TestRewriteGitignoreReplacesOldWholeDirLine guards the §3 gitignore block
// (internal/cli/init.go's rewriteGitignore) against two failure modes at
// once:
//
//  1. A substring false-positive — a line like ".depbot-exp/" must never be
//     mistaken for the old ".depbot/" whole-directory ignore and be removed
//     by mistake (the historical bug TestAddToGitignoreLineMatch used to
//     guard, against the old addToGitignore's simpler append-only logic).
//  2. The "replace, not append" requirement itself: git will not descend
//     into an already-ignored directory to evaluate `!` exceptions inside
//     it, so the OLD whole-directory line must be gone entirely, not just
//     shadowed by a new block placed after it — otherwise the exceptions
//     silently do nothing and graph.jsonl stays ignored.
func TestRewriteGitignoreReplacesOldWholeDirLine(t *testing.T) {
	dir := t.TempDir()
	paths.SetChronicleDir("")
	t.Cleanup(func() { paths.SetChronicleDir("") })

	gitignorePath := filepath.Join(dir, ".gitignore")
	seed := "node_modules/\n.depbot-exp/\n.depbot/\n*.log\n"
	if err := os.WriteFile(gitignorePath, []byte(seed), 0644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	rewriteGitignore(dir)

	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(content)
	lines := strings.Split(got, "\n")

	if containsLine(lines, ".depbot/") || containsLine(lines, ".depbot") {
		t.Errorf(".gitignore still has the old whole-directory ignore line, want it replaced:\n%s", got)
	}
	for _, want := range []string{"node_modules/", ".depbot-exp/", "*.log"} {
		if !containsLine(lines, want) {
			t.Errorf(".gitignore lost unrelated line %q (substring false-positive?):\n%s", want, got)
		}
	}
	for _, want := range []string{".depbot/*", "!.depbot/graph.jsonl", "!.depbot/chronicle.domain.yaml", "!.depbot/chronicle.types.yaml"} {
		if !containsLine(lines, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, got)
		}
	}

	// Idempotent: a second call converges rather than duplicating the block.
	rewriteGitignore(dir)
	after, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore (2nd): %v", err)
	}
	if string(after) != got {
		t.Errorf("rewriteGitignore not idempotent:\nfirst=%q\nsecond=%q", got, string(after))
	}
	if strings.Count(string(after), ".depbot/*") != 1 {
		t.Errorf(".gitignore = %q, want exactly one .depbot/* line", string(after))
	}
}

// TestChronicleInitWritesGitWiring is the brief's Step-1 CLI test: `chronicle
// init` in a fresh git repo must leave .gitignore/.gitattributes/the merge
// driver in the graph-snapshot design spec's §3 shape
// (docs/superpowers/specs/2026-09-25-graph-snapshot-contract-design.md §3:
// `.depbot/*` plus THREE `!` exceptions, not two) — proven not just by
// substring but by real `git check-ignore` behavior against the repo init
// just wired.
func TestChronicleInitWritesGitWiring(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")

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
	paths.SetProjectRoot("")
	t.Cleanup(func() {
		paths.SetChronicleDir("")
		paths.SetProjectRoot("")
	})

	cmd := newInitCmd()
	cmd.Run(cmd, nil)

	gitignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, want := range []string{".depbot/*", "!.depbot/graph.jsonl", "!.depbot/chronicle.domain.yaml", "!.depbot/chronicle.types.yaml"} {
		if !strings.Contains(string(gitignore), want) {
			t.Errorf(".gitignore missing %q, got:\n%s", want, gitignore)
		}
	}

	gitattributes, err := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	if !strings.Contains(string(gitattributes), "merge=chronicle-graph") {
		t.Errorf(".gitattributes missing merge=chronicle-graph, got:\n%s", gitattributes)
	}

	driver := gitCapture(t, dir, "config", "--get", "merge.chronicle-graph.driver")
	if strings.TrimSpace(driver) == "" {
		t.Error("merge.chronicle-graph.driver is empty after chronicle init")
	}

	// Strongest proof the exceptions are effective: git itself must not
	// consider .depbot/graph.jsonl ignored...
	if err := os.WriteFile(filepath.Join(dir, ".depbot", "graph.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", dir, "check-ignore", ".depbot/graph.jsonl").Run(); err == nil {
		t.Error("git check-ignore .depbot/graph.jsonl unexpectedly succeeded — it should NOT be ignored")
	}
	// ...while a sibling file outside the three exceptions still is.
	if err := os.WriteFile(filepath.Join(dir, ".depbot", "chronicle.db"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", dir, "check-ignore", ".depbot/chronicle.db").Run(); err != nil {
		t.Error("git check-ignore .depbot/chronicle.db failed — it should still be ignored")
	}
}

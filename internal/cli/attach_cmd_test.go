package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/paths"
)

// TestAttachWritesGitWiring guards the task-6 requirement that `attach`
// reaches the exact same .gitignore/.gitattributes/merge-driver §3 shape
// `init` does (internal/cli/init.go's ensureGitWiring) — before this task,
// attach.go had no gitignore-related code at all, so a project wired only
// via `attach` (never `chronicle init`) still needs graph.jsonl tracked and
// mergeable.
func TestAttachWritesGitWiring(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	paths.SetChronicleDir("")
	t.Cleanup(func() { paths.SetChronicleDir("") })

	root := t.TempDir()
	gitRun(t, root, "init", "-q")

	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}

	gitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("attach should write .gitignore: %v", err)
	}
	for _, want := range []string{".depbot/*", "!.depbot/graph.jsonl", "!.depbot/chronicle.domain.yaml", "!.depbot/chronicle.types.yaml"} {
		if !strings.Contains(string(gitignore), want) {
			t.Errorf(".gitignore missing %q, got:\n%s", want, gitignore)
		}
	}

	gitattributes, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatalf("attach should write .gitattributes: %v", err)
	}
	if !strings.Contains(string(gitattributes), "merge=chronicle-graph") {
		t.Errorf(".gitattributes missing merge=chronicle-graph, got:\n%s", gitattributes)
	}

	driver := gitCapture(t, root, "config", "--get", "merge.chronicle-graph.driver")
	if strings.TrimSpace(driver) == "" {
		t.Error("merge.chronicle-graph.driver is empty after attach")
	}
}

func TestAttachThenDetachRoundtrip(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Existing agents doc\n"), 0644)

	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	ag, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(ag), wiring.AgentsMarkerStart) || !strings.Contains(string(ag), "# Existing agents doc") {
		t.Fatalf("AGENTS.md: %s", ag)
	}
	cl, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if !strings.Contains(string(cl), "@AGENTS.md") {
		t.Fatalf("CLAUDE.md: %s", cl)
	}
	settings, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if !strings.Contains(string(settings), "hook fire") {
		t.Fatalf("settings.json: %s", settings)
	}
	if !strings.Contains(out.String(), "chronicle scan") {
		t.Fatalf("missing scan hint:\n%s", out.String())
	}

	out.Reset()
	if err := runDetach(root, &out); err != nil {
		t.Fatal(err)
	}
	ag2, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Contains(string(ag2), wiring.AgentsMarkerStart) || !strings.Contains(string(ag2), "# Existing agents doc") {
		t.Fatalf("detached AGENTS.md: %s", ag2)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("chronicle-created CLAUDE.md should be deleted on detach")
	}
	settings2, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if strings.Contains(string(settings2), "hook fire") {
		t.Fatalf("hook still present: %s", settings2)
	}
}

func TestDetachWarnsOnInvalidSettingsJSON(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()

	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	id := wiring.ProjectID(root, gitRemote(root))

	settingsFile := filepath.Join(root, ".claude", "settings.json")
	if err := os.WriteFile(settingsFile, []byte("{ not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runDetach(root, &out); err != nil {
		t.Fatalf("runDetach should not abort on invalid settings.json, got: %v", err)
	}
	if !strings.Contains(out.String(), "not valid JSON") || !strings.Contains(out.String(), "left in place") {
		t.Fatalf("missing warning line:\n%s", out.String())
	}

	ag, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Contains(string(ag), wiring.AgentsMarkerStart) {
		t.Fatalf("AGENTS.md chronicle section should still be removed: %s", ag)
	}

	settings, _ := os.ReadFile(settingsFile)
	if string(settings) != "{ not valid json" {
		t.Fatalf("corrupt settings.json should be left untouched: %s", settings)
	}

	rec, err := wiring.LoadAttachment(id)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Fatalf("attachment record should be deleted, got: %+v", rec)
	}
}

func TestAttachAbortsOnInvalidSettingsJSON(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()

	settingsFile := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsFile, []byte("{ not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runAttach(root, &out)
	if err == nil {
		t.Fatal("expected runAttach to abort on invalid settings.json")
	}
	if !strings.Contains(err.Error(), settingsFile) {
		t.Fatalf("error should mention settings path %q, got: %v", settingsFile, err)
	}

	if _, statErr := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(statErr) {
		t.Fatal("AGENTS.md should not have been created — ApplyPlan never ran")
	}
	if _, statErr := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(statErr) {
		t.Fatal("CLAUDE.md should not have been created — ApplyPlan never ran")
	}
}

func TestReattachPreservesOwnershipRecord(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()

	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	id := wiring.ProjectID(root, gitRemote(root))
	rec, err := wiring.LoadAttachment(id)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Changes.ClaudeFileCreated {
		t.Fatalf("expected ClaudeFileCreated=true after fresh attach, got record: %+v", rec)
	}

	out.Reset()
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	rec2, err := wiring.LoadAttachment(id)
	if err != nil {
		t.Fatal(err)
	}
	if !rec2.Changes.ClaudeFileCreated {
		t.Fatalf("expected ClaudeFileCreated to remain true after re-attach, got record: %+v", rec2)
	}
}

// ─── root --project flag ────────────────────────────────────────────────

func TestAttachHonorsRootProjectFlag(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	dir := t.TempDir()

	projectPath = dir
	defer func() { projectPath = "" }()

	cmd := newAttachCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md not written to --project dir: %v", err)
	}
}

func TestAttachPositionalArgBeatsRootProjectFlag(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	flagDir := t.TempDir()
	argDir := t.TempDir()

	projectPath = flagDir
	defer func() { projectPath = "" }()

	cmd := newAttachCmd()
	if err := cmd.RunE(cmd, []string{argDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(argDir, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md not written to positional [dir]: %v", err)
	}
	if _, err := os.Stat(filepath.Join(flagDir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("--project dir should be untouched when a positional [dir] is given, got err=%v", err)
	}
}

func TestDetachHonorsRootProjectFlag(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	dir := t.TempDir()

	var out bytes.Buffer
	if err := runAttach(dir, &out); err != nil {
		t.Fatal(err)
	}

	projectPath = dir
	defer func() { projectPath = "" }()

	cmd := newDetachCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	ag, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if strings.Contains(string(ag), wiring.AgentsMarkerStart) {
		t.Fatalf("detach did not use --project dir, chronicle section still present: %s", ag)
	}
}

func TestDetachPositionalArgBeatsRootProjectFlag(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	flagDir := t.TempDir()
	argDir := t.TempDir()

	var out bytes.Buffer
	if err := runAttach(flagDir, &out); err != nil {
		t.Fatal(err)
	}
	if err := runAttach(argDir, &out); err != nil {
		t.Fatal(err)
	}

	projectPath = flagDir
	defer func() { projectPath = "" }()

	cmd := newDetachCmd()
	if err := cmd.RunE(cmd, []string{argDir}); err != nil {
		t.Fatal(err)
	}
	agArg, _ := os.ReadFile(filepath.Join(argDir, "AGENTS.md"))
	if strings.Contains(string(agArg), wiring.AgentsMarkerStart) {
		t.Fatalf("detach should have used positional [dir], chronicle section still present: %s", agArg)
	}
	agFlag, _ := os.ReadFile(filepath.Join(flagDir, "AGENTS.md"))
	if !strings.Contains(string(agFlag), wiring.AgentsMarkerStart) {
		t.Fatalf("--project dir should be untouched when a positional [dir] is given, got: %s", agFlag)
	}
}

func TestAttachIsIdempotent(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()
	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if string(first) != string(second) {
		t.Fatalf("attach not idempotent:\n%s\nvs\n%s", first, second)
	}
}

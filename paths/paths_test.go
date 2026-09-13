package paths

import "testing"

func reset() {
	SetProjectRoot("")
	SetChronicleDir("")
}

func TestDefaults(t *testing.T) {
	reset()
	if got := ConfiguredDir(); got != ".depbot" {
		t.Errorf("ConfiguredDir() = %q, want .depbot", got)
	}
	if got := Dir(); got != ".depbot" {
		t.Errorf("Dir() = %q, want .depbot (relative, no project root)", got)
	}
	if got := Root(); got != "" {
		t.Errorf("Root() = %q, want empty", got)
	}
}

func TestRelativeDirWithProjectRoot(t *testing.T) {
	reset()
	SetProjectRoot("/proj")
	if got := Dir(); got != "/proj/.depbot" {
		t.Errorf("Dir() = %q, want /proj/.depbot", got)
	}
	SetChronicleDir(".depbot-exp")
	if got := Dir(); got != "/proj/.depbot-exp" {
		t.Errorf("Dir() = %q, want /proj/.depbot-exp", got)
	}
}

func TestRelativeDirNoProjectRoot(t *testing.T) {
	reset()
	SetChronicleDir(".depbot-exp")
	if got := Dir(); got != ".depbot-exp" {
		t.Errorf("Dir() = %q, want .depbot-exp (relative to cwd, like today's default)", got)
	}
}

func TestAbsoluteDirWinsOverRoot(t *testing.T) {
	reset()
	SetProjectRoot("/proj")
	SetChronicleDir("/tmp/exp")
	if got := Dir(); got != "/tmp/exp" {
		t.Errorf("Dir() = %q, want /tmp/exp", got)
	}
}

func TestDirAt(t *testing.T) {
	reset()
	SetChronicleDir(".depbot-exp")
	if got := DirAt("/other"); got != "/other/.depbot-exp" {
		t.Errorf("DirAt(/other) = %q, want /other/.depbot-exp", got)
	}
	if got := DirAt(""); got != ".depbot-exp" {
		t.Errorf("DirAt(\"\") = %q, want .depbot-exp", got)
	}
	SetChronicleDir("/abs/dir")
	if got := DirAt("/other"); got != "/abs/dir" {
		t.Errorf("DirAt with absolute config = %q, want /abs/dir", got)
	}
}

func TestEmptyResetsToDefault(t *testing.T) {
	reset()
	SetChronicleDir(".depbot-exp")
	SetChronicleDir("")
	if got := Dir(); got != ".depbot" {
		t.Errorf("Dir() after reset = %q, want .depbot", got)
	}
}

// The directory the graph lives in and the directory git is measured in are
// two different answers, and the split is the whole point: a linked worktree
// resolves its graph to the main checkout while its HEAD stays its own. One
// accessor owns the second answer so the MCP tools, the CLI and the dashboard
// cannot each pick a different one.
func TestGitDirIsIndependentOfTheGraphRoot(t *testing.T) {
	defer func() {
		SetProjectRoot("")
		SetGitDir("")
	}()

	SetGitDir("")
	if got := GitDir(); got != "." {
		t.Errorf("unset GitDir must mean the process working directory, got %q", got)
	}

	SetGitDir("/work/feature")
	SetProjectRoot("/work/main")
	if got := GitDir(); got != "/work/feature" {
		t.Errorf("GitDir must not follow the graph root, got %q", got)
	}
	if got := Root(); got != "/work/main" {
		t.Errorf("Root changed unexpectedly: %q", got)
	}
}

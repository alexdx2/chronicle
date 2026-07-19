package wiring

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const codexAdapterVersion = 1

// CodexAdapter wires the Codex CLI: config.toml MCP server block + SessionStart
// hook (both user-owned, sentinel-wrapped), a global AGENTS.md section
// (user-owned), and chronicle-owned prompt files. Project AGENTS.md is not in
// this plan — that's `attach`'s write boundary, not machine-level setup.
type CodexAdapter struct {
	CodexDir   string                       // default ~/.codex
	BinaryPath string                       // default CanonicalBinaryPath()
	LookPath   func(string) (string, error) // default exec.LookPath
}

func NewCodexAdapter() *CodexAdapter {
	home, _ := os.UserHomeDir()
	return &CodexAdapter{
		CodexDir:   filepath.Join(home, ".codex"),
		BinaryPath: CanonicalBinaryPath(),
		LookPath:   exec.LookPath,
	}
}

func (a *CodexAdapter) ID() string          { return "codex" }
func (a *CodexAdapter) DisplayName() string { return "Codex CLI" }

func (a *CodexAdapter) configPath() string { return filepath.Join(a.CodexDir, "config.toml") }

func (a *CodexAdapter) Detect() Detection {
	if _, err := a.LookPath("codex"); err == nil {
		return Detection{Confidence: DetectionConfirmed, Evidence: []string{"codex executable on PATH"}}
	}
	if info, err := os.Stat(a.configPath()); err == nil && info.Size() > 0 {
		return Detection{Confidence: DetectionProbable, Evidence: []string{a.configPath() + " exists"}}
	}
	if _, err := os.Stat(a.CodexDir); err == nil {
		return Detection{Confidence: DetectionStale, Evidence: []string{a.CodexDir + " exists but no CLI, no active config"}}
	}
	return Detection{Confidence: DetectionAbsent}
}

// readTarget returns (content, exists) and treats read errors as non-existence
// at Detect time; Plan re-reads and propagates real errors.
func readTarget(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (a *CodexAdapter) Plan() (*Plan, error) {
	p := &Plan{Agent: "codex"}

	cfg, cfgExists, err := readTarget(a.configPath())
	if err != nil {
		return nil, err
	}
	action, content := planCodexMCPConfig(cfg, cfgExists, a.BinaryPath)
	p.Changes = append(p.Changes, PlannedChange{
		Target: a.configPath(), Ownership: OwnershipUser, Action: action,
		Summary: "MCP server block (sentinel-wrapped)", NewContent: content,
	})
	// Session hook splices into the config AFTER the MCP block change; plan it
	// against the post-MCP content so both land in one write when both change.
	base, baseExists := cfg, cfgExists
	if content != nil {
		base, baseExists = content, true
	}
	hookAction, hookContent := planCodexSessionHook(base, baseExists)
	if hookContent != nil {
		// merge: hook content already includes the MCP change — replace the
		// earlier planned config write with the combined content
		p.Changes[0].NewContent = hookContent
		if p.Changes[0].Action == ActionUnchanged {
			p.Changes[0].Action = hookAction
		}
		p.Changes[0].Summary = "MCP server block + SessionStart reminder (sentinel-wrapped)"
	}

	agentsPath := filepath.Join(a.CodexDir, "AGENTS.md")
	ag, agExists, err := readTarget(agentsPath)
	if err != nil {
		return nil, err
	}
	agAction, agContent := PlanMarkedSection(ag, agExists, GlobalAgentsSection())
	p.Changes = append(p.Changes, PlannedChange{
		Target: agentsPath, Ownership: OwnershipUser, Action: agAction,
		Summary: "global Chronicle guidance section", NewContent: agContent,
	})

	for name, body := range CodexPrompts() {
		target := filepath.Join(a.CodexDir, "prompts", name)
		want := []byte(body + "\n")
		cur, exists, err := readTarget(target)
		if err != nil {
			return nil, err
		}
		act := ActionCreate
		switch {
		case exists && string(cur) == string(want):
			act = ActionUnchanged
			want = nil
		case exists:
			act = ActionUpdate
		}
		p.Changes = append(p.Changes, PlannedChange{
			Target: target, Ownership: OwnershipChronicle, Action: act,
			Summary: "custom prompt /" + strings.TrimSuffix(name, ".md"), NewContent: want,
		})
	}
	return p, nil
}

func (a *CodexAdapter) Verify() Verification {
	cfg, exists, _ := readTarget(a.configPath())
	if !exists || !strings.Contains(string(cfg), codexSentinelStart) {
		return Verification{Level: VerifyNone, Health: HealthNotInstalled}
	}
	v := Verification{Level: VerifyFile, Health: HealthInstalled,
		Notes: []string{"MCP handshake not verified (functional check lands with the Claude adapter plan)"}}
	if info, err := os.Stat(a.BinaryPath); err == nil && info.Mode().IsRegular() {
		v.Level = VerifyReachable
		v.Notes = append(v.Notes, "canonical binary present")
	}
	return v
}

func (a *CodexAdapter) Remove() (*Plan, error) {
	p := &Plan{Agent: "codex"}
	cfg, exists, err := readTarget(a.configPath())
	if err != nil {
		return nil, err
	}
	if exists {
		text := string(cfg)
		text = removeSentinelBlock(text, codexSentinelStart, codexSentinelEnd)
		text = removeSentinelBlock(text, codexHookSentinelStart, codexHookSentinelEnd)
		if text != string(cfg) {
			p.Changes = append(p.Changes, PlannedChange{
				Target: a.configPath(), Ownership: OwnershipUser, Action: ActionUpdate,
				Summary: "remove Chronicle sentinel blocks", NewContent: []byte(text),
			})
		}
	}
	agentsPath := filepath.Join(a.CodexDir, "AGENTS.md")
	if ag, agExists, _ := readTarget(agentsPath); agExists {
		if act, content, found := RemoveMarkedSection(ag); found {
			p.Changes = append(p.Changes, PlannedChange{
				Target: agentsPath, Ownership: OwnershipUser, Action: act,
				Summary: "remove Chronicle section", NewContent: content,
			})
		}
	}
	for name := range CodexPrompts() {
		target := filepath.Join(a.CodexDir, "prompts", name)
		if _, exists, _ := readTarget(target); exists {
			p.Changes = append(p.Changes, PlannedChange{
				Target: target, Ownership: OwnershipChronicle, Action: ActionDelete,
				Summary: "delete prompt " + name,
			})
		}
	}
	return p, nil
}

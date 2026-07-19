package cli

// Agent setup — delivers Chronicle usage instructions to coding agents that
// don't read CLAUDE.md (Codex, OpenCode, Gemini CLI, ...).
//
// Two delivery channels, both idempotent:
//   - AGENTS.md sections wrapped in <!-- chronicle:start/end --> markers,
//     upserted without touching surrounding user content
//   - ~/.codex/config.toml [mcp_servers.chronicle] block wrapped in sentinel
//     comments; a user-managed section outside the sentinels is never touched
//
// Content and splice logic live in internal/wiring as pure planners
// (byte-in, (action, newContent)-out); this command wires them to the
// filesystem via wiring's transitional Upsert*File convenience functions
// (Task 8 rewrites this command onto explicit Plan/Apply).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/spf13/cobra"
)

// Actions reported to the user by setup commands.
const (
	setupCreated   = "created"
	setupUpdated   = "updated"
	setupUnchanged = "unchanged"
	setupSkipped   = "skipped (user-managed)"
)

// chronicleBinaryPath returns the path to write into agent configs. Prefers
// the invocation path (keeps a ~/.local/bin symlink pointing at "latest"
// working after upgrades) over the fully resolved executable.
func chronicleBinaryPath() string {
	if p, err := exec.LookPath(os.Args[0]); err == nil {
		if abs, absErr := filepath.Abs(p); absErr == nil {
			return abs
		}
		return p
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "chronicle"
}

func newSetupCmd() *cobra.Command {
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Configure coding agents to use Chronicle MCP",
	}
	setup.AddCommand(newSetupCodexCmd())
	return setup
}

func newSetupCodexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "codex",
		Short: "Register Chronicle in ~/.codex/config.toml and write AGENTS.md guidance",
		Run: func(cmd *cobra.Command, args []string) {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(os.Stderr, "cannot resolve home directory: %v\n", err)
				os.Exit(1)
			}

			configPath := filepath.Join(home, ".codex", "config.toml")
			wiringAction, err := wiring.UpsertCodexMCPConfigFile(configPath, chronicleBinaryPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error updating %s: %v\n", configPath, err)
				os.Exit(1)
			}
			fmt.Printf("%-40s %s\n", configPath, setupActionWord(wiringAction))

			hookChanged, err := wiring.UpsertCodexSessionHookFile(configPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error installing SessionStart hook in %s: %v\n", configPath, err)
				os.Exit(1)
			}
			fmt.Printf("%-40s %s\n", configPath+" (SessionStart hook)", changedWord(hookChanged))

			globalAgents := filepath.Join(home, ".codex", "AGENTS.md")
			changed, err := wiring.UpsertMarkedSectionFile(globalAgents, wiring.GlobalAgentsSection())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error updating %s: %v\n", globalAgents, err)
				os.Exit(1)
			}
			fmt.Printf("%-40s %s\n", globalAgents, changedWord(changed))

			promptsDir := filepath.Join(home, ".codex", "prompts")
			prompts := wiring.CodexPrompts()
			for name, content := range prompts {
				if err := wiring.AtomicWrite(filepath.Join(promptsDir, name), []byte(content+"\n"), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "error writing prompts to %s: %v\n", promptsDir, err)
					os.Exit(1)
				}
			}
			fmt.Printf("%-40s %d prompts (/chronicle-scan, /chronicle-impact, ...)\n", promptsDir, len(prompts))

			// Project-level AGENTS.md only when run inside a Chronicle project;
			// other projects get it automatically on first chronicle command.
			if _, statErr := os.Stat(paths.Dir()); statErr == nil {
				changed, err = wiring.UpsertMarkedSectionFile("AGENTS.md", wiring.ProjectAgentsSection())
				if err != nil {
					fmt.Fprintf(os.Stderr, "error updating AGENTS.md: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("%-40s %s\n", "AGENTS.md", changedWord(changed))
			}

			fmt.Println("\nDone. Restart Codex to pick up the MCP server.")
		},
	}
}

func changedWord(changed bool) string {
	if changed {
		return setupUpdated
	}
	return setupUnchanged
}

// setupActionWord translates a wiring.Action* code into this command's
// user-facing wording (preserved verbatim from the pre-refactor CLI output).
func setupActionWord(action string) string {
	switch action {
	case wiring.ActionCreate:
		return setupCreated
	case wiring.ActionUpdate:
		return setupUpdated
	case wiring.ActionSkip:
		return setupSkipped
	default: // wiring.ActionUnchanged
		return setupUnchanged
	}
}

package cli

// chronicle attach/detach — per-project wiring (AGENTS.md section, CLAUDE.md
// import bridge, freshness hook) with an ownership record so detach removes
// exactly what chronicle added. Runs no scan: scans are agent-driven.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/spf13/cobra"
)

func gitRemote(root string) string {
	out, err := exec.Command("git", "-C", root, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readProjectFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func runAttach(root string, out io.Writer) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	remote := gitRemote(root)
	id := wiring.ProjectID(root, remote)
	prev, err := wiring.LoadAttachment(id)
	if err != nil {
		return err
	}
	rec := &wiring.AttachmentRecord{SchemaVersion: 1, ProjectPath: root, GitRemote: remote,
		AttachedAt: time.Now().UTC().Format(time.RFC3339)}
	if prev != nil {
		rec.Changes = prev.Changes // upgrade run: keep ownership facts
	}

	plan := &wiring.Plan{Agent: "project-attach"}

	agentsPath := filepath.Join(root, "AGENTS.md")
	ag, agExists, err := readProjectFile(agentsPath)
	if err != nil {
		return err
	}
	hadMarker := agExists && strings.Contains(string(ag), wiring.AgentsMarkerStart)
	agAction, agContent := wiring.PlanMarkedSection(ag, agExists, wiring.ProjectAgentsSection())
	plan.Changes = append(plan.Changes, wiring.PlannedChange{
		Target: agentsPath, Ownership: wiring.OwnershipUser, Action: agAction,
		Summary: "Chronicle project section", NewContent: agContent,
	})
	if !hadMarker && agAction != wiring.ActionUnchanged {
		rec.Changes.AgentsSectionAdded = true
	}

	claudePath := filepath.Join(root, "CLAUDE.md")
	cl, clExists, err := readProjectFile(claudePath)
	if err != nil {
		return err
	}
	clAction, clContent, created, importAdded := wiring.PlanClaudeBridge(cl, clExists)
	plan.Changes = append(plan.Changes, wiring.PlannedChange{
		Target: claudePath, Ownership: wiring.OwnershipUser, Action: clAction,
		Summary: "CLAUDE.md → @AGENTS.md import", NewContent: clContent,
	})
	rec.Changes.ClaudeFileCreated = rec.Changes.ClaudeFileCreated || created
	rec.Changes.ClaudeImportAdded = rec.Changes.ClaudeImportAdded || importAdded

	settingsFile := filepath.Join(root, ".claude", "settings.json")
	settings, _, err := readProjectFile(settingsFile)
	if err != nil {
		return err
	}
	hookCmd := fmt.Sprintf("%q hook fire", wiring.CanonicalBinaryPath())
	merged, hookChanged, err := mergeHookIntoSettings(settings, hookMatcher, hookCmd)
	if err != nil {
		return fmt.Errorf("%s is not valid JSON — fix it by hand and re-run: %w", settingsFile, err)
	}
	if hookChanged {
		act := wiring.ActionUpdate
		if len(settings) == 0 {
			act = wiring.ActionCreate
		}
		plan.Changes = append(plan.Changes, wiring.PlannedChange{
			Target: settingsFile, Ownership: wiring.OwnershipUser, Action: act,
			Summary: "PreToolUse freshness hook", NewContent: merged,
		})
		rec.Changes.HookEntryAdded = true
	}

	if _, err := wiring.ApplyPlan(plan); err != nil {
		return err
	}
	if err := wiring.SaveAttachment(id, rec); err != nil {
		return err
	}

	fmt.Fprintln(out, "✓ Project attached")
	for _, c := range plan.Changes {
		fmt.Fprintf(out, "  %-9s %s\n", c.Action, c.Target)
	}
	if _, err := os.Stat(filepath.Join(root, chronicleDir, "chronicle.db")); os.IsNotExist(err) {
		fmt.Fprintln(out, "\nGraph has not been scanned yet.")
		fmt.Fprintln(out, `Ask your agent: "chronicle scan" (scans run through the agent, not this CLI).`)
	}
	return nil
}

func runDetach(root string, out io.Writer) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	id := wiring.ProjectID(root, gitRemote(root))
	rec, err := wiring.LoadAttachment(id)
	if err != nil {
		return err
	}
	changes := wiring.AttachmentChanges{AgentsSectionAdded: true, ClaudeImportAdded: true, HookEntryAdded: true}
	if rec != nil {
		changes = rec.Changes
	} else {
		fmt.Fprintln(out, "note: no attachment record — removing chronicle-marked wiring conservatively")
	}

	plan := &wiring.Plan{Agent: "project-detach"}

	agentsPath := filepath.Join(root, "AGENTS.md")
	if ag, agExists, _ := readProjectFile(agentsPath); agExists {
		if act, content, found := wiring.RemoveMarkedSection(ag); found {
			plan.Changes = append(plan.Changes, wiring.PlannedChange{
				Target: agentsPath, Ownership: wiring.OwnershipUser, Action: act,
				Summary: "remove Chronicle section", NewContent: content,
			})
		}
	}
	claudePath := filepath.Join(root, "CLAUDE.md")
	cl, clExists, _ := readProjectFile(claudePath)
	if act, content := wiring.PlanClaudeBridgeRemoval(cl, clExists, changes); act != wiring.ActionUnchanged {
		plan.Changes = append(plan.Changes, wiring.PlannedChange{
			Target: claudePath, Ownership: wiring.OwnershipUser, Action: act,
			Summary: "remove CLAUDE.md import bridge", NewContent: content,
		})
	}
	settingsFile := filepath.Join(root, ".claude", "settings.json")
	if settings, sExists, _ := readProjectFile(settingsFile); sExists && changes.HookEntryAdded {
		cleaned, hookRemoved, herr := removeHookFromSettings(settings, hookFireMarker())
		if herr == nil && hookRemoved {
			plan.Changes = append(plan.Changes, wiring.PlannedChange{
				Target: settingsFile, Ownership: wiring.OwnershipUser, Action: wiring.ActionUpdate,
				Summary: "remove Chronicle hook", NewContent: cleaned,
			})
		}
	}

	if _, err := wiring.ApplyPlan(plan); err != nil {
		return err
	}
	if err := wiring.DeleteAttachment(id); err != nil {
		return err
	}
	fmt.Fprintln(out, "✓ Project detached")
	for _, c := range plan.Changes {
		fmt.Fprintf(out, "  %-9s %s\n", c.Action, c.Target)
	}
	return nil
}

func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach [dir]",
		Short: "Wire this project (AGENTS.md section, CLAUDE.md import, freshness hook)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			return runAttach(root, os.Stdout)
		},
	}
	cmd.Flags().Bool("upgrade", false, "Refresh chronicle-managed sections (attach is idempotent; flag kept for discoverability)")
	return cmd
}

func newDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detach [dir]",
		Short: "Remove Chronicle project wiring added by attach",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			return runDetach(root, os.Stdout)
		},
	}
}

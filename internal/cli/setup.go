package cli

// chronicle setup — machine-level agent wiring via the adapter registry.
// Write boundary: this command (plus attach/detach/doctor) is the ONLY place
// wiring files are written. See the design spec
// (docs/superpowers/specs/2026-07-18-agent-setup-distribution-design.md).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/version"
	"github.com/spf13/cobra"
)

type setupOptions struct {
	agents   []string
	all      bool
	detected bool
	yes      bool
	dryRun   bool
	jsonOut  bool
	status   bool
	// test seam: when set, codex adapter uses this dir instead of ~/.codex
	codexDirOverride string
	stdin            io.Reader
	// test seam: destination for human/progress output when jsonOut is set.
	// Defaults to os.Stderr when nil.
	errOut io.Writer
}

func selectAdapters(opts setupOptions) ([]wiring.Adapter, error) {
	pick := func(id string) (wiring.Adapter, error) {
		a, ok := wiring.AdapterByID(id)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q (available: %s)", id, strings.Join(wiring.AdapterIDs(), ", "))
		}
		if ca, isCodex := a.(*wiring.CodexAdapter); isCodex && opts.codexDirOverride != "" {
			ca.CodexDir = opts.codexDirOverride
		}
		return a, nil
	}
	if len(opts.agents) > 0 {
		var out []wiring.Adapter
		for _, id := range opts.agents {
			a, err := pick(id)
			if err != nil {
				return nil, err
			}
			out = append(out, a)
		}
		return out, nil
	}
	// --all / --detected / default: walk the registry
	var out []wiring.Adapter
	for _, id := range wiring.AdapterIDs() {
		a, err := pick(id)
		if err != nil {
			return nil, err
		}
		if opts.all {
			out = append(out, a)
			continue
		}
		switch a.Detect().Confidence {
		case wiring.DetectionConfirmed:
			out = append(out, a)
		case wiring.DetectionProbable:
			if opts.yes {
				fmt.Fprintf(os.Stderr, "skipping %s: only probable detection (re-run with --agent %s to force)\n", a.ID(), a.ID())
				continue
			}
			out = append(out, a) // interactive confirm happens before apply
		}
	}
	return out, nil
}

func runSetup(opts setupOptions, out io.Writer) error {
	if opts.status {
		return runWiringStatus(out)
	}
	// In JSON mode, out carries ONLY the final JSON document. All human
	// progress/prompt output routes to a separate writer (stderr by default)
	// so stdout stays parseable.
	human := out
	if opts.jsonOut {
		human = opts.errOut
		if human == nil {
			human = os.Stderr
		}
	}
	adapters, err := selectAdapters(opts)
	if err != nil {
		return err
	}
	if len(adapters) == 0 {
		fmt.Fprintln(human, "No coding agents detected. Supported:", strings.Join(wiring.AdapterIDs(), ", "))
		fmt.Fprintln(human, "Force one with: chronicle setup --agent <id>")
		return nil
	}

	binPath := wiring.CanonicalBinaryPath()
	if !opts.dryRun {
		src, err := wiring.ResolveSourceBinary()
		if err != nil {
			return fmt.Errorf("resolve chronicle binary: %w", err)
		}
		if binPath, err = wiring.EnsureCanonicalBinaryFrom(src); err != nil {
			return fmt.Errorf("install canonical binary: %w", err)
		}
		fmt.Fprintf(human, "canonical binary: %s\n\n", binPath)
	} else {
		fmt.Fprintf(human, "canonical binary (dry-run, not written): %s\n\n", binPath)
	}

	type agentReport struct {
		ID           string `json:"id"`
		Detection    string `json:"detection"`
		Applied      int    `json:"applied"`
		Verification string `json:"verification"`
		Health       string `json:"health"`
		Error        string `json:"error,omitempty"`
	}
	var reports []agentReport

	st, err := wiring.LoadState()
	if err != nil {
		return err
	}
	st.ChronicleVersion = version.Identity().Fingerprint

	for _, a := range adapters {
		det := a.Detect()
		rep := agentReport{ID: a.ID(), Detection: string(det.Confidence)}
		plan, perr := a.Plan()
		if perr != nil {
			rep.Error = perr.Error()
			rep.Health = string(wiring.HealthFailed)
			reports = append(reports, rep)
			fmt.Fprintf(human, "✗ %s: plan failed: %v (fix the file by hand and re-run)\n", a.ID(), perr)
			continue
		}
		fmt.Fprintf(human, "%s (%s):\n", a.DisplayName(), det.Confidence)
		mutating := 0
		for _, c := range plan.Changes {
			fmt.Fprintf(human, "  %-9s %-11s %s — %s\n", c.Action, "["+string(c.Ownership)+"]", c.Target, c.Summary)
			if c.Mutating() {
				mutating++
			}
		}
		if opts.dryRun {
			reports = append(reports, rep)
			continue
		}
		if mutating > 0 && !opts.yes {
			if !confirm(opts.stdin, human, fmt.Sprintf("Apply %d change(s) for %s? [Y/n] ", mutating, a.ID())) {
				fmt.Fprintf(human, "  skipped by user\n")
				reports = append(reports, rep)
				continue
			}
		}
		res, aerr := wiring.ApplyPlan(plan)
		if aerr != nil {
			rep.Error = aerr.Error()
			rep.Health = res.Status // rolled-back | partial
			reports = append(reports, rep)
			fmt.Fprintf(human, "✗ %s: %v\n", a.ID(), aerr)
			continue // one adapter failing never blocks the rest
		}
		rep.Applied = res.Applied
		v := a.Verify()
		rep.Verification, rep.Health = string(v.Level), string(v.Health)
		reports = append(reports, rep)

		agState := wiring.AgentState{
			AdapterVersion: 1,
			InstalledAt:    time.Now().UTC().Format(time.RFC3339),
			Detection:      string(det.Confidence),
			Installation:   string(wiring.HealthInstalled),
			Verification:   string(v.Level),
			Health:         string(v.Health),
		}
		for _, c := range plan.Changes {
			if !c.Mutating() {
				continue
			}
			if c.Ownership == wiring.OwnershipChronicle {
				h, _ := wiring.FileSHA256(c.Target)
				agState.Artifacts = append(agState.Artifacts, wiring.Artifact{Path: c.Target, Ownership: "chronicle", Hash: h})
			} else {
				agState.ConfigChanges = append(agState.ConfigChanges, wiring.ConfigChange{Path: c.Target, Key: c.Summary, Marker: wiring.AgentsMarkerStart})
			}
		}
		st.Agents[a.ID()] = agState
	}

	if !opts.dryRun {
		if err := wiring.SaveState(st); err != nil {
			return fmt.Errorf("save install state: %w", err)
		}
	}
	if opts.jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"agents": reports})
	}
	fmt.Fprintln(human, "\nSummary:")
	for _, r := range reports {
		mark := "✓"
		if r.Error != "" {
			mark = "✗"
		}
		fmt.Fprintf(human, "  %s %-10s detection=%s applied=%d verify=%s health=%s %s\n",
			mark, r.ID, r.Detection, r.Applied, r.Verification, r.Health, r.Error)
	}
	fmt.Fprintln(human, "\nNext: run 'chronicle attach' inside each project that should use the graph.")
	return nil
}

func confirm(in io.Reader, out io.Writer, prompt string) bool {
	if in == nil {
		in = os.Stdin
	}
	fmt.Fprint(out, prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

// renderAgentStatus is shared by `setup --status` and `chronicle doctor`.
func renderAgentStatus(out io.Writer, a wiring.Adapter, st *wiring.State) {
	det := a.Detect()
	v := a.Verify()
	fmt.Fprintf(out, "%s (%s):\n", a.DisplayName(), a.ID())
	fmt.Fprintf(out, "  detection:    %s %v\n", det.Confidence, det.Evidence)
	fmt.Fprintf(out, "  verification: %s\n", v.Level)
	fmt.Fprintf(out, "  health:       %s\n", v.Health)
	for _, n := range v.Notes {
		fmt.Fprintf(out, "  note:         %s\n", n)
	}
	if ag, ok := st.Agents[a.ID()]; ok {
		fmt.Fprintf(out, "  installed:    %s (adapter v%d)\n", ag.InstalledAt, ag.AdapterVersion)
	} else if v.Health == wiring.HealthNotInstalled {
		fmt.Fprintf(out, "  suggested:    chronicle setup --agent %s\n", a.ID())
	}
}

func runWiringStatus(out io.Writer) error {
	st, err := wiring.LoadState()
	if err != nil {
		return err
	}
	for _, id := range wiring.AdapterIDs() {
		a, _ := wiring.AdapterByID(id)
		renderAgentStatus(out, a, st)
	}
	return nil
}

func newSetupCmd() *cobra.Command {
	opts := setupOptions{}
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Wire coding agents (MCP + trigger surface) on this machine",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(opts, os.Stdout)
		},
	}
	cmd.Flags().StringArrayVar(&opts.agents, "agent", nil, "Agent to wire (repeatable); omit to use detection")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Wire every supported agent regardless of detection")
	cmd.Flags().BoolVar(&opts.detected, "detected", false, "Wire detected agents (default behavior, kept for scripts)")
	cmd.Flags().BoolVar(&opts.yes, "yes", false, "Skip Chronicle's confirmation prompts (never overrides conflicts or host approvals)")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Show exact paths and changes, write nothing")
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "Machine-readable report")
	cmd.Flags().BoolVar(&opts.status, "status", false, "Show wiring status per agent")

	// Deprecated alias: chronicle setup codex
	codexAlias := &cobra.Command{
		Use:        "codex",
		Deprecated: "use 'chronicle setup --agent codex'; project wiring moved to 'chronicle attach'",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(setupOptions{agents: []string{"codex"}, yes: true}, os.Stdout)
		},
	}
	cmd.AddCommand(codexAlias)
	return cmd
}

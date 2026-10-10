package cli

// chronicle doctor — per-agent detection/verification/health, plus recovery
// hints for interrupted (partial/in-progress) setup operations. Read-only:
// this command never writes wiring files.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/spf13/cobra"
)

func runDoctor(agentFilter []string, out io.Writer) error {
	st, err := wiring.LoadState()
	if err != nil {
		return err
	}
	ids := wiring.AdapterIDs()
	if len(agentFilter) > 0 {
		ids = agentFilter
	}
	for _, id := range ids {
		a, ok := wiring.AdapterByID(id)
		if !ok {
			fmt.Fprintf(out, "unknown agent %q\n", id)
			continue
		}
		renderAgentStatus(out, a, st)
	}

	// Surface interrupted/partial applies — the recovery path for the
	// "no multi-file transaction" reality.
	jdir := filepath.Join(wiring.Home(), "journal")
	entries, _ := os.ReadDir(jdir)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(jdir, e.Name()))
		if err != nil {
			continue
		}
		var j struct {
			OperationID string `json:"operationId"`
			Agent       string `json:"agent"`
			Status      string `json:"status"`
		}
		if json.Unmarshal(data, &j) != nil {
			continue
		}
		if j.Status == "partial" || j.Status == "in-progress" {
			journalPath := filepath.Join(jdir, e.Name())
			fmt.Fprintf(out, "\n! operation %s (%s) is %s\n", j.OperationID, j.Agent, j.Status)
			fmt.Fprintf(out, "  journal: %s\n", journalPath)
			fmt.Fprintf(out, "  backups: %s\n", filepath.Join(wiring.Home(), "backups", j.OperationID))
			rerunCmd := reattachCommandFor(j.Agent)
			fmt.Fprintf(out, "  recover: restore listed backups, then re-run '%s'\n", rerunCmd)
			fmt.Fprintf(out, "           (re-running clears this alert; deleting %s manually also works)\n", journalPath)
		}
	}
	publicationReport(paths.GitDir(), out)
	return nil
}

// publicationReport prints the state of graph publications (spec 4.2, 7):
// trust mode, trusted publication count per chronicle/graphs/* ref, and
// every warning the publication map produced. Silent outside a git
// repository; a verifier error is shown as one "!" line and never aborts
// doctor.
func publicationReport(repoRoot string, out io.Writer) {
	if _, err := gitutil.Run(repoRoot, "rev-parse", "--git-dir"); err != nil {
		return
	}
	refs, err := snapshot.ChainRefs(repoRoot)
	if err != nil {
		fmt.Fprintf(out, "\ngraph publications:\n  ! %v\n", err)
		return
	}
	if len(refs) == 0 {
		// No publications: the trust list is irrelevant, do not load it.
		fmt.Fprintf(out, "\ngraph publications:\n  no chronicle/graphs/* publications in this clone\n")
		return
	}
	v, err := snapshot.LoadVerifier(repoRoot)
	if err != nil {
		fmt.Fprintf(out, "\ngraph publications:\n  ! %v\n", err)
		return
	}
	defer v.Close()
	fmt.Fprintf(out, "\ngraph publications:\n")
	switch v.Mode {
	case snapshot.VerifySigned:
		fmt.Fprintf(out, "  trust: signed by publishers in %s\n", v.Source)
	default:
		fmt.Fprintf(out, "  trust: no publishers configured, trusting every chronicle/graphs/* commit\n")
	}
	pubs, warns, err := snapshot.PublicationMap(repoRoot, snapshot.ResolveOptions{Verifier: v})
	if err != nil {
		fmt.Fprintf(out, "  ! %v\n", err)
		return
	}
	perRef := map[string]int{}
	for _, p := range pubs {
		perRef[p.Ref]++
	}
	for _, ref := range refs {
		fmt.Fprintf(out, "  %s: %d trusted publication(s)\n", ref, perRef[ref])
	}
	for _, w := range warns {
		if v.Mode == snapshot.TrustAll && strings.Contains(w, "trusting every") {
			continue // the trust line above already says it
		}
		fmt.Fprintf(out, "  ! %s\n", w)
	}
}

// reattachCommandFor maps a journal's agent to the real CLI command that
// re-runs the operation it recorded. "project-attach"/"project-detach" are
// internal Plan.Agent values for `chronicle attach`/`chronicle detach` — not
// adapter IDs — so `chronicle setup --agent <id>` (which only knows adapter
// IDs) is not a valid recovery command for them.
func reattachCommandFor(agent string) string {
	switch agent {
	case "project-attach":
		return "chronicle attach <dir>"
	case "project-detach":
		return "chronicle detach <dir>"
	default:
		return fmt.Sprintf("chronicle setup --agent %s", agent)
	}
}

func newDoctorCmd() *cobra.Command {
	var agents []string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose Chronicle agent wiring (detection, verification, interrupted installs)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(agents, os.Stdout)
		},
	}
	cmd.Flags().StringArrayVar(&agents, "agent", nil, "Limit to specific agent(s)")
	return cmd
}

package cli

import (
	"fmt"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

// migrateNextSteps is the exact follow-up a human runs after `chronicle
// graph migrate`: this command deliberately does not stage, commit, or run
// `git rm` itself (see newGraphMigrateCmd's Long text) — it only writes
// .depbot/graph.jsonl and brings .gitignore/.gitattributes up to date, and
// prints this line so the person doing the migration can paste it verbatim.
const migrateNextSteps = `git rm -r --cached .depbot/events && git add .depbot/graph.jsonl .gitignore .gitattributes && git commit`

func newGraphMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Backfill blobs from scan history and adopt the graph.jsonl snapshot format",
		Long: `For a project scanned before blob tracking existed, or whose event journal
used to be committed to git instead of .depbot/graph.jsonl: reconstructs
every already-scanned file's git blob id from scan history (see
snapshot.BackfillBlobs), writes the current graph as .depbot/graph.jsonl, and
brings .gitignore/.gitattributes up to the same shape "chronicle init"
writes so graph.jsonl is tracked and mergeable.

It does not stage, commit, or run "git rm" itself — it prints the exact
follow-up command for a human to run. A project with no Chronicle scan
history at all has nothing to migrate FROM, so this simply does what
"chronicle graph update" already does.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			g := openGraph()
			defer g.Store().Close()

			repoRoot := paths.GitDir()
			graphPath := filepath.Join(paths.Dir(), filepath.Base(snapshot.Path))

			result, err := runGraphMigrate(g.Store(), g.Registry(), repoRoot, graphPath)
			if err != nil {
				return err
			}

			if !result.Migrated {
				// Nothing to migrate from — behave exactly like `chronicle
				// graph update`, JSON output included.
				outputJSON(result.Update)
				return nil
			}
			printMigrateReport(result, graphPath)
			return nil
		},
	}
}

// migrateResult is runGraphMigrate's report. Migrated is false exactly when
// the project had no Chronicle scan history at all (hasScanHistory), in
// which case Backfill is the zero value and Update is the plain `chronicle
// graph update` result the command falls back to; Migrated true means the
// real backfill ran and both fields are populated.
type migrateResult struct {
	Migrated bool
	Backfill snapshot.BackfillReport
	Update   graphUpdateResult
}

// runGraphMigrate is the testable core of `chronicle graph migrate`: no
// cobra, no process exit, so it can be called directly against a real store
// and a temp repoRoot/graphPath.
//
// A project with no scan history at all (hasScanHistory false) has nothing
// for BackfillBlobs to backfill FROM, so this simply calls runGraphUpdate —
// the same testable core `chronicle graph update` itself calls — rather than
// reimplementing export, and skips the backfill and git-wiring steps below
// entirely (Migrated=false tells the caller which happened).
//
// Otherwise: BackfillBlobs reconstructs whatever blobs it can from scan
// history, runGraphUpdate exports the (now blob-aware) store to graphPath,
// and ensureGitWiring brings .gitignore/.gitattributes up to Task 6's
// shape — the same function `init`/`attach` already call for exactly this,
// reused rather than duplicated.
func runGraphMigrate(st *store.Store, reg *registry.Registry, repoRoot, graphPath string) (migrateResult, error) {
	hasHistory, err := hasScanHistory(st)
	if err != nil {
		return migrateResult{}, err
	}
	if !hasHistory {
		result, _, err := runGraphUpdate(st, reg, graphPath, false)
		if err != nil {
			return migrateResult{}, err
		}
		return migrateResult{Migrated: false, Update: result}, nil
	}

	backfill, err := snapshot.BackfillBlobs(st, repoRoot)
	if err != nil {
		return migrateResult{}, fmt.Errorf("backfill blobs: %w", err)
	}

	result, _, err := runGraphUpdate(st, reg, graphPath, false)
	if err != nil {
		return migrateResult{}, err
	}

	ensureGitWiring(repoRoot)

	return migrateResult{Migrated: true, Backfill: backfill, Update: result}, nil
}

// hasScanHistory reports whether any domain has ever recorded a scan
// revision — a project with none has nothing for `graph migrate` to migrate
// FROM (see runGraphMigrate). ListScanRevisions(domain, 1) is the cheapest
// possible existence check: a limit of 1 is a real, positive limit (unlike
// <= 0, which ListScanRevisions itself defaults to 20 rows for), and one row
// is all that's needed to prove the domain has scan history at all.
func hasScanHistory(st *store.Store) (bool, error) {
	domains, err := st.GetDomains()
	if err != nil {
		return false, fmt.Errorf("list domains: %w", err)
	}
	for _, d := range domains {
		revs, err := st.ListScanRevisions(d, 1)
		if err != nil {
			return false, fmt.Errorf("list revisions for %q: %w", d, err)
		}
		if len(revs) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// printMigrateReport prints the human-readable backfill report and the next
// steps for the person running `chronicle graph migrate` — plain text, not
// JSON: unlike `graph update`/`graph status`, this command's whole point is
// readable next steps for a human to act on, not a machine-consumable
// summary.
func printMigrateReport(result migrateResult, graphPath string) {
	fmt.Printf("backfilled %d file(s) from scan history, %d unresolved (revision not found in git history)\n",
		result.Backfill.Updated, result.Backfill.Unresolved)
	fmt.Printf("wrote %s (seq %d)\n", graphPath, result.Update.Seq)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  " + migrateNextSteps)
}

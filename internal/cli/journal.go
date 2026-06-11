package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

func newJournalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "journal",
		Short: "Manage the event journal (outbox, flush, shadow verify)",
	}
	cmd.AddCommand(newJournalStatusCmd(), newJournalFlushCmd(), newJournalVerifyCmd())
	return cmd
}

func newJournalStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show outbox totals and events directory",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			var pending, total int
			if err := g.Store().QueryRowScan(
				`SELECT COUNT(*) FROM journal_outbox WHERE flushed_at IS NULL`, &pending); err != nil {
				outputError(err)
			}
			if err := g.Store().QueryRowScan(
				`SELECT COUNT(*) FROM journal_outbox`, &total); err != nil {
				outputError(err)
			}
			outputJSON(map[string]any{
				"outbox_pending": pending,
				"outbox_total":   total,
				"events_dir":     filepath.Join(g.Store().Dir(), "events"),
			})
		},
	}
}

func newJournalFlushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "flush",
		Short: "Flush pending outbox events to .depbot/events/<domain>.jsonl",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			n, err := g.Store().FlushJournal()
			if err != nil {
				outputError(err)
			}
			fmt.Printf("journal flush: %d event(s) written to %s\n",
				n, filepath.Join(g.Store().Dir(), "events"))
		},
	}
}

func newJournalVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Shadow validator: replay the journal into a temp db and diff against the live db",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			g := openGraph()
			live := g.Store()
			defer live.Close()

			if _, err := live.FlushJournal(); err != nil {
				return fmt.Errorf("flush before verify: %w", err)
			}

			tmpDir, err := os.MkdirTemp("", "chronicle-journal-verify-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmpDir)

			replayed, err := store.Open(filepath.Join(tmpDir, "replay.db"))
			if err != nil {
				return fmt.Errorf("open replay db: %w", err)
			}
			defer replayed.Close()

			eventsDir := filepath.Join(live.Dir(), "events")
			res, err := replayed.ReplayJournal(eventsDir)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Println("journal verify: no journal yet (events dir missing, nothing to replay)")
					return nil
				}
				return fmt.Errorf("replay: %w", err)
			}

			// Trust/confidence are derived, never journaled — recompute on the
			// replayed db so derived statuses match the live db before diffing.
			if err := graph.New(replayed, g.Registry()).RecalculateAllTrust(); err != nil {
				return fmt.Errorf("recalculate trust on replay db: %w", err)
			}

			diff, err := store.DiffGraphStores(live, replayed)
			if err != nil {
				return fmt.Errorf("diff: %w", err)
			}
			if diff.Clean() {
				fmt.Printf("journal verify: OK — replay matches live db (%d applied, %d skipped)\n",
					res.Applied, res.Skipped)
				return nil
			}
			fmt.Print(diff.String())
			return fmt.Errorf("journal verify: replay diverges from live db (%d only-live, %d only-replay, %d changed; %d applied, %d skipped)",
				len(diff.OnlyInA), len(diff.OnlyInB), len(diff.Changed), res.Applied, res.Skipped)
		},
	}
}

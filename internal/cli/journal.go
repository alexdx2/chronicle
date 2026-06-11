package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

func newJournalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "journal",
		Short: "Manage the event journal (outbox, flush, shadow verify)",
	}
	cmd.AddCommand(newJournalStatusCmd(), newJournalFlushCmd(), newJournalVerifyCmd(), newJournalRebuildCmd())
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

func newJournalRebuildCmd() *cobra.Command {
	var noVerify, keepBackup bool
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild chronicle.db from the event journal (replay + carry-over + atomic swap)",
		Long: `Rebuild chronicle.db from the event journal.

Replays .depbot/events/*.jsonl into a fresh database, recomputes derived
trust/confidence, carries over non-journaled local state from the old db,
verifies the result against the live db, then swaps the files.

Carried over from the old db (not journaled by design):
  graph_revisions, graph_snapshots, knowledge_contexts, graph_changelog,
  graph_discoveries, project_settings, diagram_sessions, mcp_request_log,
  scan_runs, scan_obligations, scan_extractions, evidence_verification_runs,
  journal_outbox (unflushed rows only), journal_state (lamport floor, MAX-merged).

Deliberately dropped:
  journal_applied_events (rebuilt by the replay itself),
  flushed journal_outbox rows (already durable in events/*.jsonl).

Note one asymmetry: carried graph_revisions keep their original ids, while
replayed graph rows have revision-id columns 0/NULL by design — the verify
diff ignores revision-id columns, and trust is recomputed, not replayed.

The old db is kept at .depbot/chronicle.db.pre-rebuild.bak until the swap
succeeds (always kept with --keep-backup).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			g := openGraph()
			live := g.Store()
			defer live.Close()
			livePath := dbPath // resolved by openGraph
			depDir := live.Dir()

			// 1. Drain the outbox: unflushed events exist only in the live db;
			// replay reads files, so anything pending would be lost otherwise.
			if _, err := live.FlushJournal(); err != nil {
				return fmt.Errorf("flush before rebuild: %w", err)
			}

			// 2. Build the new db at a tmp path (clear any stale attempt first).
			tmpPath := filepath.Join(depDir, "chronicle.rebuild.tmp.db")
			removeDBWithSidecars(tmpPath)
			rebuilt, err := store.Open(tmpPath)
			if err != nil {
				return fmt.Errorf("open rebuild db: %w", err)
			}
			rebuiltOpen := true
			defer func() {
				if rebuiltOpen {
					rebuilt.Close()
				}
			}()
			rebuilt.SetJournalActor(journalActor())

			eventsDir := filepath.Join(depDir, "events")
			res, err := rebuilt.ReplayJournal(eventsDir)
			if err != nil {
				removeOnAbort := func() { rebuilt.Close(); rebuiltOpen = false; removeDBWithSidecars(tmpPath) }
				if errors.Is(err, os.ErrNotExist) {
					removeOnAbort()
					return fmt.Errorf("journal rebuild: no journal yet (%s missing) — nothing to rebuild from", eventsDir)
				}
				removeOnAbort()
				return fmt.Errorf("replay: %w", err)
			}

			// Trust/confidence are derived, never journaled — recompute.
			if err := graph.New(rebuilt, g.Registry()).RecalculateAllTrust(); err != nil {
				return fmt.Errorf("recalculate trust on rebuilt db: %w", err)
			}

			// 3. Carry over non-journaled local state.
			carried, err := rebuilt.CopyLocalState(live)
			if err != nil {
				return fmt.Errorf("carry over local state: %w", err)
			}

			// 4. Verify: rebuilt graph must match the live graph semantically.
			if !noVerify {
				diff, err := store.DiffGraphStores(live, rebuilt)
				if err != nil {
					return fmt.Errorf("diff: %w", err)
				}
				if !diff.Clean() {
					fmt.Print(diff.String())
					fmt.Printf("rebuilt db kept for inspection at %s\n", tmpPath)
					return fmt.Errorf("journal rebuild: rebuilt db diverges from live db (%d only-live, %d only-rebuilt, %d changed) — aborted, live db untouched",
						len(diff.OnlyInA), len(diff.OnlyInB), len(diff.Changed))
				}
			}

			// 5. Swap. Checkpoint both dbs first so their -wal files are empty —
			// renaming/removing a db while it has live WAL content is the known
			// corruption path (see store.RecoveryHint). A busy checkpoint means
			// another process holds the db — abort rather than risk data loss.
			if err := checkpointTruncate(live, "live"); err != nil {
				return err
			}
			if err := checkpointTruncate(rebuilt, "rebuilt"); err != nil {
				return err
			}
			live.Close()
			rebuilt.Close()
			rebuiltOpen = false

			bakPath := livePath + ".pre-rebuild.bak"
			removeDBWithSidecars(bakPath)
			// Sidecars are empty after a TRUNCATE checkpoint + close: remove them
			// together with moving the db so no orphaned -wal/-shm pair survives.
			removeSidecars(livePath)
			if err := os.Rename(livePath, bakPath); err != nil {
				return fmt.Errorf("backup live db: %w (rebuilt db at %s)", err, tmpPath)
			}
			removeSidecars(tmpPath)
			if err := os.Rename(tmpPath, livePath); err != nil {
				// Restore: put the old db back so the workspace is not left empty.
				if rerr := os.Rename(bakPath, livePath); rerr != nil {
					return fmt.Errorf("swap failed (%v) AND restore failed (%v) — old db at %s, rebuilt db at %s", err, rerr, bakPath, tmpPath)
				}
				return fmt.Errorf("swap failed, old db restored: %w (rebuilt db at %s)", err, tmpPath)
			}
			if !keepBackup {
				removeDBWithSidecars(bakPath)
			}

			// 6. Summary.
			fmt.Printf("journal rebuild: OK — %d event(s) applied, %d skipped\n", res.Applied, res.Skipped)
			tables := make([]string, 0, len(carried))
			for t := range carried {
				tables = append(tables, t)
			}
			sort.Strings(tables)
			for _, t := range tables {
				if carried[t] > 0 {
					fmt.Printf("  carried %-28s %d row(s)\n", t, carried[t])
				}
			}
			fmt.Printf("swapped %s (rebuilt from %s)\n", livePath, eventsDir)
			if keepBackup {
				fmt.Printf("old db kept at %s\n", bakPath)
			}
			fmt.Println("WARNING: restart any long-lived chronicle processes (MCP server / admin dashboard) — they still hold the OLD database inode and will read stale data until restarted.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "Skip the live-vs-rebuilt diff before swapping")
	cmd.Flags().BoolVar(&keepBackup, "keep-backup", false, "Keep .depbot/chronicle.db.pre-rebuild.bak after a successful swap")
	return cmd
}

// checkpointTruncate runs PRAGMA wal_checkpoint(TRUNCATE) and fails if the
// checkpoint could not complete (another process holds the database).
func checkpointTruncate(s *store.Store, label string) error {
	var busy, logFrames, checkpointed int
	if err := s.QueryRowScan(`PRAGMA wal_checkpoint(TRUNCATE)`, &busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint %s db: %w", label, err)
	}
	if busy != 0 {
		return fmt.Errorf("checkpoint %s db: busy — another chronicle process (MCP server / admin) is holding the database; stop it and retry", label)
	}
	return nil
}

// removeSidecars deletes a db's -wal/-shm files (best effort).
func removeSidecars(dbFile string) {
	os.Remove(dbFile + "-wal")
	os.Remove(dbFile + "-shm")
}

// removeDBWithSidecars deletes a db file together with its sidecars.
// Order matters: sidecars first — a db without sidecars is a valid db, but
// orphaned -wal/-shm next to a recreated db corrupt it (store.RecoveryHint).
func removeDBWithSidecars(dbFile string) {
	removeSidecars(dbFile)
	os.Remove(dbFile)
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

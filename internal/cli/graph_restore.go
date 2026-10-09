package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

type restoreInput struct {
	RepoRoot  string // git worktree root
	DBPath    string // target .depbot/chronicle.db
	GraphPath string // .depbot/graph.jsonl in the worktree (used when At == "")
	At        string // git rev; "" = the file at GraphPath
	Force     bool
	Registry  *registry.Registry
}

type restoreResult struct {
	Path      string           `json:"path"`
	Seq       int              `json:"seq"`
	Commit    string           `json:"commit"` // commit the snapshot was taken from (At resolved) or HEAD
	Domains   []string         `json:"domains"`
	Counts    snapshot.Counts  `json:"counts"`
	Revisions map[string]int64 `json:"revisions"` // domain → restore revision id
}

var errDBExists = errors.New("database already exists — pass --force to replace it")

// runGraphRestore materialises a committed graph snapshot into a local
// database: the first step of a CI pipeline that rebuilds the graph so that
// `chronicle refresh` can run on it. It registers one "restore" scan revision
// per domain. The trigger_kind column is CHECK-constrained, so the revision
// is trigger "ci_pipeline" with metadata kind "restore"; anything but a
// git_hook revision counts as a scan revision, which
// is what refresh uses as its base) and marks the on-disk event journal as
// consumed — the snapshot supersedes it.
func runGraphRestore(in restoreInput) (restoreResult, error) {
	var (
		snap   *snapshot.Snapshot
		commit string
		src    string
	)
	if in.At != "" {
		v, err := snapshot.ResolveVersion(in.RepoRoot, in.At)
		if err != nil {
			return restoreResult{}, err
		}
		snap, _, err = snapshot.AtCommit(in.RepoRoot, v)
		if err != nil {
			return restoreResult{}, err
		}
		commit = v.Commit
		src = in.At
	} else {
		data, err := os.ReadFile(in.GraphPath)
		if err != nil {
			return restoreResult{}, err
		}
		snap, _, err = snapshot.Decode(bytes.NewReader(data), snapshot.DefaultLimits())
		if err != nil {
			return restoreResult{}, err
		}
		// A repository without commits has no HEAD; the revision then
		// records no commit.
		commit, _ = gitutil.Run(in.RepoRoot, "rev-parse", "HEAD")
		src = in.GraphPath
	}

	if _, err := os.Stat(in.DBPath); err == nil {
		if !in.Force {
			return restoreResult{}, errDBExists
		}
		for _, p := range []string{in.DBPath, in.DBPath + "-wal", in.DBPath + "-shm"} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return restoreResult{}, err
			}
		}
	}

	g, closer, err := snapshot.Load(snap, in.Registry, snapshot.LoadOptions{DBPath: in.DBPath, SkipJournalSync: true})
	if err != nil {
		return restoreResult{}, err
	}
	defer closer()

	revs := make(map[string]int64, len(snap.Header.Domains))
	for _, d := range snap.Header.Domains {
		id, _, err := g.Store().ClaimRevision(store.RevisionClaim{
			DomainKey: d, AfterSHA: commit, TriggerKind: "ci_pipeline", Mode: "full",
			Metadata: fmt.Sprintf(`{"kind":"restore","seq":%d}`, snap.Header.Seq),
		})
		if err != nil {
			return restoreResult{}, fmt.Errorf("claiming restore revision for %s: %w", d, err)
		}
		revs[d] = id
	}

	if err := g.Store().MarkJournalConsumed(filepath.Join(g.Store().Dir(), "events")); err != nil {
		return restoreResult{}, err
	}

	return restoreResult{
		Path: src, Seq: snap.Header.Seq, Commit: commit,
		Domains: snap.Header.Domains, Counts: snap.Header.Counts, Revisions: revs,
	}, nil
}

func newGraphRestoreCmd() *cobra.Command {
	var at string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Rebuild the local database from a committed graph.jsonl",
		Long: `Materialise the committed graph snapshot into .depbot/chronicle.db and register
a scan revision per domain at the snapshot's commit, so that 'chronicle refresh'
can continue from it. Meant for CI, where the database does not exist yet.

--at <rev> reads the snapshot from git history (the nearest commit at or before
<rev> that changed graph.jsonl); without it the file in the working tree is used.
--force replaces an existing database.

Exit codes: 0 restored, 2 no snapshot to restore, 1 any other error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			resolveDefaults()

			root, err := filepath.Abs(paths.GitDir())
			if err != nil {
				return err
			}
			var reg *registry.Registry
			if _, statErr := os.Stat(registryPath); statErr == nil {
				reg, err = registry.LoadFile(registryPath)
			} else {
				reg, err = registry.LoadDefaults()
			}
			if err != nil {
				return fmt.Errorf("loading registry: %w", err)
			}

			res, err := runGraphRestore(restoreInput{
				RepoRoot:  root,
				DBPath:    dbPath,
				GraphPath: filepath.Join(paths.Dir(), filepath.Base(snapshot.Path)),
				At:        at,
				Force:     force,
				Registry:  reg,
			})
			if err != nil {
				if errors.Is(err, snapshot.ErrNoVersion) || os.IsNotExist(err) {
					msg, _ := json.Marshal(map[string]string{"error": err.Error()})
					fmt.Fprintln(os.Stderr, string(msg))
					os.Exit(2)
				}
				return err
			}
			outputJSON(res)
			return nil
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "git revision to read the snapshot from (default: the working-tree file)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing database")
	return cmd
}

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	// MergeBaseWith, when set, resolves At as `git merge-base <At> <MergeBaseWith>`
	// before the snapshot lookup, so a restore from a moving branch tip picks a
	// commit that is an ancestor of the checkout.
	MergeBaseWith string
	Registry      *registry.Registry
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
		at := in.At
		if in.MergeBaseWith != "" {
			mb, err := gitutil.Run(in.RepoRoot, "merge-base", in.At, in.MergeBaseWith)
			if err != nil {
				return restoreResult{}, fmt.Errorf("merge-base of %s and %s: %w", in.At, in.MergeBaseWith, err)
			}
			at = strings.TrimSpace(mb)
		}
		v, err := snapshot.ResolveVersion(in.RepoRoot, at)
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

	loadPath := in.DBPath
	replacing := false
	if _, err := os.Stat(in.DBPath); err == nil {
		if !in.Force {
			return restoreResult{}, errDBExists
		}
		// Load next to the old database and swap only once the load worked:
		// a corrupt snapshot must not cost the caller the database it has.
		replacing = true
		loadPath = in.DBPath + ".restore-tmp"
		for _, p := range []string{loadPath, loadPath + "-wal", loadPath + "-shm"} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return restoreResult{}, err
			}
		}
	}

	g, closer, err := snapshot.Load(snap, in.Registry, snapshot.LoadOptions{DBPath: loadPath, SkipJournalSync: true})
	if err != nil {
		if replacing {
			for _, p := range []string{loadPath, loadPath + "-wal", loadPath + "-shm"} {
				os.Remove(p)
			}
		}
		return restoreResult{}, err
	}
	closed := false
	defer func() {
		if !closed {
			closer()
		}
	}()

	revs := make(map[string]int64, len(snap.Header.Domains))
	for _, d := range snap.Header.Domains {
		id, _, err := g.Store().ClaimRevision(store.RevisionClaim{
			DomainKey: d, AfterSHA: commit, TriggerKind: "ci_pipeline", Mode: "full",
			// A committed snapshot is, by contract, the state after its last
			// refresh: the pointer lets refresh's structural phase diff from
			// the snapshot's commit instead of sweeping the whole repository.
			Metadata: fmt.Sprintf(`{"kind":"restore","seq":%d,"structural":{"complete":true,"processed":0}}`, snap.Header.Seq),
		})
		if err != nil {
			return restoreResult{}, fmt.Errorf("claiming restore revision for %s: %w", d, err)
		}
		revs[d] = id
	}

	if err := g.Store().MarkJournalConsumed(filepath.Join(g.Store().Dir(), "events")); err != nil {
		return restoreResult{}, err
	}

	if replacing {
		// Close (checkpoints the WAL) before the rename, then drop the old
		// database's sidecars so they are not replayed onto the new file.
		closer()
		closed = true
		for _, p := range []string{in.DBPath + "-wal", in.DBPath + "-shm", loadPath + "-wal", loadPath + "-shm"} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return restoreResult{}, err
			}
		}
		if err := os.Rename(loadPath, in.DBPath); err != nil {
			return restoreResult{}, err
		}
	}

	return restoreResult{
		Path: src, Seq: snap.Header.Seq, Commit: commit,
		Domains: snap.Header.Domains, Counts: snap.Header.Counts, Revisions: revs,
	}, nil
}

func newGraphRestoreCmd() *cobra.Command {
	var at, mergeBaseWith string
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

				MergeBaseWith: mergeBaseWith,
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
	cmd.Flags().StringVar(&mergeBaseWith, "merge-base-with", "", "resolve --at as the merge base of --at and this revision (CI: --at origin/main --merge-base-with HEAD)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing database")
	return cmd
}

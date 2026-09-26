package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/internal/wiring"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

func newGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Manage the graph.jsonl snapshot — the git-committed source of truth",
	}
	cmd.AddCommand(newGraphUpdateCmd())
	return cmd
}

// graphUpdateResult is the JSON body `chronicle graph update` (and
// `--check`) print: the file it wrote (or, under --check, would write), its
// new seq, the record counts, and how that compares to what was on disk
// before.
type graphUpdateResult struct {
	Path    string          `json:"path"`
	Seq     int             `json:"seq"`
	Counts  snapshot.Counts `json:"counts"`
	Added   int             `json:"added"`
	Removed int             `json:"removed"`
	Changed int             `json:"changed"`
}

func newGraphUpdateCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Export the live graph to .depbot/graph.jsonl",
		Long: `Export the current state of the database into the canonical, git-committed
graph.jsonl snapshot (atomic write: temp file + rename). Prints a JSON
summary: path, new seq, record counts, and how many records were
added/removed/changed relative to the file it replaced.

--check makes no changes: it reports whether graph.jsonl on disk already
matches what this command would write, and exits 1 if it does not — for a
pre-commit hook or CI gate.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			g := openGraph()
			defer g.Store().Close()

			graphPath := filepath.Join(paths.Dir(), filepath.Base(snapshot.Path))
			result, mismatch, err := runGraphUpdate(g.Store(), g.Registry(), graphPath, check)
			if err != nil {
				return err
			}
			outputJSON(result)
			if check && mismatch {
				return fmt.Errorf("%s is out of date", graphPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Report whether graph.jsonl is up to date instead of writing it (exit 1 if not)")
	return cmd
}

// runGraphUpdate is the testable core of `chronicle graph update`: no
// cobra, no process exit, so it can be called directly against a real store
// and a temp graphPath. mismatch is only meaningful when checkOnly is true.
//
// checkOnly deliberately reconstructs the snapshot using the ON-DISK file's
// OWN seq rather than the next one: a real update always advances seq by
// one (seq is a display-only counter, not a change signal — the version's
// real identity is graph.jsonl's own commit and blob, per the design spec),
// so "what would the NEXT update write" would never byte-match the current
// file even when nothing at all changed. --check instead asks "does
// graph.jsonl still match the store right now", which means holding seq
// steady and comparing everything else.
func runGraphUpdate(st *store.Store, reg *registry.Registry, graphPath string, checkOnly bool) (graphUpdateResult, bool, error) {
	existing, existingErr := os.ReadFile(graphPath)

	existingSeq := 0
	if existingErr == nil {
		if h, err := snapshot.ReadHeader(bytes.NewReader(existing)); err == nil {
			existingSeq = h.Seq
		}
	}
	prevSeq := existingSeq
	if checkOnly && existingSeq > 0 {
		prevSeq = existingSeq - 1
	}

	next, err := snapshot.FromStore(st, snapshot.ExportOptions{
		PrevSeq:  prevSeq,
		Registry: snapshot.RegistryFingerprint(reg),
	})
	if err != nil {
		return graphUpdateResult{}, false, fmt.Errorf("export graph: %w", err)
	}

	prevSnap := &snapshot.Snapshot{}
	if existingErr == nil {
		if decoded, _, derr := snapshot.Decode(bytes.NewReader(existing), snapshot.DefaultLimits()); derr == nil {
			prevSnap = decoded
		}
	}
	changes := snapshot.Summarize(prevSnap, next)

	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, next); err != nil {
		return graphUpdateResult{}, false, fmt.Errorf("encode graph: %w", err)
	}

	result := graphUpdateResult{
		Path:    graphPath,
		Seq:     next.Header.Seq,
		Counts:  next.Header.Counts,
		Added:   changes.Added,
		Removed: changes.Removed,
		Changed: changes.Changed,
	}

	if checkOnly {
		mismatch := existingErr != nil || !bytes.Equal(existing, buf.Bytes())
		return result, mismatch, nil
	}

	if err := wiring.AtomicWrite(graphPath, buf.Bytes(), 0644); err != nil {
		return graphUpdateResult{}, false, fmt.Errorf("write %s: %w", graphPath, err)
	}
	return result, false, nil
}

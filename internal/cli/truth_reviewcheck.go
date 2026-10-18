package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth/reviewcheck"
	"github.com/spf13/cobra"
)

func newTruthReviewCheckCmd() *cobra.Command {
	var root, base, head, reviewPath, baseGraph, headGraph string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "review-check",
		Short: "Check a review (chronicle review --format json) against the reference delta of the same change",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if base == "" || head == "" || reviewPath == "" {
				return fmt.Errorf("--base, --head and --review are required")
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			data, err := os.ReadFile(reviewPath)
			if err != nil {
				return fmt.Errorf("read review: %w", err)
			}
			rv := &review.ReviewResult{}
			if err := json.Unmarshal(data, rv); err != nil {
				return fmt.Errorf("parse review %s: %w", reviewPath, err)
			}
			repo := truthRoot(root)
			baseRep, err := truthExtract(ctx, repo, base)
			if err != nil {
				return fmt.Errorf("reference at base: %w", err)
			}
			headRep, err := truthExtract(ctx, repo, head)
			if err != nil {
				return fmt.Errorf("reference at head: %w", err)
			}
			baseSnap, err := reviewCheckSnapshot(ctx, repo, base, baseGraph)
			if err != nil {
				return fmt.Errorf("graph at base: %w", err)
			}
			headSnap, err := reviewCheckSnapshot(ctx, repo, head, headGraph)
			if err != nil {
				return fmt.Errorf("graph at head: %w", err)
			}
			res := reviewcheck.Check(reviewcheck.Input{
				Base: baseRep, Head: headRep, BaseSnap: baseSnap, HeadSnap: headSnap, Review: rv,
			})
			if jsonOut {
				return writeIndentedJSON(cmd.OutOrStdout(), res)
			}
			printReviewCheck(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Git repository (default: --project or cwd)")
	cmd.Flags().StringVar(&base, "base", "", "Base revision (required)")
	cmd.Flags().StringVar(&head, "head", "", "Head revision (required)")
	cmd.Flags().StringVar(&reviewPath, "review", "", "Review result as JSON from chronicle review --format json (required)")
	cmd.Flags().StringVar(&baseGraph, "base-graph", "", "graph.jsonl for the base side (default: read from git at --base)")
	cmd.Flags().StringVar(&headGraph, "head-graph", "", "graph.jsonl for the head side (default: read from git at --head)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the result as JSON")
	return cmd
}

// reviewCheckSnapshot returns the graph snapshot of one side: from an
// explicit file when given, else the one committed at rev (nil when the
// revision has no graph; the facts then count as not_in_graph).
func reviewCheckSnapshot(ctx context.Context, repo, rev, path string) (*snapshot.Snapshot, error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		snap, _, err := snapshot.Decode(f, snapshot.DefaultLimits())
		return snap, err
	}
	vg, err := review.SnapshotSource{RepoRoot: repo, Rev: rev}.Load(ctx)
	if err != nil {
		return nil, err
	}
	defer vg.Close()
	if vg.Origin == review.OriginMissing {
		return nil, nil
	}
	return vg.Export, nil
}

func printReviewCheck(w io.Writer, res reviewcheck.Result) {
	fmt.Fprintf(w, "%-10s | %8s | %6s | %12s\n", "class", "reported", "missed", "not_in_graph")
	for _, c := range res.ByClass {
		fmt.Fprintf(w, "%-10s | %8d | %6d | %12d\n", c.Class, c.Reported, c.Missed, c.NotInGraph)
	}
	recall := truthNoDataLabel
	if res.Recall != nil {
		recall = fmt.Sprintf(truthPercentFormat, int(*res.Recall*100+1e-9))
	}
	fmt.Fprintf(w, "\nrecall: %s (reported %d, missed %d, not in graph %d)\n", recall, res.Reported, res.Missed, res.NotInGraph)
	for _, st := range []reviewcheck.Status{reviewcheck.MissedByReview, reviewcheck.NotInGraph} {
		var lines []string
		for _, f := range res.Facts {
			if f.Status == st {
				lines = append(lines, fmt.Sprintf("  - %s %s %s  %s:%d", f.Change, f.Fact.Class, f.Fact.Key, f.Fact.Primary.File, f.Fact.Primary.Line))
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(w, "\n%s:\n%s\n", st, strings.Join(lines, "\n"))
		}
	}
	if len(res.Unexplained) > 0 {
		fmt.Fprintf(w, "\nunexplained (%d):\n", len(res.Unexplained))
		for _, u := range res.Unexplained {
			fmt.Fprintf(w, "  - %s %s %s\n", u.Change, u.Kind, u.ID)
		}
	}
}

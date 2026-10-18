package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing/fstest"
	"time"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/compare"
	"github.com/alexdx2/chronicle-core/truth/extract"
	"github.com/spf13/cobra"
)

const (
	truthMaxFile       = 1 << 20 // files above this are skipped when reading a git archive
	truthMissingCap    = 50
	truthGitTimeout    = 5 * time.Minute
	truthNoDataLabel   = "нет данных"
	truthPercentFormat = "%d%%"
)

func newTruthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "truth",
		Short: "Deterministic ground truth from sources, compared against the graph",
	}
	cmd.AddCommand(newTruthExtractCmd(), newTruthCompareCmd(), newTruthReviewCheckCmd())
	return cmd
}

func truthRoot(root string) string {
	if root != "" {
		return root
	}
	if projectPath != "" {
		return projectPath
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func newTruthExtractCmd() *cobra.Command {
	var root, sha string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract reference facts from sources (no model involved)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := truthExtract(cmd.Context(), truthRoot(root), sha)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeIndentedJSON(cmd.OutOrStdout(), rep)
			}
			printReport(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Directory to read (default: --project or cwd)")
	cmd.Flags().StringVar(&sha, "sha", "", "Read this git revision instead of the working tree (files marked export-ignore in .gitattributes are not read)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the report as JSON")
	return cmd
}

func newTruthCompareCmd() *cobra.Command {
	var root, sha, graphPath, reportPath string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare the extracted reference against a graph snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if graphPath == "" {
				return fmt.Errorf("--graph is required")
			}
			f, err := os.Open(graphPath)
			if err != nil {
				return fmt.Errorf("open graph: %w", err)
			}
			snap, warns, err := snapshot.Decode(f, snapshot.DefaultLimits())
			f.Close()
			if err != nil {
				return fmt.Errorf("decode graph %s: %w", graphPath, err)
			}
			if len(warns) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "snapshot decoder warnings: %d\n", len(warns))
			}

			var rep *truth.Report
			if reportPath != "" {
				data, err := os.ReadFile(reportPath)
				if err != nil {
					return fmt.Errorf("read report: %w", err)
				}
				rep = &truth.Report{}
				if err := json.Unmarshal(data, rep); err != nil {
					return fmt.Errorf("parse report %s: %w", reportPath, err)
				}
			} else if rep, err = truthExtract(cmd.Context(), truthRoot(root), sha); err != nil {
				return err
			}

			v := compare.Compare(rep, snap)
			if jsonOut {
				return writeIndentedJSON(cmd.OutOrStdout(), v)
			}
			printVerdict(cmd.OutOrStdout(), v)
			return nil
		},
	}
	cmd.Flags().StringVar(&graphPath, "graph", "", "Path to graph.jsonl snapshot (required)")
	cmd.Flags().StringVar(&reportPath, "report", "", "Path to a saved extract report (JSON); extracts when absent")
	cmd.Flags().StringVar(&root, "root", "", "Directory to read (default: --project or cwd)")
	cmd.Flags().StringVar(&sha, "sha", "", "Read this git revision instead of the working tree (files marked export-ignore in .gitattributes are not read)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the verdict as JSON")
	return cmd
}

// truthExtract runs the extractor over a directory or a git revision.
func truthExtract(ctx context.Context, root, sha string) (*truth.Report, error) {
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("root %q is not a readable directory", root)
	}
	if sha == "" {
		return extract.Extract(os.DirFS(root))
	}
	if strings.HasPrefix(sha, "-") {
		return nil, fmt.Errorf("invalid --sha %q: must not start with '-'", sha)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, truthGitTimeout)
	defer cancel()
	var stderr bytes.Buffer
	c := exec.CommandContext(ctx, "git", "-C", root, "archive", "--format=tar", "--", sha)
	c.Stderr = &stderr
	out, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("git archive: %w", err)
	}
	fsys, rerr := tarToFS(out)
	// Drain what tarToFS left unread (tar padding / early error) so git can exit
	// instead of blocking on a full pipe before Wait.
	io.Copy(io.Discard, out)
	if werr := c.Wait(); werr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git archive %s: %w: %s", sha, werr, msg)
		}
		return nil, fmt.Errorf("git archive %s: %w", sha, werr)
	}
	if rerr != nil {
		return nil, fmt.Errorf("read git archive: %w", rerr)
	}
	return extract.Extract(fsys)
}

// tarToFS loads regular files (<= truthMaxFile) of a tar stream into memory.
// Memory is bounded by the repository size: every kept file is held in the map.
func tarToFS(r io.Reader) (fs.FS, error) {
	m := fstest.MapFS{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return m, nil
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag != tar.TypeReg || h.Size > truthMaxFile || !fs.ValidPath(name) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, truthMaxFile+1))
		if err != nil {
			return nil, err
		}
		m[name] = &fstest.MapFile{Data: data, Mode: 0o644}
	}
}

func writeIndentedJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printReport(w io.Writer, rep *truth.Report) {
	for _, c := range truth.Classes {
		var n int
		for _, f := range rep.Facts {
			if f.Class != c {
				continue
			}
			if n == 0 {
				fmt.Fprintf(w, "\n[%s]\n", c)
			}
			n++
			fmt.Fprintf(w, "%-10s  %s  %s:%d  %s\n", f.Class, f.Key, f.Primary.File, f.Primary.Line, strings.Join(f.Sources, ","))
		}
	}
	fmt.Fprintf(w, "\nfiles read: %d\nskipped: %d\nwarnings: %d\n", rep.Files, len(rep.SkippedFiles), len(rep.Warnings))
	for _, wn := range rep.Warnings {
		fmt.Fprintf(w, "  warning: %s\n", wn)
	}
}

func pct(m truth.Metrics, v float64) string {
	if !m.HasData {
		return truthNoDataLabel
	}
	return fmt.Sprintf(truthPercentFormat, int(math.Floor(v*100+1e-9)))
}

// printVerdict renders the summary table. The "confirmed" column is
// Confirmed+Loose (strict and loose matches together).
func printVerdict(w io.Writer, v *truth.Verdict) {
	fmt.Fprintf(w, "%-10s | %5s | %9s | %7s | %12s | %7s | %9s | %-10s | %-10s | %s\n",
		"class", "known", "confirmed", "missing", "out_of_scope", "ignored", "uncovered", "coverage", "scope", "recall")
	for _, c := range v.Classes {
		fmt.Fprintf(w, "%-10s | %5d | %9d | %7d | %12d | %7d | %9d | %-10s | %-10s | %s\n",
			c.Class, c.Known, c.Confirmed+c.Loose, c.Missing, c.OutOfScope, c.Ignored, c.Uncovered,
			pct(c.Metrics, c.Coverage), pct(c.Metrics, c.Scope), pct(c.Metrics, c.Recall))
	}
	for _, cls := range truth.Classes {
		var missing []truth.JudgedFact
		for _, f := range v.Facts {
			if f.Class == cls && f.Status == truth.Missing {
				missing = append(missing, f)
			}
		}
		if len(missing) == 0 {
			continue
		}
		fmt.Fprintf(w, "\nmissing %s:\n", cls)
		for i, f := range missing {
			if i == truthMissingCap {
				fmt.Fprintf(w, "  … and %d more\n", len(missing)-truthMissingCap)
				break
			}
			fmt.Fprintf(w, "  - %s  %s:%d  %s\n", f.Key, f.Primary.File, f.Primary.Line, f.Primary.Text)
		}
	}
	for _, wn := range v.Warnings {
		fmt.Fprintf(w, "warning: %s\n", wn)
	}
}

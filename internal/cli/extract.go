package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/headless"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/spf13/cobra"
)

const defaultExtractModel = "anthropic:claude-haiku-5-5"

// extractCLIOptions bundles chronicle extract's flags.
type extractCLIOptions struct {
	PlanOnly    bool
	PlanFile    string   // read a plan JSON instead of computing one
	Files       []string // explicit list; overrides plan computation
	Base, Head  string   // Head default "HEAD"
	Domain      string
	Radius      int
	MaxFiles    int
	MaxBytes    int64
	Model       string // default: $CHRONICLE_MODEL or defaultExtractModel
	Concurrency int
	UsageOut    string // "" = summary line only
}

// runExtract is chronicle extract's testable core. It returns the process exit
// code: 0 ok, 1 error, 2 no graph/plan input, 3 no API key (llm.ErrNoKey). A
// non-nil error accompanies codes 1 and 2 and is for stderr.
func runExtract(ctx context.Context, w io.Writer, g *graph.Graph, repoRoot, chronicleDir string, opts extractCLIOptions, env func(string) string) (int, error) {
	if g == nil {
		return 2, errors.New("chronicle extract: no graph")
	}
	m, err := manifest.LoadFile(filepath.Join(chronicleDir, "chronicle.domain.yaml"))
	if err != nil {
		return 2, fmt.Errorf("chronicle extract: load manifest: %w", err)
	}

	var plan *headless.Plan
	if opts.PlanFile != "" {
		data, err := os.ReadFile(opts.PlanFile)
		if err != nil {
			return 2, fmt.Errorf("chronicle extract: read plan file: %w", err)
		}
		plan = &headless.Plan{}
		if err := json.Unmarshal(data, plan); err != nil {
			return 1, fmt.Errorf("chronicle extract: parse plan file: %w", err)
		}
	}

	domain := opts.Domain
	if domain == "" && plan != nil {
		domain = plan.Domain
	}
	if domain == "" {
		switch len(m.Domains) {
		case 1:
			domain = m.Domains[0].Name
		default:
			return 1, fmt.Errorf("chronicle extract: manifest has %d domains — pass --domain", len(m.Domains))
		}
	}

	head := opts.Head
	if head == "" {
		head = "HEAD"
	}

	var files []string
	switch {
	case len(opts.Files) > 0:
		files = opts.Files
	case plan != nil:
		files = plan.Paths()
	default:
		base := opts.Base
		if base == "" {
			if base, err = defaultBaseRef(repoRoot); err != nil {
				return 1, err
			}
		}
		scan, ok := m.ScanConfigFor(domain)
		if !ok {
			merged := m.MergedScanConfig()
			scan = &merged
		}
		plan, err = headless.BuildPlan(headless.PlanInput{
			G: g, RepoRoot: repoRoot, Domain: domain, Scan: scan,
			Base: base, Head: head, Radius: opts.Radius, MaxFiles: opts.MaxFiles, MaxBytes: opts.MaxBytes,
		})
		if err != nil {
			return 1, fmt.Errorf("chronicle extract: plan: %w", err)
		}
		files = plan.Paths()
	}

	if opts.PlanOnly {
		if plan == nil {
			plan = &headless.Plan{Domain: domain, Head: head, Files: []headless.PlanFile{}}
			for _, f := range files {
				plan.Files = append(plan.Files, headless.PlanFile{Path: f, Reason: "explicit"})
			}
		}
		if err := writeJSON(w, plan); err != nil {
			return 1, err
		}
		return 0, nil
	}

	model := opts.Model
	if model == "" {
		model = env("CHRONICLE_MODEL")
	}
	if model == "" {
		model = defaultExtractModel
	}
	backend, err := llm.Open(model, env)
	if errors.Is(err, llm.ErrNoKey) {
		_ = writeJSON(w, map[string]any{"error": err.Error(), "skipped": true})
		return 3, nil
	}
	if err != nil {
		return 1, fmt.Errorf("chronicle extract: %w", err)
	}

	headSHA, err := gitutil.Run(repoRoot, "rev-parse", "--verify", "--end-of-options", head+"^{commit}")
	if err != nil {
		return 1, fmt.Errorf("chronicle extract: resolve %q: %w", head, err)
	}

	res, runErr := headless.Run(ctx, headless.RunInput{
		G: g, RepoRoot: repoRoot, ChronicleDir: chronicleDir, Domain: domain, Manifest: m,
		HeadSHA: headSHA, Files: files, Backend: backend, Concurrency: opts.Concurrency,
	})
	if res != nil {
		cost := "unknown"
		if res.CostKnown {
			cost = fmt.Sprintf("%.4f", res.CostUSD)
		}
		fmt.Fprintf(w, "chronicle extract: %d files, %d extracted, %d failed, in=%d out=%d cost=$%s (%s)\n",
			len(res.Files), res.Extracted, res.Failed, res.Totals.InputTokens, res.Totals.OutputTokens, cost, res.Model)
		for _, l := range res.Limitations {
			if strings.HasPrefix(l, "nothing to extract") {
				fmt.Fprintf(w, "chronicle extract: %s\n", l)
			}
		}
		// After the summary, so a failed write never hides it.
		if opts.UsageOut != "" {
			if data, err := json.MarshalIndent(res, "", "  "); err != nil {
				return 1, fmt.Errorf("chronicle extract: encode usage: %w", err)
			} else if err := os.WriteFile(opts.UsageOut, append(data, '\n'), 0o644); err != nil {
				return 1, fmt.Errorf("chronicle extract: write usage: %w", err)
			}
		}
	}
	if runErr != nil {
		return 1, fmt.Errorf("chronicle extract: %w", runErr)
	}
	return 0, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func newExtractCmd() *cobra.Command {
	var o extractCLIOptions
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Plan and run LLM re-extraction for a git range",
		Long: `Computes which files an LLM should read for the range --base...--head, i.e. from the merge base of the two (changed
files plus graph neighbours), then re-extracts them through the scan pipeline.

Exit code 0: ok (also for --plan). 1: error. 2: no graph or plan input.
3: no API key for the model (prints {"error":...,"skipped":true}).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			g := openGraph()
			repoRoot := paths.Root()
			if repoRoot == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				repoRoot = wd
				paths.SetProjectRoot(wd)
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			code, err := runExtract(ctx, cmd.OutOrStdout(), g, repoRoot, paths.Dir(), o, os.Getenv)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.PlanOnly, "plan", false, "Print the extraction plan as JSON and exit without calling a model")
	f.StringVar(&o.PlanFile, "plan-file", "", "Read a plan JSON instead of computing one")
	f.StringSliceVar(&o.Files, "files", nil, "Explicit file list (overrides plan computation)")
	f.StringVar(&o.Base, "base", "", "Base git ref (default: origin/main, origin/master, main, master); the diff starts at the merge base of --base and --head, like `chronicle review A...B`")
	f.StringVar(&o.Head, "head", "HEAD", "Head git ref")
	f.StringVar(&o.Domain, "domain", "", "Domain key (default: the manifest's only domain)")
	f.IntVar(&o.Radius, "radius", 1, "Graph neighbour radius")
	f.IntVar(&o.MaxFiles, "max-files", 80, "Maximum files to read")
	f.Int64Var(&o.MaxBytes, "max-bytes", 1<<20, "Maximum total bytes to read")
	f.StringVar(&o.Model, "model", "", "Model spec: anthropic:<model>, gemini:<model>, fake:<dir> (default $CHRONICLE_MODEL or "+defaultExtractModel+")")
	f.IntVar(&o.Concurrency, "concurrency", 4, "Parallel model calls")
	f.StringVar(&o.UsageOut, "usage-out", "", "Write the full run result JSON to this file")
	return cmd
}

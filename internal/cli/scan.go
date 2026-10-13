package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/scan"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/spf13/cobra"
)

// scanCLIOptions bundles chronicle scan's flags.
type scanCLIOptions struct {
	Full           bool
	Model          string // default: $CHRONICLE_MODEL or defaultExtractModel
	Concurrency    int
	ContextWindow  int
	FileTimeout    time.Duration // 0 = no limit
	Domain         string
	StructuralOnly bool
	NoExport       bool
	ProjectPath    string
	UsageOut       string // "" = none
	// ProfileModel is the model spec for the repository profile; "" = the
	// extraction model. NoProfile takes the deterministic seed instead.
	ProfileModel string
	NoProfile    bool
	// Out, when set, builds a layered snapshot (inventory, dependencies,
	// profile, packs, extraction) and writes its files under <Out>/.depbot/
	// instead of filling the local database.
	Out string
}

// runScanFull is chronicle scan --full's testable core. It returns the process
// exit code: 0 complete, 1 error, 3 no API key (llm.ErrNoKey), 4 finished but
// incomplete (failed files or an unfinished structural pass). A non-nil error
// accompanies code 1 and is for stderr; the report is printed whenever there is
// one, even next to an error.
func runScanFull(ctx context.Context, w io.Writer, g *graph.Graph, repoRoot string, opts scanCLIOptions, env func(string) string) (int, error) {
	if !opts.Full {
		return 1, errors.New("chronicle scan: only --full is implemented here (interactive scans run through the MCP scan command)")
	}
	if opts.Out != "" {
		return runScanSnapshot(ctx, w, repoRoot, opts, env)
	}
	if g == nil {
		return 1, errors.New("chronicle scan: no graph")
	}

	var backend llm.Backend
	if !opts.StructuralOnly {
		model := opts.Model
		if model == "" {
			model = env("CHRONICLE_MODEL")
		}
		if model == "" {
			model = defaultExtractModel
		}
		var err error
		backend, err = llm.Open(model, env)
		if errors.Is(err, llm.ErrNoKey) {
			_ = writeJSON(w, map[string]any{"error": err.Error(), "skipped": true})
			return 3, nil
		}
		if err != nil {
			return 1, fmt.Errorf("chronicle scan: %w", err)
		}
	}

	rep, scanErr := scan.FullScan(ctx, g, scan.FullOptions{
		RepoRoot: repoRoot, ProjectPath: opts.ProjectPath, Domain: opts.Domain, Backend: backend,
		Concurrency: opts.Concurrency, ContextWindow: opts.ContextWindow, FileTimeout: opts.FileTimeout,
		NoExtract: opts.StructuralOnly,
	})
	if rep != nil {
		if err := writeJSON(w, rep); err != nil {
			return 1, fmt.Errorf("chronicle scan: write report: %w", err)
		}
		if opts.UsageOut != "" {
			if data, err := json.MarshalIndent(rep, "", "  "); err != nil {
				return 1, fmt.Errorf("chronicle scan: encode usage: %w", err)
			} else if err := os.WriteFile(opts.UsageOut, append(data, '\n'), 0o644); err != nil {
				return 1, fmt.Errorf("chronicle scan: write usage: %w", err)
			}
		}
	}
	if scanErr != nil {
		return 1, fmt.Errorf("chronicle scan: %w", scanErr)
	}

	if !opts.NoExport {
		graphPath := filepath.Join(paths.DirAt(repoRoot), filepath.Base(snapshot.Path))
		if _, _, err := runGraphUpdate(g.Store(), g.Registry(), graphPath, false); err != nil {
			return 1, fmt.Errorf("chronicle scan: export %s: %w", graphPath, err)
		}
	}
	if !rep.Complete {
		return 4, nil
	}
	return 0, nil
}

// openExtractBackend opens the extraction model from opts/env.
func openExtractBackend(opts scanCLIOptions, env func(string) string) (llm.Backend, error) {
	model := opts.Model
	if model == "" {
		model = env("CHRONICLE_MODEL")
	}
	if model == "" {
		model = defaultExtractModel
	}
	return llm.Open(model, env)
}

// runScanSnapshot is scan --full --out: it builds the layered snapshot of HEAD
// (inventory, dependencies, profile with seed fallback, packs, extraction) and
// writes graph.jsonl, the manifest, the types file and profile.json under
// <Out>/.depbot/. Exit codes as runScanFull.
func runScanSnapshot(ctx context.Context, w io.Writer, repoRoot string, opts scanCLIOptions, env func(string) string) (int, error) {
	if opts.StructuralOnly {
		return 1, errors.New("chronicle scan: --out builds a snapshot with the model; it cannot be combined with --structural-only")
	}
	backend, err := openExtractBackend(opts, env)
	if errors.Is(err, llm.ErrNoKey) {
		_ = writeJSON(w, map[string]any{"error": err.Error(), "skipped": true})
		return 3, nil
	}
	if err != nil {
		return 1, fmt.Errorf("chronicle scan: %w", err)
	}
	prof := scan.ProfileOptions{Backend: backend}
	switch {
	case opts.NoProfile:
		prof = scan.ProfileOptions{Off: true, OffReason: "profile disabled (--no-profile)"}
	case opts.ProfileModel != "":
		pb, perr := llm.Open(opts.ProfileModel, env)
		if errors.Is(perr, llm.ErrNoKey) {
			_ = writeJSON(w, map[string]any{"error": perr.Error(), "skipped": true})
			return 3, nil
		}
		if perr != nil {
			return 1, fmt.Errorf("chronicle scan: --profile-model: %w", perr)
		}
		prof.Backend = pb
	}
	sha, err := gitutil.Run(repoRoot, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil {
		return 1, fmt.Errorf("chronicle scan: resolve HEAD in %s: %w", repoRoot, err)
	}
	repo := filepath.Base(repoRoot)
	res, berr := scan.BuildSnapshot(ctx, scan.BuildInput{
		Repo: repo, CodeRepo: repoRoot, Sha: sha, ProjectPath: opts.ProjectPath,
		Backend: backend, Concurrency: opts.Concurrency, ContextWindow: opts.ContextWindow,
		FileTimeout: opts.FileTimeout, Profile: prof,
	})
	if berr != nil {
		return 1, fmt.Errorf("chronicle scan: %w", berr)
	}
	for rel, data := range map[string][]byte{
		snapshot.Path:         res.Files.Graph,
		snapshot.ManifestPath: res.Files.Domain,
		snapshot.TypesPath:    res.Files.Types,
		snapshot.ProfilePath:  res.Files.Profile,
	} {
		if len(data) == 0 {
			continue
		}
		dst := filepath.Join(opts.Out, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 1, fmt.Errorf("chronicle scan: %w", err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return 1, fmt.Errorf("chronicle scan: write %s: %w", dst, err)
		}
	}
	if err := writeJSON(w, map[string]any{
		"code_sha": res.CodeSha, "out": opts.Out, "partial": res.Partial, "failed": res.Failed,
		"limitations": res.Limitations, "profile_seeded": res.Profile != nil && res.Profile.Seeded,
	}); err != nil {
		return 1, fmt.Errorf("chronicle scan: write report: %w", err)
	}
	if res.Partial {
		return 4, nil
	}
	return 0, nil
}

func newScanCmd() *cobra.Command {
	var o scanCLIOptions
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan a repository without an agent (--full)",
		Long: `Takes the repository at HEAD from an empty database to a full graph without an agent: creates the domain manifest
when there is none (an existing one is never touched), runs the structural pass, then extracts every in-scope file
that is not yet covered at its current content through the headless pipeline. Rerunning resumes: covered files are not
sent again, failed files are retried. The phases that were not run are listed in the report's limitations.

Exit code 0: complete. 1: error. 3: no API key for the model (prints {"error":...,"skipped":true}).
4: finished but incomplete (failed files, or the structural pass did not finish).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			if !o.Full {
				err := errors.New("chronicle scan: only --full is implemented here (interactive scans run through the MCP scan command)")
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			if notice, linked := linkedWorktreeRefusal(); linked {
				refuseWrite(notice, false)
				return nil
			}
			repoRoot := paths.Root()
			if repoRoot == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				repoRoot = wd
				paths.SetProjectRoot(wd)
			}
			// A missing manifest is bootstrapped by the scan itself, not stubbed.
			g := openGraphWith(false)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			code, err := runScanFull(ctx, cmd.OutOrStdout(), g, repoRoot, o, os.Getenv)
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
	f.BoolVar(&o.Full, "full", false, "Scan the whole repository without an agent (required)")
	f.StringVar(&o.Model, "model", "", "Model spec: anthropic:<model>, gemini:<model>, openai:<model>, openrouter:<model>, ollama:<model>, fake:<dir> (default $CHRONICLE_MODEL or "+defaultExtractModel+")")
	f.IntVar(&o.Concurrency, "concurrency", 2, "Parallel model calls")
	f.IntVar(&o.ContextWindow, "context-window", 0, "Token window to fit each request into, splitting long files into parts (0 = the backend's own)")
	f.DurationVar(&o.FileTimeout, "file-timeout", 5*time.Minute, "Model time limit per file; a file over it is marked failed and retried by the next run (0 = no limit)")
	f.StringVar(&o.Domain, "domain", "", "Scan only this manifest domain (default: all)")
	f.BoolVar(&o.StructuralOnly, "structural-only", false, "Run only the structural pass; no model is called")
	f.BoolVar(&o.NoExport, "no-export", false, "Do not write .depbot/graph.jsonl afterwards")
	f.StringVar(&o.ProjectPath, "project-path", "", "Project path used to name a generated manifest's domain (default: the repository directory name)")
	f.StringVar(&o.ProfileModel, "profile-model", "", "Model spec for the repository profile (default: the extraction model); used with --out")
	f.BoolVar(&o.NoProfile, "no-profile", false, "Skip the profile request and take the deterministic seed profile; used with --out")
	f.StringVar(&o.Out, "out", "", "Build a layered snapshot of HEAD and write graph.jsonl, the manifest and profile.json under <dir>/.depbot/")
	f.StringVar(&o.UsageOut, "usage-out", "", "Write the full report JSON to this file")
	return cmd
}

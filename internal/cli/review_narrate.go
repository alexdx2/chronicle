package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alexdx2/chronicle-core/headless"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/review"
	"github.com/spf13/cobra"
)

type narrateOptions struct {
	ReviewFile string
	Changed    string // optional file with one changed path per line
	Model      string
	MaxWords   int
	Out        string // "" = w
	UsageOut   string
}

// runNarrate is chronicle review narrate's testable core. Exit codes: 0 ok,
// 3 no API key (prints {"error":...,"skipped":true}, writes nothing), 1 other.
func runNarrate(ctx context.Context, w io.Writer, opts narrateOptions, env func(string) string) (int, error) {
	if opts.ReviewFile == "" {
		return 1, errors.New("chronicle review narrate: --review is required")
	}
	data, err := os.ReadFile(opts.ReviewFile)
	if err != nil {
		return 1, fmt.Errorf("chronicle review narrate: %w", err)
	}
	var r review.ReviewResult
	if err := json.Unmarshal(data, &r); err != nil {
		return 1, fmt.Errorf("chronicle review narrate: %s is not a review result: %w", opts.ReviewFile, err)
	}
	var changed []string
	if opts.Changed != "" {
		b, err := os.ReadFile(opts.Changed)
		if err != nil {
			return 1, fmt.Errorf("chronicle review narrate: %w", err)
		}
		changed = []string{}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				changed = append(changed, l)
			}
		}
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
		return 1, fmt.Errorf("chronicle review narrate: %w", err)
	}

	resp, err := backend.Complete(ctx, headless.BuildNarratePrompt(&r, changed, opts.MaxWords))
	if err != nil {
		return 1, fmt.Errorf("chronicle review narrate: %w", err)
	}
	if opts.UsageOut != "" {
		cost, known := llm.Cost(backend.Model(), resp.Usage)
		if resp.Usage.CostUSD > 0 {
			cost, known = resp.Usage.CostUSD, true
		}
		u := map[string]any{"model": backend.Name() + ":" + backend.Model(), "usage": resp.Usage, "cost_usd": cost, "cost_known": known}
		b, _ := json.MarshalIndent(u, "", "  ")
		if err := os.WriteFile(opts.UsageOut, append(b, '\n'), 0o644); err != nil {
			return 1, fmt.Errorf("chronicle review narrate: write usage: %w", err)
		}
	}
	text := strings.TrimSpace(resp.Text)
	var s string
	if json.Unmarshal([]byte(text), &s) == nil { // fixtures are JSON strings
		text = strings.TrimSpace(s)
	}
	if text == "" {
		return 1, errors.New("chronicle review narrate: the model returned an empty answer")
	}
	md := fmt.Sprintf("### What this means (model's reading)\n\n%s\n\n*Written by %s:%s from the deterministic review above; the facts are the tables, this paragraph is one reading of them.*\n",
		text, backend.Name(), backend.Model())
	if opts.Out != "" {
		if err := os.WriteFile(opts.Out, []byte(md), 0o644); err != nil {
			return 1, fmt.Errorf("chronicle review narrate: %w", err)
		}
		return 0, nil
	}
	if _, err := io.WriteString(w, md); err != nil {
		return 1, err
	}
	return 0, nil
}

func newReviewNarrateCmd() *cobra.Command {
	var o narrateOptions
	cmd := &cobra.Command{
		Use:   "narrate",
		Short: "Have a model write the short human paragraph for a review",
		Long: `Reads a review JSON (chronicle review --format json) and asks a model for a short paragraph:
what the change means, what may be affected, what to test. This is the only model-written part of a review comment.

Exit code 0: ok. 1: error (provider failure, empty answer). 3: no API key (prints {"error":...,"skipped":true}).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			code, err := runNarrate(ctx, cmd.OutOrStdout(), o, os.Getenv)
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
	f.StringVar(&o.ReviewFile, "review", "", "Review JSON file (required)")
	f.StringVar(&o.Changed, "changed", "", "File listing changed paths, one per line (default: the review's own list)")
	f.StringVar(&o.Model, "model", "", "Model spec (default $CHRONICLE_MODEL or "+defaultExtractModel+")")
	f.IntVar(&o.MaxWords, "max-words", 120, "Word cap given to the model")
	f.StringVar(&o.Out, "out", "", "Write the markdown here (default stdout)")
	f.StringVar(&o.UsageOut, "usage-out", "", "Write token usage and cost JSON to this file")
	return cmd
}

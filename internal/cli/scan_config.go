package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/mcpserver"
	"github.com/alexdx2/chronicle-core/scan"
)

func init() {
	mcpserver.ScanConfig = &mcpserver.ScanConfigBackend{
		Get:    func(root string) any { return scan.ScanConfigGet(root) },
		DryRun: func(root, y string) any { return scan.ScanConfigDryRun(root, y) },
		Valid: func(root, y string) (bool, string) {
			r := scan.ScanConfigDryRun(root, y)
			return r.Valid, r.Errors
		},
		Explain: func(root, f string) any { return scan.ScanConfigExplain(root, f) },
		Save: func(root, y string, acc bool) (any, error) {
			return scan.ScanConfigSave(root, y, acc)
		},
	}
}

func newScanConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan-config",
		Short: "Read, validate, dry-run and save the project's scan configuration (.depbot/scan.yaml)",
	}
	var file string
	readCandidate := func() (string, error) {
		var b []byte
		var err error
		if file == "" || file == "-" {
			b, err = io.ReadAll(os.Stdin)
		} else {
			b, err = os.ReadFile(file)
		}
		return string(b), err
	}
	out := func(v any) error {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	get := &cobra.Command{Use: "get", Short: "Show the project layer and every rule in force", RunE: func(c *cobra.Command, _ []string) error {
		return out(scan.ScanConfigGet(graph.ProjectRoot()))
	}}
	validate := &cobra.Command{Use: "validate", Short: "Check a candidate (--file, default stdin)", RunE: func(c *cobra.Command, _ []string) error {
		y, err := readCandidate()
		if err != nil {
			return err
		}
		r := scan.ScanConfigDryRun(graph.ProjectRoot(), y)
		_ = out(map[string]any{"valid": r.Valid, "errors": r.Errors})
		if !r.Valid {
			c.SilenceUsage = true
			return fmt.Errorf("invalid scan config")
		}
		return nil
	}}
	dry := &cobra.Command{Use: "dry-run", Short: "Run a candidate against the repository without saving it", RunE: func(c *cobra.Command, _ []string) error {
		y, err := readCandidate()
		if err != nil {
			return err
		}
		return out(scan.ScanConfigDryRun(graph.ProjectRoot(), y))
	}}
	explain := &cobra.Command{Use: "explain <file>", Short: "What the configuration in force finds in one file", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		return out(scan.ScanConfigExplain(graph.ProjectRoot(), a[0]))
	}}
	var accept bool
	save := &cobra.Command{Use: "save", Short: "Write the candidate to .depbot/scan.yaml (refuses silent fact loss)", RunE: func(c *cobra.Command, _ []string) error {
		y, err := readCandidate()
		if err != nil {
			return err
		}
		res, err := scan.ScanConfigSave(graph.ProjectRoot(), y, accept)
		if err != nil {
			return err
		}
		_ = out(res)
		if !res.Saved {
			c.SilenceUsage = true
			return fmt.Errorf("not saved: %s", res.Refused)
		}
		return nil
	}}
	save.Flags().BoolVar(&accept, "accept-removed", false, "accept that facts found today disappear")
	for _, c := range []*cobra.Command{validate, dry, save} {
		c.Flags().StringVar(&file, "file", "", "candidate scan.yaml (default: stdin)")
	}
	var model string
	var rounds int
	author := &cobra.Command{Use: "author", Short: "Ask a model to write the project layer; each answer is dry-run and corrected up to --rounds times", RunE: func(c *cobra.Command, _ []string) error {
		if model == "" {
			model = os.Getenv("CHRONICLE_MODEL")
		}
		if model == "" {
			model = defaultExtractModel
		}
		b, err := llm.Open(model, os.Getenv)
		if errors.Is(err, llm.ErrNoKey) {
			_ = out(map[string]any{"error": err.Error(), "skipped": true})
			os.Exit(3)
		}
		if err != nil {
			return err
		}
		res, err := scan.ScanConfigAuthor(c.Context(), b, graph.ProjectRoot(), scan.AuthorOptions{MaxRounds: rounds})
		_ = out(res)
		return err
	}}
	author.Flags().StringVar(&model, "model", "", "provider:model (default $CHRONICLE_MODEL)")
	author.Flags().IntVar(&rounds, "rounds", 3, "dry-run/correction rounds")
	cmd.AddCommand(get, validate, dry, explain, save, author)
	return cmd
}

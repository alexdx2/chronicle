package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/live"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/spf13/cobra"
)

func newReviewImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <bundle.zip>",
		Short: "Import a review bundle into the local dashboard as a marked diagram",
		Long: `Unpacks a review bundle (written by chronicle review --bundle) into
<chronicle dir>/reviews/<base>..<head>/ and publishes its full diagram, with
the changed and affected marks, to the local dashboard.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			g := openGraph()
			defer g.Store().Close()
			return importReviewBundle(g, args[0], paths.Dir(), cmd.OutOrStdout())
		},
	}
}

// shortCommit is the first 7 characters of a commit, "none" when empty.
func shortCommit(c string) string {
	if c == "" {
		return "none"
	}
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// importReviewBundle unpacks the bundle at zipPath into
// <chronicleDir>/reviews/<base7>..<head7>/ and publishes its full diagram as
// a live event on g's store.
func importReviewBundle(g *graph.Graph, zipPath, chronicleDir string, w io.Writer) error {
	b, err := review.ReadBundle(zipPath)
	if err != nil {
		return err
	}
	id := shortCommit(b.Manifest.BaseCommit) + ".." + shortCommit(b.Manifest.HeadCommit)

	var result review.ReviewResult
	if err := json.Unmarshal(b.ReviewJSON, &result); err != nil {
		return fmt.Errorf("bundle review.json: %w", err)
	}
	snap, _, err := snapshot.Decode(bytes.NewReader(b.HeadSnapshot), snapshot.DefaultLimits())
	if err != nil {
		return fmt.Errorf("bundle head graph: %w", err)
	}
	headGraph, closeHead, err := snapshot.Load(snap, g.Registry(), snapshot.LoadOptions{})
	if err != nil {
		return fmt.Errorf("load bundle head graph: %w", err)
	}
	defer closeHead()

	head := &review.VersionGraph{
		Ref: b.Manifest.HeadRef, Commit: b.Manifest.HeadCommit,
		Graph: headGraph, Export: snap, Origin: review.OriginSnapshot,
		Close: closeHead,
	}
	d, err := review.FullDiagram(&result, head)
	if err != nil {
		return fmt.Errorf("build review diagram: %w", err)
	}

	dir := filepath.Join(chronicleDir, "reviews", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
	}{
		{"review.md", []byte(b.Markdown)},
		{"review.json", b.ReviewJSON},
		{"head.graph.jsonl", b.HeadSnapshot},
	}
	if b.BaseSnapshot != nil {
		files = append(files, struct {
			name string
			data []byte
		}{"base.graph.jsonl", b.BaseSnapshot})
	}
	if b.UsageJSON != nil {
		files = append(files, struct {
			name string
			data []byte
		}{"usage.json", b.UsageJSON})
	}
	manifest, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return err
	}
	files = append(files, struct {
		name string
		data []byte
	}{"manifest.json", manifest})
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, 0o644); err != nil {
			return err
		}
	}

	domain := ""
	if len(snap.Header.Domains) > 0 {
		domain = snap.Header.Domains[0]
	}
	if _, err := live.PublishDiagram(g.Store(), "review-import", live.Diagram{
		Title:  "Chronicle review " + id,
		Level:  "review",
		Domain: domain,
		Spec:   d.Spec,
		Marks:  len(d.Spec.Marks),
		URL:    "/?review=" + id,
	}); err != nil {
		return fmt.Errorf("publish review diagram: %w", err)
	}

	fmt.Fprintf(w, "imported %s; open the dashboard with: chronicle admin\n", dir)
	return nil
}

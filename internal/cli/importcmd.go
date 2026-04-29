package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import graph data",
	}
	cmd.AddCommand(newImportAllCmd())
	return cmd
}

func newImportAllCmd() *cobra.Command {
	var (
		file       string
		revisionID int64
	)

	cmd := &cobra.Command{
		Use:   "all",
		Short: "Import all nodes, edges, and evidence from a JSON file",
		Run: func(cmd *cobra.Command, args []string) {
			if file == "" {
				fmt.Println("--file is required")
				return
			}
			if revisionID == 0 {
				fmt.Println("--revision is required")
				return
			}

			data, err := os.ReadFile(file)
			if err != nil {
				outputError(fmt.Errorf("reading file %q: %w", file, err))
			}

			var payload graph.ImportPayload
			if err := json.Unmarshal(data, &payload); err != nil {
				outputError(fmt.Errorf("parsing import file: %w", err))
			}

			g := openGraph()
			defer g.Store().Close()

			result, err := g.ImportAll(payload, revisionID)
			if err != nil {
				outputError(err)
			}
			outputJSON(result)
		},
	}

	cmd.Flags().StringVar(&file, "file", "", "Path to JSON import file (required)")
	cmd.Flags().Int64Var(&revisionID, "revision", 0, "Revision ID (required)")

	return cmd
}

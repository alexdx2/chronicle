package cli

import (
	"strings"

	"github.com/alexdx2/chronicle-core/internal/graph"
	"github.com/spf13/cobra"
)

func newImpactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact [node_key]",
		Short: "Analyze impact of a node change",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			depth, _ := cmd.Flags().GetInt("depth")
			derivation, _ := cmd.Flags().GetString("derivation")
			minScore, _ := cmd.Flags().GetFloat64("min-score")
			topK, _ := cmd.Flags().GetInt("top-k")
			includeStructural, _ := cmd.Flags().GetBool("include-structural")

			var filter []string
			if derivation != "" {
				filter = strings.Split(derivation, ",")
			}

			result, err := g.QueryImpact(args[0], graph.ImpactOptions{
				MaxDepth:          depth,
				MinScore:          minScore,
				TopK:              topK,
				DerivationFilter:  filter,
				IncludeStructural: includeStructural,
			})
			if err != nil {
				outputError(err)
			}
			outputJSON(result)
		},
	}

	cmd.Flags().Int("depth", 4, "Max traversal depth")
	cmd.Flags().String("derivation", "", "Comma-separated derivation filter")
	cmd.Flags().Float64("min-score", 0.1, "Minimum impact score")
	cmd.Flags().Int("top-k", 50, "Max results")
	cmd.Flags().Bool("include-structural", false, "Include structural edges")

	return cmd
}

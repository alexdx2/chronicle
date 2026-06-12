package cli

import (
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Find node keys by name fragment (deterministic lexical match)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			layer, _ := cmd.Flags().GetString("layer")
			nodeType, _ := cmd.Flags().GetString("node-type")
			domain, _ := cmd.Flags().GetString("domain")
			limit, _ := cmd.Flags().GetInt("limit")

			results, err := g.NodeSearch(args[0], store.NodeFilter{
				Layer:    layer,
				NodeType: nodeType,
				Domain:   domain,
			}, limit)
			if err != nil {
				outputError(err)
			}
			outputJSON(results)
		},
	}
	cmd.Flags().String("layer", "", "Filter by layer")
	cmd.Flags().String("node-type", "", "Filter by node type")
	cmd.Flags().String("domain", "", "Filter by domain key")
	cmd.Flags().Int("limit", 10, "Max results (cap 50)")
	return cmd
}

func newSubgraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subgraph [node_key]",
		Short: "BFS neighborhood around a node (trust-truncated)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			depth, _ := cmd.Flags().GetInt("depth")
			direction, _ := cmd.Flags().GetString("direction")
			maxNodes, _ := cmd.Flags().GetInt("max-nodes")

			key, err := g.ResolveNodeKey(args[0])
			if err != nil {
				outputError(err)
			}
			result, err := g.Subgraph(key, graph.SubgraphOptions{
				Depth:     depth,
				Direction: direction,
				MaxNodes:  maxNodes,
			})
			if err != nil {
				outputError(err)
			}
			outputJSON(result)
		},
	}
	cmd.Flags().Int("depth", 2, "Hops from root")
	cmd.Flags().String("direction", "both", "out, in, or both")
	cmd.Flags().Int("max-nodes", 50, "Node cap")
	return cmd
}

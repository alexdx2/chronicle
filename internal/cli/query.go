package cli

import (
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query the graph",
	}
	cmd.AddCommand(
		newQueryNodeCmd(),
		newQueryEdgesCmd(),
		newQueryDepsCmd(),
		newQueryReverseDepsCmd(),
		newQueryStatsCmd(),
		newQueryEvidenceCmd(),
		newQueryPathCmd(),
	)
	return cmd
}

func newQueryPathCmd() *cobra.Command {
	pathCmd := &cobra.Command{
		Use:   "path [from_node_key] [to_node_key]",
		Short: "Find paths between two nodes",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			maxDepth, _ := cmd.Flags().GetInt("max-depth")
			topK, _ := cmd.Flags().GetInt("top-k")
			mode, _ := cmd.Flags().GetString("mode")
			derivation, _ := cmd.Flags().GetString("derivation")
			includeStructural, _ := cmd.Flags().GetBool("include-structural")
			var filter []string
			if derivation != "" {
				filter = strings.Split(derivation, ",")
			}
			result, err := g.QueryPath(args[0], args[1], graph.PathOptions{
				MaxDepth:          maxDepth,
				TopK:              topK,
				Mode:              mode,
				DerivationFilter:  filter,
				IncludeStructural: includeStructural,
			})
			if err != nil {
				outputError(err)
			}
			outputJSON(result)
		},
	}
	pathCmd.Flags().Int("max-depth", 6, "Max traversal depth")
	pathCmd.Flags().Int("top-k", 3, "Max paths to return")
	pathCmd.Flags().String("mode", "directed", "Traversal mode: directed or connected")
	pathCmd.Flags().String("derivation", "", "Comma-separated derivation filter")
	pathCmd.Flags().Bool("include-structural", false, "Include structural edges")
	return pathCmd
}

func newQueryNodeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "node <node_key>",
		Short: "Get a node with its evidence",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			node, err := g.Store().GetNodeByKey(args[0])
			if err != nil {
				outputError(err)
			}

			evidence, err := g.Store().ListEvidenceByNode(node.NodeID)
			if err != nil {
				outputError(err)
			}

			outputJSON(map[string]any{
				"node":     node,
				"evidence": evidence,
			})
		},
	}
}

func newQueryEdgesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edges <node_key>",
		Short: "Get outgoing and incoming edges for a node",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			nodeID, err := g.Store().GetNodeIDByKey(args[0])
			if err != nil {
				outputError(err)
			}

			outgoing, err := g.Store().ListEdges(store.EdgeFilter{FromNodeID: nodeID})
			if err != nil {
				outputError(err)
			}

			incoming, err := g.Store().ListEdges(store.EdgeFilter{ToNodeID: nodeID})
			if err != nil {
				outputError(err)
			}

			outputJSON(map[string]any{
				"outgoing": outgoing,
				"incoming": incoming,
			})
		},
	}
}

func newQueryDepsCmd() *cobra.Command {
	var (
		depth      int
		derivation string
	)

	cmd := &cobra.Command{
		Use:   "deps <node_key>",
		Short: "Query dependencies (outgoing BFS)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			var filter []string
			if derivation != "" {
				filter = strings.Split(derivation, ",")
			}

			deps, err := g.QueryDeps(args[0], depth, filter)
			if err != nil {
				outputError(err)
			}
			outputJSON(deps)
		},
	}

	cmd.Flags().IntVar(&depth, "depth", 1, "Maximum traversal depth")
	cmd.Flags().StringVar(&derivation, "derivation", "", "Comma-separated derivation kinds to filter")

	return cmd
}

func newQueryReverseDepsCmd() *cobra.Command {
	var (
		depth      int
		derivation string
	)

	cmd := &cobra.Command{
		Use:   "reverse-deps <node_key>",
		Short: "Query reverse dependencies (incoming BFS)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			var filter []string
			if derivation != "" {
				filter = strings.Split(derivation, ",")
			}

			deps, err := g.QueryReverseDeps(args[0], depth, filter)
			if err != nil {
				outputError(err)
			}
			outputJSON(deps)
		},
	}

	cmd.Flags().IntVar(&depth, "depth", 1, "Maximum traversal depth")
	cmd.Flags().StringVar(&derivation, "derivation", "", "Comma-separated derivation kinds to filter")

	return cmd
}

func newQueryStatsCmd() *cobra.Command {
	var domain string

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Query aggregate stats for a domain",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			stats, err := g.QueryStats(domain)
			if err != nil {
				outputError(err)
			}
			outputJSON(stats)
		},
	}

	cmd.Flags().StringVar(&domain, "domain", "", "Domain key to filter stats")

	return cmd
}

func newQueryEvidenceCmd() *cobra.Command {
	var (
		nodeKey string
		edgeKey string
	)

	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "Query evidence for a node or edge",
		Run: func(cmd *cobra.Command, args []string) {
			if nodeKey == "" && edgeKey == "" {
				fmt.Println("at least one of --node-key or --edge-key is required")
				return
			}

			g := openGraph()
			defer g.Store().Close()

			if nodeKey != "" {
				nodeID, err := g.Store().GetNodeIDByKey(nodeKey)
				if err != nil {
					outputError(err)
				}
				evidence, err := g.Store().ListEvidenceByNode(nodeID)
				if err != nil {
					outputError(err)
				}
				outputJSON(evidence)
				return
			}

			edge, err := g.Store().GetEdgeByKey(edgeKey)
			if err != nil {
				outputError(err)
			}
			evidence, err := g.Store().ListEvidenceByEdge(edge.EdgeID)
			if err != nil {
				outputError(err)
			}
			outputJSON(evidence)
		},
	}

	cmd.Flags().StringVar(&nodeKey, "node-key", "", "Node key")
	cmd.Flags().StringVar(&edgeKey, "edge-key", "", "Edge key")

	return cmd
}

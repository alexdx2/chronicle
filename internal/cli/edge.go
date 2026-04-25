package cli

import (
	"fmt"

	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
	"github.com/spf13/cobra"
)

func newEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edge",
		Short: "Manage graph edges",
	}
	cmd.AddCommand(
		newEdgeUpsertCmd(),
		newEdgeGetCmd(),
		newEdgeListCmd(),
		newEdgeDeleteCmd(),
	)
	return cmd
}

func newEdgeUpsertCmd() *cobra.Command {
	var (
		from       string
		to         string
		edgeType   string
		derivation string
		edgeKey    string
		revision   int64
		confidence float64
		metadata   string
	)

	cmd := &cobra.Command{
		Use:   "upsert",
		Short: "Upsert an edge",
		Run: func(cmd *cobra.Command, args []string) {
			if from == "" || to == "" || edgeType == "" || derivation == "" {
				fmt.Println("--from, --to, --type, and --derivation are required")
				return
			}

			g := openGraph()
			defer g.Store().Close()

			// Look up from/to nodes to get their layers for validation.
			fromNode, err := g.Store().GetNodeByKey(from)
			if err != nil {
				outputError(fmt.Errorf("from node %q: %w", from, err))
			}
			toNode, err := g.Store().GetNodeByKey(to)
			if err != nil {
				outputError(fmt.Errorf("to node %q: %w", to, err))
			}

			input := validate.EdgeInput{
				EdgeKey:        edgeKey,
				FromNodeKey:    from,
				ToNodeKey:      to,
				EdgeType:       edgeType,
				DerivationKind: derivation,
				FromLayer:      fromNode.Layer,
				ToLayer:        toNode.Layer,
				Confidence:     confidence,
				Metadata:       metadata,
			}

			id, err := g.UpsertEdge(input, revision)
			if err != nil {
				outputError(err)
			}

			edge, err := g.Store().GetEdgeByKey(buildEdgeKey(from, to, edgeType, edgeKey))
			if err != nil {
				// Fall back to listing by ID
				_ = id
				outputJSON(map[string]int64{"edge_id": id})
				return
			}
			outputJSON(edge)
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "From node key (required)")
	cmd.Flags().StringVar(&to, "to", "", "To node key (required)")
	cmd.Flags().StringVar(&edgeType, "type", "", "Edge type (required)")
	cmd.Flags().StringVar(&derivation, "derivation", "", "Derivation kind (required)")
	cmd.Flags().StringVar(&edgeKey, "edge-key", "", "Edge key (auto-generated if omitted)")
	cmd.Flags().Int64Var(&revision, "revision", 0, "Revision ID")
	cmd.Flags().Float64Var(&confidence, "confidence", 1.0, "Confidence score [0,1]")
	cmd.Flags().StringVar(&metadata, "metadata", "{}", "JSON metadata")

	return cmd
}

// buildEdgeKey returns the effective edge key (custom or auto-generated).
func buildEdgeKey(from, to, edgeType, customKey string) string {
	if customKey != "" {
		return customKey
	}
	return validate.BuildEdgeKey(from, to, edgeType)
}

func newEdgeGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <edge_key>",
		Short: "Get an edge by key",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			edge, err := g.Store().GetEdgeByKey(args[0])
			if err != nil {
				outputError(err)
			}
			outputJSON(edge)
		},
	}
}

func newEdgeListCmd() *cobra.Command {
	var (
		from       string
		to         string
		edgeType   string
		derivation string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List edges",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			filter := store.EdgeFilter{
				EdgeType:       edgeType,
				DerivationKind: derivation,
			}

			if from != "" {
				fromID, err := g.Store().GetNodeIDByKey(from)
				if err != nil {
					outputError(fmt.Errorf("from node %q: %w", from, err))
				}
				filter.FromNodeID = fromID
			}

			if to != "" {
				toID, err := g.Store().GetNodeIDByKey(to)
				if err != nil {
					outputError(fmt.Errorf("to node %q: %w", to, err))
				}
				filter.ToNodeID = toID
			}

			edges, err := g.Store().ListEdges(filter)
			if err != nil {
				outputError(err)
			}
			outputJSON(edges)
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "Filter by from node key")
	cmd.Flags().StringVar(&to, "to", "", "Filter by to node key")
	cmd.Flags().StringVar(&edgeType, "type", "", "Filter by edge type")
	cmd.Flags().StringVar(&derivation, "derivation", "", "Filter by derivation kind")

	return cmd
}

func newEdgeDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <edge_key>",
		Short: "Delete (soft) an edge by key",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			if err := g.Store().DeleteEdge(args[0]); err != nil {
				outputError(err)
			}
			outputJSON(map[string]string{"deleted": args[0]})
		},
	}
}

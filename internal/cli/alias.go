package cli

import (
	"fmt"
	"strconv"

	"github.com/alexdx2/chronicle-core/internal/store"
	"github.com/spf13/cobra"
)

func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage node aliases",
	}
	cmd.AddCommand(
		newAliasAddCmd(),
		newAliasListCmd(),
		newAliasRemoveCmd(),
	)
	return cmd
}

func newAliasAddCmd() *cobra.Command {
	var (
		kind       string
		confidence float64
	)

	cmd := &cobra.Command{
		Use:   "add <node_key> <alias>",
		Short: "Add an alias to a node",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			nodeKey := args[0]
			alias := args[1]

			node, err := g.Store().GetNodeByKey(nodeKey)
			if err != nil {
				outputError(err)
				return
			}

			id, err := g.Store().AddAlias(store.AliasRow{
				NodeID:     node.NodeID,
				Alias:      alias,
				AliasKind:  kind,
				Confidence: confidence,
			})
			if err != nil {
				outputError(err)
				return
			}

			outputJSON(map[string]any{
				"alias_id": id,
				"node_key": nodeKey,
				"alias":    alias,
				"kind":     kind,
			})
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "manual", "Alias kind: dns, package, http_base_url, kafka_topic, openapi_title, manual")
	cmd.Flags().Float64Var(&confidence, "confidence", 0.9, "Confidence score (0-1)")

	return cmd
}

func newAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <node_key>",
		Short: "List aliases for a node",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			node, err := g.Store().GetNodeByKey(args[0])
			if err != nil {
				outputError(err)
				return
			}

			aliases, err := g.Store().ListAliasesByNode(node.NodeID)
			if err != nil {
				outputError(err)
				return
			}

			outputJSON(aliases)
		},
	}
}

func newAliasRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <alias_id>",
		Short: "Remove an alias by ID",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				outputError(fmt.Errorf("invalid alias_id: %w", err))
				return
			}

			if err := g.Store().RemoveAlias(id); err != nil {
				outputError(err)
				return
			}

			outputJSON(map[string]string{"status": "removed"})
		},
	}
}

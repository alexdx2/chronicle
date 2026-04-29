package cli

import (
	"fmt"

	"github.com/alexdx2/chronicle-core/internal/store"
	"github.com/alexdx2/chronicle-core/internal/validate"
	"github.com/spf13/cobra"
)

func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage graph nodes",
	}
	cmd.AddCommand(
		newNodeUpsertCmd(),
		newNodeGetCmd(),
		newNodeListCmd(),
		newNodeDeleteCmd(),
	)
	return cmd
}

func newNodeUpsertCmd() *cobra.Command {
	var (
		nodeKey    string
		layer      string
		nodeType   string
		domain     string
		name       string
		repo       string
		file       string
		revision   int64
		confidence float64
		metadata   string
	)

	cmd := &cobra.Command{
		Use:   "upsert",
		Short: "Upsert a node",
		Run: func(cmd *cobra.Command, args []string) {
			if nodeKey == "" || layer == "" || nodeType == "" || domain == "" || name == "" {
				fmt.Println("--node-key, --layer, --type, --domain, and --name are required")
				return
			}

			g := openGraph()
			defer g.Store().Close()

			input := validate.NodeInput{
				NodeKey:    nodeKey,
				Layer:      layer,
				NodeType:   nodeType,
				DomainKey:  domain,
				Name:       name,
				RepoName:   repo,
				FilePath:   file,
				Confidence: confidence,
				Metadata:   metadata,
			}

			id, err := g.UpsertNode(input, revision)
			if err != nil {
				outputError(err)
			}

			node, err := g.Store().GetNodeByID(id)
			if err != nil {
				outputError(err)
			}
			outputJSON(node)
		},
	}

	cmd.Flags().StringVar(&nodeKey, "node-key", "", "Node key (required)")
	cmd.Flags().StringVar(&layer, "layer", "", "Layer (required)")
	cmd.Flags().StringVar(&nodeType, "type", "", "Node type (required)")
	cmd.Flags().StringVar(&domain, "domain", "", "Domain key (required)")
	cmd.Flags().StringVar(&name, "name", "", "Node name (required)")
	cmd.Flags().StringVar(&repo, "repo", "", "Repository name")
	cmd.Flags().StringVar(&file, "file", "", "File path")
	cmd.Flags().Int64Var(&revision, "revision", 0, "Revision ID")
	cmd.Flags().Float64Var(&confidence, "confidence", 1.0, "Confidence score [0,1]")
	cmd.Flags().StringVar(&metadata, "metadata", "{}", "JSON metadata")

	return cmd
}

func newNodeGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <node_key>",
		Short: "Get a node by key",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			node, err := g.Store().GetNodeByKey(args[0])
			if err != nil {
				outputError(err)
			}
			outputJSON(node)
		},
	}
}

func newNodeListCmd() *cobra.Command {
	var (
		layer    string
		nodeType string
		domain   string
		repo     string
		status   string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List nodes",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			nodes, err := g.Store().ListNodes(store.NodeFilter{
				Layer:    layer,
				NodeType: nodeType,
				Domain:   domain,
				RepoName: repo,
				Status:   status,
			})
			if err != nil {
				outputError(err)
			}
			outputJSON(nodes)
		},
	}

	cmd.Flags().StringVar(&layer, "layer", "", "Filter by layer")
	cmd.Flags().StringVar(&nodeType, "type", "", "Filter by node type")
	cmd.Flags().StringVar(&domain, "domain", "", "Filter by domain key")
	cmd.Flags().StringVar(&repo, "repo", "", "Filter by repo name")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status")

	return cmd
}

func newNodeDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <node_key>",
		Short: "Delete (soft) a node by key",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			if err := g.Store().DeleteNode(args[0]); err != nil {
				outputError(err)
			}
			outputJSON(map[string]string{"deleted": args[0]})
		},
	}
}

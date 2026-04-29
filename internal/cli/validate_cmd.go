package cli

import (
	"strings"

	"github.com/alexdx2/chronicle-core/internal/store"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate graph integrity",
	}
	cmd.AddCommand(newValidateGraphCmd())
	return cmd
}

type validationIssue struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Message string `json:"message"`
}

type validationResult struct {
	Valid  bool              `json:"valid"`
	Issues []validationIssue `json:"issues"`
}

func newValidateGraphCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "graph",
		Short: "Run basic graph integrity checks",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			result := validationResult{
				Issues: []validationIssue{},
			}

			// Check nodes.
			nodes, err := g.Store().ListNodes(store.NodeFilter{})
			if err != nil {
				outputError(err)
			}

			for _, n := range nodes {
				// Check key format: must have 4 colon-separated parts.
				parts := strings.SplitN(n.NodeKey, ":", 4)
				if len(parts) < 4 {
					result.Issues = append(result.Issues, validationIssue{
						Kind:    "node",
						Key:     n.NodeKey,
						Message: "node_key does not have format layer:type:domain:qualified_name",
					})
				}

				// Check confidence range.
				if n.Confidence < 0 || n.Confidence > 1 {
					result.Issues = append(result.Issues, validationIssue{
						Kind:    "node",
						Key:     n.NodeKey,
						Message: "confidence out of range [0,1]",
					})
				}
			}

			// Check edges.
			edges, err := g.Store().ListEdges(store.EdgeFilter{})
			if err != nil {
				outputError(err)
			}

			for _, e := range edges {
				// Check edge key format: must contain "->".
				if !strings.Contains(e.EdgeKey, "->") {
					result.Issues = append(result.Issues, validationIssue{
						Kind:    "edge",
						Key:     e.EdgeKey,
						Message: "edge_key does not contain '->' separator",
					})
				}

				// Check confidence range.
				if e.Confidence < 0 || e.Confidence > 1 {
					result.Issues = append(result.Issues, validationIssue{
						Kind:    "edge",
						Key:     e.EdgeKey,
						Message: "confidence out of range [0,1]",
					})
				}
			}

			result.Valid = len(result.Issues) == 0
			outputJSON(result)
		},
	}
}

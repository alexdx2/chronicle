package cli

import (
	"fmt"

	"github.com/alexdx2/chronicle-core/validate"
	"github.com/spf13/cobra"
)

func newEvidenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "Manage graph evidence",
	}
	cmd.AddCommand(newEvidenceAddCmd(), newEvidenceListCmd())
	return cmd
}

func newEvidenceAddCmd() *cobra.Command {
	var (
		targetKind       string
		nodeKey          string
		edgeKey          string
		sourceKind       string
		repo             string
		file             string
		lineStart        int
		lineEnd          int
		extractorID      string
		extractorVersion string
		commitSHA        string
		confidence       float64
		metadata         string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add evidence for a node or edge",
		Run: func(cmd *cobra.Command, args []string) {
			if targetKind == "" {
				fmt.Println("--target-kind is required")
				return
			}
			if sourceKind == "" {
				fmt.Println("--source-kind is required")
				return
			}
			if extractorID == "" {
				fmt.Println("--extractor-id is required")
				return
			}
			if extractorVersion == "" {
				fmt.Println("--extractor-version is required")
				return
			}

			g := openGraph()
			defer g.Store().Close()

			input := validate.EvidenceInput{
				TargetKind:       targetKind,
				SourceKind:       sourceKind,
				RepoName:         repo,
				FilePath:         file,
				LineStart:        lineStart,
				LineEnd:          lineEnd,
				ExtractorID:      extractorID,
				ExtractorVersion: extractorVersion,
				CommitSHA:        commitSHA,
				Confidence:       confidence,
				Metadata:         metadata,
			}

			var id int64
			var err error

			switch targetKind {
			case "node":
				if nodeKey == "" {
					fmt.Println("--node-key is required for target-kind=node")
					return
				}
				id, err = g.AddNodeEvidence(nodeKey, input)
			case "edge":
				if edgeKey == "" {
					fmt.Println("--edge-key is required for target-kind=edge")
					return
				}
				id, err = g.AddEdgeEvidence(edgeKey, input)
			default:
				fmt.Printf("unknown target-kind %q\n", targetKind)
				return
			}

			if err != nil {
				outputError(err)
			}
			outputJSON(map[string]int64{"evidence_id": id})
		},
	}

	cmd.Flags().StringVar(&targetKind, "target-kind", "", "Target kind: node or edge (required)")
	cmd.Flags().StringVar(&nodeKey, "node-key", "", "Node key (required if target-kind=node)")
	cmd.Flags().StringVar(&edgeKey, "edge-key", "", "Edge key (required if target-kind=edge)")
	cmd.Flags().StringVar(&sourceKind, "source-kind", "", "Source kind (required)")
	cmd.Flags().StringVar(&repo, "repo", "", "Repository name")
	cmd.Flags().StringVar(&file, "file", "", "File path")
	cmd.Flags().IntVar(&lineStart, "line-start", 0, "Line start")
	cmd.Flags().IntVar(&lineEnd, "line-end", 0, "Line end")
	cmd.Flags().StringVar(&extractorID, "extractor-id", "", "Extractor ID (required)")
	cmd.Flags().StringVar(&extractorVersion, "extractor-version", "", "Extractor version (required)")
	cmd.Flags().StringVar(&commitSHA, "commit-sha", "", "Commit SHA")
	cmd.Flags().Float64Var(&confidence, "confidence", 1.0, "Confidence score [0,1]")
	cmd.Flags().StringVar(&metadata, "metadata", "{}", "JSON metadata")

	return cmd
}

func newEvidenceListCmd() *cobra.Command {
	var (
		nodeKey string
		edgeKey string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List evidence for a node or edge",
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

			// edgeKey
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

package cli

import (
	"fmt"

	"github.com/anthropics/depbot/internal/store"
	"github.com/spf13/cobra"
)

func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage graph snapshots",
	}
	cmd.AddCommand(newSnapshotCreateCmd(), newSnapshotListCmd())
	return cmd
}

func newSnapshotCreateCmd() *cobra.Command {
	var (
		revisionID int64
		domain     string
		kind       string
		nodeCount  int
		edgeCount  int
		summary    string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a snapshot",
		Run: func(cmd *cobra.Command, args []string) {
			if revisionID == 0 {
				fmt.Println("--revision is required")
				return
			}
			if domain == "" {
				fmt.Println("--domain is required")
				return
			}

			g := openGraph()
			defer g.Store().Close()

			row := store.SnapshotRow{
				RevisionID: revisionID,
				DomainKey:  domain,
				Kind:       kind,
				NodeCount:  nodeCount,
				EdgeCount:  edgeCount,
				Summary:    summary,
			}

			id, err := g.Store().CreateSnapshot(row)
			if err != nil {
				outputError(err)
			}

			snap, err := g.Store().GetSnapshot(id)
			if err != nil {
				outputError(err)
			}
			outputJSON(snap)
		},
	}

	cmd.Flags().Int64Var(&revisionID, "revision", 0, "Revision ID (required)")
	cmd.Flags().StringVar(&domain, "domain", "", "Domain key (required)")
	cmd.Flags().StringVar(&kind, "kind", "full", "Snapshot kind (full|incremental)")
	cmd.Flags().IntVar(&nodeCount, "node-count", 0, "Node count")
	cmd.Flags().IntVar(&edgeCount, "edge-count", 0, "Edge count")
	cmd.Flags().StringVar(&summary, "summary", "{}", "JSON summary")

	return cmd
}

func newSnapshotListCmd() *cobra.Command {
	var domain string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List snapshots for a domain",
		Run: func(cmd *cobra.Command, args []string) {
			if domain == "" {
				fmt.Println("--domain is required")
				return
			}

			g := openGraph()
			defer g.Store().Close()

			snaps, err := g.Store().ListSnapshots(domain)
			if err != nil {
				outputError(err)
			}
			outputJSON(snaps)
		},
	}

	cmd.Flags().StringVar(&domain, "domain", "", "Domain key (required)")

	return cmd
}

package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func newRevisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revision",
		Short: "Manage graph revisions",
	}
	cmd.AddCommand(newRevisionCreateCmd(), newRevisionGetCmd())
	return cmd
}

func newRevisionCreateCmd() *cobra.Command {
	var (
		domain    string
		afterSHA  string
		beforeSHA string
		trigger   string
		mode      string
		metadata  string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new revision",
		Run: func(cmd *cobra.Command, args []string) {
			if domain == "" {
				fmt.Println("--domain is required")
				return
			}
			if afterSHA == "" {
				fmt.Println("--after-sha is required")
				return
			}

			g := openGraph()
			defer g.Store().Close()

			id, err := g.Store().CreateRevision(domain, beforeSHA, afterSHA, trigger, mode, metadata)
			if err != nil {
				outputError(err)
			}

			rev, err := g.Store().GetRevision(id)
			if err != nil {
				outputError(err)
			}
			outputJSON(rev)
		},
	}

	cmd.Flags().StringVar(&domain, "domain", "", "Domain key (required)")
	cmd.Flags().StringVar(&afterSHA, "after-sha", "", "Git after SHA (required)")
	cmd.Flags().StringVar(&beforeSHA, "before-sha", "", "Git before SHA")
	cmd.Flags().StringVar(&trigger, "trigger", "manual", "Trigger kind")
	cmd.Flags().StringVar(&mode, "mode", "full", "Scan mode (full|incremental)")
	cmd.Flags().StringVar(&metadata, "metadata", "{}", "JSON metadata")

	return cmd
}

func newRevisionGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <revision_id>",
		Short: "Get a revision by ID",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				fmt.Printf("invalid revision_id %q: %v\n", args[0], err)
				return
			}

			g := openGraph()
			defer g.Store().Close()

			rev, err := g.Store().GetRevision(id)
			if err != nil {
				outputError(err)
			}
			outputJSON(rev)
		},
	}
}

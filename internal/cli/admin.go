package cli

import (
	"github.com/alexdx2/chronicle-core/internal/admin"
	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Start the admin dashboard (localhost only)",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			port, _ := cmd.Flags().GetInt("port")
			dev, _ := cmd.Flags().GetBool("dev")
			srv := admin.NewServer(g, g.Store(), port, manifestPath, dev, projectPath)
			if err := srv.Start(); err != nil {
				outputError(err)
			}
		},
	}
	cmd.Flags().Int("port", 4200, "HTTP port")
	cmd.Flags().Bool("dev", false, "Serve static files from disk (live reload without rebuild)")
	return cmd
}

package cli

import (
	"github.com/anthropics/depbot/internal/admin"
	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Start the admin dashboard (localhost only)",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			port, _ := cmd.Flags().GetInt("port")
			srv := admin.NewServer(g, g.Store(), port, manifestPath)
			if err := srv.Start(); err != nil {
				outputError(err)
			}
		},
	}
	cmd.Flags().Int("port", 4200, "HTTP port")
	return cmd
}

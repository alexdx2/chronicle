package cli

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/alexdx2/chronicle-core/internal/admin"
	mcpserver "github.com/alexdx2/chronicle-core/internal/mcp"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func portFromPath(dir string) int {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	h := fnv.New32a()
	h.Write([]byte(abs))
	return 4200 + int(h.Sum32()%800)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default: // linux
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "MCP server"}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start MCP server (stdio transport) with admin dashboard",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			mcpserver.SetManifestPath(manifestPath)
			mcpserver.SetGuideStore(g.Store())

			debugMode, _ := cmd.Flags().GetBool("debug")
			if debugMode {
				cwd, _ := os.Getwd()
				debugDir := filepath.Join(cwd, ".depbot", "debug")
				dl, err := mcpserver.NewDebugLogger(debugDir, "0.4.0")
				if err != nil {
					fmt.Fprintf(os.Stderr, "debug mode init failed: %v\n", err)
				} else {
					mcpserver.SetDebugLogger(dl)
					fmt.Fprintf(os.Stderr, "debug mode: logging to %s\n", debugDir)
				}
			}

			s := mcpserver.NewServerWithLogging(g, g.Store())

			adminPort, _ := cmd.Flags().GetInt("admin-port")
			noAdmin, _ := cmd.Flags().GetBool("no-admin")
			openDashboard, _ := cmd.Flags().GetBool("open")

			if !cmd.Flags().Changed("admin-port") {
				cwd, _ := os.Getwd()
				adminPort = portFromPath(cwd)
			}

			mcpserver.SetAdminPort(adminPort)

			if !noAdmin {
				srv := admin.NewServer(g, g.Store(), adminPort, manifestPath, false, projectPath)
				go func() {
					if err := srv.Start(); err != nil {
						fmt.Fprintf(os.Stderr, "admin dashboard failed: %v\n", err)
					}
				}()

				if openDashboard {
					go func() {
						time.Sleep(500 * time.Millisecond)
						url := fmt.Sprintf("http://localhost:%d", adminPort)
						openBrowser(url)
					}()
				}
			}

			if err := server.ServeStdio(s); err != nil {
				outputError(err)
			}

			// Shutdown: check for abandoned revisions and close debug logger
			if dl := mcpserver.GetDebugLogger(); dl != nil {
				allNodes, _ := g.Store().ListNodes(store.NodeFilter{})
				if len(allNodes) > 0 {
					domain := allNodes[0].DomainKey
					if rev, err := g.Store().GetLatestRevision(domain); err == nil {
						if _, snapErr := g.Store().GetLatestSnapshot(domain); snapErr != nil {
							dl.LogAbandoned(rev.RevisionID)
						}
					}
				}
				dl.Close()
				mcpserver.SetDebugLogger(nil)
			}
		},
	}

	serveCmd.Flags().Int("admin-port", 4200, "Admin dashboard HTTP port")
	serveCmd.Flags().Bool("no-admin", false, "Disable admin dashboard")
	serveCmd.Flags().Bool("open", false, "Auto-open dashboard in browser")
	serveCmd.Flags().Bool("debug", false, "Enable debug logging to .depbot/debug/")

	cmd.AddCommand(serveCmd)
	return cmd
}

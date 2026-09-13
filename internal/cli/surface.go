package cli

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/surface"
)

func newSurfaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "surface",
		Short: "Import a product's own surface extract (screens, panels, controls) into the ui layer",
	}
	cmd.AddCommand(newSurfaceImportCmd())
	return cmd
}

func newSurfaceImportCmd() *cobra.Command {
	var domain string
	var allowUnresolved bool
	var allowDiverged bool

	cmd := &cobra.Command{
		Use:   "import <surface.json>",
		Short: "Import a surface extract into the ui layer",
		Long: `Reads a product's surface.json — the screens, panels and controls it reports
about itself — resolves every name it uses against the graph a scan already
built, and writes the result as ui-layer nodes named by the commit the extract
was made at.

The import is closed-world per product: a control this file no longer mentions
is tombstoned. It never touches the code layer.

Exit code 2 means the extract was refused: its commit is not an ancestor of
HEAD, its bytes changed without its commit changing, or a name it uses does not
resolve. Each has a flag that says "yes, I meant that".`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			f, err := surface.Load(args[0])
			if err != nil {
				outputError(err)
			}

			g := openGraph()
			defer g.Store().Close()

			res, err := surface.Import(g, f, surface.ImportOptions{
				Domain:          domain,
				RepoDir:         paths.GitDir(),
				AllowUnresolved: allowUnresolved,
				AllowDiverged:   allowDiverged,
			})
			if err != nil {
				outputRefusal(err)
			}
			outputJSON(res)
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "Domain key (default: the store's only domain)")
	cmd.Flags().BoolVar(&allowUnresolved, "allow-unresolved", false, "Import what resolves; report the names that do not")
	cmd.Flags().BoolVar(&allowDiverged, "allow-diverged", false, "Import even though the commit is not an ancestor of HEAD")
	return cmd
}

// outputRefusal exits 2 for the refusals a caller can act on (a diverged
// commit, a changed extract at an unchanged commit, a name that does not
// resolve) so a script can tell "you asked for something impossible" apart
// from "Chronicle broke".
func outputRefusal(err error) {
	code := 1
	for _, refusal := range []error{surface.ErrDiverged, surface.ErrCommitChanged, surface.ErrUnresolved} {
		if errors.Is(err, refusal) {
			code = 2
		}
	}
	json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
	os.Exit(code)
}

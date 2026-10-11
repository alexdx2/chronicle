// Package scan holds the deterministic refresh phases — verification and
// structure — behind an explicit environment, so a caller that cannot import
// the CLI (a server worker, for one) can drive them without process globals.
package scan

import "github.com/alexdx2/chronicle-core/graph"

// RefreshExtensions are the file types whose evidence the deterministic refresh
// can re-verify. Anything outside this set is left to a full scan.
var RefreshExtensions = []string{".ts", ".tsx", ".js", ".jsx", ".cs", ".prisma", ".graphql", ".proto", ".go", ".py"}

// Env is where a refresh runs: which working copy git is measured in, and where
// the domain manifest lives.
type Env struct {
	// RepoDir is the working copy whose HEAD is processed (git is measured here).
	RepoDir string
	// ManifestPath is <chronicle dir>/chronicle.domain.yaml.
	ManifestPath string
	// StructuralFn replaces graph.StructuralRefresh (tests make phase 2 fail);
	// nil means the real one.
	StructuralFn func(g *graph.Graph, in graph.StructuralInput) (*graph.StructuralResult, error)
}

func (e Env) structural(g *graph.Graph, in graph.StructuralInput) (*graph.StructuralResult, error) {
	if e.StructuralFn != nil {
		return e.StructuralFn(g, in)
	}
	return g.StructuralRefresh(in)
}

// ShortSHA truncates a git id to its conventional 7 characters.
func ShortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

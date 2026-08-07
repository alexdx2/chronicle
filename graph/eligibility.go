package graph

import "github.com/alexdx2/chronicle-core/store"

// SQ-Contract 2 axis 2: edge type alone is insufficient evidence of runtime
// use. DEPENDS_ON (and any other dependency-shaped edge) carries a
// dependency_source (Task 3, store.EdgeRow.DependencySource): "code" comes
// from AST/evidence and is real at runtime; "manifest" is declared in a
// package manifest (dependencies/optionalDependencies) but not necessarily
// exercised at runtime; "manifest_peer" is a declared peer requirement and is
// never treated as a real dependency by default.
//
// "" counts as code: pre-column rows default there (store.UpsertEdge backfills
// "code" for any row that doesn't set it), and non-dependency edge types
// (INJECTS, EXPOSES_ENDPOINT, CALLS_SERVICE, ...) never set this field either,
// so they trivially pass both gates below — this file only changes behavior
// for edges that actually carry "manifest"/"manifest_peer".
//
// Produced for pro's Task 12 — names and signatures are binding, called
// verbatim from pro.

// IsRuntimeDep reports whether e is backed by real (code/AST) evidence of use,
// as opposed to merely being declared in a manifest.
func IsRuntimeDep(e store.EdgeRow) bool {
	return e.DependencySource == "" || e.DependencySource == "code"
}

// IsDeclaredDep reports whether e is at least declared — runtime deps plus
// manifest-declared deps, but excluding manifest_peer (peer requirements are
// excluded everywhere by default; nothing in this package widens that).
func IsDeclaredDep(e store.EdgeRow) bool { return IsRuntimeDep(e) || e.DependencySource == "manifest" }

// filterRuntimeEdges keeps only edges that pass IsRuntimeDep. Used by
// listings that feed impact-shaped math (derive_flows' REQUIRES closure)
// where the per-edge gate is more naturally applied to a whole slice than
// inline in a traversal loop.
func filterRuntimeEdges(edges []store.EdgeRow) []store.EdgeRow {
	out := edges[:0]
	for _, e := range edges {
		if IsRuntimeDep(e) {
			out = append(out, e)
		}
	}
	return out
}

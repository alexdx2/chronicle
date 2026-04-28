package graph

// QueryContext holds context information for filtering queries.
type QueryContext struct {
	ContextID    int64
	AsOfRevision int64
}

// VisibleRevisionsCTE returns the SQL CTE and args for the given query context.
// If ctx is nil, returns empty (no filtering — legacy behavior).
func (g *Graph) VisibleRevisionsCTE(ctx *QueryContext) (string, []any) {
	if ctx == nil || ctx.ContextID == 0 {
		return "", nil
	}
	return g.store.BuildVisibleRevisionsCTE(ctx.ContextID, ctx.AsOfRevision)
}

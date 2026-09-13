package store

// Revision selection for the structural refresh — the deterministic phase that
// re-extracts imports, routes, models and calls on every commit. It gets its
// own pointer because it succeeds and fails independently of the evidence
// re-verification that runs beside it: a verification that landed at B and a
// structural phase that died at A must leave verified@B and structured@A, and
// the next run must diff A..HEAD. One pointer for both would silently claim
// structure over the commits only the verifier saw.
//
// The pointer is a STAMP, not a row of its own. graph_revisions is
// UNIQUE(domain_key, git_after_sha) and the refresh's verification phase has
// already written a revision at HEAD by the time the structural phase runs, so
// a second row there is impossible. The phase merges
//
//	"structural": {"complete": true, "pack": "<rules pack>", "processed": N, ...}
//
// onto whatever revision sits at HEAD — the hook's refresh row, the scan's own
// row, or a git_hook row it created itself when nothing had claimed that commit
// yet. Which row it is says nothing about the claim, so the query asks only
// about the stamp.

// revisionStructuralCompleteExpr reads metadata.structural.complete
// defensively. json_extract raises on malformed JSON, which would fail the
// whole query because of one bad row; CASE is documented to short-circuit, so
// json_extract never sees invalid input and a non-JSON metadata simply reads as
// "no structural stamp". SQLite's JSON1 maps a JSON true to the integer 1, so
// `= 1` is the check for `"complete": true` and also accepts a writer that
// stored 1 directly.
const revisionStructuralCompleteExpr = `CASE WHEN json_valid(metadata)
	THEN json_extract(metadata,'$.structural.complete') ELSE NULL END`

// LatestStructuralRevision is the newest completed structural phase of a
// domain (empty domainKey = any domain): the commit up to which deterministic
// structure is guaranteed. ErrNotFound when the domain has none.
//
// complete = true is the whole condition, and it is load-bearing: the pointer
// means "the whole diff was processed and no file is left on an older rules
// pack", and a run that stopped halfway — or one that drained only its batch of
// the backlog — processed an unknown prefix of it. Whatever such a run wrote
// stays in the graph; it just does not move the claim.
func (s *Store) LatestStructuralRevision(domainKey string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE ` + revisionStructuralCompleteExpr + ` = 1`
	var args []any
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY revision_id DESC LIMIT 1`
	return s.oneRevision("LatestStructuralRevision", q, args...)
}

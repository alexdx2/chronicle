package graph

import (
	"fmt"

	"github.com/alexdx2/chronicle-core/store"
)

// RefreshResult summarizes a zero-token structural refresh.
type RefreshResult struct {
	RevisionID      int64             `json:"revision_id"`
	HeadSHA         string            `json:"head_sha"`
	ChangedFiles    int               `json:"changed_files"`
	DeletedFiles    int               `json:"deleted_files"`
	Invalidated     *InvalidateResult `json:"invalidated"`
	PendingSemantic []string          `json:"pending_semantic"`
}

// RefreshFromDiff re-anchors structural evidence for a set of changed/deleted
// files without any LLM call. It reuses InvalidateChanged, which stale-marks
// evidence on those files, mechanically re-verifies what it can (import lines
// moved/removed, etc.), recomputes trust, and reports which files still need an
// agent rescan (PendingSemantic).
//
// This does NOT discover new structural facts (a freshly added import) — that
// remains the scan's job. It keeps existing evidence honest and makes semantic
// staleness enumerable instead of silent.
func (g *Graph) RefreshFromDiff(domainKey, headSHA string, changedFiles, deletedFiles []string) (*RefreshResult, error) {
	res := &RefreshResult{
		HeadSHA:      headSHA,
		ChangedFiles: len(changedFiles),
		DeletedFiles: len(deletedFiles),
	}

	all := append(append([]string{}, changedFiles...), deletedFiles...)
	if len(all) == 0 {
		return res, nil
	}

	// trigger_kind and mode are fixed enums; refresh is the git-hook-driven
	// incremental path, distinguished by a metadata marker.
	revID, err := g.store.CreateRevision(domainKey, "", headSHA, "git_hook", "incremental", `{"kind":"refresh"}`)
	if err != nil {
		return nil, fmt.Errorf("RefreshFromDiff: create revision: %w", err)
	}
	res.RevisionID = revID

	inv, err := g.InvalidateChanged(domainKey, revID, all)
	if err != nil {
		return nil, fmt.Errorf("RefreshFromDiff: invalidate: %w", err)
	}
	res.Invalidated = inv
	res.PendingSemantic = inv.NeedsClaude

	// Snapshot so the dashboard and changelog record the refresh point.
	stats, err := g.QueryStats(domainKey)
	if err == nil {
		_, _ = g.store.CreateSnapshot(store.SnapshotRow{
			RevisionID:       revID,
			DomainKey:        domainKey,
			Kind:             "refresh",
			NodeCount:        stats.NodeCount,
			EdgeCount:        stats.EdgeCount,
			ChangedFileCount: len(all),
			ChangedNodeCount: inv.AffectedNodes,
			ChangedEdgeCount: inv.AffectedEdges,
			Summary:          "{}",
		})
	}

	return res, nil
}

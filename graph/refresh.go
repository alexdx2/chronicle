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

// RecordRefreshNoop names HEAD as re-verified when the diff since the base
// touched no file the graph holds evidence for — a docs-only commit, a
// lockfile bump, a README.
//
// "No file the graph knows about", NOT "no file this refresh can check". The
// second reading would stamp verified@HEAD on every commit of a repo written
// in a language outside refreshExtensions, which is the graph claiming to have
// checked code it has never read. Callers must gate on
// store.KnownFilePaths — see internal/cli/refresh.go.
//
// Without it those commits quietly age the graph: freshness compares the
// newest scan/refresh commit against HEAD, so a repo whose knowledge is
// demonstrably still correct drifts from "verified" to "stale, 12 commits
// behind" purely because nothing wrote down that the 12 commits could not have
// invalidated anything. Nothing is invalidated and nothing is verified here —
// the revision records the check, and says noop so a reader is not misled into
// thinking evidence was re-examined.
//
// A commit that already carries a revision keeps it: graph_revisions is
// UNIQUE(domain_key, git_after_sha), and there is nothing to add to a commit
// that was just scanned. The check is still recorded on that row — a scan owns
// the trigger, but a surface import can own it too, and a refresh that left no
// mark on somebody else's row would report a commit it did verify as stale.
func (g *Graph) RecordRefreshNoop(domainKey, headSHA string) (int64, error) {
	if headSHA == "" {
		return 0, nil
	}
	id, _, err := g.store.ClaimRevision(store.RevisionClaim{
		DomainKey:   domainKey,
		AfterSHA:    headSHA,
		TriggerKind: "git_hook",
		Mode:        "incremental",
		Metadata:    `{"kind":"refresh","noop":true}`,
		Merge:       map[string]any{"refresh": map[string]any{"noop": true}},
	})
	if err != nil {
		return 0, fmt.Errorf("RecordRefreshNoop: %w", err)
	}
	return id, nil
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
	//
	// Claimed, not created: the commit may already be named by a scan or by a
	// surface import that reached it first, and inserting a second row for one
	// commit is a UNIQUE(domain_key, git_after_sha) failure — phase 1 dying on
	// a constraint error rather than re-anchoring anything.
	revID, _, err := g.store.ClaimRevision(store.RevisionClaim{
		DomainKey:   domainKey,
		AfterSHA:    headSHA,
		TriggerKind: "git_hook",
		Mode:        "incremental",
		Metadata:    `{"kind":"refresh"}`,
		Merge:       map[string]any{"refresh": map[string]any{"noop": false}},
	})
	if err != nil {
		return nil, fmt.Errorf("RefreshFromDiff: claim revision: %w", err)
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

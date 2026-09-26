package snapshot

import (
	"fmt"
	"math"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/store"
)

// unboundedScanRevisions is passed to ListScanRevisions so BackfillBlobs
// walks a project's ENTIRE scan history, not the newest slice. Its own doc
// comment is explicit that limit<=0 does NOT mean unlimited — it means a
// default of 20 rows — and this codebase has no "give me everything"
// sentinel (the one other caller, freshness/freshness.go, uses a small
// finite constant for a different, bounded purpose). This is simply a value
// no real project's revision count will ever reach, so SQLite's LIMIT
// clause behaves as if there were none.
const unboundedScanRevisions = math.MaxInt32

// backfillExtractorID/backfillExtractorVersion name the coverage this
// package writes for a file whose blob came from a scan_extractions row
// (BackfillBlobs' main pass, below) — the same extractor identity the live
// scan pipeline itself stamps on an agent-reported file's coverage (see
// mcpserver's recordScanCoverage), so a migrated project's file_coverage is
// shaped exactly like a freshly-scanned one's.
const backfillExtractorID = "chronicle-scan"

func backfillExtractorVersion() string {
	return fmt.Sprintf("fact-schema@%d", store.FactSchemaVersion)
}

// BackfillReport summarizes one BackfillBlobs run: Updated is how many files
// got a file_coverage row with a real blob; Unresolved is how many could
// not be resolved at all because the revision that recorded them names a
// commit no longer reachable in repoRoot's git history (a shallow clone, or
// a repository recreated from scratch).
type BackfillReport struct{ Updated, Unresolved int }

// coverageKey identifies one (domain, file) pair BackfillBlobs has already
// attempted to resolve — successfully or not. It mirrors file_coverage's own
// primary key minus extractor_id: BackfillBlobs only ever writes one
// extractor's worth of coverage per file, so the pair is enough to dedupe on.
type coverageKey struct {
	domain string
	path   string
}

// BackfillBlobs reconstructs file_coverage.blob_sha for a project whose scan
// history predates blob tracking, or whose event journal used to be
// committed to git instead of the graph.jsonl snapshot: neither
// scan_extractions nor graph_evidence carried a git blob id back then (the
// column defaults to an empty string for any row written before it
// existed), so a project adopting the snapshot format has nothing telling
// `chronicle graph status` which blob each already-scanned file was last
// seen at.
//
// Main pass: for every domain (GetDomains), every scan revision
// (ListScanRevisions, newest first — see unboundedScanRevisions) and every
// scan_extractions row on it (ListScanExtractions), the file's content at
// revision.GitAfterSHA is read with `git show <sha>:<path>` (gitdiff.Show)
// and hashed with gitutil.BlobSHA — two deliberate steps, rather than asking
// git directly for the blob id via rev-parse/ls-tree, so the blob
// computation always goes through the one function this project already
// trusts for it. Revisions are walked newest-first and a (domain, file) pair
// is only ever resolved once, so the coverage row this writes always
// reflects the file's MOST RECENTLY recorded content — file_coverage's usual
// "standing latest observation" shape (UpsertCoverage overwrites in place).
//
// Fallback pass: every current node's and edge's evidence is then walked to
// catch files the main pass never touched at all — a file captured only by
// the structural phase (whose scan_extractions rows use the "structural"
// role, which ListScanExtractions deliberately excludes) has no "serving"
// scan_extractions row, so its blob is instead reconstructed from the
// evidence's OWN valid_from_revision_id and file_path. Evidence that already
// carries a blob, or whose (domain, file) the main pass already settled, is
// skipped.
//
// A file whose revision's git_after_sha is not reachable in repoRoot's
// history at all (a shallow clone, or a repository recreated from scratch)
// cannot be resolved: it is left without a blob and counted as Unresolved
// rather than failing the whole run. The only mutation this function
// performs is st.UpsertCoverage — it never writes graph_evidence directly;
// evidence is read here only to find files scan_extractions missed.
func BackfillBlobs(st *store.Store, repoRoot string) (BackfillReport, error) {
	var report BackfillReport
	resolved := map[coverageKey]bool{}

	domains, err := st.GetDomains()
	if err != nil {
		return BackfillReport{}, fmt.Errorf("BackfillBlobs: list domains: %w", err)
	}

	for _, domain := range domains {
		revisions, err := st.ListScanRevisions(domain, unboundedScanRevisions)
		if err != nil {
			return BackfillReport{}, fmt.Errorf("BackfillBlobs: list revisions for %q: %w", domain, err)
		}
		for _, rev := range revisions {
			extractions, err := st.ListScanExtractions(rev.RevisionID, domain)
			if err != nil {
				return BackfillReport{}, fmt.Errorf("BackfillBlobs: list scan extractions for revision %d: %w", rev.RevisionID, err)
			}
			for _, ext := range extractions {
				key := coverageKey{domain, ext.FilePath}
				if resolved[key] {
					continue // a newer revision already settled this file
				}
				resolved[key] = true

				blob, ok := resolveBlob(repoRoot, rev.GitAfterSHA, ext.FilePath)
				if !ok {
					report.Unresolved++
					continue
				}
				if err := st.UpsertCoverage(store.CoverageRow{
					DomainKey:        domain,
					FilePath:         ext.FilePath,
					ExtractorID:      backfillExtractorID,
					ExtractorVersion: backfillExtractorVersion(),
					Status:           ext.Status,
					BlobSHA:          blob,
				}); err != nil {
					return BackfillReport{}, fmt.Errorf("BackfillBlobs: upsert coverage %q/%q: %w", domain, ext.FilePath, err)
				}
				report.Updated++
			}
		}
	}

	if err := backfillFromEvidence(st, repoRoot, resolved, &report); err != nil {
		return BackfillReport{}, err
	}

	return report, nil
}

// backfillFromEvidence is BackfillBlobs' fallback pass — see its doc
// comment. It walks every current node's and edge's evidence the same way
// FromStore itself does (ListNodes/ListEdges, then per-node/per-edge
// evidence), rather than adding a new store method to select graph_evidence
// broadly: this is a one-time migration path, not a hot one, and reusing an
// established read pattern keeps it inside "never mutate graph_* tables with
// raw SQL" without inventing a new store query surface for it.
func backfillFromEvidence(st *store.Store, repoRoot string, resolved map[coverageKey]bool, report *BackfillReport) error {
	nodes, err := st.ListNodes(store.NodeFilter{})
	if err != nil {
		return fmt.Errorf("BackfillBlobs: list nodes: %w", err)
	}
	for _, n := range nodes {
		ev, err := st.ListEvidenceByNode(n.NodeID)
		if err != nil {
			return fmt.Errorf("BackfillBlobs: evidence for node %d: %w", n.NodeID, err)
		}
		if err := backfillEvidenceRows(st, repoRoot, n.DomainKey, ev, resolved, report); err != nil {
			return err
		}
	}

	edges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		return fmt.Errorf("BackfillBlobs: list edges: %w", err)
	}
	for _, e := range edges {
		ev, err := st.ListEvidenceByEdge(e.EdgeID)
		if err != nil {
			return fmt.Errorf("BackfillBlobs: evidence for edge %d: %w", e.EdgeID, err)
		}
		// EdgeRow carries no domain_key column of its own — derive it from
		// the FROM node's key the same way AddEvidence resolves an edge's
		// domain for journaling. UpsertEdge backfills from_node_key on
		// write, so this is populated for any edge written through the
		// ordinary store API.
		domain := store.DomainFromNodeKey(e.FromNodeKey)
		if err := backfillEvidenceRows(st, repoRoot, domain, ev, resolved, report); err != nil {
			return err
		}
	}
	return nil
}

// backfillEvidenceRows applies BackfillBlobs' evidence fallback to one
// node's or edge's evidence rows, scoped to domain (see BackfillBlobs' doc
// comment for the algorithm).
func backfillEvidenceRows(st *store.Store, repoRoot, domain string, rows []store.EvidenceRow, resolved map[coverageKey]bool, report *BackfillReport) error {
	for _, e := range rows {
		if e.BlobSHA != "" || e.FilePath == "" {
			continue // already has a blob, or carries no file to backfill one for
		}
		key := coverageKey{domain, e.FilePath}
		if resolved[key] {
			continue // scan_extractions (or an earlier evidence row) already settled this file
		}
		resolved[key] = true

		rev, err := st.GetRevision(e.ValidFromRevisionID)
		if err != nil {
			report.Unresolved++
			continue
		}
		blob, ok := resolveBlob(repoRoot, rev.GitAfterSHA, e.FilePath)
		if !ok {
			report.Unresolved++
			continue
		}

		extractorID := e.ExtractorID
		if extractorID == "" {
			extractorID = backfillExtractorID
		}
		extractorVersion := e.ExtractorVersion
		if extractorVersion == "" {
			extractorVersion = backfillExtractorVersion()
		}
		if err := st.UpsertCoverage(store.CoverageRow{
			DomainKey:        domain,
			FilePath:         e.FilePath,
			ExtractorID:      extractorID,
			ExtractorVersion: extractorVersion,
			Status:           "extracted",
			BlobSHA:          blob,
		}); err != nil {
			return fmt.Errorf("BackfillBlobs: upsert coverage %q/%q: %w", domain, e.FilePath, err)
		}
		report.Updated++
	}
	return nil
}

// resolveBlob reads path's content at ref (`git show ref:path`, via
// gitdiff.Show) and hashes it exactly the way git itself would hash a blob
// (gitutil.BlobSHA) — the show-then-hash approach BackfillBlobs uses
// everywhere, so blob computation always goes through the one function this
// project already trusts for it, rather than asking git directly via
// rev-parse/ls-tree.
//
// ok is false whenever ref or path is empty, ref does not name a commit
// reachable in repoRoot's history, or that commit's tree has no such path —
// a shallow or recreated repository being the case this exists for. This is
// BackfillBlobs' only source of Unresolved results.
func resolveBlob(repoRoot, ref, path string) (blob string, ok bool) {
	// An empty ref would turn gitdiff.Show's "ref:path" into the bare
	// ":path", which git itself parses as a stage-0 INDEX lookup — a
	// different, unintended, and potentially misleading answer rather than
	// an error. Reject it before it ever reaches git.
	if ref == "" || path == "" {
		return "", false
	}
	content, err := gitdiff.Show(repoRoot, ref, path)
	if err != nil {
		return "", false
	}
	sum, err := gitutil.BlobSHA(repoRoot, content)
	if err != nil {
		return "", false
	}
	return sum, true
}

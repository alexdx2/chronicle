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

// BackfillReport summarizes one BackfillBlobs run: Updated is how many
// distinct (domain, file) pairs got a file_coverage row with a real blob;
// Unresolved is how many could not be resolved at all because the revision
// that recorded them names a commit no longer reachable in repoRoot's git
// history (a shallow clone, or a repository recreated from scratch).
//
// Counted once per (domain, file), not once per row: a file can carry many
// scan_extractions rows (voting) and many evidence rows (one per fact any
// node or edge asserts from it), and a human reading "47 unresolved" means
// "47 files", not "many facts about a handful of files". Evidence rows that
// get their own blob_sha patched by the fallback pass below by reusing an
// already-resolved file's cached blob do not add to either count — only the
// (domain, file) resolution itself does.
type BackfillReport struct{ Updated, Unresolved int }

// coverageKey identifies one (domain, file) pair. It mirrors file_coverage's
// own primary key minus extractor_id: BackfillBlobs only ever writes one
// extractor's worth of coverage per file, so the pair is enough to key a
// cache (and BackfillReport's counts) on.
type coverageKey struct {
	domain string
	path   string
}

// blobResolution is a cached outcome of resolving one coverageKey's blob:
// Blob is set and OK is true on success; OK is false (Blob "") when the
// revision that would answer it is not reachable in repoRoot's history.
// Cached so a (domain, file) pair already settled by one row — whether a
// scan_extractions row in the main pass or an earlier evidence row in the
// fallback pass — is never re-resolved (no repeat git subprocess calls, no
// double-counted report) when a LATER evidence row for the very same file
// needs the same answer.
type blobResolution struct {
	Blob string
	OK   bool
}

// BackfillBlobs reconstructs blob ids for a project whose scan history
// predates blob tracking, or whose event journal used to be committed to
// git instead of the graph.jsonl snapshot: neither scan_extractions,
// file_coverage nor graph_evidence carried a git blob id back then (blob_sha
// defaults to an empty string for any row written before the column
// existed), so a project adopting the snapshot format has nothing telling
// `chronicle graph status` which blob each already-scanned file was last
// seen at, and would export every old evidence row's Blob field empty in
// graph.jsonl forever.
//
// It fills in BOTH places blob_sha lives:
//
//   - file_coverage, via the existing UpsertCoverage — what
//     snapshot.ComputeFreshness (chronicle graph status) actually compares
//     against.
//   - graph_evidence.blob_sha on any evidence row that has none, via the new
//     UpdateEvidenceBlobSHA — what FromStore's own evidence export
//     (filterEvidence, snapshot/export.go) reads directly off the row to
//     fill Evidence.Blob in graph.jsonl. Leaving this column alone would
//     mean a migrated project's graph.jsonl still shows every old evidence
//     entry with an empty blob, defeating half the point of migrating.
//
// Main pass: for every domain (GetDomains), every scan revision
// (ListScanRevisions, newest first — see unboundedScanRevisions) and every
// scan_extractions row on it (ListScanExtractions), the file's content at
// revision.GitAfterSHA is read with `git show <sha>:<path>` (gitdiff.Show)
// and hashed with gitutil.BlobSHA — two deliberate steps, rather than asking
// git directly for the blob id via rev-parse/ls-tree, so the blob
// computation always goes through the one function this project already
// trusts for it. Revisions are walked newest-first and a (domain, file) pair
// is only ever resolved once (cached in a blobResolution), so the coverage
// row this writes always reflects the file's MOST RECENTLY recorded
// content — file_coverage's usual "standing latest observation" shape
// (UpsertCoverage overwrites in place).
//
// Fallback pass (backfillFromEvidence): every current node's and edge's
// evidence is then walked. Two independent things happen per evidence row
// with a non-empty FilePath:
//
//  1. If that (domain, file) has no cached resolution yet (the main pass
//     never reached it — e.g. a file captured only by the structural phase,
//     whose scan_extractions rows use the "structural" role, which
//     ListScanExtractions deliberately excludes), it is resolved from the
//     evidence's OWN valid_from_revision_id and file_path, cached, and
//     (on success) written to file_coverage too, exactly like the main pass
//     would have.
//  2. If the row's own blob_sha is empty and a resolution (fresh or cached
//     from any earlier row or the main pass) succeeded, THIS row is patched
//     via UpdateEvidenceBlobSHA — regardless of whether its (domain, file)
//     was already settled by the main pass or by a previous evidence row.
//     This runs for every qualifying row, not just the first one seen for a
//     given file: a file the main pass already resolved can still have
//     several evidence rows with no blob of their own, and every one of
//     them needs patching, not just whichever file happens to need a NEW
//     resolution.
//
// A file whose revision's git_after_sha is not reachable in repoRoot's
// history at all (a shallow clone, or a repository recreated from scratch)
// cannot be resolved: it is left without a blob and counted as Unresolved
// rather than failing the whole run.
func BackfillBlobs(st *store.Store, repoRoot string) (BackfillReport, error) {
	var report BackfillReport
	cache := map[coverageKey]blobResolution{}

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
				if _, already := cache[key]; already {
					continue // a newer revision already settled this file
				}

				res := blobResolution{}
				res.Blob, res.OK = resolveBlob(repoRoot, rev.GitAfterSHA, ext.FilePath)
				cache[key] = res
				if !res.OK {
					report.Unresolved++
					continue
				}
				if err := st.UpsertCoverage(store.CoverageRow{
					DomainKey:        domain,
					FilePath:         ext.FilePath,
					ExtractorID:      backfillExtractorID,
					ExtractorVersion: backfillExtractorVersion(),
					Status:           ext.Status,
					BlobSHA:          res.Blob,
				}); err != nil {
					return BackfillReport{}, fmt.Errorf("BackfillBlobs: upsert coverage %q/%q: %w", domain, ext.FilePath, err)
				}
				report.Updated++
			}
		}
	}

	if err := backfillFromEvidence(st, repoRoot, cache, &report); err != nil {
		return BackfillReport{}, err
	}

	return report, nil
}

// backfillFromEvidence is BackfillBlobs' fallback pass — see its doc
// comment. It walks every current node's and edge's evidence the same way
// FromStore itself does (ListNodes/ListEdges, then per-node/per-edge
// evidence), rather than adding a new store method to select graph_evidence
// broadly: this is a one-time migration path, not a hot one, and reusing an
// established read pattern keeps it from growing the store's read surface
// for a single caller.
func backfillFromEvidence(st *store.Store, repoRoot string, cache map[coverageKey]blobResolution, report *BackfillReport) error {
	nodes, err := st.ListNodes(store.NodeFilter{})
	if err != nil {
		return fmt.Errorf("BackfillBlobs: list nodes: %w", err)
	}
	for _, n := range nodes {
		ev, err := st.ListEvidenceByNode(n.NodeID)
		if err != nil {
			return fmt.Errorf("BackfillBlobs: evidence for node %d: %w", n.NodeID, err)
		}
		if err := backfillEvidenceRows(st, repoRoot, n.DomainKey, ev, cache, report); err != nil {
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
		if err := backfillEvidenceRows(st, repoRoot, domain, ev, cache, report); err != nil {
			return err
		}
	}
	return nil
}

// backfillEvidenceRows applies BackfillBlobs' evidence fallback to one
// node's or edge's evidence rows, scoped to domain — see BackfillBlobs' doc
// comment for the two independent things this does per row: settle (and
// cache/write coverage for) the row's (domain, file) pair if nothing has
// yet, and separately patch the row's OWN blob_sha if it has none and a
// resolution is available.
func backfillEvidenceRows(st *store.Store, repoRoot, domain string, rows []store.EvidenceRow, cache map[coverageKey]blobResolution, report *BackfillReport) error {
	for _, e := range rows {
		if e.FilePath == "" {
			continue // synthetic/derived evidence has no file to backfill a blob for
		}

		key := coverageKey{domain, e.FilePath}
		res, cached := cache[key]
		if !cached {
			// Nothing — not the main pass, not an earlier evidence row — has
			// resolved this (domain, file) yet. Resolve it from THIS row's
			// own revision (the brief's "evidence without a serving
			// scan_extractions row" case) and write the file_coverage row
			// the main pass never got to, exactly as it would have.
			rev, revErr := st.GetRevision(e.ValidFromRevisionID)
			if revErr != nil {
				res = blobResolution{}
			} else {
				res.Blob, res.OK = resolveBlob(repoRoot, rev.GitAfterSHA, e.FilePath)
			}
			cache[key] = res

			if !res.OK {
				report.Unresolved++
			} else {
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
					BlobSHA:          res.Blob,
				}); err != nil {
					return fmt.Errorf("BackfillBlobs: upsert coverage %q/%q: %w", domain, e.FilePath, err)
				}
				report.Updated++
			}
		}

		// Patch THIS row's own blob_sha whenever it has none and a
		// resolution is available — whether that resolution came from the
		// main pass, from the block just above, or from an earlier evidence
		// row on the same file. This must run for every qualifying row, not
		// just the first one seen for a given (domain, file): a file the
		// main pass already resolved can still carry several evidence rows
		// with no blob of their own, and each one needs patching.
		if e.BlobSHA == "" && res.OK {
			if err := st.UpdateEvidenceBlobSHA(e.EvidenceID, res.Blob); err != nil {
				return fmt.Errorf("BackfillBlobs: update evidence %d blob: %w", e.EvidenceID, err)
			}
		}
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

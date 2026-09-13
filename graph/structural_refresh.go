package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/rules"
	"github.com/alexdx2/chronicle-core/extract/structural"
	"github.com/alexdx2/chronicle-core/store"
)

// The structural refresh: zero-token knowledge of today's code.
//
// It re-extracts imports, routes, models and calls for the files a commit
// touched, resolves them deterministically, and REPLACES each file's previous
// structural contribution — the closed-world half of the graph, where "the file
// no longer says this" is a fact and not an absence of one.
//
// Three properties make the phase honest, and each of them is a separate piece
// of machinery below:
//
//  1. It is complete or it is nothing. The pointer that says "structure is
//     guaranteed up to this commit" is stamped only when the whole diff AND the
//     whole rules-pack backlog were dealt with. A run that died, or one that
//     drained only its batch, leaves the previous pointer standing and the next
//     run diffs from there — nothing between the two is ever skipped.
//  2. It replaces only its own. Superseding is scoped to
//     (file, structural.ExtractorID), so a product ruling, an agent's reading or
//     an importer's row on the same file survives a structural pass untouched.
//  3. It never guesses. A file it could not read is `failed`: the previous
//     contribution stands, because "I did not look" is not "it is not there".

// DefaultBacklogBatch is how many files a single run will parse. It bounds the
// cost of a git hook: a pack bump or a first run on an existing graph makes
// every file re-extractable at once, and a hook that parses ten thousand files
// is a hook people uninstall. The remainder is reported, `structured` does not
// advance, and the next run takes the next batch.
const DefaultBacklogBatch = 200

// StructuralInput is one run of the phase: what changed, what to read it with,
// and how much work to do.
type StructuralInput struct {
	DomainKey string
	HeadSHA   string
	// Changed are added/modified/renamed paths (repo-relative). Files with no
	// deterministic extractor are dropped, not failed.
	Changed []string
	// Deleted are the paths that are gone (and the OLD path of a rename): their
	// structural contribution is closed exactly as a replacement closes what a
	// surviving file stopped asserting.
	Deleted []string
	// Tech selects the rule packs (manifest `tech`).
	Tech []string
	// ReadFile reads one repo-relative path. Injected so the phase can be
	// tested without a checkout; the CLI passes a reader rooted at
	// paths.GitDir(). Nil means os.ReadFile.
	ReadFile func(path string) ([]byte, error)
	// BacklogBatch caps the files parsed in one run (default
	// DefaultBacklogBatch; negative means no bound).
	BacklogBatch int
}

// StructuralResult is what one run did, and what it left undone.
type StructuralResult struct {
	RevisionID int64    `json:"revision_id"`
	Processed  int      `json:"processed"`
	Facts      int      `json:"facts"`
	Failed     []string `json:"failed,omitempty"` // parse errors — the semantic queue's input
	Unread     []string `json:"unread,omitempty"` // the bytes never arrived; nobody can read those
	Unresolved int      `json:"unresolved"`       // name lookups with 0 or >1 candidates
	Superseded int64    `json:"superseded"`       // rows no longer asserted by their file
	Backlog    int      `json:"backlog"`          // files still owed structure after this run
	Skipped    int      `json:"skipped"`          // unchanged content on the current pack
	Complete   bool     `json:"complete"`         // nothing left over → the pointer moves
}

// structuralResolveFn is the resolve step as a package variable so a test can
// make it fail on demand. A crash between "the verification phase recorded its
// revision" and "the structural phase finished" is the single failure the whole
// two-pointer design exists for, and an untested recovery path is a recovery
// path that silently stops working.
var structuralResolveFn = func(g *Graph, domainKey string, revisionID int64, opts ResolveOptions) (*ResolveExtractionsResult, error) {
	return g.ResolveExtractionsWithOptions(domainKey, revisionID, opts)
}

// StructuralRefresh re-extracts structure for the changed files and the oldest
// slice of the rules-pack backlog, then stamps metadata.structural onto the
// revision at HeadSHA. Any error before the final stamp returns with the
// revision left incomplete — the next run reuses it and redoes the work.
func (g *Graph) StructuralRefresh(in StructuralInput) (*StructuralResult, error) {
	if in.DomainKey == "" {
		return nil, errors.New("structural: domain is required")
	}
	if in.HeadSHA == "" {
		return nil, errors.New("structural: head sha is required")
	}
	readFile := in.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	batch := in.BacklogBatch
	if batch == 0 {
		batch = DefaultBacklogBatch
	}
	pack := rules.PackVersion

	res := &StructuralResult{}
	revID, err := g.structuralRevisionAt(in.DomainKey, in.HeadSHA)
	if err != nil {
		return nil, err
	}
	res.RevisionID = revID

	// Say "in progress" before touching anything: until the phase finishes,
	// the claim "structure is guaranteed at this commit" is not true.
	//
	// Unless a previous run already earned that claim at this very commit.
	// Marking it incomplete first would drop a true claim for the duration of
	// the run, and a crash would drop it for good.
	//
	// This does NOT mean a rerun can never withdraw the claim: the final stamp
	// is written from this run's own accounting, so a rerun that finds a
	// rules-pack backlog it cannot drain in one batch writes complete:false
	// and the pointer falls back — correctly, because part of the graph is now
	// known to be on rules this commit no longer stands for. What the guard
	// prevents is losing the claim to a CRASH, not to an honest answer.
	alreadyComplete, err := g.structuralAlreadyComplete(revID)
	if err != nil {
		return nil, err
	}
	if !alreadyComplete {
		if err := g.stampStructural(revID, map[string]any{"complete": false, "pack": pack}); err != nil {
			return nil, err
		}
	}

	// The backlog is the phase's own record of which files it looked at under
	// which pack — not a query over evidence rows, because a file that yields
	// zero facts has no evidence to carry a version and would be invisible
	// backlog forever.
	backlogFiles, err := g.store.FilesWithStructuralHashNotOnPack(in.DomainKey, pack, batch)
	if err != nil {
		return nil, fmt.Errorf("structural: backlog: %w", err)
	}
	// A file whose standing answer is "no answer" is retried on every run.
	// It has no content record — a failure never writes one — so nothing else
	// would ever bring it back: not the diff (it stopped changing), not the
	// pack backlog (it is not in it). The retry set is the only thing between
	// a transient read error and a file the graph forgets about permanently.
	retryFailed, retryUnread, err := g.store.StructuralFailures(in.DomainKey)
	if err != nil {
		return nil, fmt.Errorf("structural: retry set: %w", err)
	}
	targets := structuralTargets(in.Changed, backlogFiles, retryFailed, retryUnread)

	hashes := make(map[string]string, len(targets))
	var extracted []string
	deferredStillOwed := 0

	for _, file := range targets {
		content, rerr := readFile(file)
		if rerr != nil {
			// Not an emptiness and not a parse failure: a file whose bytes
			// never arrived keeps whatever it used to assert, and handing it
			// to a model would not help. It stays in the retry set instead.
			if _, err := g.store.SaveStructuralExtraction(revID, in.DomainKey, file, "failed", "", "[]",
				rerr.Error(), store.StructuralOutcomeUnreadable); err != nil {
				return nil, fmt.Errorf("structural: record failure for %s: %w", file, err)
			}
			res.Unread = append(res.Unread, file)
			continue
		}

		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])
		prevHash, prevPack, known, err := g.store.GetStructuralHash(in.DomainKey, file)
		if err != nil {
			return nil, fmt.Errorf("structural: hash of %s: %w", file, err)
		}
		// Same bytes, same rules: the previous answer is still this file's
		// answer. Reading and hashing is cheap; parsing is not.
		if known && prevHash == hash && prevPack == pack {
			res.Skipped++
			continue
		}
		// The batch bounds PARSING only — the skip above must stay reachable
		// for every target, or a big first run would never make progress
		// through the files it already finished.
		if batch > 0 && res.Processed >= batch {
			if !(known && prevPack != pack) {
				// A file already recorded on an older pack is counted by the
				// backlog query below; anything else has to be added by hand.
				deferredStillOwed++
			}
			continue
		}

		r := structural.ExtractFile(file, content, in.Tech)
		switch r.Outcome {
		case structural.Extracted:
			if _, err := g.store.SaveStructuralExtraction(revID, in.DomainKey, file, "extracted", r.FromType, r.FactsJSON, "", ""); err != nil {
				return nil, fmt.Errorf("structural: save extraction for %s: %w", file, err)
			}
			res.Facts += r.FactCount
			res.Processed++
			hashes[file] = r.ContentHash
			extracted = append(extracted, file)
		case structural.Failed, structural.Unreadable:
			msg := "structural extraction failed"
			if r.Err != nil {
				msg = r.Err.Error()
			}
			outcome := ""
			if r.Outcome == structural.Unreadable {
				outcome = store.StructuralOutcomeUnreadable
			}
			if _, err := g.store.SaveStructuralExtraction(revID, in.DomainKey, file, "failed", "", "[]", msg, outcome); err != nil {
				return nil, fmt.Errorf("structural: record failure for %s: %w", file, err)
			}
			if r.Outcome == structural.Unreadable {
				res.Unread = append(res.Unread, file)
			} else {
				res.Failed = append(res.Failed, file)
			}
			res.Processed++
		default:
			// Unsupported: not a structural file at all. structuralTargets
			// already filtered these out of Changed; a backlog record for one
			// is a leftover and deleting it stops it coming back forever.
			if err := g.store.DeleteStructuralHash(in.DomainKey, file); err != nil {
				return nil, fmt.Errorf("structural: forget %s: %w", file, err)
			}
		}
	}

	if len(extracted) > 0 {
		rr, err := structuralResolveFn(g, in.DomainKey, revID, ResolveOptions{
			Deterministic:    true,
			ExtractorID:      structural.ExtractorID,
			ExtractorVersion: pack,
			// Only this phase's own rows: the revision belongs to whoever
			// opened it, and an agent's extraction on it is not our input.
			ExtractionRole: store.StructuralExtractionRole,
			// The graph-wide derivations are a scan's job, not a commit's.
			DerivedPasses: &structuralDerivedPasses,
		})
		if err != nil {
			return nil, fmt.Errorf("structural: resolve: %w", err)
		}
		res.Unresolved = rr.UnresolvedCount

		// The replacement contract: what a file asserts now is ALL it asserts.
		for _, file := range extracted {
			keep, err := g.structuralKeep(file, revID, rr.EvidenceIDsByFile[file])
			if err != nil {
				return nil, err
			}
			n, err := g.store.SupersedeEvidenceNotIn(file, structural.ExtractorID, keep, revID)
			if err != nil {
				return nil, fmt.Errorf("structural: supersede %s: %w", file, err)
			}
			res.Superseded += n
		}
	}

	// A deleted file asserts nothing at all — the same contract with an empty
	// right-hand side.
	for _, file := range in.Deleted {
		n, err := g.store.SupersedeEvidenceNotIn(file, structural.ExtractorID, nil, revID)
		if err != nil {
			return nil, fmt.Errorf("structural: supersede deleted %s: %w", file, err)
		}
		res.Superseded += n
		if err := g.store.DeleteStructuralHash(in.DomainKey, file); err != nil {
			return nil, fmt.Errorf("structural: forget %s: %w", file, err)
		}
		// And its standing answer, or a `failed` row would keep a file that
		// cannot come back in the retry set forever.
		if err := g.store.DeleteStructuralExtraction(in.DomainKey, file); err != nil {
			return nil, fmt.Errorf("structural: forget %s: %w", file, err)
		}
	}

	if res.Superseded > 0 {
		if err := g.recalculateSupersededTrust(revID); err != nil {
			return nil, err
		}
	}

	// Only now is a file's answer applied. Recording the hash any earlier would
	// let a run that died between the parse and the graph write count as "this
	// content is already in the graph", and the file would never be looked at
	// again.
	for _, file := range extracted {
		if err := g.store.SetStructuralHash(in.DomainKey, file, hashes[file], pack, revID); err != nil {
			return nil, fmt.Errorf("structural: record %s: %w", file, err)
		}
	}

	onOldPack, err := g.store.CountStructuralHashesNotOnPack(in.DomainKey, pack)
	if err != nil {
		return nil, fmt.Errorf("structural: backlog count: %w", err)
	}
	res.Backlog = onOldPack + deferredStillOwed
	res.Complete = res.Backlog == 0

	if err := g.stampStructural(revID, map[string]any{
		"complete":   res.Complete,
		"pack":       pack,
		"processed":  res.Processed,
		"failed":     len(res.Failed),
		"unread":     len(res.Unread),
		"unresolved": res.Unresolved,
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// structuralRevisionAt finds the revision the phase's stamp belongs on.
//
// graph_revisions is UNIQUE(domain_key, git_after_sha), and by the time the
// structural phase runs the refresh's verification phase has normally already
// written the row at HEAD — so the phase almost never owns a revision. It
// stamps that row instead. A commit nothing has claimed (a hook that skipped
// verification, a test, a repo whose graph holds no evidence at all) gets a
// revision of its own, marked kind=structural so LatestRefreshRevision can
// never mistake it for a re-verification.
func (g *Graph) structuralRevisionAt(domainKey, headSHA string) (int64, error) {
	rev, err := g.store.GetRevisionBySHA(domainKey, headSHA)
	switch {
	case err == nil:
		return rev.RevisionID, nil
	case !errors.Is(err, store.ErrNotFound):
		return 0, fmt.Errorf("structural: revision at %s: %w", headSHA, err)
	}
	id, err := g.store.CreateRevision(domainKey, "", headSHA, "git_hook", "incremental", `{"kind":"structural"}`)
	if err != nil {
		return 0, fmt.Errorf("structural: create revision at %s: %w", headSHA, err)
	}
	return id, nil
}

// structuralDerivedPasses is the value ResolveOptions.DerivedPasses points at
// for every structural run: off. A package var because the option is a *bool
// (absent means on, so that no existing caller has to opt in).
var structuralDerivedPasses = false

// structuralAlreadyComplete reports whether the revision already carries a
// finished structural phase — the one case where the up-front "in progress"
// marker would destroy a true claim rather than withhold an untrue one.
func (g *Graph) structuralAlreadyComplete(revisionID int64) (bool, error) {
	rev, err := g.store.GetRevision(revisionID)
	if err != nil {
		return false, fmt.Errorf("structural: read revision %d: %w", revisionID, err)
	}
	var md struct {
		Structural struct {
			// json.Number, not bool: SQLite's JSON1 maps a JSON true to the
			// integer 1, and store.LatestStructuralRevision's query accepts
			// `= 1` — so a writer that stored 1 directly reads as complete to
			// the pointer. This guard has to agree with the pointer, or a
			// revision the graph calls complete would still have its claim
			// withdrawn here.
			Complete json.RawMessage `json:"complete"`
		} `json:"structural"`
	}
	// Unreadable metadata is not a completed phase; the marker is written and
	// the final stamp replaces whatever was there.
	if err := json.Unmarshal([]byte(rev.Metadata), &md); err != nil {
		return false, nil
	}
	switch strings.TrimSpace(string(md.Structural.Complete)) {
	case "true", "1":
		return true, nil
	}
	return false, nil
}

// stampStructural merges the phase's own key into the revision's metadata.
// UpdateRevisionMetadata merges top-level keys, so everything else on the row —
// the refresh's kind, a surface import's source — survives, while the whole
// "structural" object is replaced by this run's account of itself.
func (g *Graph) stampStructural(revisionID int64, stamp map[string]any) error {
	if err := g.store.UpdateRevisionMetadata(revisionID, map[string]any{"structural": stamp}); err != nil {
		return fmt.Errorf("structural: stamp revision %d: %w", revisionID, err)
	}
	return nil
}

// structuralKeep is the set of evidence rows this pass re-asserted for one
// file: what the resolver handed back, plus anything else it stamped with this
// revision. The second half is a belt — AddEvidence's dedup path updates an
// existing row rather than inserting one, so a row the pass genuinely
// re-observed can reach the store by a path the result map did not record, and
// superseding it would delete knowledge the file still asserts.
func (g *Graph) structuralKeep(filePath string, revisionID int64, fromResolve []int64) ([]int64, error) {
	created, err := g.store.EvidenceIDsCreatedIn(filePath, structural.ExtractorID, revisionID)
	if err != nil {
		return nil, fmt.Errorf("structural: evidence of %s: %w", filePath, err)
	}
	seen := make(map[int64]bool, len(fromResolve)+len(created))
	keep := make([]int64, 0, len(fromResolve)+len(created))
	for _, ids := range [][]int64{fromResolve, created} {
		for _, id := range ids {
			if id > 0 && !seen[id] {
				seen[id] = true
				keep = append(keep, id)
			}
		}
	}
	return keep, nil
}

// recalculateSupersededTrust recomputes trust for the nodes and edges that lost
// evidence in this revision. The resolver already recomputed everything it
// wrote evidence for; these are the ones nothing in the pass mentioned, which
// is exactly why they need it — an edge whose last observation was superseded
// must stop being current instead of keeping the score it had when the source
// still said so.
func (g *Graph) recalculateSupersededTrust(revisionID int64) error {
	nodeIDs, edgeIDs, err := g.store.EvidenceOwnersSupersededIn(revisionID)
	if err != nil {
		return fmt.Errorf("structural: superseded owners: %w", err)
	}
	for _, id := range edgeIDs {
		if err := g.RecalculateEdgeTrust(id); err != nil {
			return fmt.Errorf("structural: edge trust %d: %w", id, err)
		}
	}
	for _, id := range nodeIDs {
		if err := g.RecalculateNodeTrust(id); err != nil {
			return fmt.Errorf("structural: node trust %d: %w", id, err)
		}
	}
	return nil
}

// structuralTargets is the changed files that have a deterministic extractor,
// followed by every other list the caller owes work to (the rules-pack backlog,
// the standing failures), deduplicated with the changed files first: a file
// whose content moved deserves the batch slot more than one whose only claim is
// an older pack or a failure that has been there for weeks.
//
// Only `changed` is filtered by Supported — the other lists come from the
// phase's own records, so anything in them was structural when it was written.
func structuralTargets(changed []string, rest ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range changed {
		if f == "" || seen[f] || !structural.Supported(f) {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	for _, list := range rest {
		for _, f := range list {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// Package freshness answers one question with one report: how old is the
// knowledge an answer was built from?
//
// The graph is always behind the working tree — the useful facts are *how far*
// behind, whether anything re-verified it since, and whether the commit the
// knowledge was built from is even on the current branch. Compute reads the
// store (authoritative) and git (advisory: every git failure degrades to empty
// fields, never to an error), so a report is always renderable: as JSON for
// chronicle_freshness and /api/freshness, and as one line for the knowledge
// block appended to every query result.
package freshness

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/rules"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/store"
)

// Point is one commit the knowledge is pinned to: HEAD, the last scan, the
// last refresh, or the head of one layer.
type Point struct {
	SHA        string `json:"sha"`
	At         string `json:"at"` // revision created_at, or commit date for head
	RevisionID int64  `json:"revision_id,omitempty"`
	Files      int    `json:"files,omitempty"`  // scanned: scan_runs.extracted_files of that revision, 0 if unknown
	Nodes      int    `json:"nodes,omitempty"`  // scanned: graph_nodes count at compute time
	Source     string `json:"source,omitempty"` // ui: metadata.source
	// Branch names the branch this point was taken on: read from git for the
	// head, read back from the scan's own metadata for a scan (a commit does
	// not remember which branch was checked out when it was read). Empty when
	// nothing recorded it — a detached HEAD, or a scan written before the
	// branch was stored.
	Branch string `json:"branch,omitempty"`
	// OldRules is the structural point's backlog: files whose deterministic
	// evidence was written by an older rules pack and so still need
	// re-extraction, even though nothing in them changed.
	OldRules int `json:"old_rules,omitempty"`
	// Failed is how many files the structural phase currently has no answer
	// for because the parser could not read them, and Unread how many because
	// the bytes never arrived. Their previous contribution stands, so nothing
	// is wrong with the graph — but nothing new arrived for them either, and a
	// pointer that says "structure is guaranteed here" owes the reader both
	// numbers.
	//
	// They count STANDING rows, not the last run's tally: a file that failed
	// three commits ago is still without an answer, and reading the newest
	// stamp made that disclosure vanish the moment any other commit landed.
	Failed int `json:"failed,omitempty"`
	Unread int `json:"unread,omitempty"`
}

// Distance is how far the working tree has moved past the knowledge.
type Distance struct {
	Commits int `json:"commits"`
	Files   int `json:"files"`
	// MergeBase is set only when Status == diverged: the common ancestor and how far each side is from it.
	MergeBase      string `json:"merge_base,omitempty"`
	HeadAhead      int    `json:"head_ahead,omitempty"`
	KnowledgeAhead int    `json:"knowledge_ahead,omitempty"`
}

// Touched counts what the graph itself already marked as no longer trusted.
type Touched struct {
	NodesStale    int `json:"nodes_stale"`
	EvidenceStale int `json:"evidence_stale"`
}

// How a scan relates to the checkout being reported on.
const (
	// ScanAnswering is the scan the report speaks for — the newest one that
	// HEAD actually descends from.
	ScanAnswering = "answering"
	// ScanSkipped is a NEWER scan that cannot answer here, because HEAD does
	// not descend from it. Branch off main, scan the branch, check main back
	// out, and the branch's scan is this: real knowledge, about a line of
	// history you are not on.
	ScanSkipped = "skipped"
	// ScanSuperseded is an older scan on the answering scan's own line: usable
	// in principle, simply further back than one already chosen.
	ScanSuperseded = "superseded"
	// ScanGone is a scan whose commit git cannot find at all — rebased,
	// amended or force-pushed away.
	ScanGone = "gone"
)

// ScanEntry is one scan this graph holds, and what it is to the current
// checkout. The list exists because "which commit does this graph describe"
// has no useful answer without "and why not one of the others": a skipped
// scan that is newer than the answering one is the difference between a graph
// that is merely behind and one that was built somewhere else.
type ScanEntry struct {
	SHA        string `json:"sha"`
	At         string `json:"at,omitempty"`
	RevisionID int64  `json:"revision_id,omitempty"`
	Branch     string `json:"branch,omitempty"` // recorded at scan time; "" when unknown
	Use        string `json:"use"`              // answering | skipped | superseded | gone
	Commits    int    `json:"commits,omitempty"`  // answering only: commits between it and HEAD
}

// Report is the whole answer: one struct for the tool, the dashboard and the
// knowledge line.
type Report struct {
	Repo        string            `json:"repo"`
	Domain      string            `json:"domain"`
	Path        string            `json:"path"`       // repo dir (parent of .depbot)
	Head        *Point            `json:"head"`       // nil when git is unavailable
	Scanned     *Point            `json:"scanned"`    // nil when empty
	Structured  *Point            `json:"structured"` // nil when no structural phase ever completed
	Verified    *Point            `json:"verified"`   // nil when never refreshed
	Scans       []ScanEntry       `json:"scans,omitempty"`
	Unscanned   Distance          `json:"unscanned"`
	Touched     Touched           `json:"touched"`
	Layers      map[string]*Point `json:"layers"` // "code" (= scanned), "ui" when a surface import exists
	LastQueryAt string            `json:"last_query_at,omitempty"`
	Status      string            `json:"status"`  // empty | fresh | structured | verified | stale | diverged
	Message     string            `json:"message"` // one human line, also the knowledge line body
}

// Status values. Closed set — dashboards and the pro lane switch on these.
const (
	StatusEmpty = "empty" // nothing scanned yet
	StatusFresh = "fresh" // scanned at HEAD
	// StatusStructured: the scan is behind HEAD, but the deterministic phase
	// completed at HEAD — today's imports, routes and models are known, only
	// their meanings are as old as the scan. Between verified and fresh: it is
	// a wider claim than "the old evidence still holds" and a narrower one
	// than "everything was read at this commit".
	StatusStructured = "structured"
	StatusVerified   = "verified" // behind HEAD, but refreshed at HEAD
	StatusStale      = "stale"    // behind HEAD, not refreshed
	StatusDiverged   = "diverged" // scanned commit is not an ancestor of HEAD
)

// QueryToolNames are the read-only tools an agent calls to get an answer out
// of the graph — the ones whose results carry the knowledge block, and the
// ones whose timestamps answer "when was this graph last consulted".
// mcpserver.QueryTools is built from this list; keep it the only copy.
var QueryToolNames = []string{
	"chronicle_node_search",
	"chronicle_query_deps",
	"chronicle_query_reverse_deps",
	"chronicle_impact",
	"chronicle_query_path",
	"chronicle_subgraph",
	"chronicle_insights",
	"chronicle_review_report",
	"chronicle_wiki_federated",
	"chronicle_wiki_page",
}

// uiLayer is the non-code layer a report breaks out. "code" is the scan
// itself; the ui layer is imported separately and can be older or newer.
const uiLayer = "ui"

// Compute reads the store and git in repoDir. repo/domain are labels the
// caller knows; an empty domain resolves to the domain of the newest revision
// in the store. It returns an error only when the store fails — a missing or
// broken git leaves Head nil and the distances zero.
func Compute(repoDir, repo, domain string, s *store.Store) (*Report, error) {
	if s == nil {
		return nil, errors.New("freshness: nil store")
	}
	if repoDir == "" {
		repoDir = "."
	}

	if domain == "" {
		d, err := s.NewestRevisionDomain()
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		domain = d
	}

	r := &Report{
		Repo:   repo,
		Domain: domain,
		Path:   repoDir,
		Layers: map[string]*Point{},
		Head:   gitHead(repoDir),
	}

	scanRev, scans, err := pickScan(repoDir, domain, r.Head, s)
	if err != nil {
		return nil, err
	}
	r.Scans = scans
	// A revision that recorded no commit pins nothing: reporting it as
	// Scanned would contradict the "empty" status it still produces.
	if scanRev != nil && scanRev.GitAfterSHA != "" {
		p := &Point{
			SHA: scanRev.GitAfterSHA, At: scanRev.CreatedAt,
			RevisionID: scanRev.RevisionID, Branch: revisionBranch(scanRev),
		}
		if run, err := s.GetScanRunByRevision(scanRev.RevisionID); err == nil && run != nil {
			p.Files = run.ExtractedFiles
		}
		if n, err := s.CountNodesByStatus(domain, "active"); err == nil {
			p.Nodes = n
		}
		r.Scanned = p
		r.Layers["code"] = p
	}

	refreshRev, err := latest(s.LatestRefreshRevision(domain))
	if err != nil {
		return nil, err
	}
	if refreshRev != nil {
		r.Verified = &Point{SHA: refreshRev.GitAfterSHA, At: refreshRev.CreatedAt, RevisionID: refreshRev.RevisionID}
	}

	// The structural point moves on its own: only a phase that processed its
	// whole diff writes one (store.LatestStructuralRevision), so an
	// interrupted run leaves the previous SHA standing rather than claiming
	// coverage it did not finish.
	structRev, err := latest(s.LatestStructuralRevision(domain))
	if err != nil {
		return nil, err
	}
	if structRev != nil && structRev.GitAfterSHA != "" {
		p := &Point{SHA: structRev.GitAfterSHA, At: structRev.CreatedAt, RevisionID: structRev.RevisionID}
		// The backlog is the structural phase's OWN record of what it looked
		// at and under which pack (project_settings), not a count over
		// evidence rows: a file that yields zero facts has no evidence to
		// carry a version, and a row written before the phase existed was
		// never a pack's claim at all.
		if n, err := s.CountStructuralHashesNotOnPack(domain, rules.PackVersion); err == nil {
			p.OldRules = n
		}
		if failed, unread, err := s.StructuralFailures(domain); err == nil {
			p.Failed, p.Unread = len(failed), len(unread)
		}
		r.Structured = p
	}

	// The ui layer is found by LatestSurfaceRevision, not by metadata.layer
	// alone: an import onto a commit a scan already named rides along on that
	// row and marks itself with metadata.surface instead (see
	// store.LatestSurfaceRevision). Reading only the layer stamp made the ui
	// layer vanish from the report in exactly the commonest flow.
	surfRev, err := latest(s.LatestSurfaceRevision(domain))
	if err != nil {
		return nil, err
	}
	if surfRev != nil {
		r.Layers[uiLayer] = &Point{
			SHA:        surfRev.GitAfterSHA,
			At:         surfRev.CreatedAt,
			RevisionID: surfRev.RevisionID,
			Source:     surfaceSource(surfRev.Metadata),
		}
	}

	if n, err := s.CountNodesByStatus(domain, "stale"); err == nil {
		r.Touched.NodesStale = n
	}
	// Code evidence only: importer-owned rows (a surface extract, a declared
	// decision) are not work a rescan can clear, so counting them here would
	// report a graph as damaged when nothing is wrong with it.
	if counts, err := s.CountCodeEvidenceByStatus(domain); err == nil {
		r.Touched.EvidenceStale = counts["stale"]
	}
	if ts, err := s.LastQueryAt(QueryToolNames); err == nil {
		r.LastQueryAt = ts
	}

	r.Status = r.resolveStatus(repoDir)
	r.Message = r.body()
	return r, nil
}

// resolveStatus fills Unscanned as a side effect: the distance and the status
// come from the same three git questions (does the commit exist, is it an
// ancestor, how far).
func (r *Report) resolveStatus(repoDir string) string {
	if r.Scanned == nil || r.Scanned.SHA == "" {
		return StatusEmpty
	}
	// Without git there is nothing to compare against. Say stale rather than
	// fresh: an unverifiable claim of freshness is the one failure mode that
	// makes an agent trust a stale answer.
	if r.Head == nil {
		return StatusStale
	}
	if r.Scanned.SHA == r.Head.SHA {
		return StatusFresh
	}

	known := gitOK(repoDir, "cat-file", "-e", r.Scanned.SHA+"^{commit}")
	if !known || !gitOK(repoDir, "merge-base", "--is-ancestor", r.Scanned.SHA, "HEAD") {
		if known {
			r.Unscanned.Commits = gitCount(repoDir, r.Scanned.SHA+"..HEAD")
			r.Unscanned.Files = gitChangedFiles(repoDir, r.Scanned.SHA, "HEAD")
			if mb, ok := gitOut(repoDir, "merge-base", r.Scanned.SHA, "HEAD"); ok {
				r.Unscanned.MergeBase = mb
				r.Unscanned.HeadAhead = gitCount(repoDir, mb+"..HEAD")
				r.Unscanned.KnowledgeAhead = gitCount(repoDir, mb+".."+r.Scanned.SHA)
			}
		}
		return StatusDiverged
	}

	r.Unscanned.Commits = gitCount(repoDir, r.Scanned.SHA+"..HEAD")
	r.Unscanned.Files = gitChangedFiles(repoDir, r.Scanned.SHA, "HEAD")
	// Structure at HEAD outranks a verification at HEAD, and does not need
	// one: re-extracting the diff deterministically is a stronger statement
	// about today's commit than re-checking what was already believed.
	if r.Structured != nil && r.Structured.SHA != "" && r.Structured.SHA == r.Head.SHA {
		return StatusStructured
	}
	if r.VerifiedAfterScan() && r.Verified.SHA == r.Head.SHA && r.Unscanned.Commits > 0 {
		return StatusVerified
	}
	return StatusStale
}

// SetRepo relabels the report and rebuilds Message so the knowledge line
// matches. Callers that know a better name than the directory (the MCP tool's
// repo argument, pro's federated repo names) use it instead of writing Repo
// directly and leaving a stale Message behind.
func (r *Report) SetRepo(repo string) {
	r.Repo = repo
	r.Message = r.body()
}

// VerifiedAfterScan reports whether the last refresh actually re-verified the
// scan — i.e. it happened after it. A refresh that predates the scan is older
// knowledge about an older commit: rendering "scanned@N+5 · verified@N" would
// read as "re-verified since the scan" when the truth is the reverse, and a
// stale graph would wear the verified status.
//
// Exported because the federated line (chronicle-pro) has to answer the same
// question, and it is a rule, not a formatting detail: which pointer counts as
// a re-verification cannot have two different answers in two renderers.
func (r *Report) VerifiedAfterScan() bool {
	return r.Verified != nil && r.Verified.SHA != "" &&
		r.Scanned != nil && r.Verified.RevisionID > r.Scanned.RevisionID
}

// Line renders the one-line knowledge string used by the MCP knowledge block:
//
//	knowledge: auto scanned@22f9f92 (07.08) · verified@dd193ad · 796 unscanned commits · 118 nodes touched
//	knowledge: auto scanned@8df170b · current
//	knowledge: auto · no scan yet
func (r *Report) Line() string {
	body := r.Message
	if body == "" {
		body = r.body()
	}
	return "knowledge: " + body
}

// body builds the human line. Kept separate from Line so a Report that
// travelled through JSON (or one a federated caller re-labelled) still
// renders, and so Compute can store the same text in Message.
func (r *Report) body() string {
	name := r.Repo
	if name == "" {
		name = r.Domain
	}

	if r.Scanned == nil || r.Scanned.SHA == "" {
		if name == "" {
			return "no scan yet"
		}
		return name + " · no scan yet"
	}

	head := "scanned@" + short(r.Scanned.SHA)
	if name != "" {
		head = name + " " + head
	}
	if r.Status != StatusFresh {
		if d := shortDate(r.Scanned.At); d != "" {
			head += " (" + d + ")"
		}
	}
	// The structural point rides with the scan rather than in its own segment:
	// the two together are the answer to "what does this graph know about
	// today's code", and showing one without the other is the lie §3 names.
	// Unless they are the same commit — then it is one fact printed twice, and
	// the line exists to be read at a glance.
	if r.Structured != nil && r.Structured.SHA != "" && r.Structured.SHA != r.Scanned.SHA {
		head += " structured@" + short(r.Structured.SHA)
	}
	parts := []string{head}

	if r.VerifiedAfterScan() && r.Verified.SHA != r.Scanned.SHA {
		parts = append(parts, "verified@"+short(r.Verified.SHA))
	}

	switch r.Status {
	case StatusFresh:
		parts = append(parts, "current")
	case StatusDiverged:
		d := "diverged"
		if r.Unscanned.MergeBase != "" {
			d += fmt.Sprintf(" from %s (HEAD +%d, knowledge +%d)",
				short(r.Unscanned.MergeBase), r.Unscanned.HeadAhead, r.Unscanned.KnowledgeAhead)
		}
		parts = append(parts, d)
	default:
		switch {
		case r.Unscanned.Commits > 0:
			parts = append(parts, plural(r.Unscanned.Commits, "unscanned commit", "unscanned commits"))
		case r.Head == nil:
			parts = append(parts, "HEAD unknown (no git)")
		}
	}

	// A newer scan that cannot answer here is not a detail. The agent is
	// being handed an older scan on purpose, and saying only "3 unscanned
	// commits" would let it read the graph as merely behind when the fresher
	// knowledge it might expect is real, just about another branch.
	if n, br := r.skippedScans(); n > 0 {
		skipped := plural(n, "newer scan", "newer scans")
		if br != "" {
			skipped += " on " + br
		}
		parts = append(parts, skipped+" not on this branch")
	}

	if r.Structured != nil && r.Structured.OldRules > 0 {
		parts = append(parts, plural(r.Structured.OldRules, "file on old rules", "files on old rules"))
	}
	if r.Structured != nil && r.Structured.Failed > 0 {
		parts = append(parts, plural(r.Structured.Failed, "file failed to parse", "files failed to parse"))
	}
	if r.Structured != nil && r.Structured.Unread > 0 {
		parts = append(parts, plural(r.Structured.Unread, "file not read", "files not read"))
	}
	if r.Touched.NodesStale > 0 {
		parts = append(parts, plural(r.Touched.NodesStale, "node touched", "nodes touched"))
	}
	return strings.Join(parts, " · ")
}

// ---------------------------------------------------------------------------
// git — advisory. Every helper answers "unknown" instead of failing, because a
// missing git must degrade the report, not break the tool that carries it.
// ---------------------------------------------------------------------------

func gitOut(repoDir string, args ...string) (string, bool) {
	out, err := gitutil.Run(repoDir, args...)
	if err != nil {
		return "", false
	}
	return out, true
}

func gitOK(repoDir string, args ...string) bool {
	return gitutil.OK(repoDir, args...)
}

func gitHead(repoDir string) *Point {
	sha, ok := gitOut(repoDir, "rev-parse", "HEAD")
	if !ok || sha == "" {
		return nil
	}
	p := &Point{SHA: sha}
	if branch, ok := gitOut(repoDir, "rev-parse", "--abbrev-ref", "HEAD"); ok {
		p.Branch = branch
	}
	if at, ok := gitOut(repoDir, "log", "-1", "--format=%cI", "HEAD"); ok {
		p.At = at
	}
	return p
}

func gitCount(repoDir, revRange string) int {
	out, ok := gitOut(repoDir, "rev-list", "--count", revRange)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0
	}
	return n
}

func gitChangedFiles(repoDir, from, to string) int {
	out, ok := gitOut(repoDir, "diff", "--name-only", from, to)
	if !ok || out == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

// ---------------------------------------------------------------------------
// formatting
// ---------------------------------------------------------------------------

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// shortDate renders an ISO timestamp as DD.MM — the form the knowledge line
// uses. Anything it cannot parse renders as nothing rather than as noise.
func shortDate(ts string) string {
	if len(ts) < 10 {
		return ""
	}
	y, m, d := ts[0:4], ts[5:7], ts[8:10]
	if ts[4] != '-' || ts[7] != '-' {
		return ""
	}
	for _, part := range []string{y, m, d} {
		if _, err := strconv.Atoi(part); err != nil {
			return ""
		}
	}
	return d + "." + m
}

func plural(n int, one, many string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}

// latest maps ErrNotFound to (nil, nil): "no such revision" is an answer, not
// a failure. Any other store error propagates.
func latest(rev *store.Revision, err error) (*store.Revision, error) {
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rev, nil
}

// surfaceSource reads the extract's path from either metadata shape: top-level
// "source" on an import's own revision, nested under "surface" when it rode
// along on a scan's.
func surfaceSource(raw string) string {
	if v := metadataString(raw, "source"); v != "" {
		return v
	}
	var m struct {
		Surface struct {
			Source string `json:"source"`
		} `json:"surface"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	return m.Surface.Source
}

func metadataString(raw, key string) string {
	if raw == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// maxScanCandidates bounds how far back pickScan will look for a scan the
// current HEAD descends from. A graph accumulates a scan per rescan, and
// walking all of them would mean a git call per row on every knowledge line;
// beyond a handful of branch switches the honest answer is "rescan", not a
// scan from months ago.
const maxScanCandidates = 12

// pickScan chooses the scan that answers for this checkout, and describes the
// others.
//
// The rule is the nearest USABLE scan, not the newest one: a scan can only
// speak for a working tree whose HEAD descends from it, because everything it
// says about a file is a claim about that file at that commit. Taking the
// newest row unconditionally — which is what LatestScanRevision alone does —
// means that scanning a feature branch and then checking main back out leaves
// the graph answering from the feature branch, while a perfectly usable scan
// of main sits one row below it.
//
// Without git there is nothing to test ancestry against, so the newest scan
// stands and the list says nothing it cannot support. When no scan is an
// ancestor the newest one is still returned — resolveStatus reports diverged
// off it — but every entry is marked for what it is, so the caller can say
// "no usable scan" rather than quietly answering from another branch.
func pickScan(repoDir, domain string, head *Point, s *store.Store) (*store.Revision, []ScanEntry, error) {
	revs, err := s.ListScanRevisions(domain, maxScanCandidates)
	if err != nil {
		return nil, nil, err
	}
	if len(revs) == 0 {
		return nil, nil, nil
	}

	newest := revs[0]
	if head == nil || head.SHA == "" {
		return newest, nil, nil
	}

	var chosen *store.Revision
	entries := make([]ScanEntry, 0, len(revs))
	for _, rev := range revs {
		e := ScanEntry{
			SHA: rev.GitAfterSHA, At: rev.CreatedAt,
			RevisionID: rev.RevisionID, Branch: revisionBranch(rev),
		}
		switch {
		case rev.GitAfterSHA == "":
			// A revision that named no commit pins nothing; it is not a
			// position in history and cannot be chosen or compared.
			continue
		case !gitOK(repoDir, "cat-file", "-e", rev.GitAfterSHA+"^{commit}"):
			e.Use = ScanGone
		case chosen != nil:
			e.Use = ScanSuperseded
		case rev.GitAfterSHA == head.SHA ||
			gitOK(repoDir, "merge-base", "--is-ancestor", rev.GitAfterSHA, "HEAD"):
			e.Use = ScanAnswering
			e.Commits = gitCount(repoDir, rev.GitAfterSHA+"..HEAD")
			chosen = rev
		default:
			e.Use = ScanSkipped
		}
		entries = append(entries, e)
	}

	if chosen == nil {
		return newest, entries, nil
	}
	return chosen, entries, nil
}

// skippedScans counts the scans newer than the answering one that HEAD does
// not descend from, and names their branch when they all share one — "on feat"
// is worth saying, "on feat, main, feat" is not.
func (r *Report) skippedScans() (int, string) {
	n, branch := 0, ""
	for _, e := range r.Scans {
		if e.Use != ScanSkipped {
			continue
		}
		n++
		switch {
		case n == 1:
			branch = e.Branch
		case branch != e.Branch:
			branch = ""
		}
	}
	return n, branch
}

// revisionBranch reads metadata.branch off a revision, defensively: metadata
// we cannot parse simply names no branch.
func revisionBranch(rev *store.Revision) string {
	if rev == nil {
		return ""
	}
	var md struct {
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(rev.Metadata), &md); err != nil {
		return ""
	}
	return md.Branch
}

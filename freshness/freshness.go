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
	Branch     string `json:"branch,omitempty"` // head only
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

// Report is the whole answer: one struct for the tool, the dashboard and the
// knowledge line.
type Report struct {
	Repo        string            `json:"repo"`
	Domain      string            `json:"domain"`
	Path        string            `json:"path"`     // repo dir (parent of .depbot)
	Head        *Point            `json:"head"`     // nil when git is unavailable
	Scanned     *Point            `json:"scanned"`  // nil when empty
	Verified    *Point            `json:"verified"` // nil when never refreshed
	Unscanned   Distance          `json:"unscanned"`
	Touched     Touched           `json:"touched"`
	Layers      map[string]*Point `json:"layers"` // "code" (= scanned), "ui" when a surface import exists
	LastQueryAt string            `json:"last_query_at,omitempty"`
	Status      string            `json:"status"`  // empty | fresh | verified | stale | diverged
	Message     string            `json:"message"` // one human line, also the knowledge line body
}

// Status values. Closed set — dashboards and the pro lane switch on these.
const (
	StatusEmpty    = "empty"    // nothing scanned yet
	StatusFresh    = "fresh"    // scanned at HEAD
	StatusVerified = "verified" // behind HEAD, but refreshed at HEAD
	StatusStale    = "stale"    // behind HEAD, not refreshed
	StatusDiverged = "diverged" // scanned commit is not an ancestor of HEAD
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

	scanRev, err := latest(s.LatestScanRevision(domain))
	if err != nil {
		return nil, err
	}
	// A revision that recorded no commit pins nothing: reporting it as
	// Scanned would contradict the "empty" status it still produces.
	if scanRev != nil && scanRev.GitAfterSHA != "" {
		p := &Point{SHA: scanRev.GitAfterSHA, At: scanRev.CreatedAt, RevisionID: scanRev.RevisionID}
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
	if r.verifiedAfterScan() && r.Verified.SHA == r.Head.SHA && r.Unscanned.Commits > 0 {
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

// verifiedAfterScan reports whether the last refresh actually re-verified the
// scan — i.e. it happened after it. A refresh that predates the scan is older
// knowledge about an older commit: rendering "scanned@N+5 · verified@N" would
// read as "re-verified since the scan" when the truth is the reverse, and a
// stale graph would wear the verified status.
func (r *Report) verifiedAfterScan() bool {
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
	parts := []string{head}

	if r.verifiedAfterScan() && r.Verified.SHA != r.Scanned.SHA {
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

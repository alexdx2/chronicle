package freshness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/structural"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/store"
)

// Upkeep answers a different question from Report. Report says how old the
// knowledge is; Upkeep says whether anything is keeping it current, and how
// much of the repo it ever reached.
//
// The two are separate calls on purpose. Report is computed on every query
// answer, every hook fire and every dashboard poll, so it may only read the
// database. Upkeep walks HEAD's tree and reads files off disk — cheap for a
// person looking at a panel, far too expensive to hang off every answer.
//
// A stale graph and an unattended one look identical from a distance and need
// opposite responses: the first wants a scan, the second wants the hook that
// would have made the scan unnecessary.
type Upkeep struct {
	Repo   string `json:"repo"`
	Domain string `json:"domain"`
	Path   string `json:"path"`

	// CommitHook is what keeps the graph moving with the code: a git
	// post-commit hook running `chronicle refresh`. Without it nothing updates
	// the graph until somebody remembers to.
	CommitHook Hook `json:"commit_hook"`
	// AgentHook is what keeps the agent honest: the PreToolUse nudge that
	// reports the graph's freshness before a tool call leans on it.
	AgentHook Hook `json:"agent_hook"`

	Coverage Coverage      `json:"coverage"`
	Scan     *ScanProgress `json:"scan,omitempty"`

	Status  string `json:"status"`
	Message string `json:"message"`
}

// Hook is one piece of the upkeep machinery and, when it is missing, the exact
// command that installs it — a panel that reports a gap without naming the fix
// makes the reader go looking.
type Hook struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Fix       string `json:"fix,omitempty"`
}

// Coverage is how much of what HEAD actually contains the graph has ever read.
//
// Supported counts only files Chronicle can extract from, because a repo's
// README and lockfiles are not gaps. Known counts those the graph holds
// evidence for: a file with no evidence has never been read by anything, which
// is invisible in a freshness report — the graph can be perfectly current about
// the half of the repo it happens to know.
type Coverage struct {
	Supported int `json:"supported"`
	Known     int `json:"known"`
	// Missing is Supported minus Known, named so a reader does not have to do
	// the subtraction to find the number they care about.
	Missing int `json:"missing"`
	// Percent is Known/Supported as a whole number, 100 when there is nothing
	// supported to cover.
	Percent int `json:"percent"`
}

// ScanProgress is an agent scan that has not finished. Present only while one
// is open, so a panel can show movement instead of a number that will not
// change until somebody acts.
type ScanProgress struct {
	Phase     string `json:"phase"`
	Status    string `json:"status"`
	Total     int    `json:"total_files"`
	Extracted int    `json:"extracted_files"`
	Percent   int    `json:"percent"`
	StartedAt string `json:"started_at,omitempty"`
}

// Upkeep status values. Closed set, like Report's.
const (
	// UpkeepUnattended: no commit hook. Nothing moves this graph when the code
	// moves, so however fresh it is today is an accident of when somebody last
	// ran a scan by hand.
	UpkeepUnattended = "unattended"
	// UpkeepPartial: the machinery is in place but does not reach everything —
	// supported files the graph has never read.
	UpkeepPartial = "partial"
	// UpkeepOK: a commit hook is installed and every supported file at HEAD is
	// known.
	UpkeepOK = "ok"
)

// AgentHookMarker identifies Chronicle's PreToolUse entry inside a
// .claude/settings.json hook command, whatever absolute binary path was baked
// into it at install time.
const AgentHookMarker = "hook fire"

// CommitHookMarker identifies Chronicle's git post-commit script. It is a
// comment rather than the command, so the script can change without the
// detector losing sight of it — and so a hand-written hook that happens to call
// chronicle is not mistaken for ours.
const CommitHookMarker = "# Chronicle:"

// UpkeepOptions narrows what counts as this domain's file.
type UpkeepOptions struct {
	// InScope decides whether a path at HEAD belongs to this domain. nil means
	// every supported file does, which is the right answer for a repo whose
	// manifest declares no scan globs.
	InScope func(path string) bool
}

// ComputeUpkeep reports whether this repo's knowledge is being maintained.
//
// Everything is best-effort per field: a repo without git still reports its
// hooks, a repo whose settings file is unreadable still reports its coverage.
// A panel that goes blank because one of four answers was unavailable is worse
// than one that shows three.
func ComputeUpkeep(repoDir, repo, domain string, s *store.Store, o UpkeepOptions) (*Upkeep, error) {
	if s == nil {
		return nil, fmt.Errorf("upkeep: no store")
	}
	if domain == "" {
		if d, err := s.NewestRevisionDomain(); err == nil {
			domain = d
		}
	}
	u := &Upkeep{Repo: repo, Domain: domain, Path: repoDir}
	if u.Repo == "" {
		u.Repo = repoLabel(repoDir)
	}

	u.AgentHook = detectAgentHook(repoDir)
	u.CommitHook = detectCommitHook(repoDir)
	u.Coverage = computeCoverage(repoDir, s, o.InScope)
	u.Scan = activeScan(s, domain)
	u.Status, u.Message = u.verdict()
	return u, nil
}

// repoLabel names a repo the way a human would: the directory's own name.
func repoLabel(repoDir string) string {
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		abs = repoDir
	}
	return filepath.Base(abs)
}

func detectAgentHook(repoDir string) Hook {
	path := filepath.Join(repoDir, ".claude", "settings.json")
	h := Hook{Path: path, Fix: "chronicle hook install"}
	b, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	h.Installed = strings.Contains(string(b), AgentHookMarker)
	return h
}

func detectCommitHook(repoDir string) Hook {
	h := Hook{Fix: "chronicle hook install --git"}
	// --git-path hooks, not --git-dir: it resolves to the directory git
	// actually looks in, which is the main checkout's inside a linked worktree
	// and the configured one when core.hooksPath is set. Looking anywhere else
	// reports "not installed" for a hook that is installed and running.
	hooks, err := gitutil.Run(repoDir, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return h
	}
	hooks = strings.TrimSpace(hooks)
	if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(repoDir, hooks)
	}
	path := filepath.Join(hooks, "post-commit")
	h.Path = path
	b, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	h.Installed = strings.Contains(string(b), CommitHookMarker)
	return h
}

// computeCoverage asks git what HEAD holds and the graph what it has read.
func computeCoverage(repoDir string, s *store.Store, inScope func(string) bool) Coverage {
	c := Coverage{Percent: 100}
	out, ok := gitOut(repoDir, "-c", "core.quotePath=false", "ls-tree", "-r", "--name-only", "-z", "HEAD")
	if !ok {
		return c
	}
	var supported []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || !structural.Supported(p) {
			continue
		}
		if inScope != nil && !inScope(p) {
			continue
		}
		supported = append(supported, p)
	}
	c.Supported = len(supported)
	if c.Supported == 0 {
		return c
	}
	known, err := s.KnownFilePaths(supported)
	if err != nil {
		return c
	}
	c.Known = len(known)
	c.Missing = c.Supported - c.Known
	c.Percent = c.Known * 100 / c.Supported
	return c
}

func activeScan(s *store.Store, domain string) *ScanProgress {
	run, err := s.GetActiveScanRun(domain)
	if err != nil || run == nil {
		return nil
	}
	p := &ScanProgress{
		Phase:     run.Phase,
		Status:    run.Status,
		Total:     run.TotalFiles,
		Extracted: run.ExtractedFiles,
		StartedAt: run.CreatedAt,
	}
	if run.TotalFiles > 0 {
		p.Percent = run.ExtractedFiles * 100 / run.TotalFiles
	}
	return p
}

// verdict is the one line a panel shows and the status it colours by.
func (u *Upkeep) verdict() (string, string) {
	var parts []string
	status := UpkeepOK

	switch {
	case !u.CommitHook.Installed:
		status = UpkeepUnattended
		parts = append(parts, "no commit hook — nothing updates the graph when code changes")
	case u.Coverage.Missing > 0:
		status = UpkeepPartial
		parts = append(parts, plural(u.Coverage.Missing, "supported file never read", "supported files never read"))
	default:
		parts = append(parts, fmt.Sprintf("kept current, %d/%d files covered", u.Coverage.Known, u.Coverage.Supported))
	}

	if !u.AgentHook.Installed {
		parts = append(parts, "no agent nudge — answers do not say how old they are")
		if status == UpkeepOK {
			status = UpkeepPartial
		}
	}
	if u.Scan != nil {
		parts = append(parts, fmt.Sprintf("scan in progress: %s, %d/%d files",
			u.Scan.Phase, u.Scan.Extracted, u.Scan.Total))
	}
	return status, strings.Join(parts, " · ")
}

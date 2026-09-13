package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/rules"
	"github.com/alexdx2/chronicle-core/extract/structural"
	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/spf13/cobra"
)

// refreshExtensions are the file types whose evidence the deterministic refresh
// can re-verify. Anything outside this set is left to a full scan.
var refreshExtensions = []string{".ts", ".tsx", ".js", ".jsx", ".cs", ".prisma", ".graphql", ".proto", ".go", ".py"}

// refreshOutput is what one `chronicle refresh` did, in both phases. The
// verification result is embedded so its JSON shape is unchanged for anything
// already reading it; the structural phase adds one key.
type refreshOutput struct {
	*graph.RefreshResult
	Structural      *graph.StructuralResult `json:"structural,omitempty"`
	StructuralError string                  `json:"structural_error,omitempty"`
}

// structuralFn is phase 2 as a package variable so a test can make it fail
// where the real thing would crash. The whole two-pointer design exists for
// that one moment — verification recorded, structure not — and an untested
// recovery path is a recovery path that silently stops working.
var structuralFn = func(g *graph.Graph, in graph.StructuralInput) (*graph.StructuralResult, error) {
	return g.StructuralRefresh(in)
}

func newRefreshCmd() *cobra.Command {
	var quiet bool
	var noStructural bool
	var structuralBatch int
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Zero-token structural freshness — re-verify evidence and re-extract structure for git-changed files",
		Long: `Runs two phases against the working tree, neither of which calls a model.

Phase 1 (verification) diffs against the last scan or refresh, re-verifies
structural evidence on changed files mechanically, stale-marks evidence on
deleted files, and reports which files still need an agent rescan.

Phase 2 (structure) diffs against its OWN pointer — the last commit whose
structural extraction completed — re-extracts imports, routes, models and calls
for the files that changed since, and replaces what those files no longer say.
The two pointers move independently, so a phase that fails costs only its own
claim. Use --no-structural to run phase 1 alone.

Safe to run from a git post-commit hook (see 'chronicle hook install --git').`,
		Run: func(cmd *cobra.Command, args []string) {
			runRefresh(quiet, noStructural, structuralBatch)
		},
	}
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (for git hooks)")
	cmd.Flags().BoolVar(&noStructural, "no-structural", false, "Skip phase 2 (deterministic structural re-extraction)")
	cmd.Flags().IntVar(&structuralBatch, "structural-batch", 0,
		fmt.Sprintf("Files phase 2 parses per run (default %d; -1 = no bound). Raise it to drain a first pass or a rules-pack backlog in one go, away from a hook.", graph.DefaultBacklogBatch))
	return cmd
}

// runRefresh is the command body, extracted so tests can drive both phases
// without a process.
func runRefresh(quiet, noStructural bool, structuralBatch int) {
	if notice, linked := linkedWorktreeRefusal(); linked {
		// A commit in any worktree runs the MAIN checkout's post-commit hook,
		// so without this a feature branch writes refresh revisions — at its
		// own tip — into main's graph. Both phases are refused: phase 2 writes
		// more than phase 1 does, not less.
		refuseWrite(notice, quiet)
		return
	}

	g := openGraph()
	defer g.Store().Close()

	rev, err := refreshBase(g.Store(), repoDirForGit())
	if err != nil {
		outputError(err)
	}
	head, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		outputError(fmt.Errorf("not a git repository or no HEAD: %w", err))
	}
	head = strings.TrimSpace(head)

	out := &refreshOutput{}
	var quietLine, humanLine string
	out.RefreshResult, quietLine, humanLine, err = verifyPhase(g, rev.DomainKey, rev.GitAfterSHA, head)
	if err != nil {
		outputError(err)
	}
	if out.RefreshResult == nil {
		// Embedding a nil pointer is a marshalling panic, and a hook that
		// panics looks exactly like a broken install.
		out.RefreshResult = &graph.RefreshResult{HeadSHA: head}
	}

	// Phase 2 never undoes phase 1: whatever it does or fails to do, the
	// verification above is already recorded.
	var structuralErr error
	if !noStructural {
		out.Structural, structuralErr = structuralPhase(g, rev.DomainKey, head, structuralBatch)
		if structuralErr != nil {
			out.StructuralError = structuralErr.Error()
		}
	}

	switch {
	case quiet:
		// A hook must not talk unless it has something to say, and must not
		// fail the commit it is attached to.
		if quietLine != "" {
			fmt.Println(quietLine)
		}
		if line := structuralQuietLine(out.Structural); line != "" {
			fmt.Println(line)
		}
	case humanLine != "":
		fmt.Println(humanLine)
		if line := structuralQuietLine(out.Structural); line != "" {
			fmt.Println(line)
		}
		if structuralErr != nil {
			// This path prints a sentence, not the JSON that would have
			// carried structural_error — so the failure has to be said out
			// loud, or phase 2 fails invisibly behind "Graph is current".
			fmt.Fprintln(os.Stderr, "chronicle refresh: structural phase failed: "+structuralErr.Error())
		}
	default:
		outputJSON(out)
	}
	if structuralErr != nil && !quiet {
		os.Exit(1)
	}
}

// verifyPhase is the original refresh, unchanged in behaviour: it returns what
// to print rather than printing, so phase 2 can add to the same output.
//
// quietLine is what a hook says (empty = stay silent), humanLine is the one
// sentence the no-op case prints instead of JSON, and a non-empty one means the
// verification phase has nothing JSON-worthy to report.
func verifyPhase(g *graph.Graph, domainKey, base, head string) (*graph.RefreshResult, string, string, error) {
	touchedAll, err := gitDiffFiles(base, "d") // added/copied/modified/renamed/type-changed
	if err != nil {
		return nil, "", "", err
	}
	deletedAll, err := gitDiffFiles(base, "D") // deleted only
	if err != nil {
		return nil, "", "", err
	}
	changed := filterRefreshable(touchedAll)
	deleted := filterRefreshable(deletedAll)

	if len(changed) == 0 && len(deleted) == 0 {
		// Nothing this phase can check mechanically — and two very different
		// reasons why, which must not produce the same answer.
		//
		// If NO file the graph holds evidence for changed, the graph really is
		// still good at HEAD, and only a revision recorded there says so:
		// without one a docs-only commit ages the repo into "stale, N commits
		// behind" for changes that cannot have invalidated anything.
		//
		// But if a KNOWN file changed in a language refreshExtensions does not
		// cover — Ruby, PHP, Java — stamping verified@HEAD would be the graph
		// claiming to have checked code it never read. In a repo written
		// entirely in such a language that is every single commit. Those files
		// are reported as pending instead, which is the honest status: an agent
		// has to look.
		known, err := g.Store().KnownFilePaths(append(append([]string{}, touchedAll...), deletedAll...))
		if err != nil {
			return nil, "", "", err
		}
		res := &graph.RefreshResult{
			HeadSHA:      head,
			ChangedFiles: len(touchedAll),
			DeletedFiles: len(deletedAll),
		}
		if len(known) > 0 {
			res.PendingSemantic = known
			return res, fmt.Sprintf("chronicle refresh: %d file(s) the graph knows changed and need an agent rescan: %s",
				len(known), strings.Join(known, ", ")), "", nil
		}
		if head != base {
			if _, err := g.RecordRefreshNoop(domainKey, head); err != nil {
				return nil, "", "", err
			}
		}
		return res, "", "Graph is current — no refreshable changes since last scan.", nil
	}

	res, err := g.RefreshFromDiff(domainKey, head, changed, deleted)
	if err != nil {
		return nil, "", "", err
	}
	line := ""
	if len(res.PendingSemantic) > 0 {
		line = fmt.Sprintf("chronicle refresh: %d files re-verified, %d need rescan",
			res.ChangedFiles, len(res.PendingSemantic))
	}
	return res, line, "", nil
}

// structuralPhase re-extracts the structure of everything that changed since
// the structural pointer (not since the verification one — they move apart on
// purpose) and hands graph.StructuralRefresh the files and the reader.
func structuralPhase(g *graph.Graph, domainKey, head string, batch int) (*graph.StructuralResult, error) {
	gitDir := repoDirForGit()
	in := graph.StructuralInput{
		DomainKey:    domainKey,
		HeadSHA:      head,
		Tech:         manifestTech(),
		BacklogBatch: batch,
		// The phase diffs COMMITS, so it reads commits. Reading the working
		// tree would put structure in the graph that no commit contains —
		// a half-staged file, an editor buffer — attributed to the commit
		// that does not contain it, and record its bytes as already seen.
		ReadFile: func(rel string) ([]byte, error) {
			return gitdiff.Show(gitDir, head, rel)
		},
	}

	base, sweep, forgetAfter := structuralBase(g.Store(), gitDir, domainKey)
	if forgetAfter > 0 {
		// The pointer was earned on a branch this checkout cannot reach; the
		// content records those runs wrote go with it.
		if _, err := g.Store().DeleteStructuralHashesAfter(domainKey, forgetAfter); err != nil {
			return nil, err
		}
	}
	if sweep {
		// No structural phase has ever completed here, so there is no baseline
		// to diff against: every supported file is owed structure. Bounded per
		// run by graph.DefaultBacklogBatch — the remainder is reported and the
		// pointer stays put until it is drained.
		files, err := supportedFilesAtHead(gitDir, domainKey)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, nil
		}
		in.Changed = files
		return structuralFn(g, in)
	}

	if base == head {
		// The pointer is at HEAD, so the diff is empty — but a rules-pack bump
		// makes files re-extractable without anyone committing anything, and
		// gating on "has the diff moved" would leave that backlog sitting
		// there until somebody happened to commit.
		backlog, err := g.Store().CountStructuralHashesNotOnPack(domainKey, rules.PackVersion)
		if err != nil {
			return nil, err
		}
		if backlog == 0 {
			return nil, nil
		}
		return structuralFn(g, in)
	}
	files, err := gitdiff.ChangedFiles(gitDir, base, head)
	if err != nil {
		return nil, fmt.Errorf("structural diff (base %s): %w", shortSHA(base), err)
	}
	for _, f := range files {
		switch f.Status {
		case "D":
			in.Deleted = append(in.Deleted, f.Path)
		case "R":
			// A rename is both: the new path has to be read, and the old one
			// stops asserting anything at all.
			in.Changed = append(in.Changed, f.Path)
			if f.OldPath != "" {
				in.Deleted = append(in.Deleted, f.OldPath)
			}
		default:
			in.Changed = append(in.Changed, f.Path)
		}
	}
	if len(in.Changed) == 0 && len(in.Deleted) == 0 {
		// Nothing structural in the diff — but the pointer still has to reach
		// HEAD, or a run of docs-only commits leaves the graph looking as if
		// nobody had structured them.
		in.Changed = nil
	}
	return structuralFn(g, in)
}

// structuralBase picks the commit phase 2 diffs from, and whether it has to
// sweep the whole repo instead.
//
// Its own pointer first: the newest COMPLETED structural phase, which is the
// only commit up to which structure is guaranteed. A pointer this checkout
// cannot reach (built on a branch that was never merged) is unusable, and the
// scan is the fallback — a scan read those files, so diffing from it re-reads
// only what changed since. With neither, nothing structural has ever been
// established here and the answer is the whole repo.
// forgetAfter, when non-zero, is the revision past which this domain's content
// records have to be forgotten before anything else happens: they were written
// by structural runs on a branch this history does not contain.
func structuralBase(s *store.Store, repoDir, domainKey string) (base string, sweep bool, forgetAfter int64) {
	rev, err := revOrNil(s.LatestStructuralRevision(domainKey))
	if err == nil && rev != nil && rev.GitAfterSHA != "" {
		if isAncestorOfHEAD(repoDir, rev.GitAfterSHA) {
			return rev.GitAfterSHA, false, 0
		}
		if scan, err := revOrNil(s.LatestScanRevision(domainKey)); err == nil && scan != nil &&
			scan.GitAfterSHA != "" && isAncestorOfHEAD(repoDir, scan.GitAfterSHA) {
			return scan.GitAfterSHA, false, scan.RevisionID
		}
	}
	return "", true, 0
}

// supportedFilesAtHead is every file in HEAD's tree with a deterministic
// extractor, narrowed to the domain's own scan.include/exclude when the
// manifest defines them — the sweep must not claim files another domain owns.
//
// HEAD's tree, not the index: the phase reads committed blobs, and a file that
// is staged but never committed has nothing at HEAD to read.
func supportedFilesAtHead(gitDir, domainKey string) ([]string, error) {
	out, err := gitutil.Run(gitDir, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("structural: git ls-tree HEAD: %w", err)
	}
	inScope := domainScopeFilter(domainKey)
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !structural.Supported(line) || !inScope(line) {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

// domainScopeFilter reads the domain's scan globs out of the manifest. A
// manifest that does not mention this domain says nothing about which files are
// its own, so everything supported is in scope — narrowing by another domain's
// patterns would silently leave files unstructured forever.
func domainScopeFilter(domainKey string) func(string) bool {
	all := func(string) bool { return true }
	m, err := manifest.LoadFile(manifestPath)
	if err != nil {
		return all
	}
	for _, d := range m.Domains {
		if d.Key != domainKey && d.Name != domainKey {
			continue
		}
		if len(d.Scan.Include) == 0 {
			return all
		}
		include, exclude := d.Scan.Include, d.Scan.Exclude
		return func(path string) bool {
			for _, p := range exclude {
				if manifest.MatchGlob(path, p) {
					return false
				}
			}
			for _, p := range include {
				if manifest.MatchGlob(path, p) {
					return true
				}
			}
			return false
		}
	}
	return all
}

// manifestTech is the rule packs the structural extractor applies. A missing or
// broken manifest is not an error here: the packs are an enrichment, and a
// refresh that refused to run without one would break every repo that has not
// filled it in.
func manifestTech() []string {
	m, err := manifest.LoadFile(manifestPath)
	if err != nil {
		return nil
	}
	return m.Tech
}

// structuralQuietLine is the one line a hook prints — only when the phase
// actually did something, so an ordinary commit stays silent.
func structuralQuietLine(res *graph.StructuralResult) string {
	// Superseded counts too: a commit that only DELETED files parses nothing
	// and still changed what the graph asserts, which is exactly the change a
	// reader would want to hear about.
	if res == nil || (res.Processed == 0 && res.Superseded == 0) {
		return ""
	}
	return fmt.Sprintf("chronicle refresh: structure %d files (%d failed, %d unresolved, %d remaining)",
		res.Processed, len(res.Failed), res.Unresolved, res.Backlog)
}

// refreshBase picks the commit the VERIFICATION phase diffs from: the newer of
// the last scan and the last refresh, and never a layer import or the
// structural phase's own revision (LatestRefreshRevision excludes those).
//
// The two rules it enforces are the two ways the naive "newest revision of any
// kind" answer goes wrong.
//
// A surface import is named by the commit the EXTRACT was made at — a product
// generates it whenever it likes, on whatever branch it likes — so it says
// nothing about which commit the code knowledge came from. Diffing from it
// re-reads files nobody touched: measured live in okeep/auto, 72 files
// stale-marked where the true base gives 1.
//
// And a base must actually be on this branch. A commit that is not an ancestor
// of HEAD produces a diff of the symmetric difference — every file either side
// touched — which reads as "the world changed" rather than as "I cannot answer
// that". The scan is the fallback, and when even that is off-branch the answer
// is a refusal, not a guess.
func refreshBase(s *store.Store, repoDir string) (*store.Revision, error) {
	scanRev, err := revOrNil(s.LatestScanRevision(""))
	if err != nil {
		return nil, err
	}
	refreshRev, err := revOrNil(s.LatestRefreshRevision(""))
	if err != nil {
		return nil, err
	}
	if scanRev == nil {
		return nil, errors.New("no prior scan found — run a full scan before refresh")
	}

	base := scanRev
	if refreshRev != nil && refreshRev.RevisionID > base.RevisionID && refreshRev.GitAfterSHA != "" {
		base = refreshRev
	}
	if base.GitAfterSHA == "" {
		return nil, errors.New("last revision has no git SHA — cannot diff; run a full scan")
	}
	if isAncestorOfHEAD(repoDir, base.GitAfterSHA) {
		return base, nil
	}
	if base != scanRev && scanRev.GitAfterSHA != "" && isAncestorOfHEAD(repoDir, scanRev.GitAfterSHA) {
		return scanRev, nil
	}
	return nil, fmt.Errorf(
		"the graph's commit %s is not an ancestor of HEAD — it was built on a branch this checkout does not contain; "+
			"check out that branch or rescan before refresh", shortSHA(scanRev.GitAfterSHA))
}

// isAncestorOfHEAD is false both for "no" and for "that commit is not in this
// repo at all" — a base this checkout cannot reach is unusable either way.
func isAncestorOfHEAD(repoDir, sha string) bool {
	if !gitutil.OK(repoDir, "cat-file", "-e", sha+"^{commit}") {
		return false
	}
	return gitutil.OK(repoDir, "merge-base", "--is-ancestor", sha, "HEAD")
}

// revOrNil maps ErrNotFound to (nil, nil): "there is no such revision" is an
// answer this caller acts on, not a failure.
func revOrNil(rev *store.Revision, err error) (*store.Revision, error) {
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rev, nil
}

// latestRevisionAnyDomain returns the most recent revision across all domains,
// since GetLatestRevision is domain-scoped and some callers have none.
func latestRevisionAnyDomain(g *graph.Graph) *store.Revision {
	rev, err := g.Store().LatestRevisionAnyDomain()
	if err != nil {
		return nil
	}
	return rev
}

// gitDiffFiles returns files matching a diff-filter between base..HEAD.
func gitDiffFiles(base, filter string) ([]string, error) {
	out, err := gitOutput("diff", "--name-only", "--diff-filter="+filter, base+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff failed (base %s): %w", base, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func gitOutput(args ...string) (string, error) {
	return gitutil.Run(repoDirForGit(), args...)
}

func filterRefreshable(files []string) []string {
	var out []string
	for _, f := range files {
		for _, ext := range refreshExtensions {
			if strings.HasSuffix(f, ext) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
)

// ErrIncomplete is returned (wrapped) by BuildWithStore, together with the
// build report, when files failed extraction and the caller did not allow a
// partial snapshot: nothing was stored. Store.Put is first-write-wins, so an
// incomplete snapshot put by accident would pin the gaps for good.
var ErrIncomplete = errors.New("scan: snapshot is incomplete")

// Base is the snapshot an incremental build starts from.
type Base struct {
	CodeSha string
	Files   snapshot.Files
}

// BuildInput describes one snapshot build (see BuildSnapshot).
type BuildInput struct {
	Repo          string // repository name (for metrics and the default ProjectPath)
	CodeRepo      string // any git directory (bare included) holding Sha and the history down to Base.CodeSha
	Sha           string // 40/64 hex
	WorkDir       string // parent of the temporary working copy; "" = os.TempDir()
	ProjectPath   string // GitLab project path for the manifest bootstrap; "" = Repo
	Base          *Base  // nil = full scan from nothing
	Backend       llm.Backend
	Concurrency   int           // 0 -> 2
	ContextWindow int           // 0 -> the backend's window
	FileTimeout   time.Duration // 0 = no limit
	Producer      string        // "" -> "chronicle-scan"
	// OnFiles is called after each committed wave (at most 40 files).
	OnFiles func([]FileMetric)
	// OnFileDone is called as soon as a file's extraction finishes (any
	// status), before the wave is committed; progress only, the metrics still
	// come through OnFiles. It may be called concurrently.
	OnFileDone func(path, status string)
	// AllowPartial lets BuildWithStore put a snapshot whose build has failed
	// files (the result is then marked Partial). Without it such a build is
	// not stored and BuildWithStore returns ErrIncomplete.
	AllowPartial bool
	// StateDir, when set, is a persistent directory for this build (this Sha
	// from this Base): a copy of the working database, the manifest when the
	// bootstrap generated it, and state.json. BuildSnapshot saves it after each
	// committed wave and on every exit, and a later call for the same sha and
	// base starts from it, so a restart redoes no covered file. State of another
	// build, or unusable state, is discarded (said in Limitations). The caller
	// removes the directory once the snapshot is stored.
	StateDir string
	// OnPlan is called once per domain, before the first request to the model.
	OnPlan func(Plan)
	// RetryPaths are files that failed in a partial Base. An incremental build
	// sends them to the model although they did not change since the base (when
	// still present, regular and in scope). Ignored by a full scan, which sends
	// everything not yet covered anyway.
	RetryPaths []string
	// Profile steers the profile layer (see ProfileOptions). The zero value
	// takes the deterministic seed on a full scan and inherits the base's
	// profile on an incremental one.
	Profile ProfileOptions
}

// BuildResult is what BuildSnapshot produced. On error it is the partial
// state: the metrics of the files that were done are still there.
type BuildResult struct {
	Files        snapshot.Files
	SnapshotID   string
	CodeSha      string
	BaseSha      string // "" for a full scan
	Incremental  bool
	ChangedFiles int // files sent to extraction
	Failed       []FailedFile
	// Skipped are in-scope files that were not scanned on purpose (too large,
	// binary, a symlink, a submodule, not readable), each with its reason.
	Skipped     []FailedFile
	Limitations []string
	Metrics     []FileMetric
	// Profile is what profile.json holds; never nil.
	Profile *ProfileFile
	// Partial: files failed extraction, so the snapshot has known gaps.
	Partial bool
}

// buildMu serialises builds: paths is process-global state (project root), so
// two builds in one process would read each other's working copy.
var buildMu sync.Mutex

// BuildSnapshot builds the snapshot of code sha in.Sha. With a Base it restores
// the base graph, brings it to Sha deterministically (verification and
// structure) and sends to the model only the files that changed between the
// two commits (not their neighbours, not a capped number: spec 5.2). Without a
// Base, or when the base commit is not in the repository, it is a full scan.
//
// The build works in a temporary git worktree of in.CodeRepo, removed on every
// exit path, and never writes to the code repository or to in.Base.
//
// It sets process-wide state (paths) and holds a process-wide mutex for the
// whole build; more parallelism takes separate processes.
func BuildSnapshot(ctx context.Context, in BuildInput) (*BuildResult, error) {
	if !snapshot.ValidCodeSha(in.Sha) {
		return nil, snapshot.ErrBadSha
	}
	if in.Base != nil && !snapshot.ValidCodeSha(in.Base.CodeSha) {
		return nil, fmt.Errorf("scan: base code sha: %w", snapshot.ErrBadSha)
	}
	if in.Backend == nil || in.CodeRepo == "" {
		return nil, errors.New("scan: BuildSnapshot needs a backend and a code repository")
	}
	buildMu.Lock()
	defer buildMu.Unlock()

	res := &BuildResult{CodeSha: in.Sha, Failed: []FailedFile{}, Skipped: []FailedFile{}, Limitations: []string{}, Metrics: []FileMetric{}, Profile: &ProfileFile{}}
	if err := ctx.Err(); err != nil {
		return res, err
	}

	tmp, err := os.MkdirTemp(in.WorkDir, "chronicle-build-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(tmp)
	// Absolute: git runs inside CodeRepo, so a relative path would resolve there.
	if tmp, err = filepath.Abs(tmp); err != nil {
		return res, err
	}
	wt := filepath.Join(tmp, "wt")
	defer func() {
		_, _ = gitutil.Run(in.CodeRepo, "worktree", "remove", "--force", "--", wt)
		_, _ = gitutil.Run(in.CodeRepo, "worktree", "prune")
	}()
	if out, err := gitutil.Output(in.CodeRepo, "worktree", "add", "--detach", "--force", "--", wt, in.Sha); err != nil {
		return res, fmt.Errorf("scan: worktree for %s: %w: %s", in.Sha, err, out)
	}

	prevRoot, prevGit, prevDir := paths.Root(), paths.GitDir(), paths.ConfiguredDir()
	paths.SetProjectRoot(wt)
	paths.SetGitDir(wt)
	paths.SetChronicleDir(".depbot")
	defer func() {
		paths.SetProjectRoot(prevRoot)
		paths.SetGitDir(prevGit)
		paths.SetChronicleDir(prevDir)
	}()

	// 1. A base that this repository does not have cannot be diffed against.
	base := in.Base
	if base != nil && !gitutil.OK(wt, "cat-file", "-e", base.CodeSha+"^{commit}") {
		res.Limitations = append(res.Limitations, fmt.Sprintf("base commit %s is not in the repository: full scan", base.CodeSha))
		base = nil
	}

	// 2. Manifest and types: the tree of Sha wins, then the base, then (manifest
	// only) the deterministic bootstrap. Whatever is there is never rewritten.
	projectPath := in.ProjectPath
	if projectPath == "" {
		projectPath = in.Repo
	}
	depDir := filepath.Join(wt, filepath.Dir(snapshot.ManifestPath))
	manPath := filepath.Join(wt, filepath.FromSlash(snapshot.ManifestPath))
	typesPath := filepath.Join(wt, filepath.FromSlash(snapshot.TypesPath))
	// The layers before the scan: inventory, dependencies, profile. The tree's
	// own manifest, when it has one, owns the scope.
	_, treeHasManifest, err := regularFile(manPath)
	if err != nil {
		return res, err
	}
	// The saved state of an earlier attempt at this very build: its database
	// and a manifest the bootstrap generated then (the one thing the tree and
	// the base cannot give back).
	baseSha := ""
	if base != nil {
		baseSha = base.CodeSha
	}
	var restored bool
	if in.StateDir != "" {
		had := stateHasContent(in.StateDir)
		var rerr error
		restored, rerr = restoreState(in.StateDir, wt, in.Sha, baseSha)
		switch {
		case rerr != nil:
			res.Limitations = append(res.Limitations, "build state not restored: "+rerr.Error())
		case had && !restored:
			res.Limitations = append(res.Limitations, "discarded unusable build state")
		}
	}
	var resume *stateProfile
	if restored {
		resume = readStateProfile(in.StateDir)
	}
	stage, err := buildProfileStage(ctx, in, wt, DomainKey(projectPathOrDir(BootstrapInput{RepoRoot: wt, ProjectPath: projectPath})), base, treeHasManifest, resume)
	if err != nil {
		return res, err
	}
	res.Profile = &stage.file
	res.Limitations = append(res.Limitations, stage.limitations...)
	manifestGenerated, err := prepareConfigFiles(depDir, manPath, typesPath, base, projectPath, wt, stage.writeManifest)
	if err != nil {
		return res, err
	}
	if restored {
		if _, serr := os.Stat(filepath.Join(in.StateDir, stateManifest)); serr == nil {
			manifestGenerated = true
		}
	}

	// 3. The graph: the base restored (with the same restore revision that
	// `chronicle graph restore` writes, so the structural phase continues from
	// the base commit), or an empty database.
	reg := registry.LoadDefaults
	if _, ok, _ := regularFile(typesPath); ok {
		reg = func() (*registry.Registry, error) { return registry.LoadFile(typesPath) }
	}
	registryV, err := reg()
	if err != nil {
		return res, fmt.Errorf("scan: load registry: %w", err)
	}
	dbPath := filepath.Join(depDir, "chronicle.db")
	// A database committed in the tree is not this build's state; the restored
	// one is.
	if !restored {
		removeDB(dbPath)
	}
	var (
		g       *graph.Graph
		baseSeq int
	)
	if restored {
		// The database already holds the base graph and the work done on it;
		// only the base's sequence number is still needed for the export.
		if base != nil {
			snap, _, derr := snapshot.Decode(bytes.NewReader(base.Files.Graph), snapshot.DefaultLimits())
			if derr != nil {
				return res, fmt.Errorf("scan: decode base graph: %w", derr)
			}
			baseSeq = snap.Header.Seq
		}
		s, serr := store.OpenWithoutJournalSync(dbPath)
		if serr != nil {
			return res, fmt.Errorf("scan: open restored database: %w", serr)
		}
		defer s.Close()
		g = graph.New(s, registryV)
	} else if base != nil {
		snap, _, derr := snapshot.Decode(bytes.NewReader(base.Files.Graph), snapshot.DefaultLimits())
		if derr != nil {
			return res, fmt.Errorf("scan: decode base graph: %w", derr)
		}
		var closer func() error
		g, closer, err = snapshot.Load(snap, registryV, snapshot.LoadOptions{DBPath: dbPath, SkipJournalSync: true})
		if err != nil {
			return res, fmt.Errorf("scan: restore base graph: %w", err)
		}
		defer closer()
		baseSeq = snap.Header.Seq
		for _, d := range snap.Header.Domains {
			// The same row runGraphRestore writes, from memory instead of git.
			if _, _, err := g.Store().ClaimRevision(store.RevisionClaim{
				DomainKey: d, AfterSHA: base.CodeSha, TriggerKind: "ci_pipeline", Mode: "full",
				Metadata: fmt.Sprintf(`{"kind":"restore","seq":%d,"structural":{"complete":true,"processed":0}}`, snap.Header.Seq),
			}); err != nil {
				return res, fmt.Errorf("scan: claim restore revision for %s: %w", d, err)
			}
		}
	} else {
		s, serr := store.OpenWithoutJournalSync(dbPath)
		if serr != nil {
			return res, fmt.Errorf("scan: open database: %w", serr)
		}
		defer s.Close()
		g = graph.New(s, registryV)
	}
	// The journal is for the live working copy; this database is a build artifact.
	g.Store().SetJournaling(false)

	// The build state is saved after each wave and once more on the way out,
	// whatever the outcome. This defer runs before the database is closed and
	// the working copy removed (those were deferred earlier).
	onFiles := in.OnFiles
	if in.StateDir != "" {
		checkpoint := func() error {
			if err := stage.saveStateProfile(in.StateDir); err != nil {
				return err
			}
			return checkpointState(in.StateDir, wt, in.Sha, baseSha, g.Store(), manifestGenerated)
		}
		defer func() {
			if err := checkpoint(); err != nil {
				res.Limitations = append(res.Limitations, "build state not saved: "+err.Error())
			}
		}()
		var ckptWarned bool
		onFiles = func(ms []FileMetric) {
			if in.OnFiles != nil {
				in.OnFiles(ms)
			}
			if err := checkpoint(); err != nil && !ckptWarned {
				ckptWarned = true
				res.Limitations = append(res.Limitations, "build state not saved: "+err.Error())
			}
		}
	}

	// 4/5. Scan.
	opts := FullOptions{
		RepoRoot: wt, ProjectPath: projectPath, Backend: in.Backend, Concurrency: in.Concurrency,
		ContextWindow: in.ContextWindow, FileTimeout: in.FileTimeout, OnFiles: onFiles, OnFileDone: in.OnFileDone, OnPlan: in.OnPlan,
		Catalog: stage.catalog, Deps: &stage.deps, Profile: &stage.file.Profile, Inventory: stage.inv, ScopeSource: stage.file.ScopeSource,
	}
	var (
		rep    *FullReport
		runErr error
	)
	if base == nil {
		rep, runErr = FullScan(ctx, g, opts)
	} else {
		res.Incremental, res.BaseSha = true, base.CodeSha
		rep, runErr = incrementalScan(ctx, g, opts, base.CodeSha, manPath, base.Files.Domain, in.RetryPaths)
	}
	if rep != nil {
		res.Failed = append(res.Failed, rep.Failed...)
		res.Skipped = append(res.Skipped, rep.Skipped...)
		res.Metrics = append(res.Metrics, rep.Metrics...)
		res.Limitations = append(res.Limitations, rep.Limitations...)
		for _, d := range rep.Domains {
			res.ChangedFiles += d.Sent
		}
	}
	res.Partial = len(res.Failed) > 0
	var checks []LayerCheck
	if rep != nil {
		checks = rep.Checks
	}
	if runErr != nil {
		return res, runErr
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}

	// 6. Export.
	snapOut, err := snapshot.FromStore(g.Store(), snapshot.ExportOptions{PrevSeq: baseSeq, Registry: snapshot.RegistryFingerprint(registryV)})
	if err != nil {
		return res, fmt.Errorf("scan: export graph: %w", err)
	}
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, snapOut); err != nil {
		return res, fmt.Errorf("scan: encode graph: %w", err)
	}
	res.Files.Graph = buf.Bytes()
	if res.Files.Domain, _, err = regularFile(manPath); err != nil {
		return res, err
	}
	if res.Files.Types, _, err = regularFile(typesPath); err != nil {
		return res, err
	}
	var checkLims []string
	if res.Files.Profile, checkLims, err = stage.finish(manPath, checks); err != nil {
		return res, fmt.Errorf("scan: profile file: %w", err)
	}
	res.Limitations = append(res.Limitations, checkLims...)
	if res.SnapshotID, err = res.Files.ContentID(); err != nil {
		return res, fmt.Errorf("scan: snapshot id: %w", err)
	}
	return res, nil
}

// regularFile reads path when it is a regular file. A symlink or anything else
// is an error: the tree being built is not trusted, and a link to a file
// outside it must never end up in a snapshot or be written through.
func regularFile(path string) (data []byte, exists bool, err error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, true, fmt.Errorf("scan: %s is not a regular file", filepath.Base(path))
	}
	data, err = os.ReadFile(path)
	return data, true, err
}

// prepareConfigFiles leaves the manifest (and, when there is one, the types
// file) of the commit being built in the working copy: from its tree, else
// from the base, else a bootstrap for the manifest. generated says the
// manifest is the bootstrap's (neither the tree nor the base had one).
//
// profiled, when set, writes the manifest from the build's fresh profile and
// takes the place of the base's copy and the bootstrap.
func prepareConfigFiles(depDir, manPath, typesPath string, base *Base, projectPath, wt string, profiled func(manPath string) error) (generated bool, err error) {
	if fi, err := os.Lstat(depDir); err == nil && !fi.IsDir() {
		return false, fmt.Errorf("scan: %s in the tree is not a directory", filepath.Base(depDir))
	}
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		return false, err
	}
	_, haveMan, err := regularFile(manPath)
	if err != nil {
		return false, err
	}
	switch {
	case haveMan:
	case profiled != nil:
		if err := profiled(manPath); err != nil {
			return false, err
		}
		generated = true
	case base != nil && len(base.Files.Domain) > 0:
		if err := os.WriteFile(manPath, base.Files.Domain, 0o644); err != nil {
			return false, err
		}
	default:
		if _, err := BootstrapManifest(BootstrapInput{RepoRoot: wt, ProjectPath: projectPath}); err != nil {
			return false, err
		}
		generated = true
	}
	_, haveTypes, err := regularFile(typesPath)
	if err != nil {
		return generated, err
	}
	if !haveTypes && base != nil && len(base.Files.Types) > 0 {
		return generated, os.WriteFile(typesPath, base.Files.Types, 0o644)
	}
	return generated, nil
}

// incrementalScan brings a restored base graph to HEAD of o.RepoRoot: per
// domain the deterministic phases, then extraction of the files that changed
// between the base commit and HEAD (and only those). Like FullScan, the report
// is returned even with an error.
func incrementalScan(ctx context.Context, g *graph.Graph, o FullOptions, baseSha, manPath string, baseManifest []byte, retry []string) (*FullReport, error) {
	rep := &FullReport{Domains: []DomainReport{}, Failed: []FailedFile{}, Skipped: []FailedFile{}, Metrics: []FileMetric{}, Limitations: []string{}}
	if o.Concurrency <= 0 {
		o.Concurrency = 2
	}
	head, err := gitutil.Run(o.RepoRoot, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil {
		return rep, fmt.Errorf("scan: resolve HEAD in %s: %w", o.RepoRoot, err)
	}
	rep.HeadSHA = head
	m, err := manifest.LoadFile(manPath)
	if err != nil {
		return rep, fmt.Errorf("scan: load manifest: %w", err)
	}
	// The tree difference of the two commits, which is right for a base that is
	// not an ancestor too.
	changed, err := gitdiff.ChangedFiles(o.RepoRoot, baseSha, head)
	if err != nil {
		return rep, err
	}
	run := &fullRun{g: g, o: o, m: m, env: Env{RepoDir: o.RepoRoot, ManifestPath: manPath}, head: head, rep: rep}
	if len(baseManifest) > 0 {
		if bm, berr := manifest.Load(baseManifest); berr == nil {
			run.prevM = bm
		}
	}
	domains, _ := selectDomains(m, BootstrapResult{}, "")
	var runErr error
	for _, d := range domains {
		if runErr = ctx.Err(); runErr != nil {
			break
		}
		if runErr = run.incrementalDomain(ctx, d, baseSha, changed, retry, len(m.Domains) > 1); runErr != nil {
			break
		}
	}
	rep.Limitations = append(run.extra, limitationsFor(run.structuralOK, len(rep.Failed))...)
	rep.Complete = runErr == nil && ctx.Err() == nil && len(rep.Failed) == 0 && run.structuralOK
	return rep, runErr
}

func (r *fullRun) incrementalDomain(ctx context.Context, d manifest.DomainEntry, baseSha string, changed []gitdiff.ChangedFile, retry []string, multi bool) error {
	if !r.started {
		r.started = true
		r.structuralOK = true
	}
	dr := DomainReport{Domain: d.Key}
	defer func() { r.rep.Domains = append(r.rep.Domains, dr) }()

	if _, _, _, err := r.env.VerifyPhase(r.g, d.Key, baseSha, r.head, false); err != nil {
		return fmt.Errorf("scan: verify domain %s: %w", d.Key, err)
	}
	sr, serr := r.env.StructuralPhase(r.g, d.Key, r.head, -1, false)
	dr.Structural = sr
	switch {
	case serr != nil:
		r.structuralOK = false
		r.addExtra("structural pass failed: " + serr.Error())
	case sr != nil && !sr.Complete:
		r.structuralOK = false
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := d.Scan
	seen := map[string]bool{}
	var candidates []string
	add := func(p string) {
		if seen[p] || !graph.InScope(p, &cfg) {
			return
		}
		if multi && r.m.DomainForFile(p) != d.Key {
			return
		}
		if r.skipIrregular(p) {
			return
		}
		seen[p] = true
		candidates = append(candidates, p)
	}
	for _, f := range changed {
		if f.Status != "D" { // a rename reports its new path
			add(f.Path)
		}
	}
	// Files that failed in a partial base are retried although unchanged. The
	// paths come from an older snapshot, so each is checked against the tree
	// being built: only a tracked regular blob at HEAD qualifies (a directory
	// that has since become a symlink must not lead the read out of the clone).
	forced := map[string]bool{} // valid retry paths: extracted even when covered
	if len(retry) > 0 {
		modes, err := trackedModes(r.o.RepoRoot)
		if err != nil {
			return err
		}
		for _, raw := range retry {
			p := path.Clean(filepath.ToSlash(raw))
			if raw == "" || p == "." || !filepath.IsLocal(filepath.FromSlash(p)) {
				continue // not a path inside the tree
			}
			switch mode, ok := modes[p]; {
			case ok && (mode == "100644" || mode == "100755"):
				add(p)
				forced[p] = true
			case ok:
				if !seen[p] {
					seen[p] = true
					r.rep.Skipped = append(r.rep.Skipped, FailedFile{Path: p, Error: "retry: not a regular file (mode " + mode + ")"})
				}
			default:
				// Not tracked at HEAD. Gone is nobody's business; anything that
				// still resolves (e.g. through a symlinked directory) is reported.
				if _, err := os.Lstat(filepath.Join(r.o.RepoRoot, filepath.FromSlash(p))); err == nil && !seen[p] {
					seen[p] = true
					r.rep.Skipped = append(r.rep.Skipped, FailedFile{Path: p, Error: "retry: not a tracked regular file at HEAD"})
				}
			}
		}
	}

	// Every in-scope file is a candidate: pendingFiles keeps those with no
	// coverage row or whose blob or coverage version differs (a rescan with
	// other packs, a file a scope change newly admits, a file that failed in
	// the base).
	scopeList, err := r.inScopeFiles(d, &cfg, multi)
	if err != nil {
		return err
	}
	for _, p := range scopeList {
		if !seen[p] {
			seen[p] = true
			candidates = append(candidates, p)
		}
	}
	if err := r.invalidateDropped(d, multi); err != nil {
		return err
	}
	st := r.g.Store()
	guide, err := r.guide()
	if err != nil {
		return err
	}
	todo, done, err := pendingFiles(st, d.Key, r.o.RepoRoot, candidates, versionFrom(guide))
	if err != nil {
		return err
	}
	if len(forced) > 0 {
		// RetryPaths is the caller's order to extract again: a retried file
		// that matches its coverage row is sent anyway.
		var still []string
		for _, p := range done {
			if forced[p] {
				todo = append(todo, p)
			} else {
				still = append(still, p)
			}
		}
		done = still
		sort.Strings(todo)
	}
	dr.InScope, dr.AlreadyCovered, dr.Sent = len(candidates), len(done), len(todo)
	if err := reopenObligations(st, d.Key, r.head, done); err != nil {
		return err
	}
	if r.o.OnPlan != nil {
		r.o.OnPlan(Plan{Domain: d.Key, InScope: len(candidates), AlreadyCovered: len(done), ToSend: len(todo)})
	}
	if len(todo) > 0 {
		r.extracted = nil
		if err := r.extract(ctx, d, todo, &dr, guide); err != nil {
			return err
		}
		if err := r.retireVanished(d, r.extracted); err != nil {
			return err
		}
	}
	// Dependencies are written after extraction so a manifest's service node
	// (minted by the model's declares_service) exists: its edges then start
	// there and the model's and the deps pass's DEPENDS_ON are one edge.
	if err := r.writeDeps(d, multi); err != nil {
		return err
	}
	r.runChecks(d, dr, &cfg, multi)
	unmatched, _ := r.g.FindUnmatchedHTTPCalls(d.Key)
	dr.UnmatchedHTTPCalls = len(unmatched)
	return ctx.Err()
}

// retireVanished deactivates the edges and nodes whose only evidence came from
// files that were just extracted again and no longer state them: a removed
// endpoint must leave the graph, not stay active on stale evidence of the old
// blob. Entities that still have live evidence (declared elsewhere, or again
// in the new version of the file) are untouched.
func (r *fullRun) retireVanished(d manifest.DomainEntry, files []string) error {
	if len(files) == 0 {
		return nil
	}
	var revID int64
	if rev, err := r.g.Store().GetRevisionBySHA(d.Key, r.head); err == nil {
		revID = rev.RevisionID
	} else if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("scan: look up revision at %s: %w", ShortSHA(r.head), err)
	}
	if _, _, err := r.g.Store().RetireUnbackedByFiles(d.Key, files, revID); err != nil {
		return fmt.Errorf("scan: retire vanished facts: %w", err)
	}
	return nil
}

// invalidateDropped marks the evidence of files that the base's scan covered
// and the current scope no longer does as stale: the graph must not keep
// vouching for code the profile now leaves out.
func (r *fullRun) invalidateDropped(d manifest.DomainEntry, multi bool) error {
	if r.prevM == nil {
		return nil
	}
	prev, ok := r.prevM.ScanConfigFor(d.Name)
	if !ok {
		return nil
	}
	now := d.Scan
	modes, err := trackedModes(r.o.RepoRoot)
	if err != nil {
		return err
	}
	var dropped []string
	for p := range modes {
		if graph.InScope(p, prev) && !graph.InScope(p, &now) {
			if multi && r.m.DomainForFile(p) != d.Key {
				continue
			}
			dropped = append(dropped, p)
		}
	}
	if len(dropped) == 0 {
		return nil
	}
	sort.Strings(dropped)
	// Stale-mark directly: InvalidateChanged would also re-verify each path
	// against the process cwd and could mark an unchanged file valid again.
	// No verify step, no obligations; trust is recomputed for the owners.
	_, edgeIDs, nodeIDs, err := r.g.Store().MarkEvidenceStaleByFiles(dropped)
	if err != nil {
		return fmt.Errorf("scan: stale-mark dropped files: %w", err)
	}
	// The coverage row must not claim the file is still observed: if the scope
	// admits it again, its stale evidence has to be re-extracted.
	for _, p := range dropped {
		for _, dk := range []string{d.Key, ""} {
			if err := r.g.Store().ClearCoverageBlob(dk, p); err != nil {
				return fmt.Errorf("scan: clear coverage of dropped %s: %w", p, err)
			}
		}
	}
	for _, id := range edgeIDs {
		if err := r.g.RecalculateEdgeTrust(id); err != nil {
			return fmt.Errorf("scan: edge trust %d: %w", id, err)
		}
	}
	for _, id := range nodeIDs {
		if err := r.g.RecalculateNodeTrust(id); err != nil {
			return fmt.Errorf("scan: node trust %d: %w", id, err)
		}
	}
	return nil
}

// trackedModes maps every path tracked at HEAD of root to its git file mode
// (100644/100755 regular, 120000 symlink, 160000 submodule).
func trackedModes(root string) (map[string]string, error) {
	out, err := gitutil.RunRaw(root, "-c", "core.quotePath=false", "ls-files", "-s", "-z")
	if err != nil {
		return nil, fmt.Errorf("scan: list tracked files: %w", err)
	}
	modes := map[string]string{}
	for _, rec := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		if mode, _, _ := strings.Cut(meta, " "); mode != "" {
			modes[p] = mode
		}
	}
	return modes, nil
}

// StoreBuildOptions steer BuildWithStore.
type StoreBuildOptions struct {
	Lookup       snapshot.LookupConfig
	TargetSha    string
	TargetBranch string
	Put          snapshot.PutOptions
}

// BuildWithStore finds the best base for in.Sha in st (spec 4.3), builds the
// snapshot from it and stores the result. A sha that already has a snapshot is
// returned as is: no build, nil BuildResult. A build with failed files is not
// stored (ErrIncomplete, with the report) unless in.AllowPartial.
func BuildWithStore(ctx context.Context, st snapshot.Store, in BuildInput, so StoreBuildOptions) (snapshot.PutResult, *BuildResult, error) {
	if !snapshot.ValidCodeSha(in.Sha) {
		return snapshot.PutResult{}, nil, snapshot.ErrBadSha
	}
	baseID := ""
	if in.Base == nil {
		ans, err := snapshot.Lookup(ctx, st, in.CodeRepo, snapshot.LookupQuery{Repo: in.Repo, Sha: in.Sha,
			TargetSha: so.TargetSha, TargetBranch: so.TargetBranch}, so.Lookup)
		switch {
		case err == nil && ans.Match == snapshot.MatchExact:
			_, e, gerr := st.Get(ctx, in.Repo, in.Sha)
			return snapshot.PutResult{Entry: e}, nil, gerr // already built: nothing to do
		case err == nil:
			files, _, gerr := st.Get(ctx, in.Repo, ans.CodeSha)
			if gerr != nil {
				return snapshot.PutResult{}, nil, gerr
			}
			in.Base, baseID = &Base{CodeSha: ans.CodeSha, Files: files}, ans.SnapshotID
		case errors.Is(err, snapshot.ErrNoSnapshot):
			// no base: full scan
		default:
			return snapshot.PutResult{}, nil, err
		}
	} else if id, err := in.Base.Files.ContentID(); err == nil {
		baseID = id
	}
	res, err := BuildSnapshot(ctx, in)
	if err != nil {
		return snapshot.PutResult{}, res, err
	}
	if res.Partial && !in.AllowPartial {
		return snapshot.PutResult{}, res, fmt.Errorf("%w: %d files failed extraction", ErrIncomplete, len(res.Failed))
	}
	po := so.Put
	// A build that fell back to a full scan (the base commit was missing) has
	// no base to record.
	if !res.Incremental {
		po.Base = ""
	} else if po.Base == "" {
		po.Base = baseID
	}
	if po.Producer == "" {
		po.Producer = in.Producer
	}
	if po.Producer == "" {
		po.Producer = "chronicle-scan"
	}
	pr, err := st.Put(ctx, in.Repo, in.Sha, res.Files, po)
	return pr, res, err
}

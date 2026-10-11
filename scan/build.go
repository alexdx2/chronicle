package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	// AllowPartial lets BuildWithStore put a snapshot whose build has failed
	// files (the result is then marked Partial). Without it such a build is
	// not stored and BuildWithStore returns ErrIncomplete.
	AllowPartial bool
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
	Limitations  []string
	Metrics      []FileMetric
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

	res := &BuildResult{CodeSha: in.Sha, Failed: []FailedFile{}, Limitations: []string{}, Metrics: []FileMetric{}}
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
	if err := prepareConfigFiles(depDir, manPath, typesPath, base, projectPath, wt); err != nil {
		return res, err
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
	// A database committed in the tree is not this build's state.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
	var (
		g       *graph.Graph
		baseSeq int
	)
	if base != nil {
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

	// 4/5. Scan.
	opts := FullOptions{
		RepoRoot: wt, ProjectPath: projectPath, Backend: in.Backend, Concurrency: in.Concurrency,
		ContextWindow: in.ContextWindow, FileTimeout: in.FileTimeout, OnFiles: in.OnFiles,
	}
	var (
		rep    *FullReport
		runErr error
	)
	if base == nil {
		rep, runErr = FullScan(ctx, g, opts)
	} else {
		res.Incremental, res.BaseSha = true, base.CodeSha
		rep, runErr = incrementalScan(ctx, g, opts, base.CodeSha, manPath)
	}
	if rep != nil {
		res.Failed = append(res.Failed, rep.Failed...)
		res.Metrics = append(res.Metrics, rep.Metrics...)
		res.Limitations = append(res.Limitations, rep.Limitations...)
		for _, d := range rep.Domains {
			res.ChangedFiles += d.Sent
		}
	}
	res.Partial = len(res.Failed) > 0
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
// from the base, else a bootstrap for the manifest.
func prepareConfigFiles(depDir, manPath, typesPath string, base *Base, projectPath, wt string) error {
	if fi, err := os.Lstat(depDir); err == nil && !fi.IsDir() {
		return fmt.Errorf("scan: %s in the tree is not a directory", filepath.Base(depDir))
	}
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		return err
	}
	_, haveMan, err := regularFile(manPath)
	if err != nil {
		return err
	}
	switch {
	case haveMan:
	case base != nil && len(base.Files.Domain) > 0:
		if err := os.WriteFile(manPath, base.Files.Domain, 0o644); err != nil {
			return err
		}
	default:
		if _, err := BootstrapManifest(BootstrapInput{RepoRoot: wt, ProjectPath: projectPath}); err != nil {
			return err
		}
	}
	_, haveTypes, err := regularFile(typesPath)
	if err != nil {
		return err
	}
	if !haveTypes && base != nil && len(base.Files.Types) > 0 {
		return os.WriteFile(typesPath, base.Files.Types, 0o644)
	}
	return nil
}

// incrementalScan brings a restored base graph to HEAD of o.RepoRoot: per
// domain the deterministic phases, then extraction of the files that changed
// between the base commit and HEAD (and only those). Like FullScan, the report
// is returned even with an error.
func incrementalScan(ctx context.Context, g *graph.Graph, o FullOptions, baseSha, manPath string) (*FullReport, error) {
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
	domains, _ := selectDomains(m, BootstrapResult{}, "")
	var runErr error
	for _, d := range domains {
		if runErr = ctx.Err(); runErr != nil {
			break
		}
		if runErr = run.incrementalDomain(ctx, d, baseSha, changed, len(m.Domains) > 1); runErr != nil {
			break
		}
	}
	rep.Limitations = append(run.extra, limitationsFor(run.unmatched, run.structuralOK, len(rep.Failed))...)
	rep.Complete = runErr == nil && ctx.Err() == nil && len(rep.Failed) == 0 && run.structuralOK
	return rep, runErr
}

func (r *fullRun) incrementalDomain(ctx context.Context, d manifest.DomainEntry, baseSha string, changed []gitdiff.ChangedFile, multi bool) error {
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
	var todo []string
	for _, f := range changed {
		p := f.Path // a rename reports its new path
		if f.Status == "D" || seen[p] || !graph.InScope(p, &cfg) {
			continue
		}
		if multi && r.m.DomainForFile(p) != d.Key {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(r.o.RepoRoot, filepath.FromSlash(p))); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		seen[p] = true
		todo = append(todo, p)
	}
	dr.InScope, dr.Sent = len(todo), len(todo)
	if len(todo) > 0 {
		if err := r.extract(ctx, d, todo, &dr); err != nil {
			return err
		}
	}
	unmatched, _ := r.g.FindUnmatchedHTTPCalls(d.Key)
	dr.UnmatchedHTTPCalls = len(unmatched)
	r.unmatched += len(unmatched)
	return ctx.Err()
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
	if po.Base == "" {
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

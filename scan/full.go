package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/headless"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/store"
)

// FullOptions drive one full scan: a repository from an empty database to a
// complete graph without an agent.
type FullOptions struct {
	// RepoRoot is the working copy at the commit being scanned; the caller has
	// already done paths.SetProjectRoot(RepoRoot).
	RepoRoot    string
	ProjectPath string
	// Domain restricts the scan to one manifest domain (key or name); empty
	// means every domain of the manifest.
	Domain        string
	Backend       llm.Backend
	Concurrency   int // 0 -> 2
	ContextWindow int
	FileTimeout   time.Duration
	NoExtract     bool // structural pass only
	// Catalog supplies the tech packs; nil -> built-in packs plus the project's
	// <chronicle dir>/packs.
	Catalog *prompts.Catalog
	// Deps, when set, is written as role-deps extractions after the structural
	// pass of each domain (only the manifests the domain owns).
	Deps *deps.Report
	// Profile and Inventory, when Profile is set, feed the deterministic layer
	// checks that run after extraction (FullReport.Checks).
	Profile   *Profile
	Inventory *Inventory
	// OnFiles is called after each committed wave with that wave's metrics.
	OnFiles func([]FileMetric)
	// OnFileDone is called when a file's extraction finishes, before its wave
	// is committed (progress only; may be called concurrently).
	OnFileDone func(path, status string)
	// OnPlan is called once per domain, after the files to send are known and
	// before the first request to the model.
	OnPlan func(Plan)
}

// Plan is what a domain's scan is about to do, for progress reporting.
type Plan struct {
	Domain         string
	InScope        int // files to consider (a full scan: the scan scope; an incremental one: the changed and retried files)
	AlreadyCovered int // of them, covered at the same blob (a restored build state included)
	ToSend         int // going to the model
}

// FailedFile is a file the scan could not finish, or deliberately passed over.
type FailedFile struct {
	Path   string `json:"path"`
	Error  string `json:"error"`
	Domain string `json:"domain,omitempty"`
}

// DomainReport is what one domain's scan did.
type DomainReport struct {
	Domain         string `json:"domain"`
	InScope        int    `json:"in_scope"`
	AlreadyCovered int    `json:"already_covered"`
	Sent           int    `json:"sent"`
	// Extracted counts the sent files the model handled to the end (any
	// artifact status except failed, cancelled and intentionally skipped).
	Extracted          int                     `json:"extracted"`
	Structural         *graph.StructuralResult `json:"structural,omitempty"`
	UnmatchedHTTPCalls int                     `json:"unmatched_http_calls"`
}

// FullReport is the outcome of FullScan. On error or cancellation the report
// is partial and the state on disk is fit to resume from.
type FullReport struct {
	HeadSHA         string         `json:"head_sha"`
	ManifestCreated bool           `json:"manifest_created"`
	Domains         []DomainReport `json:"domains"`
	Failed          []FailedFile   `json:"failed"`
	Skipped         []FailedFile   `json:"skipped"` // intentional skips (file too large, binary), not errors
	Metrics         []FileMetric   `json:"metrics"`
	Limitations     []string       `json:"limitations"`
	Checks          []LayerCheck   `json:"checks,omitempty"`
	// Complete: no failed files, the structural pass finished everywhere and
	// the context was not cancelled.
	Complete bool `json:"complete"`
}

// scanExtractorID and the version string are the coverage identity the
// headless pipeline records; a file is "done" when both match and its blob is
// unchanged.
const scanExtractorID = "chronicle-scan"

// FullScan takes the repository at o.RepoRoot's HEAD from nothing (or from a
// partly finished earlier run in the same database) to a full graph: manifest
// bootstrap, structural pass, then LLM extraction of every in-scope file that
// has no coverage at its current blob.
func FullScan(ctx context.Context, g *graph.Graph, o FullOptions) (*FullReport, error) {
	rep := &FullReport{Domains: []DomainReport{}, Failed: []FailedFile{}, Skipped: []FailedFile{}, Metrics: []FileMetric{}, Limitations: []string{}}
	if g == nil {
		return rep, errors.New("scan: FullScan needs a graph")
	}
	if o.Backend == nil && !o.NoExtract {
		return rep, errors.New("scan: FullScan needs a backend unless NoExtract is set")
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 2
	}

	head, err := gitutil.Run(o.RepoRoot, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil {
		return rep, fmt.Errorf("scan: resolve HEAD in %s: %w", o.RepoRoot, err)
	}
	rep.HeadSHA = head

	boot, err := BootstrapManifest(BootstrapInput{RepoRoot: o.RepoRoot, ProjectPath: o.ProjectPath})
	if err != nil {
		return rep, err
	}
	rep.ManifestCreated = boot.Created
	m, err := manifest.LoadFile(boot.Path)
	if err != nil {
		return rep, fmt.Errorf("scan: load manifest: %w", err)
	}
	domains, err := selectDomains(m, boot, o.Domain)
	if err != nil {
		return rep, err
	}

	run := &fullRun{g: g, o: o, m: m, env: Env{RepoDir: o.RepoRoot, ManifestPath: boot.Path}, head: head, rep: rep}
	var runErr error
	for _, d := range domains {
		if runErr = ctx.Err(); runErr != nil {
			break
		}
		if runErr = run.scanDomain(ctx, d, len(m.Domains) > 1); runErr != nil {
			break
		}
	}
	rep.Limitations = append(run.extra, limitationsFor(run.structuralOK, len(rep.Failed))...)
	rep.Complete = runErr == nil && ctx.Err() == nil && len(rep.Failed) == 0 && run.structuralOK
	return rep, runErr
}

// selectDomains picks the domains to scan: the one asked for, or all of them
// with the bootstrap's chosen domain first and the rest by key, so the order
// does not depend on map iteration in the manifest package.
func selectDomains(m *manifest.Manifest, boot BootstrapResult, want string) ([]manifest.DomainEntry, error) {
	if want != "" {
		for _, d := range m.Domains {
			if d.Key == want || d.Name == want {
				return []manifest.DomainEntry{d}, nil
			}
		}
		return nil, fmt.Errorf("scan: manifest has no domain %q", want)
	}
	all := append([]manifest.DomainEntry(nil), m.Domains...)
	sort.SliceStable(all, func(i, j int) bool {
		if (all[i].Key == boot.Domain) != (all[j].Key == boot.Domain) {
			return all[i].Key == boot.Domain
		}
		return all[i].Key < all[j].Key
	})
	return all, nil
}

// fullRun carries what the domains of one FullScan share and accumulate.
type fullRun struct {
	g     *graph.Graph
	o     FullOptions
	m     *manifest.Manifest
	prevM *manifest.Manifest // the base build's manifest (incremental only)
	env   Env
	head  string
	rep   *FullReport

	extra        []string // limitations beyond limitationsFor's
	cat          *prompts.Catalog
	structuralOK bool
	started      bool // at least one domain began, so structuralOK is meaningful
}

// guide is the per-file guide set of this run: one catalog, the manifest's
// technologies. Extraction and the staleness test share it, so the version a
// file is recorded at is the version it is compared against.
func (r *fullRun) guide() (*headless.GuideSet, error) {
	if r.cat == nil {
		cat := r.o.Catalog
		if cat == nil {
			cat = prompts.BuiltinCatalog()
			if err := cat.AddProjectPacks(filepath.Join(paths.DirAt(r.o.RepoRoot), "packs")); err != nil {
				return nil, fmt.Errorf("scan: project packs: %w", err)
			}
		}
		r.cat = cat
	}
	return headless.NewGuide(r.cat, r.m.Tech), nil
}

func versionFrom(guide *headless.GuideSet) func(string) string {
	return func(path string) string {
		_, refs := guide.For(path)
		return headless.ExtractorVersion(refs)
	}
}

func (r *fullRun) addExtra(s string) {
	for _, e := range r.extra {
		if e == s {
			return
		}
	}
	r.extra = append(r.extra, s)
}

// scanDomain runs the structural pass and the extraction for one domain. The
// error is the run's (cancellation or infrastructure); failed files are not.
func (r *fullRun) scanDomain(ctx context.Context, d manifest.DomainEntry, multi bool) error {
	if !r.started {
		r.started = true
		r.structuralOK = true
	}
	dr := DomainReport{Domain: d.Key}
	defer func() { r.rep.Domains = append(r.rep.Domains, dr) }()

	// Structural pass: no model, no previous scan needed. A failure here is not
	// fatal — extraction does not depend on it — but the scan is then incomplete.
	sr, serr := r.env.StructuralPhase(r.g, d.Key, r.head, -1, true)
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
	scanCfg, _ := r.m.ScanConfigFor(d.Name) // matches by name, not key (P11)
	list, err := r.inScopeFiles(d, scanCfg, multi)
	if err != nil {
		return err
	}
	dr.InScope = len(list)

	st := r.g.Store()
	guide, err := r.guide()
	if err != nil {
		return err
	}
	todo, done, err := pendingFiles(st, d.Key, r.o.RepoRoot, list, versionFrom(guide))
	if err != nil {
		return err
	}
	dr.AlreadyCovered = len(done)
	dr.Sent = len(todo)
	if err := reopenObligations(st, d.Key, r.head, done); err != nil {
		return err
	}
	if r.o.OnPlan != nil {
		send := len(todo)
		if r.o.NoExtract {
			send = 0
		}
		r.o.OnPlan(Plan{Domain: d.Key, InScope: len(list), AlreadyCovered: len(done), ToSend: send})
	}

	switch {
	case r.o.NoExtract:
		dr.Sent = 0
		r.addExtra("semantic extraction not run (structural pass only)")
	case len(todo) == 0:
		r.addExtra("nothing to extract: every in-scope file already covered at its current blob")
	default:
		if err := r.extract(ctx, d, todo, &dr, guide); err != nil {
			return err
		}
	}

	// Dependencies are written after extraction so a manifest's service node
	// (minted by the model's declares_service) exists: its edges then start
	// there and the model's and the deps pass's DEPENDS_ON are one edge.
	if err := r.writeDeps(d, multi); err != nil {
		return err
	}
	r.runChecks(d)
	unmatched, _ := r.g.FindUnmatchedHTTPCalls(d.Key)
	dr.UnmatchedHTTPCalls = len(unmatched)
	return ctx.Err()
}

// writeDeps stores the dependency extractions of the manifests this domain
// owns, on the revision of head (claimed the way the headless run claims it,
// so the extraction that preceded it joins the same row).
func (r *fullRun) writeDeps(d manifest.DomainEntry, multi bool) error {
	if r.o.Deps == nil {
		return nil
	}
	own := deps.Report{Warnings: r.o.Deps.Warnings}
	for _, p := range r.o.Deps.Publishes {
		if !multi || r.m.DomainForFile(p.File) == d.Key {
			own.Publishes = append(own.Publishes, p)
		}
	}
	for _, dep := range r.o.Deps.Depends {
		if !multi || r.m.DomainForFile(dep.File) == d.Key {
			own.Depends = append(own.Depends, dep)
		}
	}
	rev, _, err := r.g.Store().ClaimRevision(store.RevisionClaim{
		DomainKey: d.Key, AfterSHA: r.head, TriggerKind: "ci_pipeline", Mode: "incremental",
		Metadata: `{"kind":"headless"}`,
	})
	if err != nil {
		return fmt.Errorf("scan: claim revision for dependencies: %w", err)
	}
	var includeDev bool
	if cfg, ok := r.m.ScanConfigFor(d.Name); ok && cfg != nil {
		includeDev = cfg.IncludeDevDeps
	}
	if _, err := r.g.WriteDepsExtractionsOpts(d.Key, rev, own, graph.DepsOptions{IncludeDevDeps: includeDev}); err != nil {
		return fmt.Errorf("scan: write dependencies of %s: %w", d.Key, err)
	}
	return nil
}

// runChecks appends the layer checks of the finished domain.
func (r *fullRun) runChecks(d manifest.DomainEntry) {
	if r.o.Profile == nil || r.o.Inventory == nil {
		return
	}
	r.rep.Checks = append(r.rep.Checks, LayerChecks(r.g, d.Key, *r.o.Profile, r.rep, r.o.Inventory)...)
}

// pendingFiles splits candidates into the files the model still has to see
// (todo) and those a finished chronicle-scan observation already covers at the
// file's current blob and at the coverage version versionFor gives for it
// (done). Order is kept.
func pendingFiles(st *store.Store, domainKey, root string, candidates []string, versionFor func(path string) string) (todo, done []string, err error) {
	cov, err := st.ListCoverage("")
	if err != nil {
		return nil, nil, fmt.Errorf("scan: list coverage: %w", err)
	}
	type coveredAt struct{ blob, version string }
	covered := map[string]coveredAt{} // path -> a finished chronicle-scan observation
	// A snapshot without nodes restores its coverage under no domain; such
	// rows count unless the domain has its own row for the file.
	own := map[string]bool{}
	for _, c := range cov {
		if c.ExtractorID != scanExtractorID || (c.DomainKey != domainKey && c.DomainKey != "") {
			continue
		}
		if c.DomainKey == "" && own[c.FilePath] {
			continue
		}
		own[c.FilePath] = own[c.FilePath] || c.DomainKey != ""
		covered[c.FilePath] = coveredAt{c.BlobSHA, c.ExtractorVersion}
	}
	for _, p := range candidates {
		blob, berr := gitutil.FileBlobSHA(root, p)
		if want, ok := covered[p]; ok && berr == nil && want.blob == blob && want.version == versionFor(p) {
			done = append(done, p)
		} else {
			todo = append(todo, p)
		}
	}
	return todo, done, nil
}

// reopenObligations: the pipeline counts a failed file's artifact as a
// satisfied obligation, so on a revision that already exists for head only the
// obligations of really covered files (done) may survive; otherwise a rerun
// would silently skip the files that failed.
func reopenObligations(st *store.Store, domainKey, head string, done []string) error {
	rev, err := st.GetRevisionBySHA(domainKey, head)
	switch {
	case err == nil:
		if _, derr := st.DeleteScanFileObligationsForRevision(rev.RevisionID, done); derr != nil {
			return fmt.Errorf("scan: reopen revision %d: %w", rev.RevisionID, derr)
		}
	case !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("scan: look up revision at %s: %w", ShortSHA(head), err)
	}
	return nil
}

// inScopeFiles lists tracked files inside the domain's scan scope (and, with
// several domains, owned by this one), in git's order.
func (r *fullRun) inScopeFiles(d manifest.DomainEntry, cfg *manifest.ScanConfig, multi bool) ([]string, error) {
	out, err := gitutil.RunRaw(r.o.RepoRoot, "-c", "core.quotePath=false", "ls-files", "-z")
	if err != nil {
		return nil, fmt.Errorf("scan: list tracked files: %w", err)
	}
	var list []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || !graph.InScope(p, cfg) {
			continue
		}
		if multi && r.m.DomainForFile(p) != d.Key {
			continue
		}
		if r.skipIrregular(p) {
			continue
		}
		list = append(list, p)
	}
	return list, nil
}

// skipIrregular says whether the in-scope path p is not a regular file of the
// working copy, and if so records it in the report's Skipped with the reason
// (it is an intentional skip, not a failure, but never silent). Only regular
// files are sent: headless reads with os.ReadFile, which follows a tracked
// symlink out of the working copy, and a submodule is a directory.
func (r *fullRun) skipIrregular(p string) bool {
	fi, err := os.Lstat(filepath.Join(r.o.RepoRoot, filepath.FromSlash(p)))
	var why string
	switch {
	case err != nil:
		why = "cannot stat: " + err.Error()
	case fi.Mode().IsRegular():
		return false
	case fi.Mode()&os.ModeSymlink != 0:
		why = "symlink: not followed"
	case fi.IsDir():
		why = "directory or submodule: not scanned"
	default:
		why = "not a regular file (" + fi.Mode().Type().String() + ")"
	}
	for _, sk := range r.rep.Skipped {
		if sk.Path == p && sk.Error == why {
			return true // already reported (a second pass over the same tree)
		}
	}
	r.rep.Skipped = append(r.rep.Skipped, FailedFile{Path: p, Error: why})
	return true
}

// extract runs the headless pipeline over todo and folds the per-file outcomes
// into the report. A cancelled or failed run still contributes what it did.
func (r *fullRun) extract(ctx context.Context, d manifest.DomainEntry, todo []string, dr *DomainReport, guide *headless.GuideSet) error {
	model := r.o.Backend.Name() + ":" + r.o.Backend.Model()
	res, runErr := headless.Run(ctx, headless.RunInput{
		G: r.g, RepoRoot: r.o.RepoRoot, ChronicleDir: paths.DirAt(r.o.RepoRoot), Domain: d.Key, Manifest: r.m,
		HeadSHA: r.head, Files: todo, Backend: r.o.Backend, Concurrency: r.o.Concurrency,
		ContextWindow: r.o.ContextWindow, FileTimeout: r.o.FileTimeout,
		OnFileDone: r.o.OnFileDone, Guide: guide, VersionFor: versionFrom(guide), IsTest: r.isTest(d),
		OnFiles: func(fs []headless.FileUsage) {
			ms := MetricsFrom(model, fs, time.Now())
			r.rep.Metrics = append(r.rep.Metrics, ms...)
			if r.o.OnFiles != nil {
				r.o.OnFiles(ms)
			}
		},
	})
	if res != nil {
		for _, f := range res.Files {
			switch {
			case f.Status == "failed" || f.Status == "provider_error":
				r.rep.Failed = append(r.rep.Failed, FailedFile{Path: f.Path, Error: f.Error, Domain: d.Key})
			case f.Status == "skipped" && f.Error != "":
				r.rep.Skipped = append(r.rep.Skipped, FailedFile{Path: f.Path, Error: f.Error, Domain: d.Key})
			case f.Status == "cancelled", f.Status == "skipped":
				// not done, not an error: the next run picks it up
			default:
				dr.Extracted++
			}
		}
	}
	if runErr != nil {
		return fmt.Errorf("scan: extract domain %s: %w", d.Key, runErr)
	}
	return nil
}

// isTest is the domain's test-glob predicate (nil without test globs).
func (r *fullRun) isTest(d manifest.DomainEntry) func(string) bool {
	cfg, ok := r.m.ScanConfigFor(d.Name)
	if !ok || cfg == nil || len(cfg.Tests) == 0 {
		return nil
	}
	return cfg.IsTest
}

// limitationsFor says what a scan did not do, honestly.
func limitationsFor(structuralComplete bool, failed int) []string {
	var out []string
	if !structuralComplete {
		out = append(out, "structural pass did not complete")
	}
	if failed > 0 {
		out = append(out, fmt.Sprintf("%d files failed extraction and will be retried by the next run", failed))
	}
	return append(out, "trace_flow phase not run")
}

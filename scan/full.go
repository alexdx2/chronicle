package scan

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
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
	// OnFiles is called after each committed wave with that wave's metrics.
	OnFiles func([]FileMetric)
}

// FailedFile is a file the scan could not finish, or deliberately passed over.
type FailedFile struct {
	Path  string `json:"path"`
	Error string `json:"error"`
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
	// Complete: no failed files, the structural pass finished everywhere and
	// the context was not cancelled.
	Complete bool `json:"complete"`
}

// scanExtractorID and the version string are the coverage identity the
// headless pipeline records; a file is "done" when both match and its blob is
// unchanged.
const scanExtractorID = "chronicle-scan"

func scanExtractorVersion() string { return fmt.Sprintf("fact-schema@%d", store.FactSchemaVersion) }

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
	rep.Limitations = append(run.extra, limitationsFor(run.unmatched, run.structuralOK, len(rep.Failed))...)
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
	g    *graph.Graph
	o    FullOptions
	m    *manifest.Manifest
	env  Env
	head string
	rep  *FullReport

	extra        []string // limitations beyond limitationsFor's
	unmatched    int
	structuralOK bool
	started      bool // at least one domain began, so structuralOK is meaningful
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
	cov, err := st.ListCoverage(d.Key)
	if err != nil {
		return fmt.Errorf("scan: list coverage: %w", err)
	}
	covered := map[string]string{} // path -> blob sha of a finished chronicle-scan observation
	for _, c := range cov {
		if c.ExtractorID == scanExtractorID && c.ExtractorVersion == scanExtractorVersion() {
			covered[c.FilePath] = c.BlobSHA
		}
	}
	var todo, done []string
	for _, p := range list {
		blob, berr := gitutil.FileBlobSHA(r.o.RepoRoot, p)
		if want, ok := covered[p]; ok && berr == nil && want == blob {
			done = append(done, p)
		} else {
			todo = append(todo, p)
		}
	}
	dr.AlreadyCovered = len(done)
	dr.Sent = len(todo)

	// The pipeline counts a failed file's artifact as a satisfied obligation, so
	// on a revision that already exists for this head only the obligations of
	// really covered files may survive; otherwise a rerun would silently skip
	// the files that failed.
	if rev, rerr := st.GetRevisionBySHA(d.Key, r.head); rerr == nil {
		if _, derr := st.DeleteScanFileObligationsForRevision(rev.RevisionID, done); derr != nil {
			return fmt.Errorf("scan: reopen revision %d: %w", rev.RevisionID, derr)
		}
	} else if !errors.Is(rerr, store.ErrNotFound) {
		return fmt.Errorf("scan: look up revision at %s: %w", ShortSHA(r.head), rerr)
	}

	switch {
	case r.o.NoExtract:
		dr.Sent = 0
		r.addExtra("semantic extraction not run (structural pass only)")
	case len(todo) == 0:
		r.addExtra("nothing to extract: every in-scope file already covered at its current blob")
	default:
		if err := r.extract(ctx, d, todo, &dr); err != nil {
			return err
		}
	}

	unmatched, _ := r.g.FindUnmatchedHTTPCalls(d.Key)
	dr.UnmatchedHTTPCalls = len(unmatched)
	r.unmatched += len(unmatched)
	return ctx.Err()
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
		list = append(list, p)
	}
	return list, nil
}

// extract runs the headless pipeline over todo and folds the per-file outcomes
// into the report. A cancelled or failed run still contributes what it did.
func (r *fullRun) extract(ctx context.Context, d manifest.DomainEntry, todo []string, dr *DomainReport) error {
	model := r.o.Backend.Name() + ":" + r.o.Backend.Model()
	res, runErr := headless.Run(ctx, headless.RunInput{
		G: r.g, RepoRoot: r.o.RepoRoot, ChronicleDir: paths.DirAt(r.o.RepoRoot), Domain: d.Key, Manifest: r.m,
		HeadSHA: r.head, Files: todo, Backend: r.o.Backend, Concurrency: r.o.Concurrency,
		ContextWindow: r.o.ContextWindow, FileTimeout: r.o.FileTimeout,
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
				r.rep.Failed = append(r.rep.Failed, FailedFile{Path: f.Path, Error: f.Error})
			case f.Status == "skipped" && f.Error != "":
				r.rep.Skipped = append(r.rep.Skipped, FailedFile{Path: f.Path, Error: f.Error})
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

// limitationsFor says what a scan did not do, honestly.
func limitationsFor(unmatched int, structuralComplete bool, failed int) []string {
	var out []string
	if !structuralComplete {
		out = append(out, "structural pass did not complete")
	}
	if failed > 0 {
		out = append(out, fmt.Sprintf("%d files failed extraction and will be retried by the next run", failed))
	}
	if unmatched > 0 {
		out = append(out, fmt.Sprintf("%d HTTP calls could not be matched to an endpoint deterministically; the LLM reconcile_endpoints phase is not run by scan --full", unmatched))
	}
	return append(out, "trace_flow phase not run")
}

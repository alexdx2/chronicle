package surface

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
)

// ImportOptions configures one surface import.
type ImportOptions struct {
	Domain          string
	RepoDir         string // where git runs for the ancestor check ("" = skip git checks, tests only)
	AllowUnresolved bool
	AllowDiverged   bool
}

// ImportResult is what one import did (or refused to do).
type ImportResult struct {
	RevisionID      int64        `json:"revision_id"`
	AlreadyImported bool         `json:"already_imported"`
	ReusedRevision  bool         `json:"reused_revision,omitempty"`
	Product         string       `json:"product"`
	Commit          string       `json:"commit"`
	ContentHash     string       `json:"content_hash"`
	Nodes           int          `json:"nodes"`
	Edges           int          `json:"edges"`
	Evidence        int          `json:"evidence"`
	Deleted         []string     `json:"deleted"`              // ui node keys of this product absent from the file
	Unresolved      []Unresolved `json:"unresolved,omitempty"` // names the graph could not confirm
	Diverged        bool         `json:"diverged"`
}

// ErrAlreadyImported names the condition Import reports through
// ImportResult.AlreadyImported: this exact extract, at this exact commit, is
// already in the graph. Import returns the result rather than this error so
// the caller still sees the revision it landed under; the sentinel exists for
// callers that want to compare.
var ErrAlreadyImported = errors.New("already imported")

// ErrCommitChanged is the refusal that keeps knowledge honest: the extract's
// bytes changed but the commit it claims did not, so one commit would mean two
// different surfaces.
var ErrCommitChanged = errors.New("same commit, different content — regenerate the extract at a new commit")

// ErrDiverged is returned when the extract describes a commit that is not in
// HEAD's history — a surface generated on a branch nobody merged.
var ErrDiverged = errors.New("surface commit is not an ancestor of HEAD")

// Import writes one product's surface extract into the ui layer.
//
// Order of business: check the commit is real and reachable, check we have not
// already been told this exact thing, plan (which resolves every name against
// the graph and fails loudly if one does not resolve), validate the whole
// payload BEFORE opening a revision, write, close the world, and only then
// record what was imported.
//
// Two properties that order buys, and that the obvious order does not:
//   - a payload the registry would partly reject never reaches the graph, so
//     there is no half-imported surface to explain afterwards;
//   - the "I have seen this extract" mark is written LAST, so any failure —
//     rejected item, tombstone error, crash — leaves the import retryable
//     instead of silently "already done".
//
// Nothing outside the ui layer is written. The data and contract nodes the
// plan points at are read, never touched.
func Import(g *graph.Graph, f *File, o ImportOptions) (*ImportResult, error) {
	s := g.Store()
	domain := o.Domain
	if domain == "" {
		d, err := defaultDomain(s)
		if err != nil {
			return nil, err
		}
		domain = d
	}

	res := &ImportResult{
		Product:     f.Product,
		Commit:      f.Commit,
		ContentHash: f.ContentHash,
		Deleted:     []string{},
	}

	// (1) Is this commit real, and is it in HEAD's history?
	if o.RepoDir != "" {
		if err := gitHasCommit(o.RepoDir, f.Commit); err != nil {
			return nil, err
		}
		ancestor, err := gitIsAncestor(o.RepoDir, f.Commit)
		if err != nil {
			return nil, err
		}
		if !ancestor {
			if !o.AllowDiverged {
				return nil, fmt.Errorf("surface: commit %s: %w", f.Commit, ErrDiverged)
			}
			res.Diverged = true
		}
	}

	// (2) Have we been told this already? The answer cannot live on the
	// revision row: the store allows ONE revision per (domain, commit), so
	// when a scan already named this commit the import has no row of its own
	// to stamp — which is exactly the flow this is for (scan at HEAD, then
	// import the surface generated at HEAD). It lives in project_settings,
	// keyed by domain+product+commit, and is written only after a fully
	// successful import.
	hashKey := importedHashKey(domain, f.Product, f.Commit)
	if prev, err := s.GetSetting(hashKey); err == nil && prev != "" {
		if prev == f.ContentHash {
			res.AlreadyImported = true
			if rev, rerr := s.GetRevisionBySHA(domain, f.Commit); rerr == nil {
				res.RevisionID = rev.RevisionID
			}
			return res, nil
		}
		return nil, fmt.Errorf("surface: commit %s (imported content %s, this file is %s): %w",
			f.Commit, short(prev), short(f.ContentHash), ErrCommitChanged)
	}

	// (3) Resolve every name the extract uses. Nothing is written if one fails
	// and the caller did not opt in to a partial import.
	r := &Resolver{Store: s, Domain: domain}
	payload, fileKeys, unresolved, err := Plan(f, r, Options{Domain: domain, AllowUnresolved: o.AllowUnresolved})
	if err != nil {
		return nil, err
	}
	res.Unresolved = unresolved

	// (4) Validate the WHOLE payload before anything is created. ImportAll
	// commits what it accepts and merely reports what it rejects, so checking
	// afterwards would leave a half-written surface behind and a revision
	// naming it.
	dry, err := g.ImportAllDryRun(payload, 0)
	if err != nil {
		return nil, fmt.Errorf("surface: validating the import: %w", err)
	}
	if !dry.Valid {
		return nil, fmt.Errorf("surface: %d item(s) the registry will not accept: %s",
			len(dry.Errors), strings.Join(dry.Errors, "; "))
	}

	// (5) Name the knowledge by the commit it describes — or, when a scan or
	// refresh already named this commit, ride along on that one rather than
	// inventing a second name for the same point in history.
	revID := int64(0)
	prior, perr := s.GetRevisionBySHA(domain, f.Commit)
	switch {
	case perr == nil:
		revID = prior.RevisionID
		res.ReusedRevision = true
	case errors.Is(perr, store.ErrNotFound):
		meta, _ := json.Marshal(revisionMeta{
			Layer:         "ui",
			Source:        f.Path,
			SchemaVersion: f.SchemaVersion,
			ContentHash:   f.ContentHash,
			Product:       f.Product,
		})
		revID, err = s.CreateRevision(domain, "", f.Commit, "manual", "incremental", string(meta))
		if err != nil {
			return nil, fmt.Errorf("surface: create revision: %w", err)
		}
	default:
		return nil, fmt.Errorf("surface: looking up revision for %s: %w", f.Commit, perr)
	}
	res.RevisionID = revID

	// (6) Write. A rejection here is a bug (step 4 just validated the same
	// payload), so it fails loudly — and, because the hash is not written, the
	// next run retries instead of reporting "already imported".
	imported, err := g.ImportAll(payload, revID)
	if err != nil {
		return nil, fmt.Errorf("surface: import: %w", err)
	}
	if len(imported.Rejected) > 0 {
		var lines []string
		for _, rj := range imported.Rejected {
			lines = append(lines, rj.Error)
		}
		return nil, fmt.Errorf("surface: %d item(s) rejected by the registry after validation passed — this is a bug: %s",
			len(imported.Rejected), strings.Join(lines, "; "))
	}
	res.Nodes = imported.NodesCreated
	res.Edges = imported.EdgesCreated
	res.Evidence = imported.EvidenceCreated

	// (7) Closed world, per product: the file is the whole truth about this
	// product's surface, so a ui node it stopped mentioning is gone.
	deleted, err := closeWorld(s, domain, f.Product, fileKeys)
	if err != nil {
		return nil, err
	}
	res.Deleted = deleted

	// (8) Only now: this exact extract, at this exact commit, is in.
	if err := s.SetSetting(hashKey, f.ContentHash); err != nil {
		return nil, fmt.Errorf("surface: recording the imported content hash: %w", err)
	}
	return res, nil
}

// importedHashKey names the content hash of the extract last imported for one
// product at one commit. Keyed independently of the revision, because a
// revision row is not always available to stamp (see step 2).
func importedHashKey(domain, product, commit string) string {
	return "surface:" + domain + ":" + product + ":" + commit
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// revisionMeta is the metadata a surface import stamps on its revision — what
// was imported, from where, and the exact bytes it was.
type revisionMeta struct {
	Layer         string `json:"layer"`
	Source        string `json:"source"`
	SchemaVersion int    `json:"schema_version"`
	ContentHash   string `json:"content_hash"`
	Product       string `json:"product"`
}

// closeWorld tombstones every active ui node of this product that the extract
// no longer names. Other products' ui nodes in the same domain are untouched —
// the metadata each node carries is what keeps the blast radius at one product.
func closeWorld(s *store.Store, domain, product string, fileKeys []string) ([]string, error) {
	present := make(map[string]bool, len(fileKeys))
	for _, k := range fileKeys {
		present[k] = true
	}
	rows, err := s.ListNodes(store.NodeFilter{Layer: "ui", Domain: domain, Status: "active"})
	if err != nil {
		return nil, fmt.Errorf("surface: listing ui nodes: %w", err)
	}
	var deleted []string
	for _, row := range rows {
		var md struct {
			Product string `json:"product"`
		}
		_ = json.Unmarshal([]byte(row.Metadata), &md)
		if md.Product != product || present[row.NodeKey] {
			continue
		}
		if err := s.DeleteNode(row.NodeKey); err != nil {
			return nil, fmt.Errorf("surface: tombstoning %s: %w", row.NodeKey, err)
		}
		deleted = append(deleted, row.NodeKey)
	}
	sort.Strings(deleted)
	if deleted == nil {
		deleted = []string{}
	}
	return deleted, nil
}

// defaultDomain resolves the domain when the caller named none: only a store
// with exactly one domain can answer, because guessing which graph a product's
// surface belongs to is not a guess worth making.
func defaultDomain(s *store.Store) (string, error) {
	domains, err := s.GetDomains()
	if err != nil {
		return "", fmt.Errorf("surface: listing domains: %w", err)
	}
	switch len(domains) {
	case 1:
		return domains[0], nil
	case 0:
		return "", fmt.Errorf("surface: the graph has no domain yet — scan the repo before importing its surface")
	default:
		return "", fmt.Errorf("surface: %d domains (%s) — name one with --domain", len(domains), strings.Join(domains, ", "))
	}
}

func gitHasCommit(dir, commit string) error {
	cmd := exec.Command("git", "-C", dir, "cat-file", "-e", commit+"^{commit}")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("surface: commit %s is not in %s: %v %s", commit, dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitIsAncestor reports whether commit is reachable from HEAD. git exits 1 for
// "no" and something else for a real failure, which must not read as "no".
func gitIsAncestor(dir, commit string) (bool, error) {
	cmd := exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", commit, "HEAD")
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("surface: git merge-base in %s: %w", dir, err)
}

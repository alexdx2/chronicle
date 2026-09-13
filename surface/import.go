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
// the graph and fails loudly if one does not resolve), open a revision named
// by the extract's commit, write, then close the world — every ui node of this
// product that the file no longer mentions is tombstoned.
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

	// (2) Have we been told this already? One revision per (domain, commit) is
	// a hard rule of the store, so the answer lives on that row's metadata.
	reuseRevision := int64(0)
	prior, err := s.GetRevisionBySHA(domain, f.Commit)
	switch {
	case err == nil:
		var md revisionMeta
		_ = json.Unmarshal([]byte(prior.Metadata), &md)
		if md.Layer == "ui" && md.Product == f.Product {
			if md.ContentHash == f.ContentHash {
				res.RevisionID = prior.RevisionID
				res.AlreadyImported = true
				return res, nil
			}
			return nil, fmt.Errorf("surface: commit %s (revision %d): %w",
				f.Commit, prior.RevisionID, ErrCommitChanged)
		}
		// A scan or refresh already named this commit. The store allows one
		// revision per commit, so this import rides along on that one rather
		// than inventing a second name for the same point in history.
		reuseRevision = prior.RevisionID
	case errors.Is(err, store.ErrNotFound):
		// first time at this commit
	default:
		return nil, fmt.Errorf("surface: looking up revision for %s: %w", f.Commit, err)
	}

	// (3) Resolve every name the extract uses. Nothing is written if one fails
	// and the caller did not opt in to a partial import.
	r := &Resolver{Store: s, Domain: domain}
	payload, fileKeys, unresolved, err := Plan(f, r, Options{Domain: domain, AllowUnresolved: o.AllowUnresolved})
	if err != nil {
		return nil, err
	}
	res.Unresolved = unresolved

	// (4) Name the knowledge by the commit it describes.
	revID := reuseRevision
	if revID == 0 {
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
	} else {
		res.ReusedRevision = true
	}
	res.RevisionID = revID

	// (5) Write.
	imported, err := g.ImportAll(payload, revID)
	if err != nil {
		return nil, fmt.Errorf("surface: import: %w", err)
	}
	if len(imported.Rejected) > 0 {
		var lines []string
		for _, rj := range imported.Rejected {
			lines = append(lines, rj.Error)
		}
		return nil, fmt.Errorf("surface: %d item(s) rejected by the registry: %s",
			len(imported.Rejected), strings.Join(lines, "; "))
	}
	res.Nodes = imported.NodesCreated
	res.Edges = imported.EdgesCreated
	res.Evidence = imported.EvidenceCreated

	// (6) Closed world, per product: the file is the whole truth about this
	// product's surface, so a ui node it stopped mentioning is gone.
	deleted, err := closeWorld(s, domain, f.Product, fileKeys)
	if err != nil {
		return nil, err
	}
	res.Deleted = deleted
	return res, nil
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

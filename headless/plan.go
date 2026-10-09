// Package headless runs Chronicle's extraction pipeline without an interactive
// agent: it decides which files an LLM reads (this file), then drives the
// extraction over them.
package headless

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/manifest"
)

const (
	defaultRadius   = 1
	defaultMaxFiles = 80
	defaultMaxBytes = int64(1 << 20)
)

// PlanInput describes one extraction plan request.
type PlanInput struct {
	G        *graph.Graph
	RepoRoot string
	Domain   string
	Scan     *manifest.ScanConfig // from Manifest.ScanConfigFor(Domain) or MergedScanConfig()
	Base     string               // git rev
	Head     string               // git rev ("" = working tree)
	Radius   int                  // 0 → 1
	MaxFiles int                  // 0 → 80
	MaxBytes int64                // 0 → 1 MiB
}

// PlanFile is one file in a plan with the reason it is (not) read.
type PlanFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"` // "changed", "neighbor:<node-key>", "skipped:budget", "skipped:scope", "skipped:missing"
	Bytes  int64  `json:"bytes"`
}

// Plan is the deterministic list of files the LLM reads, plus what was left out.
type Plan struct {
	Domain     string     `json:"domain"`
	Base       string     `json:"base"`
	Head       string     `json:"head"`
	Radius     int        `json:"radius"`
	Files      []PlanFile `json:"files"` // to read, deterministic order
	Skipped    []PlanFile `json:"skipped"`
	Changed    int        `json:"changed"`
	Neighbors  int        `json:"neighbors"`
	TotalBytes int64      `json:"total_bytes"`
}

// Paths returns the paths of Files, in order.
func (p *Plan) Paths() []string {
	out := make([]string, 0, len(p.Files))
	for _, f := range p.Files {
		out = append(out, f.Path)
	}
	return out
}

// BuildPlan picks the files to read: the files changed between Base and Head
// (deleted ones dropped, renames by new path) followed by the files backing the
// graph neighbours of the nodes those files hold, within Radius. Scope and
// existence filters apply to both; MaxFiles/MaxBytes cap the result in that
// order (changed first, then neighbours, each sorted by path).
func BuildPlan(in PlanInput) (*Plan, error) {
	if in.G == nil {
		return nil, fmt.Errorf("headless: BuildPlan needs a graph")
	}
	radius, maxFiles, maxBytes := in.Radius, in.MaxFiles, in.MaxBytes
	if radius <= 0 {
		radius = defaultRadius
	}
	if maxFiles <= 0 {
		maxFiles = defaultMaxFiles
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	p := &Plan{Domain: in.Domain, Base: in.Base, Head: in.Head, Radius: radius,
		Files: []PlanFile{}, Skipped: []PlanFile{}}

	diff, err := gitdiff.ChangedFiles(in.RepoRoot, in.Base, in.Head)
	if err != nil {
		return nil, err
	}

	changedAll := map[string]bool{} // every non-deleted changed path, in scope or not
	var changed []string            // in-scope, existing
	type skip struct{ path, reason string }
	var skips []skip
	for _, cf := range diff {
		if cf.Status == "D" {
			continue
		}
		changedAll[cf.Path] = true
		if !graph.InScope(cf.Path, in.Scan) {
			skips = append(skips, skip{cf.Path, "skipped:scope"})
			continue
		}
		changed = append(changed, cf.Path)
	}
	sort.Strings(changed)

	size := map[string]int64{}
	var live []string
	for _, path := range changed {
		st, err := os.Stat(filepath.Join(in.RepoRoot, path))
		if err != nil || st.IsDir() {
			skips = append(skips, skip{path, "skipped:missing"})
			continue
		}
		size[path] = st.Size()
		live = append(live, path)
	}

	// Neighbours of the nodes backed by the changed files.
	neighborReason := map[string]string{} // path → reason (first key in sorted order wins)
	if len(live) > 0 {
		byFile, err := in.G.Store().NodeKeysByFilePaths(live)
		if err != nil {
			return nil, fmt.Errorf("headless: nodes by file: %w", err)
		}
		keySet := map[string]bool{}
		for _, ks := range byFile {
			for _, k := range ks {
				keySet[k] = true
			}
		}
		keys := sortedKeys(keySet)
		for _, key := range keys {
			nb := map[string]bool{}
			down, err := in.G.QueryDeps(key, radius, nil)
			if err != nil {
				return nil, fmt.Errorf("headless: deps of %s: %w", key, err)
			}
			up, err := in.G.QueryReverseDeps(key, radius, nil)
			if err != nil {
				return nil, fmt.Errorf("headless: reverse deps of %s: %w", key, err)
			}
			for _, d := range down {
				nb[d.NodeKey] = true
			}
			for _, d := range up {
				nb[d.NodeKey] = true
			}
			delete(nb, key)
			if len(nb) == 0 {
				continue
			}
			nbKeys := sortedKeys(nb)
			files, err := in.G.Store().FilePathsByNodeKeys(nbKeys)
			if err != nil {
				return nil, fmt.Errorf("headless: files of neighbours of %s: %w", key, err)
			}
			for _, nk := range nbKeys {
				for _, fp := range files[nk] {
					if changedAll[fp] {
						continue
					}
					if _, seen := neighborReason[fp]; !seen {
						neighborReason[fp] = "neighbor:" + key
					}
				}
			}
		}
	}

	// Resolve neighbour candidates against disk and scope.
	type cand struct{ path, reason string }
	var cands []cand
	resolved := map[string]bool{}
	for _, fp := range sortedKeysStr(neighborReason) {
		reason := neighborReason[fp]
		path := fp
		st, err := os.Stat(filepath.Join(in.RepoRoot, path))
		if err != nil || st.IsDir() {
			// Nodes created path-keyed store file_path without its extension.
			matches := inScopeSiblings(in.RepoRoot, fp, in.Scan)
			if len(matches) != 1 {
				skips = append(skips, skip{fp, "skipped:missing"})
				continue
			}
			path = matches[0]
			if st, err = os.Stat(filepath.Join(in.RepoRoot, path)); err != nil {
				skips = append(skips, skip{fp, "skipped:missing"})
				continue
			}
		}
		if changedAll[path] || resolved[path] {
			continue
		}
		if !graph.InScope(path, in.Scan) {
			skips = append(skips, skip{path, "skipped:scope"})
			continue
		}
		resolved[path] = true
		size[path] = st.Size()
		cands = append(cands, cand{path, reason})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].path < cands[j].path })

	// Apply caps: changed first, then neighbours.
	admit := func(path, reason string) {
		b := size[path]
		if len(p.Files) >= maxFiles || p.TotalBytes+b > maxBytes {
			p.Skipped = append(p.Skipped, PlanFile{Path: path, Reason: "skipped:budget", Bytes: b})
			return
		}
		p.Files = append(p.Files, PlanFile{Path: path, Reason: reason, Bytes: b})
		p.TotalBytes += b
		if reason == "changed" {
			p.Changed++
		} else {
			p.Neighbors++
		}
	}
	for _, path := range live {
		admit(path, "changed")
	}
	for _, c := range cands {
		admit(c.path, c.reason)
	}

	for _, s := range skips {
		p.Skipped = append(p.Skipped, PlanFile{Path: s.path, Reason: s.reason, Bytes: size[s.path]})
	}
	sort.SliceStable(p.Skipped, func(i, j int) bool {
		if p.Skipped[i].Path != p.Skipped[j].Path {
			return p.Skipped[i].Path < p.Skipped[j].Path
		}
		return p.Skipped[i].Reason < p.Skipped[j].Reason
	})
	return p, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysStr(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// inScopeSiblings lists the in-scope regular files named <stem>.<ext> next to
// an extensionless stored path (repo-relative, slash-separated). It compares
// names directly rather than globbing, so '[' or '*' in a path (Next.js
// [id] routes) cannot change the match.
func inScopeSiblings(repoRoot, stem string, scan *manifest.ScanConfig) []string {
	dir, base := filepath.Split(filepath.ToSlash(stem))
	entries, err := os.ReadDir(filepath.Join(repoRoot, filepath.FromSlash(dir)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), base+".") {
			continue
		}
		rel := dir + e.Name()
		if graph.InScope(rel, scan) {
			out = append(out, rel)
		}
	}
	return out
}

// Package deps reads dependency manifests deterministically: what a
// repository publishes and what it depends on, per ecosystem, with the file
// and line of every entry. No model is involved; the result feeds the graph
// (role "deps" extraction rows), the repository profile and the federation.
package deps

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Version identifies the parser in extraction rows.
const Version = "deps@1"

// Package is something a manifest publishes.
type Package struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// Dependency is one dependency entry of a manifest. Text is the exact
// fragment of the manifest line the parser matched.
type Dependency struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Section   string `json:"section,omitempty"`
	Ecosystem string `json:"ecosystem"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Internal  bool   `json:"internal,omitempty"` // ProjectReference / workspace reference inside the repository
	Text      string `json:"text,omitempty"`
}

// Report is the sorted result of a Scan.
type Report struct {
	Publishes []Package    `json:"publishes"`
	Depends   []Dependency `json:"depends"`
	// Warnings lists manifests that were skipped, as "<file>: <error>".
	Warnings []string `json:"warnings,omitempty"`
}

type parser interface {
	matches(path string) bool
	parse(root, rel string, body []byte) (Report, error)
}

var parsers = []parser{npm{}, nuget{}, gomod{}, python{}, jvm{}}

var osReadFile = os.ReadFile

// IsManifest reports whether path is a manifest some parser understands.
func IsManifest(path string) bool {
	for _, p := range parsers {
		if p.matches(path) {
			return true
		}
	}
	return false
}

// Normalize applies the ecosystem's identity rule.
func Normalize(ecosystem, name string) string {
	name = strings.TrimSpace(name)
	switch ecosystem {
	case "go":
		return name
	default:
		return strings.ToLower(name)
	}
}

// Scan reads the manifests among files (relative paths, as in git ls-files)
// under root. Files that are not manifests are skipped. The result is sorted.
func Scan(root string, files []string) (Report, error) {
	var out Report
	if _, err := os.Stat(root); err != nil {
		return Report{}, fmt.Errorf("deps: root: %w", err)
	}
	for _, rel := range files {
		for _, p := range parsers {
			if !p.matches(rel) {
				continue
			}
			body, err := osReadFile(joinRoot(root, rel))
			if err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %v", rel, err))
				break
			}
			r, err := p.parse(root, rel, stripBOM(body))
			if err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %v", rel, err))
				break
			}
			out.Publishes = append(out.Publishes, r.Publishes...)
			out.Depends = append(out.Depends, r.Depends...)
			break
		}
	}
	fillCentralNuGetVersions(out.Depends)
	sort.Strings(out.Warnings)
	sort.Slice(out.Publishes, func(i, j int) bool {
		a, b := out.Publishes[i], out.Publishes[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	sort.Slice(out.Depends, func(i, j int) bool {
		a, b := out.Depends[i], out.Depends[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return out, nil
}

func stripBOM(b []byte) []byte { return []byte(strings.TrimPrefix(string(b), "\ufeff")) }

// lineOf returns the 1-based line that contains the byte offset, 0 if off is
// out of range (for example a failed strings.Index).
func lineOf(body []byte, off int) int {
	if off < 0 || off > len(body) {
		return 0
	}
	return strings.Count(string(body[:off]), "\n") + 1
}

// base is the last element of a slash-separated manifest path.
func base(p string) string { return path.Base(filepath.ToSlash(p)) }

func joinRoot(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

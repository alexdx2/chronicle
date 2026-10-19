// Package extract walks a source tree and runs deterministic extraction rules.
// It imports only std, truth/..., yaml and toml (see truth Independence test).
package extract

import (
	"io/fs"
	"path"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// FileCtx is what a rule sees for one file.
type FileCtx struct {
	Path  string // slash path relative to the tree root
	Dir   string // slash dir of Path ("." for the root)
	Lines []lex.Line
	Raw   []byte
	// IsTest marks test files. The walker still builds the context and calls
	// the rules; endpoint, http and channels rules must return nil when it is
	// set. Deps and services rules ignore it.
	IsTest bool
	Tree   Tree
}

// Tree gives rules access to neighbouring files.
type Tree interface {
	// ReadFile returns the bytes and true when the file exists and is <= MaxFile.
	ReadFile(path string) ([]byte, bool)
	// Siblings returns base names in dir matching glob (path.Match).
	Siblings(dir, glob string) []string
	// Subdirs returns the base names of the directories directly in dir that
	// the walker would enter (excluded and hidden directories are left out).
	Subdirs(dir string) []string
}

// Rule extracts facts from one file.
type Rule interface {
	Name() string
	Match(path string) bool
	Apply(f *FileCtx) []truth.Fact
}

// Rules is appended to by init() in each rules_*.go file.
var Rules []Rule

// Excluded directory base names are never walked.
var Excluded = []string{"node_modules", "vendor", "dist", "build", "bin", "obj", ".git", ".depbot", "__pycache__", ".venv"}

// MaxFile is the largest file the walker hands to rules.
const MaxFile = 1 << 20

// IsTestPath reports whether p looks like a test file.
func IsTestPath(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		if isTestDir(d) {
			return true
		}
	}
	base := parts[len(parts)-1]
	switch {
	case strings.HasSuffix(base, "Tests.cs"), strings.HasSuffix(base, "Test.java"), strings.HasSuffix(base, "Tests.java"):
		return true
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"):
		return true
	case strings.HasSuffix(base, "Test.kt"), strings.HasSuffix(base, "Tests.kt"), strings.HasSuffix(base, "Spec.kt"), strings.HasSuffix(base, "_test.go"):
		return true
	}
	for _, mid := range []string{".spec", ".test"} {
		for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
			if strings.HasSuffix(base, mid+ext) {
				return true
			}
		}
	}
	return false
}

// isTestDir reports whether a directory name marks tests: test, tests, spec,
// __tests__, a name ending in "tests" (Shop.Api.IntegrationTests) or a
// camel-case / separated "Test" suffix (LoginTest, shop.test). A bare
// lower-case "...test" such as "contest" does not count.
func isTestDir(d string) bool {
	l := strings.ToLower(d)
	switch l {
	case "test", "tests", "spec", "specs", "__tests__":
		return true
	}
	if strings.HasSuffix(l, "tests") {
		return true
	}
	if strings.HasSuffix(d, "Test") {
		return true
	}
	for _, sep := range []string{".", "_", "-"} {
		if strings.HasSuffix(l, sep+"test") {
			return true
		}
	}
	return false
}

type fsTree struct{ fsys fs.FS }

func (t fsTree) ReadFile(p string) ([]byte, bool) {
	st, err := fs.Stat(t.fsys, p)
	if err != nil || st.IsDir() || st.Size() > MaxFile {
		return nil, false
	}
	b, err := fs.ReadFile(t.fsys, p)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (t fsTree) Siblings(dir, glob string) []string {
	ents, err := fs.ReadDir(t.fsys, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if ok, _ := path.Match(glob, e.Name()); ok {
			out = append(out, e.Name())
		}
	}
	return out
}

func (t fsTree) Subdirs(dir string) []string {
	ents, err := fs.ReadDir(t.fsys, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !isExcluded(e.Name()) && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

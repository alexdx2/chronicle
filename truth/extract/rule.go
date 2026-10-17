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
		if d == "tests" || d == "__tests__" {
			return true
		}
	}
	base := parts[len(parts)-1]
	switch {
	case strings.HasSuffix(base, "Tests.cs"):
		return true
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"):
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

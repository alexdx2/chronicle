package graph

import (
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// fileScope limits a finalize file pass to a changed-file set. nil means full
// run (every file). Matching is exact-or-suffix in both directions because
// obligation target keys and node FilePaths may differ in relative/absolute
// form.
type fileScope map[string]bool

// matches reports whether path is inside the scope. A nil scope matches all.
func (s fileScope) matches(path string) bool {
	if s == nil {
		return true
	}
	if s[path] {
		return true
	}
	norm := filepath.ToSlash(path)
	for f := range s {
		fn := filepath.ToSlash(f)
		if strings.HasSuffix(norm, "/"+fn) || strings.HasSuffix(fn, "/"+norm) || fn == norm {
			return true
		}
	}
	return false
}

// revisionFileScope derives the changed-file set for a revision from its file
// obligations (verify_file/scan_file). Empty result means this finalize has no
// known changed set (a full scan) — callers treat that as nil = full run, so
// scoping can only narrow work, never silently skip it.
func revisionFileScope(s *store.Store, revisionID int64) fileScope {
	if revisionID <= 0 {
		return nil
	}
	obligations, err := s.ListAllObligations(revisionID)
	if err != nil {
		return nil
	}
	scope := fileScope{}
	for _, ob := range obligations {
		if ob.ObligationType == "verify_file" || ob.ObligationType == "scan_file" {
			scope[ob.TargetKey] = true
		}
	}
	if len(scope) == 0 {
		return nil
	}
	return scope
}

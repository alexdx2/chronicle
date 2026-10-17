package extract

import (
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// funcRule adapts three functions to the Rule interface.
type funcRule struct {
	name  string
	match func(path string) bool
	apply func(f *FileCtx) []truth.Fact
}

func (r funcRule) Name() string                  { return r.name }
func (r funcRule) Match(p string) bool           { return r.match(p) }
func (r funcRule) Apply(f *FileCtx) []truth.Fact { return r.apply(f) }

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func hasExt(p string, exts ...string) bool {
	lp := strings.ToLower(p)
	for _, e := range exts {
		if strings.HasSuffix(lp, e) {
			return true
		}
	}
	return false
}

func evidence(f *FileCtx, l lex.Line) truth.Evidence {
	return truth.Evidence{File: f.Path, Line: l.Num, Text: strings.TrimSpace(l.Raw)}
}

// endpointFact builds an endpoints fact. path must already be joined.
func endpointFact(rule string, f *FileCtx, l lex.Line, method, path string, derived ...truth.Evidence) truth.Fact {
	method = strings.ToUpper(method)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return truth.Fact{
		Class:   truth.ClassEndpoints,
		Key:     truth.EndpointKey(method, path),
		Attrs:   map[string]string{"method": method, "path": path},
		Primary: evidence(f, l),
		Derived: derived,
		Sources: []string{rule},
	}
}

// joinPath joins prefix and route. When replace is set a route starting with
// "/" or "~/" replaces the prefix (ASP.NET); otherwise the two concatenate.
// Duplicate slashes collapse; the result has a leading "/" and no trailing one.
func joinPath(prefix, route string, replace bool) string {
	var s string
	if replace && (strings.HasPrefix(route, "/") || strings.HasPrefix(route, "~/")) {
		s = strings.TrimPrefix(route, "~")
	} else {
		s = prefix + "/" + route
	}
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	if len(s) > 1 {
		s = strings.TrimRight(s, "/") // s starts with "/" and has no "//", so this keeps at least "/x"
	}
	return s
}

// callText joins the comment-free code of lines[i:] until the parentheses
// opened on or after line i balance out (at most max lines).
func callText(lines []lex.Line, i, max int) string {
	var sb strings.Builder
	depth, seen := 0, false
	for j := i; j < len(lines) && j < i+max; j++ {
		c := lines[j].Code
		sb.WriteString(c)
		sb.WriteByte(' ')
		for _, r := range c {
			switch r {
			case '(':
				depth++
				seen = true
			case ')':
				depth--
			}
		}
		if seen && depth <= 0 {
			break
		}
	}
	return sb.String()
}

func codeContains(f *FileCtx, subs ...string) bool {
	for _, l := range f.Lines {
		lc := strings.ToLower(l.Code)
		for _, s := range subs {
			if strings.Contains(lc, s) {
				return true
			}
		}
	}
	return false
}

// isComposePath reports whether p is a docker-compose / compose YAML file.
func isComposePath(p string) bool {
	b := strings.ToLower(baseName(p))
	return (strings.HasPrefix(b, "docker-compose") || strings.HasPrefix(b, "compose")) &&
		(strings.HasSuffix(b, ".yml") || strings.HasSuffix(b, ".yaml"))
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

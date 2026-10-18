package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

func init() {
	isPy := func(p string) bool { return hasExt(p, ".py") }
	Rules = append(Rules,
		funcRule{"fastapi.decorator", isPy, applyFastAPI},
		funcRule{"flask.route", isPy, applyFlask},
		funcRule{"django.urls", func(p string) bool { return baseName(p) == "urls.py" }, applyDjango},
	)
}

var (
	pyAPIRouter  = regexp.MustCompile(`(\w+)\s*=\s*APIRouter\(`)
	pyBlueprint  = regexp.MustCompile(`(\w+)\s*=\s*Blueprint\(`)
	pyPrefixKw   = regexp.MustCompile(`\bprefix\s*=\s*["']([^"']*)["']`)
	pyURLPrefix  = regexp.MustCompile(`\burl_prefix\s*=\s*["']([^"']*)["']`)
	pyVerbDeco   = regexp.MustCompile(`^\s*@(\w+)\.(get|post|put|delete|patch)\(\s*["']([^"']*)["']`)
	pyAPIRoute   = regexp.MustCompile(`^\s*@(\w+)\.api_route\(\s*["']([^"']*)["']`)
	pyFlaskRoute = regexp.MustCompile(`^\s*@(\w+)\.route\(\s*["']([^"']+)["']`)
	pyMethodsKw  = regexp.MustCompile(`\bmethods\s*=\s*[\[(]([^\])]*)[\])]`)
	pyQuoted     = regexp.MustCompile(`["']([A-Za-z]+)["']`)
	pyNamedGroup = regexp.MustCompile(`\(\?P<(\w+)>[^()]*\)`)
	pyDjangoPath = regexp.MustCompile(`(?:^|[^\w.])(re_)?path\(\s*r?["']([^"']*)["']`)
)

type pyPrefix struct {
	prefix string
	line   lex.Line
	lines  []lex.Line // every line that contributed to prefix (derived evidence)
}

// pyPrefixes collects `name = <ctor>(... <kw>="/p" ...)` assignments.
func pyPrefixes(f *FileCtx, ctor, kw *regexp.Regexp) map[string]pyPrefix {
	out := map[string]pyPrefix{}
	for i, l := range f.Lines {
		m := ctor.FindStringSubmatch(l.Code)
		if m == nil {
			continue
		}
		if p := kw.FindStringSubmatch(callText(f.Lines, i, 8)); p != nil {
			out[m[1]] = pyPrefix{p[1], l, []lex.Line{l}}
		}
	}
	return out
}

func prefixFact(rule string, f *FileCtx, l lex.Line, method, route string, pfx map[string]pyPrefix, recv string) (truth.Fact, bool) {
	p, has := pfx[recv]
	if route == "" && p.prefix == "" {
		return truth.Fact{}, false
	}
	var derived []truth.Evidence
	if has && p.prefix != "" {
		for _, pl := range p.lines {
			derived = append(derived, evidence(f, pl))
		}
	}
	return endpointFact(rule, f, l, method, joinPath(p.prefix, route, false), derived...), true
}

var pyInclude = regexp.MustCompile(`\.include_router\(\s*(\w+)\s*[,)]`)

// applyIncludePrefixes folds same-file `x.include_router(name, prefix="/p")`
// into the router's prefix (the include prefix comes first). A router that is
// included more than once, or without a literal prefix, is left as declared.
func applyIncludePrefixes(f *FileCtx, pfx map[string]pyPrefix) {
	type inc struct {
		prefix string
		line   lex.Line
		n      int
	}
	incs := map[string]*inc{}
	for i, l := range f.Lines {
		if !strings.Contains(l.Code, ".include_router(") {
			continue
		}
		text := callText(f.Lines, i, 8)
		m := pyInclude.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		in := incs[m[1]]
		if in == nil {
			in = &inc{}
			incs[m[1]] = in
		}
		in.n++
		if p := pyPrefixKw.FindStringSubmatch(text); p != nil {
			in.prefix, in.line = p[1], l
		}
	}
	for name, in := range incs {
		if in.n != 1 || in.prefix == "" {
			continue
		}
		own := pfx[name]
		pfx[name] = pyPrefix{
			prefix: joinPath(in.prefix, own.prefix, false),
			line:   own.line,
			lines:  append([]lex.Line{in.line}, own.lines...),
		}
	}
}

func applyFastAPI(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "fastapi") {
		return nil
	}
	pfx := pyPrefixes(f, pyAPIRouter, pyPrefixKw)
	applyIncludePrefixes(f, pfx)
	var out []truth.Fact
	for i, l := range f.Lines {
		if !strings.HasPrefix(strings.TrimSpace(l.Code), "@") {
			continue
		}
		if m := pyVerbDeco.FindStringSubmatch(callText(f.Lines, i, 6)); m != nil {
			if fc, ok := prefixFact("fastapi.decorator", f, l, m[2], m[3], pfx, m[1]); ok {
				out = append(out, fc)
			}
			continue
		}
		if m := pyAPIRoute.FindStringSubmatch(callText(f.Lines, i, 6)); m != nil {
			methods := []string{"GET"}
			if mm := pyMethodsKw.FindStringSubmatch(callText(f.Lines, i, 6)); mm != nil {
				methods = methods[:0]
				for _, q := range pyQuoted.FindAllStringSubmatch(mm[1], -1) {
					methods = append(methods, q[1])
				}
			}
			for _, meth := range methods {
				if fc, ok := prefixFact("fastapi.decorator", f, l, meth, m[2], pfx, m[1]); ok {
					out = append(out, fc)
				}
			}
		}
	}
	return out
}

func applyFlask(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "flask") {
		return nil
	}
	pfx := pyPrefixes(f, pyBlueprint, pyURLPrefix)
	var out []truth.Fact
	for i, l := range f.Lines {
		if !strings.HasPrefix(strings.TrimSpace(l.Code), "@") {
			continue
		}
		m := pyFlaskRoute.FindStringSubmatch(callText(f.Lines, i, 6))
		if m == nil {
			continue
		}
		methods := []string{"GET"}
		if mm := pyMethodsKw.FindStringSubmatch(callText(f.Lines, i, 6)); mm != nil {
			methods = methods[:0]
			for _, q := range pyQuoted.FindAllStringSubmatch(mm[1], -1) {
				methods = append(methods, q[1])
			}
		}
		for _, meth := range methods {
			if fc, ok := prefixFact("flask.route", f, l, meth, m[2], pfx, m[1]); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

func applyDjango(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		m := pyDjangoPath.FindStringSubmatch(l.Code)
		if m == nil {
			continue
		}
		if strings.Contains(callText(f.Lines, i, 4), "include(") {
			continue // a mount point, not an endpoint
		}
		route := m[2]
		if m[1] != "" { // re_path: strip the regex anchors, keep the rest literally
			route = strings.TrimSuffix(strings.TrimPrefix(route, "^"), "$")
			// Named groups become parameters. Any other regex syntax cannot be
			// expressed as a path (and "?" would be cut as a query string), so
			// the pattern is skipped rather than invented.
			route = pyNamedGroup.ReplaceAllString(route, "{$1}")
			if strings.ContainsAny(route, `()[]?*+|\\`) {
				continue
			}
		}
		out = append(out, endpointFact("django.urls", f, l, "ANY", joinPath("", route, false)))
	}
	return out
}

package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

func init() {
	Rules = append(Rules,
		funcRule{"csharp.route_attr", func(p string) bool { return hasExt(p, ".cs") }, applyCSharpRouteAttr},
		funcRule{"csharp.minimal_api", func(p string) bool { return hasExt(p, ".cs") }, applyCSharpMinimalAPI},
	)
}

var (
	csClass      = regexp.MustCompile(`\bclass\s+(\w+)`)
	csVerb       = regexp.MustCompile(`\bHttp(Get|Post|Put|Delete|Patch)\b(?:\s*\(\s*"([^"]*)")?`)
	csRoute      = regexp.MustCompile(`\bRoute\s*\(\s*"([^"]*)"`)
	csAttrPrefix = regexp.MustCompile(`^\s*(?:\[(?:"[^"]*"|[^\]"])*\]\s*)+`)
	csDecl       = regexp.MustCompile(`\b(\w+)\s*\(`)

	csMinimal = regexp.MustCompile(`(\w*)\.Map(Get|Post|Put|Delete|Patch)\(\s*"([^"]+)"`)
	csGroup   = regexp.MustCompile(`\bvar\s+(\w+)\s*=\s*\w+\.MapGroup\(\s*"([^"]+)"`)
)

type csVerbAttr struct {
	line    lex.Line
	method  string
	path    string
	hasPath bool
}

type csRouteAttr struct {
	line lex.Line
	path string
}

func applyCSharpRouteAttr(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	var verbs []csVerbAttr
	var routes []csRouteAttr
	var className, prefix string
	var prefixLine *lex.Line
	reset := func() { verbs, routes = nil, nil }

	for _, l := range f.Lines {
		trimmed := strings.TrimSpace(l.Code)
		if trimmed == "" {
			continue
		}
		rest := l.Code
		if strings.HasPrefix(trimmed, "[") {
			attrs := csAttrPrefix.FindString(l.Code)
			for _, m := range csVerb.FindAllStringSubmatch(attrs, -1) {
				verbs = append(verbs, csVerbAttr{l, m[1], m[2], strings.Contains(m[0], "(")})
			}
			for _, m := range csRoute.FindAllStringSubmatch(attrs, -1) {
				routes = append(routes, csRouteAttr{l, m[1]})
			}
			rest = l.Code[len(attrs):]
			if strings.TrimSpace(rest) == "" {
				continue
			}
		}
		if cm := csClass.FindStringSubmatch(rest); cm != nil {
			className = strings.TrimSuffix(cm[1], "Controller")
			prefix, prefixLine = "", nil
			if len(routes) > 0 {
				prefix = strings.ReplaceAll(routes[0].path, "[controller]", className)
				ln := routes[0].line
				prefixLine = &ln
			}
			reset()
			continue
		}
		if len(verbs)+len(routes) > 0 {
			if dm := csDecl.FindStringSubmatch(rest); dm != nil {
				out = append(out, csEmit(f, className, prefix, prefixLine, dm[1], verbs, routes)...)
			}
		}
		reset()
	}
	return out
}

func csEmit(f *FileCtx, className, prefix string, prefixLine *lex.Line, action string, verbs []csVerbAttr, routes []csRouteAttr) []truth.Fact {
	expand := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "[controller]", className), "[action]", action)
	}
	var out []truth.Fact
	emit := func(l lex.Line, method, route string) {
		route = expand(route)
		if prefix == "" && route == "" {
			return // nothing routes this action; do not invent "/"
		}
		abs := strings.HasPrefix(route, "/") || strings.HasPrefix(route, "~/")
		var derived []truth.Evidence
		if prefix != "" && !abs && prefixLine != nil {
			derived = append(derived, evidence(f, *prefixLine))
		}
		out = append(out, endpointFact("csharp.route_attr", f, l, method, joinPath(prefix, route, true), derived...))
	}
	if len(verbs) == 0 {
		emit(routes[0].line, "GET", routes[0].path)
		return out
	}
	for _, v := range verbs {
		route := v.path
		if !v.hasPath && len(routes) > 0 {
			route = routes[0].path
		}
		emit(v.line, v.method, route)
	}
	return out
}

func applyCSharpMinimalAPI(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	type group struct {
		prefix string
		line   lex.Line
	}
	groups := map[string]group{}
	var out []truth.Fact
	for _, l := range f.Lines {
		if m := csGroup.FindStringSubmatch(l.Code); m != nil {
			groups[m[1]] = group{m[2], l}
		}
		for _, loc := range csMinimal.FindAllStringSubmatchIndex(l.Code, -1) {
			recv := l.Code[loc[2]:loc[3]]
			method := l.Code[loc[4]:loc[5]]
			route := l.Code[loc[6]:loc[7]]
			if recv == "" {
				continue // chained off a call such as MapGroup("/x").MapGet(...): prefix unknown
			}
			if g, ok := groups[recv]; ok {
				out = append(out, endpointFact("csharp.minimal_api", f, l, method, joinPath(g.prefix, route, false), evidence(f, g.line)))
				continue
			}
			if strings.Contains(strings.ToLower(recv), "group") {
				continue // a group declared elsewhere: its prefix is unknown
			}
			out = append(out, endpointFact("csharp.minimal_api", f, l, method, joinPath("", route, false)))
		}
	}
	return out
}

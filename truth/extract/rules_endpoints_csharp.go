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
	csClass = regexp.MustCompile(`\bclass\s+(\w+)(?:\s*<[^>]*>)?(?:\s*:\s*([^{]*))?`)
	// csVerb finds the verb name only; csVerbArgs then reads its argument list.
	// The lead is "[" or "," (optionally a qualified name), never "(" or ".":
	// [Obsolete(Consts.HttpGet)] is not a verb attribute.
	csVerb = regexp.MustCompile(`[\[,]\s*(?:[\w.]+\.)?Http(Get|Post|Put|Delete|Patch)\b`)
	// csVerbArgs accepts (), ("tpl"), (template: "tpl"), optionally followed by
	// Name = ... / Order = ...; any other argument form fails closed.
	csVerbArgs = regexp.MustCompile(`^\s*\(\s*(?:(?:template\s*:\s*)?"([^"]*)"\s*)?(?:,?\s*(?:Name|Order)\s*=\s*[^,)]+)*\s*\)`)
	csRoute    = regexp.MustCompile(`[\[,.]\s*Route\s*\(\s*"([^"]*)"`)
	csAPICtrl  = regexp.MustCompile(`[\[,.]\s*ApiController\b`)
	csDecl     = regexp.MustCompile(`\b(\w+)\s*\(`)

	csMinimal = regexp.MustCompile(`(\w*)\.Map(Get|Post|Put|Delete|Patch)\(\s*"([^"]+)"`)
	csGroup   = regexp.MustCompile(`\b(?:var|[\w<>]+)\s+(\w+)\s*=\s*(\w+)((?:\.MapGroup\(\s*"[^"]+"\s*\))+)`)
	csSegment = regexp.MustCompile(`\.MapGroup\(\s*"([^"]+)"`)
)

// maskStrings blanks the contents of string literals (same length), so
// regexes never match text inside quotes.
func maskStrings(s string) string {
	b := []byte(s)
	in := false
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '\\' && in && i+1 < len(b):
			b[i], b[i+1] = 'x', 'x'
			i++
		case b[i] == '"':
			in = !in
		case in:
			b[i] = 'x'
		}
	}
	return string(b)
}

// consumeAttrs eats consecutive [...] attribute groups at the start of code,
// tracking bracket nesting outside string literals. depth is the nesting left
// open by previous lines. It returns the consumed text, the remainder and the
// nesting still open at the end of the line (an unfinished attribute).
func consumeAttrs(code string, depth int) (consumed, rest string, open int) {
	i, in := 0, false
	if depth == 0 {
		for i < len(code) && (code[i] == ' ' || code[i] == '\t') {
			i++
		}
		if i >= len(code) || code[i] != '[' {
			return "", code, 0
		}
	}
	for i < len(code) {
		c := code[i]
		switch {
		case in && c == '\\':
			i++
		case c == '"':
			in = !in
		case in:
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				j := i + 1
				for j < len(code) && (code[j] == ' ' || code[j] == '\t') {
					j++
				}
				if j < len(code) && code[j] == '[' {
					i = j
					continue
				}
				return code[:i+1], code[i+1:], 0
			}
		}
		i++
	}
	return code, "", depth
}

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
	apiCtrl := false
	openDepth := 0
	var className, prefix string
	customBase := false // controller derives from an unknown base and has no own prefix
	var prefixLine *lex.Line
	reset := func() { verbs, routes, apiCtrl = nil, nil, false }

	for _, l := range f.Lines {
		trimmed := strings.TrimSpace(l.Code)
		if trimmed == "" {
			continue
		}
		rest := l.Code
		startedOpen := openDepth > 0
		if strings.HasPrefix(trimmed, "[") || startedOpen {
			var attrs string
			attrs, rest, openDepth = consumeAttrs(l.Code, openDepth)
			masked := maskStrings(attrs)
			if startedOpen || strings.HasPrefix(trimmed, "[") {
				// the attribute may start mid-group on this line: prefix with "["
				masked = "[" + masked
				attrs = "[" + attrs
			}
			for _, m := range csVerb.FindAllStringSubmatchIndex(masked, -1) {
				// only attribute-list level: not inside another attribute's arguments
				if strings.Count(masked[:m[0]], "(") != strings.Count(masked[:m[0]], ")") {
					continue
				}
				v := csVerbAttr{line: l, method: masked[m[2]:m[3]]}
				tail := masked[m[1]:]
				if strings.HasPrefix(strings.TrimSpace(tail), "(") {
					am := csVerbArgs.FindStringSubmatchIndex(tail)
					if am == nil {
						continue // unknown argument form: fail closed
					}
					if am[2] >= 0 {
						v.path, v.hasPath = attrs[m[1]+am[2]:m[1]+am[3]], true
					}
				}
				verbs = append(verbs, v)
			}
			for _, m := range csRoute.FindAllStringSubmatchIndex(masked, -1) {
				routes = append(routes, csRouteAttr{l, attrs[m[2]:m[3]]})
			}
			if csAPICtrl.MatchString(masked) {
				apiCtrl = true
			}
			if strings.TrimSpace(rest) == "" {
				continue
			}
		}
		if cm := csClass.FindStringSubmatch(rest); cm != nil {
			if strings.HasSuffix(cm[1], "Controller") || len(routes) > 0 || apiCtrl {
				className = strings.TrimSuffix(cm[1], "Controller")
				prefix, prefixLine = "", nil
				customBase = len(routes) == 0 && !apiCtrl && hasCustomBase(cm[2])
				if len(routes) > 0 {
					prefix = strings.ReplaceAll(routes[0].path, "[controller]", className)
					ln := routes[0].line
					prefixLine = &ln
				}
			}
			reset()
			continue
		}
		if len(verbs)+len(routes) > 0 {
			if dm := csDecl.FindStringSubmatch(rest); dm != nil {
				out = append(out, csEmit(f, className, prefix, prefixLine, customBase, dm[1], verbs, routes)...)
			}
		}
		reset()
	}
	return out
}

// hasCustomBase reports whether the base list of a class declaration names a
// class other than ControllerBase/Controller (interfaces are ignored).
func hasCustomBase(list string) bool {
	custom := false
	for _, b := range strings.Split(list, ",") {
		b = strings.TrimSpace(b)
		if i := strings.Index(b, "<"); i >= 0 {
			b = b[:i]
		}
		if i := strings.LastIndex(b, "."); i >= 0 {
			b = b[i+1:]
		}
		switch {
		case b == "":
		case b == "ControllerBase" || b == "Controller":
			return false
		case len(b) > 1 && b[0] == 'I' && b[1] >= 'A' && b[1] <= 'Z':
		default:
			custom = true
		}
	}
	return custom
}

func csEmit(f *FileCtx, className, prefix string, prefixLine *lex.Line, customBase bool, action string, verbs []csVerbAttr, routes []csRouteAttr) []truth.Fact {
	expand := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "[controller]", className), "[action]", action)
	}
	var out []truth.Fact
	emit := func(l lex.Line, method, route string) {
		route = expand(route)
		prefix := expand(prefix) // the class-level prefix may hold [controller] and [action]
		if prefix == "" && route == "" {
			return // nothing routes this action; do not invent "/"
		}
		abs := strings.HasPrefix(route, "/") || strings.HasPrefix(route, "~/")
		if customBase && !abs {
			return // the real prefix lives in an unseen base class
		}
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
		lines  []lex.Line
	}
	roots := map[string]bool{"app": true, "routes": true, "endpoints": true}
	groups := map[string]group{}
	var out []truth.Fact
	evs := func(g group) []truth.Evidence {
		var e []truth.Evidence
		for _, l := range g.lines {
			e = append(e, evidence(f, l))
		}
		return e
	}
	for _, l := range f.Lines {
		if m := csGroup.FindStringSubmatch(l.Code); m != nil && strings.Count(l.Code, ".MapGroup(") == strings.Count(m[3], ".MapGroup(") {
			chain := ""
			for _, sg := range csSegment.FindAllStringSubmatch(m[3], -1) {
				chain = joinPath(chain, sg[1], false)
			}
			if parent, ok := groups[m[2]]; ok {
				groups[m[1]] = group{joinPath(parent.prefix, chain, false), append(append([]lex.Line(nil), parent.lines...), l)}
			} else if roots[m[2]] {
				groups[m[1]] = group{joinPath("", chain, false), []lex.Line{l}}
			}
		}
		for _, loc := range csMinimal.FindAllStringSubmatchIndex(l.Code, -1) {
			recv := l.Code[loc[2]:loc[3]]
			method := l.Code[loc[4]:loc[5]]
			route := l.Code[loc[6]:loc[7]]
			if g, ok := groups[recv]; ok {
				out = append(out, endpointFact("csharp.minimal_api", f, l, method, joinPath(g.prefix, route, false), evs(g)...))
			} else if roots[recv] {
				out = append(out, endpointFact("csharp.minimal_api", f, l, method, joinPath("", route, false)))
			}
		}
	}
	return out
}

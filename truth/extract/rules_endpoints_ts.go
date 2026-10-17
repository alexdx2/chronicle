package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	isJS := func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }
	Rules = append(Rules,
		funcRule{"nest.controller", isJS, applyNest},
		funcRule{"express.router", isJS, applyExpress},
	)
}

var (
	nestController = regexp.MustCompile(`@Controller\s*\(`)
	nestRoute      = regexp.MustCompile(`@(Get|Post|Put|Delete|Patch|All)\s*\(`)
	tsStringOnly   = regexp.MustCompile("^\\s*(['\"`])([^'\"`]*)['\"`]\\s*$")
	tsStringIn     = regexp.MustCompile("['\"`]([^'\"`]*)['\"`]")
	tsObjPath      = regexp.MustCompile(`\bpath\s*:\s*(\[[^\]]*\]|'[^']*'|"[^"]*"|[^\s,}]+)`)
	express        = regexp.MustCompile("\\b(router|app|fastify|server|api)\\.(get|post|put|delete|patch|all)\\(\\s*['\"`]([^'\"`]+)['\"`]")
)

// decoratorArgs returns the literal path list of a Nest decorator whose call
// text starts at the first "(" of text. ok is false for non-literal arguments.
func decoratorArgs(text string) (paths []string, ok bool) {
	i := strings.Index(text, "(")
	if i < 0 {
		return nil, false
	}
	depth, end := 0, -1
	for k := i; k < len(text) && end < 0; k++ {
		switch text[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = k
			}
		}
	}
	if end < 0 {
		return nil, false
	}
	arg := strings.TrimSpace(text[i+1 : end])
	if arg == "" {
		return []string{""}, true
	}
	if strings.HasPrefix(arg, "{") {
		m := tsObjPath.FindStringSubmatch(arg)
		if m == nil {
			return []string{""}, true // options object without a path
		}
		arg = m[1]
	}
	if m := tsStringOnly.FindStringSubmatch(arg); m != nil {
		return []string{m[2]}, true
	}
	if strings.HasPrefix(arg, "[") && strings.HasSuffix(arg, "]") {
		inner := arg[1 : len(arg)-1]
		els := tsStringIn.FindAllStringSubmatch(inner, -1)
		rest := tsStringIn.ReplaceAllString(inner, "")
		if len(els) == 0 || strings.Trim(rest, ", \t") != "" {
			return nil, false
		}
		for _, e := range els {
			paths = append(paths, e[1])
		}
		return paths, true
	}
	return nil, false
}

func applyNest(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "@controller") {
		return nil
	}
	var out []truth.Fact
	var prefixes []string
	var ctrlOK bool
	var ctrlSet bool
	var ctrlLine = -1
	for i, l := range f.Lines {
		if nestController.MatchString(l.Code) {
			txt := callText(f.Lines, i, 6)
			prefixes, ctrlOK = decoratorArgs(txt[strings.Index(txt, "@Controller"):])
			ctrlSet, ctrlLine = true, i
			continue
		}
		m := nestRoute.FindStringSubmatchIndex(l.Code)
		if m == nil || !ctrlSet || !ctrlOK {
			continue
		}
		method := strings.ToUpper(l.Code[m[2]:m[3]])
		txt := callText(f.Lines, i, 4)
		paths, ok := decoratorArgs(txt[strings.Index(txt, "@"+l.Code[m[2]:m[3]]):])
		if !ok {
			continue
		}
		for _, p := range prefixes {
			for _, r := range paths {
				var derived []truth.Evidence
				if p != "" {
					derived = append(derived, evidence(f, f.Lines[ctrlLine]))
				}
				out = append(out, endpointFact("nest.controller", f, l, method, joinPath(p, r, false), derived...))
			}
		}
	}
	return out
}

func applyExpress(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	frameworkSeen := codeContains(f, "express", "router(", "koa", "hono", "@hapi")
	axiosSeen := codeContains(f, "axios")
	var out []truth.Fact
	for _, l := range f.Lines {
		for _, m := range express.FindAllStringSubmatch(l.Code, -1) {
			recv, method, route := m[1], m[2], m[3]
			if !strings.HasPrefix(route, "/") {
				continue // app.get('port') is a settings getter, not a route
			}
			if recv != "fastify" && !frameworkSeen {
				continue
			}
			if recv == "api" && axiosSeen {
				continue // an HTTP client named api, not a server
			}
			out = append(out, endpointFact("express.router", f, l, method, joinPath("", route, false)))
		}
	}
	return out
}

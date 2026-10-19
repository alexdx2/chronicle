package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Outbound HTTP targets in Go code. A host reaches the code three ways:
//   - a default handed to an env helper: getenv("INVENTORY_URL", "http://inventory:8080"),
//     cmp.Or(os.Getenv("K"), "http://..."), or os.Getenv("K") followed by a fallback;
//   - an absolute URL literal in a client call: http.Get("https://..."),
//     http.NewRequest(method, "https://...", ...), client.Post("https://...", ...);
//   - an absolute URL literal assigned to a URL-named const, var or field.
// Hosts are canonical (host only); local hosts yield no fact.

func init() {
	isGo := func(p string) bool { return hasExt(p, ".go") }
	Rules = append(Rules,
		funcRule{"go.env_default", isGo, applyGoEnvDefault},
		funcRule{"go.http_literal", isGo, applyGoHTTPLiteral},
	)
}

var (
	goEnvCall   = regexp.MustCompile(`\b(?i:(?:get|lookup|must|read)?env(?:ironment)?(?:or|default|string|str|var|value|fallback|with)*)\s*`)
	goCmpOr     = regexp.MustCompile(`\bcmp\.Or\s*`)
	goGetenvLit = regexp.MustCompile(`\bos\.Getenv\(\s*"([^"]*)"\s*\)`)
	goAssignEnv = regexp.MustCompile(`\b(\w+)\s*(?::=|=)\s*os\.Getenv\(\s*"([^"]*)"\s*\)`)
	goReturnLit = regexp.MustCompile(`^\s*return\s+"([^"]*)"\s*$`)

	goClientVerb = regexp.MustCompile(`\.(?:Get|Post|Head|PostForm|Put|Delete|Patch)\s*`)
	goNewRequest = regexp.MustCompile(`\bhttp\.NewRequest(?:WithContext)?\s*`)
	goURLNamed   = regexp.MustCompile(`\b(\w*(?:URL|Url|Uri|URI|Endpoint|Host|BaseAddr))\s*(?::=|=|:)\s*"((?i:https?|grpcs?)://[^"]*)"`)
)

func goAbsURL(s string) (string, bool) {
	m := goStringLit.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || !urlScheme.MatchString(m[1]) {
		return "", false
	}
	return m[1], true
}

func goHTTPFact(rule string, f *FileCtx, l lex.Line, key, val string) []truth.Fact {
	if !anyServiceURL(key, val) {
		return nil
	}
	if fc, ok := httpFact(rule, f, l, val, key); ok {
		return []truth.Fact{fc}
	}
	return nil
}

func applyGoEnvDefault(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	// helper(key, default)
	goCalls(f, goEnvCall, func(l lex.Line, args []string) {
		if len(args) < 2 {
			return
		}
		key := goStringLit.FindStringSubmatch(strings.TrimSpace(args[0]))
		url, ok := goAbsURL(args[1])
		if key == nil || !ok {
			return
		}
		out = append(out, goHTTPFact("go.env_default", f, l, key[1], url)...)
	})
	// cmp.Or(os.Getenv("K"), "default")
	goCalls(f, goCmpOr, func(l lex.Line, args []string) {
		key := ""
		for _, a := range args {
			if m := goGetenvLit.FindStringSubmatch(a); m != nil {
				key = m[1]
			}
		}
		for _, a := range args {
			if url, ok := goAbsURL(a); ok && key != "" {
				out = append(out, goHTTPFact("go.env_default", f, l, key, url)...)
			}
		}
	})
	for i, l := range f.Lines {
		// v := os.Getenv("K") ... if v == "" { v = "default" }
		if m := goAssignEnv.FindStringSubmatch(l.Code); m != nil {
			fallback := regexp.MustCompile(`\b` + m[1] + `\s*==\s*""[^;]*?\b` + m[1] + `\s*=\s*"((?i:https?|grpcs?)://[^"]*)"`)
			if fm := fallback.FindStringSubmatch(codeWindow(f.Lines, i, 5)); fm != nil {
				out = append(out, goHTTPFact("go.env_default", f, l, m[2], fm[1])...)
				continue
			}
		}
		// os.Getenv("K") ... return "default"
		if m := goGetenvLit.FindStringSubmatch(l.Code); m != nil {
			for j := i + 1; j < len(f.Lines) && j <= i+5; j++ {
				c := f.Lines[j].Code
				if strings.HasPrefix(strings.TrimSpace(c), "func ") {
					break
				}
				if rm := goReturnLit.FindStringSubmatch(c); rm != nil {
					if urlScheme.MatchString(rm[1]) {
						out = append(out, goHTTPFact("go.env_default", f, l, m[1], rm[1])...)
					}
					break
				}
			}
		}
	}
	return out
}

func applyGoHTTPLiteral(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	goCalls(f, goClientVerb, func(l lex.Line, args []string) {
		if len(args) > 0 {
			if url, ok := goAbsURL(args[0]); ok {
				out = append(out, goHTTPFact("go.http_literal", f, l, "", url)...)
			}
		}
	})
	goCalls(f, goNewRequest, func(l lex.Line, args []string) {
		// NewRequest(method, url, body) / NewRequestWithContext(ctx, method, url, body)
		for _, a := range args {
			if url, ok := goAbsURL(a); ok {
				out = append(out, goHTTPFact("go.http_literal", f, l, "", url)...)
				return
			}
		}
	})
	for _, l := range f.Lines {
		for _, m := range goURLNamed.FindAllStringSubmatch(l.Code, -1) {
			out = append(out, goHTTPFact("go.http_literal", f, l, m[1], m[2])...)
		}
	}
	return out
}

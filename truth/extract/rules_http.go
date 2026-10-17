package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Outbound HTTP targets (grpc:// and grpcs:// hosts are accepted under class http).
// A fact is keyed by the canonical host of a literal
// URL, or by "var:NAME" when the host (or the whole config value) is a
// template such as ${NAME}, %NAME% or {NAME}. Local hosts yield no fact.

var (
	urlScheme  = regexp.MustCompile(`(?i)^(https?|grpcs?)://`)
	tmplWhole  = regexp.MustCompile(`^(?:\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?[-=?+][^{}]*)?\}|%([A-Za-z_][A-Za-z0-9_]*)%|\{([A-Za-z_][A-Za-z0-9_]*)\}|\$\(([A-Za-z_][A-Za-z0-9_]*)\))$`)
	hostLit    = regexp.MustCompile(`^(?:[a-z0-9]([a-z0-9._-]*[a-z0-9])?|\[[0-9a-f:]+\])$`)
	portSuffix = regexp.MustCompile(`:[0-9]*$`)
)

// templateVar returns the variable name when s is exactly one template.
func templateVar(s string) (string, bool) {
	m := tmplWhole.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	for _, g := range m[1:] {
		if g != "" {
			return g, true
		}
	}
	return "", false
}

// httpTarget classifies a URL or config value. kind is "host" (literal host),
// "var" (template) or "" (not a usable target). allowBare lets a value that is
// only a template count (config values).
func httpTarget(value string, allowBare bool) (key, host, path, variable string, ok bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return
	}
	if name, isT := templateVar(v); isT {
		if !allowBare {
			return
		}
		return "var:" + name, "", "", name, true
	}
	loc := urlScheme.FindStringIndex(v)
	if loc == nil {
		return
	}
	rest := v[loc[1]:]
	end := len(rest)
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		end = i
	}
	hostport := rest[:end]
	if i := strings.LastIndex(hostport, "@"); i >= 0 {
		hostport = hostport[i+1:]
	}
	p := rest[end:]
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(hostport, "[") {
		hostport = portSuffix.ReplaceAllString(hostport, "")
	}
	if name, isT := templateVar(hostport); isT {
		return "var:" + name, "", p, name, true
	}
	if strings.ContainsAny(hostport, "${}%") {
		return // partly templated host: not a stable target
	}
	host = truth.CanonHost(v)
	if host == "" || !hostLit.MatchString(host) {
		return
	}
	return host, host, p, "", true
}

// httpFact builds a fact. cfgKey is non-empty for config rules.
func httpFact(rule string, f *FileCtx, l lex.Line, value, cfgKey string) (truth.Fact, bool) {
	key, host, path, variable, ok := httpTarget(value, cfgKey != "")
	if !ok {
		return truth.Fact{}, false
	}
	attrs := map[string]string{"host": host, "path": path, "var": variable}
	if cfgKey != "" {
		attrs["config_key"] = cfgKey // "var" stays the template variable name only
	}
	return truth.Fact{
		Class:   truth.ClassHTTP,
		Key:     key,
		Attrs:   attrs,
		Primary: evidence(f, l),
		Sources: []string{rule},
	}, true
}

func init() {
	isCS := func(p string) bool { return hasExt(p, ".cs") }
	isJS := func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }
	isPy := func(p string) bool { return hasExt(p, ".py") }
	Rules = append(Rules,
		funcRule{"csharp.httpclient_base", isCS, applyCSharpBase},
		funcRule{"csharp.http_literal", isCS, lineRule("csharp.http_literal", csHTTPLiteral)},
		funcRule{"csharp.refit", isCS, lineRule("csharp.refit", csRefit)},
		funcRule{"axios.create", isJS, applyAxiosCreate},
		funcRule{"fetch.literal", isJS, lineRule("fetch.literal", jsFetch)},
		funcRule{"fetch.template", isJS, applyFetchTemplate},
		funcRule{"nest.httpservice", isJS, lineRule("nest.httpservice", nestHTTP)},
		funcRule{"requests.literal", isPy, lineRule("requests.literal", pyRequests)},
	)
}

// jsFetchTpl is fetch(`${NAME}/path`): the whole base is one URL-like constant
// (TOM_API_URL, apiBaseUrl). Names that do not look like a URL/host variable
// are ignored (they are usually relative path prefixes).
var jsFetchTpl = regexp.MustCompile("\\bfetch\\(\\s*`\\$\\{([A-Za-z_][A-Za-z0-9_]*)\\}")

func applyFetchTemplate(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for _, l := range f.Lines {
		for _, m := range jsFetchTpl.FindAllStringSubmatch(l.Code, -1) {
			if !cfgURLKey.MatchString(m[1]) {
				continue
			}
			out = append(out, truth.Fact{
				Class:   truth.ClassHTTP,
				Key:     "var:" + m[1],
				Attrs:   map[string]string{"host": "", "path": "", "var": m[1]},
				Primary: evidence(f, l),
				Sources: []string{"fetch.template"},
			})
		}
	}
	return out
}

const quote = "[\"'`]"

var (
	csBaseAddr    = regexp.MustCompile(`\bBaseAddress\s*=\s*new\s*(?:Uri)?\s*\(\s*\$?@?"((?i:https?|grpcs?)://[^"]*)`)
	csHTTPLiteral = regexp.MustCompile(`\.(?:Get|Post|Put|Delete|Patch|Send)(?:Async)?\(\s*\$?@?"((?i:https?|grpcs?)://[^"]*)`)
	csRefit       = regexp.MustCompile(`RestService\.For<\w+>\(\s*\$?@?"((?i:https?|grpcs?)://[^"]*)`)
	axBase        = regexp.MustCompile(`\bbaseURL\s*:\s*` + quote + `((?i:https?|grpcs?)://[^"'` + "`" + `]*)`)
	jsFetch       = regexp.MustCompile(`\bfetch\(\s*` + quote + `((?i:https?|grpcs?)://[^"'` + "`" + `]*)`)
	nestHTTP      = regexp.MustCompile(`\bthis\.http(?:Service)?\.(?:get|post|put|delete|patch)\(\s*` + quote + `((?i:https?|grpcs?)://[^"'` + "`" + `]*)`)
	pyRequests    = regexp.MustCompile(`\b(?:requests|httpx|session|client)\.(?:get|post|put|delete|patch)\(\s*[fF]?["']((?i:https?|grpcs?)://[^"']*)`)
)

// lineRule applies a one-capture regex to every code line.
func lineRule(name string, re *regexp.Regexp) func(f *FileCtx) []truth.Fact {
	return func(f *FileCtx) []truth.Fact {
		if f.IsTest {
			return nil
		}
		var out []truth.Fact
		for _, l := range f.Lines {
			for _, m := range re.FindAllStringSubmatch(l.Code, -1) {
				if fc, ok := httpFact(name, f, l, m[1], ""); ok {
					out = append(out, fc)
				}
			}
		}
		return out
	}
}

// windowRule matches re on the joined code of a 4-line window (a baseURL more
// than three lines below its axios.create( is not found) that starts at
// every line containing anchor; the fact points at the anchor line.
func windowRule(name, anchor string, re *regexp.Regexp) func(f *FileCtx) []truth.Fact {
	return func(f *FileCtx) []truth.Fact {
		if f.IsTest {
			return nil
		}
		var out []truth.Fact
		for i, l := range f.Lines {
			if !strings.Contains(l.Code, anchor) {
				continue
			}
			var sb strings.Builder
			for j := i; j < len(f.Lines) && j < i+4; j++ {
				sb.WriteString(f.Lines[j].Code)
				sb.WriteByte(' ')
			}
			win := sb.String()
			idx := re.FindStringSubmatchIndex(win)
			// Pin the match to the anchor line: a hit that starts on a later
			// line of the window belongs to that line's own anchor.
			if idx == nil || idx[0] >= len(l.Code) || idx[2] < 0 {
				continue
			}
			if fc, ok := httpFact(name, f, l, win[idx[2]:idx[3]], ""); ok {
				out = append(out, fc)
			}
		}
		return out
	}
}

var (
	applyCSharpBase = windowRule("csharp.httpclient_base", "BaseAddress", csBaseAddr)
	// [^;]*? keeps the gap inside one statement; the fact points at the create line.
	applyAxiosCreate = windowRule("axios.create", "axios.create(", regexp.MustCompile(`axios\.create\([^;]*?`+axBase.String()))
)

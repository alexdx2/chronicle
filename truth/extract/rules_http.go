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
	// A bare ${VAR} value counts only under a URL-like key: POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
	// is an environment pass-through, not an outbound target.
	key, host, path, variable, ok := httpTarget(value, cfgKey != "" && cfgURLKey.MatchString(cfgKey))
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
		funcRule{"csharp.webclient_literal", isCS, lineRule("csharp.webclient_literal", csWebClientLiteral)},
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

// tplCamel: camelCase with a real prefix (apiBaseUrl), never a bare word (url).
var tplCamel = regexp.MustCompile(`^[a-z]+[A-Za-z0-9]*[a-z0-9](?:Url|Uri|Endpoint|Host|BaseAddress)$`)

func tplVarName(n string) bool {
	if strings.Contains(n, "_") {
		return cfgURLKey.MatchString(n) && strings.Trim(n, "_") != ""
	}
	return tplCamel.MatchString(n)
}

func applyFetchTemplate(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for _, l := range f.Lines {
		for _, ix := range jsFetchTpl.FindAllStringSubmatchIndex(l.Code, -1) {
			name := l.Code[ix[2]:ix[3]]
			if !tplVarName(name) {
				continue
			}
			path := l.Code[ix[1]:]
			if i := strings.Index(path, "`"); i >= 0 {
				path = path[:i]
			}
			if i := strings.IndexAny(path, "?#"); i >= 0 {
				path = path[:i]
			}
			out = append(out, truth.Fact{
				Class:   truth.ClassHTTP,
				Key:     "var:" + name,
				Attrs:   map[string]string{"host": "", "path": path, "var": name},
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
	// WebClient / HttpClient calls that the Get/Post/... rule above does not
	// cover (DownloadString, UploadString, GetStringAsync, WebRequest.Create,
	// new HttpRequestMessage(method, url)) with a literal absolute URL.
	csWebClientLiteral = regexp.MustCompile(`(?:\.(?:Download(?:String|Data|File)|Upload(?:String|Data|Values|File)|OpenRead)(?:Async|TaskAsync)?|\.Get(?:String|ByteArray|Stream)Async|\bWebRequest\.Create(?:Http)?|\bnew\s+HttpRequestMessage\(\s*[\w.]+\s*,)\(?\s*(?:new\s+Uri\(\s*)?\$?@?"((?i:https?|grpcs?)://[^"]*)`)
	csRefit            = regexp.MustCompile(`RestService\.For<\w+>\(\s*\$?@?"((?i:https?|grpcs?)://[^"]*)`)
	axBase             = regexp.MustCompile(`\bbaseURL\s*:\s*` + quote + `((?i:https?|grpcs?)://[^"'` + "`" + `]*)`)
	jsFetch            = regexp.MustCompile(`\bfetch\(\s*` + quote + `((?i:https?|grpcs?)://[^"'` + "`" + `]*)`)
	nestHTTP           = regexp.MustCompile(`\bthis\.http(?:Service)?\.(?:get|post|put|delete|patch)\(\s*` + quote + `((?i:https?|grpcs?)://[^"'` + "`" + `]*)`)
	pyRequests         = regexp.MustCompile(`\b(?:requests|httpx|session|client)\.(?:get|post|put|delete|patch)\(\s*[fF]?["']((?i:https?|grpcs?)://[^"']*)`)
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
//
// With atCapture the fact points at the line holding the captured URL and the
// anchor line (when different) becomes derived evidence.
func windowRule(name, anchor string, re *regexp.Regexp, atCapture bool) func(f *FileCtx) []truth.Fact {
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
				if atCapture {
					off, j := 0, i
					for j < len(f.Lines) && off+len(f.Lines[j].Code)+1 <= idx[2] {
						off += len(f.Lines[j].Code) + 1
						j++
					}
					if j != i && j < len(f.Lines) {
						fc.Primary = evidence(f, f.Lines[j])
						fc.Derived = append(fc.Derived, evidence(f, l))
					}
				}
				out = append(out, fc)
			}
		}
		return out
	}
}

var (
	applyCSharpBase = windowRule("csharp.httpclient_base", "BaseAddress", csBaseAddr, false)
	// [^;]*? keeps the gap inside one statement; the fact points at the create line.
	applyAxiosCreate = windowRule("axios.create", "axios.create(", regexp.MustCompile(`axios\.create\([^;]*?`+axBase.String()), true)
)

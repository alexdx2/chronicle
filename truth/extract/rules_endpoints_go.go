package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Go HTTP routes: go-chi (verb calls, Route/Group scopes, Mount of a router
// or function of the same file) and the Go 1.22 net/http mux, whose patterns
// carry the method ("GET /x/{id}").
//
// Sub-routers whose mount point lives in another file are not guessed: a
// router received as a function parameter, or returned as chi.Router, and not
// mounted in the same file yields no fact instead of a wrongly rooted path.

func init() {
	isGo := func(p string) bool { return hasExt(p, ".go") }
	Rules = append(Rules,
		funcRule{"go.chi", isGo, applyGoChi},
		funcRule{"go.http_mux", isGo, applyGoMux},
	)
}

var (
	goWith = `(?:\.With\((?:[^()]|\([^()]*\))*\))*`

	goChiVerb   = regexp.MustCompile(`(\w+)` + goWith + `\.(Get|Post|Put|Delete|Patch|Head|Options|Connect|Trace)\(\s*"([^"]*)"`)
	goChiMethod = regexp.MustCompile(`(\w+)` + goWith + `\.(?:Method|MethodFunc)\(\s*"(\w+)"\s*,\s*"([^"]*)"`)
	goChiRoute  = regexp.MustCompile(`(\w+)` + goWith + `\.Route\(\s*"([^"]*)"\s*,\s*func\(\s*(\w+)\s+\*?chi\.(?:Router|Mux)\b`)
	goChiGroup  = regexp.MustCompile(`(\w+)` + goWith + `\.Group\(\s*func\(\s*(\w+)\s+\*?chi\.(?:Router|Mux)\b`)
	goChiMount  = regexp.MustCompile(`(\w+)\.Mount\(\s*"([^"]*)"\s*,\s*&?(\w+)(\.\w+)?\s*(\()?`)

	goFuncDecl   = regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?(\w+)\s*\(`)
	goNewRouter  = regexp.MustCompile(`(\w+)\s*(?::=|=|:)\s*&?chi\.New(?:Router|Mux)\(`)
	goRouterParm = regexp.MustCompile(`(\w+)\s+\*?chi\.(?:Router|Mux)\b`)
	goRetRouter  = regexp.MustCompile(`\)\s*chi\.Router\b`)
)

// goMasker blanks the content of string, rune and raw-string literals so that
// regexes and brace counting never look inside them. Quotes stay in place.
type goMasker struct{ inRaw bool }

func (m *goMasker) mask(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		switch {
		case m.inRaw:
			if b[i] == '`' {
				m.inRaw = false
			} else {
				b[i] = 'x'
			}
		case b[i] == '`':
			m.inRaw = true
		case b[i] == '"' || b[i] == '\'':
			q := b[i]
			j := i + 1
			for j < len(b) && b[j] != q {
				if b[j] == '\\' && j+1 < len(b) {
					b[j] = 'x'
					j++
				}
				b[j] = 'x'
				j++
			}
			i = j
		}
	}
	return string(b)
}

type goScope struct {
	kind      string // "route", "group", "fn"
	name      string // receiver name (route, group) or function name (fn)
	prefix    string
	closeAt   int // brace depth that closes the scope
	line      lex.Line
	params    map[string]bool // fn: parameters typed chi.Router
	locals    map[string]bool // fn: variables assigned from chi.NewRouter
	subRouter bool            // fn: returns chi.Router
	unknown   bool            // route/group whose parent router has no known prefix
}

type goEvent struct {
	pos  int
	kind string // verb, method, route, group, mount
	m    []string
}

type goChiState struct {
	f       *FileCtx
	masked  []string
	mounts  map[string]string
	globals map[string]bool
	out     []truth.Fact
	emit    bool
}

func goMaskLines(f *FileCtx) []string {
	var mk goMasker
	out := make([]string, len(f.Lines))
	for i, l := range f.Lines {
		out[i] = mk.mask(l.Code)
	}
	return out
}

func applyGoChi(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "go-chi/chi") {
		return nil
	}
	st := &goChiState{f: f, masked: goMaskLines(f), mounts: map[string]string{}, globals: map[string]bool{}}
	for iter := 0; iter < 4; iter++ { // chains of mounts converge in a few passes
		before := len(st.mounts)
		sum := st.sumMounts()
		st.scan()
		if len(st.mounts) == before && st.sumMounts() == sum {
			break
		}
	}
	st.emit = true
	st.scan()
	return st.out
}

func (st *goChiState) sumMounts() string {
	var sb strings.Builder
	for _, k := range sortedKeys(st.mounts) {
		sb.WriteString(k + "=" + st.mounts[k] + ";")
	}
	return sb.String()
}

// lookup resolves the path prefix of a receiver at the current position.
func (st *goChiState) lookup(stack []goScope, recv string) (prefix string, used []lex.Line, ok bool) {
	for i := len(stack) - 1; i >= 0; i-- {
		s := stack[i]
		switch s.kind {
		case "route", "group":
			if s.name == recv {
				if s.unknown {
					return "", nil, false
				}
				for _, outer := range stack[:i+1] {
					if outer.kind == "route" && outer.prefix != "" {
						used = append(used, outer.line)
					}
				}
				return s.prefix, used, true
			}
		case "fn":
			switch {
			case s.locals[recv]:
				if p, mounted := st.mounts[s.name+"."+recv]; mounted {
					return p, nil, true
				}
				return st.mounts[s.name], nil, true // the router a mounted function builds
			case s.params[recv]:
				if p, mounted := st.mounts[s.name]; mounted {
					return p, nil, true
				}
				return "", nil, false
			}
		}
	}
	if st.globals[recv] {
		return st.mounts[recv], nil, true
	}
	return "", nil, false
}

func (st *goChiState) scan() {
	f := st.f
	var stack []goScope
	var pending *goScope
	depth := 0
	for i, l := range f.Lines {
		ml := st.masked[i]
		var win strings.Builder
		for j := i; j < len(st.masked) && j < i+3; j++ {
			win.WriteString(st.masked[j])
			win.WriteByte(' ')
		}
		text := win.String()
		var ev []goEvent
		add := func(kind string, re *regexp.Regexp) {
			for _, ix := range re.FindAllStringSubmatchIndex(text, -1) {
				if ix[0] >= len(ml) {
					continue // belongs to a later line
				}
				m := make([]string, len(ix)/2)
				for k := range m {
					if ix[2*k] >= 0 {
						m[k] = text[ix[2*k]:ix[2*k+1]]
						if isStringCapture(kind, k) {
							m[k] = originalAt(f, i, ix[2*k], ix[2*k+1])
						}
					}
				}
				ev = append(ev, goEvent{ix[0], kind, m})
			}
		}
		add("verb", goChiVerb)
		add("method", goChiMethod)
		add("route", goChiRoute)
		add("group", goChiGroup)
		add("mount", goChiMount)
		if m := goFuncDecl.FindStringSubmatch(ml); m != nil {
			fs := goScope{kind: "fn", name: m[1], line: l, params: map[string]bool{}, locals: map[string]bool{}}
			for _, p := range goRouterParm.FindAllStringSubmatch(ml, -1) {
				fs.params[p[1]] = true
			}
			fs.subRouter = goRetRouter.MatchString(ml)
			pending = &fs
		}
		for _, p := range goNewRouter.FindAllStringSubmatchIndex(ml, -1) {
			name := ml[p[2]:p[3]]
			if fi := fnIndex(stack); fi >= 0 {
				stack[fi].locals[name] = true
			} else {
				st.globals[name] = true
			}
		}
		// events in position order; braces interleaved
		sortEvents(ev)
		ei := 0
		handle := func(upTo int) {
			for ei < len(ev) && ev[ei].pos < upTo {
				e := ev[ei]
				ei++
				switch e.kind {
				case "verb":
					st.route(stack, l, e.m[1], strings.ToUpper(e.m[2]), e.m[3])
				case "method":
					st.route(stack, l, e.m[1], strings.ToUpper(e.m[2]), e.m[3])
				case "route":
					if p, _, ok := st.lookup(stack, e.m[1]); ok {
						pending = &goScope{kind: "route", name: e.m[3], prefix: joinPath(p, e.m[2], false), line: l}
					} else {
						pending = &goScope{kind: "route", name: e.m[3], line: l, unknown: true} // unknown parent: facts below are refused
					}
				case "group":
					if p, _, ok := st.lookup(stack, e.m[1]); ok {
						pending = &goScope{kind: "group", name: e.m[2], prefix: p, line: l}
					} else {
						pending = &goScope{kind: "group", name: e.m[2], line: l, unknown: true}
					}
				case "mount":
					if e.m[4] != "" { // pkg.Name: resolved in another file
						continue
					}
					if p, _, ok := st.lookup(stack, e.m[1]); ok {
						key := e.m[3]
						if fi := fnIndex(stack); fi >= 0 && stack[fi].locals[key] {
							key = stack[fi].name + "." + key
						}
						st.mounts[key] = joinPath(p, e.m[2], false)
					}
				}
			}
		}
		for pos := 0; pos < len(ml); pos++ {
			handle(pos)
			switch ml[pos] {
			case '{':
				if before := strings.TrimSpace(ml[:pos]); strings.HasSuffix(before, "interface") || strings.HasSuffix(before, "struct") {
					depth++
					continue
				}
				if pending != nil {
					pending.closeAt = depth
					stack = append(stack, *pending)
					pending = nil
				}
				depth++
			case '}':
				depth--
				for len(stack) > 0 && stack[len(stack)-1].closeAt >= depth {
					stack = stack[:len(stack)-1]
				}
			}
		}
		handle(len(ml) + 1)
	}
}

func isStringCapture(kind string, k int) bool {
	switch kind {
	case "verb":
		return k == 3
	case "method":
		return k == 2 || k == 3
	case "route":
		return k == 2
	case "mount":
		return k == 2
	}
	return false
}

// originalAt returns the unmasked text of [a,b) of the window that starts on
// line i; the window joins masked lines with one space each.
func originalAt(f *FileCtx, i, a, b int) string {
	var sb strings.Builder
	for j := i; j < len(f.Lines) && j < i+3; j++ {
		sb.WriteString(f.Lines[j].Code)
		sb.WriteByte(' ')
	}
	s := sb.String()
	if b > len(s) {
		b = len(s)
	}
	return s[a:b]
}

func fnIndex(stack []goScope) int {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].kind == "fn" {
			return i
		}
	}
	return -1
}

func sortEvents(ev []goEvent) {
	for i := 1; i < len(ev); i++ {
		for j := i; j > 0 && ev[j].pos < ev[j-1].pos; j-- {
			ev[j], ev[j-1] = ev[j-1], ev[j]
		}
	}
}

func (st *goChiState) route(stack []goScope, l lex.Line, recv, method, route string) {
	if !st.emit || !goHTTPMethods[method] {
		return
	}
	for _, s := range stack {
		if s.unknown {
			return // a scope whose parent router is unknown
		}
	}
	prefix, used, ok := st.lookup(stack, recv)
	if !ok {
		return
	}
	// a sub-router returned as chi.Router and never mounted here has no known root
	if i := fnIndex(stack); i >= 0 && stack[i].subRouter && prefix == "" {
		if _, mounted := st.mounts[stack[i].name]; !mounted {
			return
		}
	}
	var derived []truth.Evidence
	for _, u := range used {
		derived = append(derived, evidence(st.f, u))
	}
	st.out = append(st.out, endpointFact("go.chi", st.f, l, method, joinPath(prefix, goCanonRoute(route), false), derived...))
}

var goHTTPMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
	"HEAD": true, "OPTIONS": true, "CONNECT": true, "TRACE": true,
}

// goBracePart finds a {param} group, regex bodies with nested braces included.
var goBracePart = regexp.MustCompile(`\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}`)

// goCanonRoute turns {id:[0-9]+}, {rest...} and {$} into plain parameters.
func goCanonRoute(p string) string {
	p = strings.ReplaceAll(p, "{$}", "")
	return goBracePart.ReplaceAllStringFunc(p, func(s string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
		if i := strings.IndexByte(name, ':'); i >= 0 {
			name = name[:i]
		}
		name = strings.TrimSuffix(name, "...")
		return "{" + name + "}"
	})
}

// ---- net/http (Go 1.22 patterns) ----

var (
	goMuxHandle = regexp.MustCompile(`(\w+)\.(?:Handle|HandleFunc)\(\s*"([^"]*)"`)
	goMuxNew    = regexp.MustCompile(`(\w+)\s*(?::=|=|:)\s*&?(?:http\.NewServeMux\(|http\.ServeMux\{)`)
	goMuxParam  = regexp.MustCompile(`(\w+)\s+\*?http\.ServeMux\b`)
	goMuxPat    = regexp.MustCompile(`^([A-Z]+)[ \t]+([^/\s]*)(/\S*)$`)
)

func applyGoMux(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "net/http") {
		return nil
	}
	masked := goMaskLines(f)
	muxes := map[string]bool{"http": true}
	for _, ml := range masked {
		for _, m := range goMuxNew.FindAllStringSubmatch(ml, -1) {
			muxes[m[1]] = true
		}
		for _, m := range goMuxParam.FindAllStringSubmatch(ml, -1) {
			muxes[m[1]] = true
		}
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		ml := masked[i]
		var win strings.Builder
		for j := i; j < len(masked) && j < i+3; j++ {
			win.WriteString(masked[j])
			win.WriteByte(' ')
		}
		text := win.String()
		for _, ix := range goMuxHandle.FindAllStringSubmatchIndex(text, -1) {
			if ix[0] >= len(ml) || !muxes[text[ix[2]:ix[3]]] {
				continue
			}
			pat := originalAt(f, i, ix[4], ix[5])
			m := goMuxPat.FindStringSubmatch(pat)
			if m == nil || !goHTTPMethods[m[1]] {
				continue // no method in the pattern: any method, no stable key
			}
			out = append(out, endpointFact("go.http_mux", f, l, m[1], joinPath("", goCanonRoute(m[3]), false)))
		}
	}
	return out
}

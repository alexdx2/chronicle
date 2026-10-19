package extract

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Rust: axum routers, rdkafka channels, env-var default URLs, Cargo.lock
// dependencies and Cargo.toml crates.

func init() {
	isRs := func(p string) bool { return hasExt(p, ".rs") }
	Rules = append(Rules,
		funcRule{"axum.router", isRs, applyAxumRouter},
		funcRule{"rdkafka", isRs, applyRustKafka},
		funcRule{"rust.http", isRs, applyRustHTTP},
		funcRule{"lock.cargo", func(p string) bool { return baseName(p) == "Cargo.lock" }, applyLockCargo},
		funcRule{"cargo.package", func(p string) bool { return baseName(p) == "Cargo.toml" }, applyCargoPackage},
	)
}

// rsSrc is a Rust file as one comment-free string with a literal mask.
type rsSrc struct {
	f      *FileCtx
	code   string
	mask   []bool
	starts []int // byte offset of each line
	tests  [][2]int
}

func newRsSrc(f *FileCtx, lines []lex.Line) *rsSrc {
	s := &rsSrc{f: f}
	var sb strings.Builder
	for _, l := range lines {
		s.starts = append(s.starts, sb.Len())
		sb.WriteString(l.Code)
		sb.WriteByte('\n')
	}
	s.code = sb.String()
	s.mask = litMask(s.code, true)
	for _, m := range regexp.MustCompile(`#\[cfg\(test\)\]`).FindAllStringIndex(s.code, -1) {
		if open := s.indexCode('{', m[1]); open >= 0 {
			s.tests = append(s.tests, [2]int{m[0], s.matchClose(open, '{', '}')})
		}
	}
	return s
}

func (s *rsSrc) lineAt(off int) int {
	return sort.Search(len(s.starts), func(i int) bool { return s.starts[i] > off }) - 1
}

// indexCode finds byte c outside literals at or after from.
func (s *rsSrc) indexCode(c byte, from int) int {
	for i := from; i < len(s.code); i++ {
		if s.code[i] == c && !s.mask[i] {
			return i
		}
	}
	return -1
}

// matchClose returns the offset of the bracket closing the one at open
// (len(code) when unbalanced).
func (s *rsSrc) matchClose(open int, o, c byte) int {
	depth := 0
	for i := open; i < len(s.code); i++ {
		if s.mask[i] {
			continue
		}
		switch s.code[i] {
		case o:
			depth++
		case c:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s.code)
}

func (s *rsSrc) inTest(off int) bool {
	for _, t := range s.tests {
		if off >= t[0] && off <= t[1] {
			return true
		}
	}
	return false
}

// ---- axum ------------------------------------------------------------------

type rsCall struct {
	name    string
	off     int // offset of the method name
	a, b    int // argument text is code[a:b]
	nameEnd int
}

var (
	rsRouterNew = regexp.MustCompile(`\bRouter(?:::<[^>]*>)?::new\(\)`)
	rsFnDecl    = regexp.MustCompile(`\bfn\s+(\w+)`)
	rsLetRouter = regexp.MustCompile(`\blet\s+(?:mut\s+)?(\w+)\s*(?::[^=;]+)?=\s*(Router(?:::<[^>]*>)?::new\(\))`)
	rsRoutePath = regexp.MustCompile(`^\s*"([^"]*)"\s*,`)
	rsNestHead  = regexp.MustCompile(`^\s*"([^"]*)"\s*,\s*`)
	rsTargetID  = regexp.MustCompile(`^&?([A-Za-z_][\w:]*)`)
	rsVerbSet   = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "options": true, "head": true}
)

func (s *rsSrc) skipWS(i int) int {
	for i < len(s.code) && (s.code[i] == ' ' || s.code[i] == '\n' || s.code[i] == '\t' || s.code[i] == '\r') {
		i++
	}
	return i
}

// chain parses `.name(args).name2(args)...` from offset i.
func (s *rsSrc) chain(i int) []rsCall {
	var calls []rsCall
	for {
		i = s.skipWS(i)
		if i >= len(s.code) || s.code[i] != '.' {
			return calls
		}
		i = s.skipWS(i + 1)
		j := i
		for j < len(s.code) && isIdentByte(s.code[j]) {
			j++
		}
		if j == i {
			return calls
		}
		name, nameEnd := s.code[i:j], j
		if strings.HasPrefix(s.code[j:], "::<") { // turbofish
			if k := strings.Index(s.code[j:], ">"); k >= 0 {
				j += k + 1
			}
		}
		j = s.skipWS(j)
		if j >= len(s.code) || s.code[j] != '(' {
			return calls
		}
		end := s.matchClose(j, '(', ')')
		calls = append(calls, rsCall{name: name, off: i, a: j + 1, b: end, nameEnd: nameEnd})
		i = end + 1
	}
}

type rsUnit struct {
	start int // offset of "Router::new()"
	calls []rsCall
}

type rsFn struct {
	name       string
	from, to   int
	unitStarts []int
}

func (s *rsSrc) units() ([]*rsUnit, []rsFn, map[string]int) {
	var units []*rsUnit
	for _, m := range rsRouterNew.FindAllStringIndex(s.code, -1) {
		if s.mask[m[0]] || s.inTest(m[0]) {
			continue
		}
		units = append(units, &rsUnit{start: m[0], calls: s.chain(m[1])})
	}
	var fns []rsFn
	for _, m := range rsFnDecl.FindAllStringSubmatchIndex(s.code, -1) {
		if s.mask[m[0]] {
			continue
		}
		open := s.indexCode('{', m[1])
		semi := s.indexCode(';', m[1])
		if open < 0 || (semi >= 0 && semi < open) {
			continue
		}
		fn := rsFn{name: s.code[m[2]:m[3]], from: open, to: s.matchClose(open, '{', '}')}
		for _, u := range units {
			if u.start > fn.from && u.start < fn.to {
				fn.unitStarts = append(fn.unitStarts, u.start)
			}
		}
		fns = append(fns, fn)
	}
	lets := map[string]int{}
	for _, m := range rsLetRouter.FindAllStringSubmatchIndex(s.code, -1) {
		if !s.mask[m[0]] {
			lets[s.code[m[2]:m[3]]] = m[4]
		}
	}
	return units, fns, lets
}

// targets resolves the router expression in code[a:b] to unit starts.
func (s *rsSrc) targets(a, b int, fns []rsFn, lets map[string]int) []int {
	expr := strings.TrimSpace(s.code[a:b])
	off := a + strings.Index(s.code[a:b], expr)
	if rsRouterNew.MatchString(expr) && strings.HasPrefix(expr, "Router") {
		return []int{off}
	}
	m := rsTargetID.FindStringSubmatch(expr)
	if m == nil {
		return nil
	}
	id := m[1]
	if i := strings.LastIndex(id, "::"); i >= 0 {
		id = id[i+2:]
	}
	if st, ok := lets[id]; ok && !strings.Contains(m[1], "::") && !strings.HasPrefix(expr[len(m[0]):], "(") {
		return []int{st}
	}
	var out []int
	for _, fn := range fns {
		if fn.name == id && strings.HasPrefix(strings.TrimSpace(expr[len(m[0]):]), "(") {
			out = append(out, fn.unitStarts...)
		}
	}
	return out
}

// verbs lists the top-level method-router calls of a route's handler argument.
func (s *rsSrc) verbs(a, b int) []string {
	var out []string
	depth := 0
	for i := a; i < b; i++ {
		if s.mask[i] {
			continue
		}
		switch c := s.code[i]; {
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case depth == 0 && (c >= 'a' && c <= 'z') && (i == a || !isIdentByte(s.code[i-1])):
			j := i
			for j < b && isIdentByte(s.code[j]) {
				j++
			}
			name := s.code[i:j]
			k := s.skipWS(j)
			if rsVerbSet[name] && k < b && s.code[k] == '(' {
				out = append(out, strings.ToUpper(name))
			}
			i = j - 1
		}
	}
	return out
}

func applyAxumRouter(f *FileCtx) []truth.Fact {
	if isTestLike(f) || !codeContains(f, "router") {
		return nil
	}
	s := newRsSrc(f, f.Lines)
	units, fns, lets := s.units()
	if len(units) == 0 {
		return nil
	}
	byStart := map[int]*rsUnit{}
	nested := map[int]bool{}
	for _, u := range units {
		byStart[u.start] = u
	}
	for _, u := range units {
		for _, c := range u.calls {
			if c.name == "nest" || c.name == "merge" {
				a := c.a
				if c.name == "nest" {
					if m := rsNestHead.FindStringIndex(s.code[c.a:c.b]); m != nil {
						a = c.a + m[1]
					} else {
						continue
					}
				}
				for _, t := range s.targets(a, c.b, fns, lets) {
					nested[t] = true
				}
			}
		}
	}
	var out []truth.Fact
	var emit func(start int, prefix string, derived []truth.Evidence, depth int)
	emit = func(start int, prefix string, derived []truth.Evidence, depth int) {
		u := byStart[start]
		if u == nil || depth > 8 {
			return
		}
		for _, c := range u.calls {
			args := s.code[c.a:c.b]
			l := f.Lines[s.lineAt(c.off)]
			switch c.name {
			case "route":
				m := rsRoutePath.FindStringSubmatchIndex(args)
				if m == nil {
					continue
				}
				p := args[m[2]:m[3]]
				for _, v := range s.verbs(c.a+m[1], c.b) {
					out = append(out, endpointFact("axum.router", f, l, v, joinPath(prefix, p, false), derived...))
				}
			case "nest":
				m := rsNestHead.FindStringSubmatchIndex(args)
				if m == nil {
					continue
				}
				np := joinPath(prefix, args[m[2]:m[3]], false)
				for _, t := range s.targets(c.a+m[1], c.b, fns, lets) {
					emit(t, np, append(append([]truth.Evidence(nil), derived...), evidence(f, l)), depth+1)
				}
			case "merge":
				for _, t := range s.targets(c.a, c.b, fns, lets) {
					emit(t, prefix, derived, depth+1)
				}
			}
		}
	}
	for _, u := range units {
		if nested[u.start] {
			continue
		}
		prefixes := []axumMount{{}}
		for _, fn := range fns {
			if fn.name != "main" && u.start > fn.from && u.start < fn.to {
				if ms := axumMountsOf(f, fn.name); len(ms) > 0 {
					prefixes = ms
				}
			}
		}
		for _, m := range prefixes {
			var d []truth.Evidence
			if m.ev.File != "" {
				d = append(d, m.ev)
			}
			emit(u.start, m.prefix, d, 0)
		}
	}
	return out
}

type axumMount struct {
	prefix string
	ev     truth.Evidence
}

var rsNestCall = regexp.MustCompile(`\.nest\(\s*"([^"]*)"\s*,\s*&?([A-Za-z_][\w:]*)\s*\(`)

// axumMountsOf finds `.nest("/p", module::fn_name(...))` in the .rs files
// next to f (same directory and its parent) for a router built by fn_name.
func axumMountsOf(f *FileCtx, fn string) []axumMount {
	var out []axumMount
	dirs := []string{f.Dir}
	if f.Dir != "." && f.Dir != "" {
		dirs = append(dirs, path.Dir(f.Dir))
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		for _, name := range f.Tree.Siblings(d, "*.rs") {
			p := path.Join(d, name)
			if p == f.Path || seen[p] {
				continue
			}
			seen[p] = true
			raw, ok := f.Tree.ReadFile(p)
			if !ok {
				continue
			}
			lines := lex.Scan(raw, lex.CLike).Lines
			o := &FileCtx{Path: p, Dir: d, Lines: lines, Raw: raw, Tree: f.Tree}
			s := newRsSrc(o, lines)
			for _, m := range rsNestCall.FindAllStringSubmatchIndex(s.code, -1) {
				if s.mask[m[0]] || s.inTest(m[0]) {
					continue
				}
				id := s.code[m[4]:m[5]]
				if i := strings.LastIndex(id, "::"); i >= 0 {
					id = id[i+2:]
				}
				if id == fn {
					l := lines[s.lineAt(m[0])]
					out = append(out, axumMount{joinPath("", s.code[m[2]:m[3]], false), evidence(o, l)})
				}
			}
		}
	}
	return out
}

// ---- rdkafka ---------------------------------------------------------------

var (
	rsProduce   = regexp.MustCompile(`\b(?:FutureRecord|BaseRecord)(?:::<[^>]*>)?::to\(\s*"([^"]+)"\s*\)`)
	rsSubscribe = regexp.MustCompile(`\.subscribe\(\s*&?(?:\[|vec!\[)?\s*([^\])]*)[\])]`)
)

func applyRustKafka(f *FileCtx) []truth.Fact {
	if isTestLike(f) || !codeContains(f, "rdkafka") {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		text := codeWindow(f.Lines, i, 3)
		mask := litMask(text, true)
		if m := rsProduce.FindStringSubmatchIndex(text); m != nil && m[0] < len(l.Code) && !mask[m[0]] {
			if fc, ok := channelFact("rdkafka", f, l, "topic", text[m[2]:m[3]], "produce"); ok {
				out = append(out, fc)
			}
		}
		if m := rsSubscribe.FindStringSubmatchIndex(text); m != nil && m[0] < len(l.Code) && !mask[m[0]] {
			for _, n := range literalItems(text[m[2]:m[3]]) {
				if fc, ok := channelFact("rdkafka", f, l, "topic", n, "consume"); ok {
					out = append(out, fc)
				}
			}
		}
	}
	return out
}

// ---- HTTP ------------------------------------------------------------------

var (
	rsEnvDefault = regexp.MustCompile(`\b(?:env::var|option_env!)\(\s*"([^"]+)"\s*\)(?:\s*\.ok\(\))?\s*\.unwrap_or(?:_else)?\(\s*(?:\|\s*_?\w*\s*\|\s*)?(?:\{\s*)?(?:String::from\(\s*)?"(https?://[^"]+)"`)
	rsClapURL    = regexp.MustCompile(`\benv\s*=\s*"([^"]+)"[^\]]*?\bdefault_value\s*=\s*"(https?://[^"]+)"`)
	rsDirect     = regexp.MustCompile(`(?:\breqwest::(?:blocking::)?get|\.(?:get|post|put|patch|delete|head)|\bUrl::parse|\bUri::from_static|\.base_url)\(\s*"(https?://[^"]+)"`)
	rsClient     = []string{"reqwest", "hyper", "ureq", "surf", "isahc", "awc", "attohttpc", "tonic"}
)

func applyRustHTTP(f *FileCtx) []truth.Fact {
	if isTestLike(f) {
		return nil
	}
	client := codeContains(f, rsClient...)
	var out []truth.Fact
	for i, l := range f.Lines {
		win := codeWindow(f.Lines, i, 3)
		mask := litMask(win, true)
		add := func(url string) {
			if fc, ok := httpFact("rust.http", f, l, url, ""); ok {
				out = append(out, fc)
			}
		}
		for _, re := range []*regexp.Regexp{rsEnvDefault, rsClapURL} {
			for _, m := range re.FindAllStringSubmatchIndex(win, -1) {
				if m[0] < len(l.Code) && !mask[m[0]] && envURLAllowed(client, win[m[2]:m[3]]) {
					add(win[m[4]:m[5]])
				}
			}
		}
		if client {
			for _, m := range rsDirect.FindAllStringSubmatchIndex(win, -1) {
				if m[0] < len(l.Code) && !mask[m[0]] {
					add(win[m[2]:m[3]])
				}
			}
		}
	}
	return out
}

// ---- Cargo -----------------------------------------------------------------

// cargoManifestDeps collects the crate names a Cargo.toml lists as direct
// dependencies ([dependencies], [dev-dependencies], [build-dependencies] and
// their target-specific forms). Path-only dependencies are workspace members,
// not crates, and are skipped; `package = "x"` renames count under x.
func cargoManifestDeps(raw []byte, members *[]string, own map[string]bool) map[string]bool {
	var doc map[string]any
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return nil
	}
	out := map[string]bool{}
	var table func(m map[string]any)
	table = func(m map[string]any) {
		for _, sec := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
			deps, _ := m[sec].(map[string]any)
			for name, v := range deps {
				if t, ok := v.(map[string]any); ok {
					if p, ok := t["package"].(string); ok {
						name = p
					}
					if _, isPath := t["path"]; isPath {
						if _, hasVer := t["version"]; !hasVer {
							continue
						}
					}
				}
				out[name] = true
			}
		}
	}
	table(doc)
	if targets, ok := doc["target"].(map[string]any); ok {
		for _, t := range targets {
			if tm, ok := t.(map[string]any); ok {
				table(tm)
			}
		}
	}
	if pkg, ok := doc["package"].(map[string]any); ok {
		if n, ok := pkg["name"].(string); ok {
			own[n] = true
		}
	}
	if ws, ok := doc["workspace"].(map[string]any); ok && members != nil {
		if ms, ok := ws["members"].([]any); ok {
			for _, m := range ms {
				if str, ok := m.(string); ok && !strings.ContainsAny(str, "*?[") {
					*members = append(*members, str)
				}
			}
		}
	}
	return out
}

// applyLockCargo lists the locked crates that the Cargo.toml next to the lock
// (and the workspace members it names) list as direct dependencies. The root
// package and workspace members are excluded. Without a usable manifest it
// falls back to the registry/git crates that source-less lock packages (the
// workspace's own crates) depend on.
func applyLockCargo(f *FileCtx) []truth.Fact {
	var doc struct {
		Package []struct {
			Name         string   `toml:"name"`
			Version      string   `toml:"version"`
			Source       string   `toml:"source"`
			Dependencies []string `toml:"dependencies"`
		} `toml:"package"`
	}
	if _, err := toml.Decode(string(f.Raw), &doc); err != nil {
		return nil
	}
	own := map[string]bool{}
	direct := map[string]bool{}
	if raw, ok := f.Tree.ReadFile(path.Join(f.Dir, "Cargo.toml")); ok {
		var members []string
		for n := range cargoManifestDeps(raw, &members, own) {
			direct[n] = true
		}
		for _, m := range members {
			if mraw, ok := f.Tree.ReadFile(path.Join(f.Dir, m, "Cargo.toml")); ok {
				for n := range cargoManifestDeps(mraw, nil, own) {
					direct[n] = true
				}
			}
		}
	}
	if len(direct) == 0 {
		for _, p := range doc.Package {
			if p.Source != "" {
				continue
			}
			for _, d := range p.Dependencies {
				if name := strings.Fields(d)[0]; name != p.Name {
					direct[name] = true
				}
			}
		}
		for _, p := range doc.Package {
			if p.Source == "" {
				own[p.Name] = true
			}
		}
	}
	s := newDepSet("lock.cargo", f)
	pkgHeader := regexp.MustCompile(`^\s*\[\[package\]\]`)
	for _, p := range doc.Package {
		if !direct[p.Name] || own[p.Name] {
			continue
		}
		if fp := s.add("cargo", p.Name, p.Version, "direct"); fp != nil {
			nameRe := regexp.MustCompile(`^\s*name\s*=\s*"` + regexp.QuoteMeta(p.Name) + `"`)
			for k := 1; k < len(f.Lines); k++ {
				if nameRe.MatchString(f.Lines[k].Raw) && pkgHeader.MatchString(f.Lines[k-1].Raw) {
					fp.Primary = evidence(f, f.Lines[k])
					break
				}
			}
		}
	}
	return s.facts
}

func applyCargoPackage(f *FileCtx) []truth.Fact {
	var doc struct {
		Package struct {
			Name string `toml:"name"`
		} `toml:"package"`
		Lib any   `toml:"lib"`
		Bin []any `toml:"bin"`
	}
	if _, err := toml.Decode(string(f.Raw), &doc); err != nil || doc.Package.Name == "" {
		return nil
	}
	_, hasMain := f.Tree.ReadFile(path.Join(f.Dir, "src/main.rs"))
	_, hasLib := f.Tree.ReadFile(path.Join(f.Dir, "src/lib.rs"))
	hasBin := hasMain || len(doc.Bin) > 0 || len(f.Tree.Siblings(path.Join(f.Dir, "src/bin"), "*.rs")) > 0
	if !hasBin && (hasLib || doc.Lib != nil) {
		return nil // a library crate is not a running service
	}
	line := f.Lines[0]
	inPkg := false
	for _, l := range f.Lines {
		t := strings.TrimSpace(l.Code)
		if strings.HasPrefix(t, "[") {
			inPkg = t == "[package]"
			continue
		}
		if inPkg && regexp.MustCompile(`^name\s*=`).MatchString(t) {
			line = l
			break
		}
	}
	fc, ok := serviceFact("cargo.package", f, "own", doc.Package.Name, "rust", "", line)
	if !ok {
		return nil
	}
	return []truth.Fact{fc}
}

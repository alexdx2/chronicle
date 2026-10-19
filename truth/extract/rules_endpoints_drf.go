package extract

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

func init() {
	Rules = append(Rules,
		funcRule{"drf.router", func(p string) bool { return hasExt(p, ".py") }, applyDRFRouter},
	)
}

// Django REST Framework routers: router.register(r"prefix", ViewSet) on a
// DefaultRouter / SimpleRouter expands to the routes the ViewSet provides.
// The mount prefix comes from the include(router.urls) line of the same file
// or, when the file only exposes router.urls, from the urls.py that includes
// this module. A ViewSet the rule cannot find yields nothing.

var (
	drfRouterDecl = regexp.MustCompile(`(\w+)\s*=\s*(?:[\w.]+\.)?(?:DefaultRouter|SimpleRouter)\(`)
	drfRegister   = regexp.MustCompile(`\b(\w+)\.register\(\s*r?["']([^"']*)["']\s*,\s*([A-Za-z_][\w.]*)`)
	drfIncludeRtr = regexp.MustCompile(`\binclude\(\s*(\w+)\.urls\s*\)`)
	drfIncludeMod = regexp.MustCompile(`\binclude\(\s*["']([\w.]+)["']\s*\)`)
	drfPathHead   = regexp.MustCompile(`(?:^|[^\w.])(?:re_)?(?:path|url)\(\s*r?["']([^"']*)["']`)
	pyClassDef    = regexp.MustCompile(`^(\s*)class\s+(\w+)\s*(?:\(([^)]*)\))?\s*:`)
	pyDefName     = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+(\w+)\s*\(`)
	pyFromImport  = regexp.MustCompile(`(?m)^[ \t]*from[ \t]+([.\w]+)[ \t]+import[ \t]+(\([^)]*\)|[^\n]*)`)
	pyImportAs    = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+([\w.]+)[ \t]+as[ \t]+(\w+)`)
	drfHTTPNames  = regexp.MustCompile(`\bhttp_method_names\s*=\s*[\[(]([^\])]*)[\])]`)
	drfActionKwD  = regexp.MustCompile(`\bdetail\s*=\s*(True|False)`)
	drfActionKwU  = regexp.MustCompile(`\burl_path\s*=\s*r?["']([^"']*)["']`)
)

// drfBaseActions maps DRF base classes and mixins to the actions they give.
var drfBaseActions = map[string][]string{
	"ModelViewSet":         {"list", "create", "retrieve", "update", "partial_update", "destroy"},
	"ReadOnlyModelViewSet": {"list", "retrieve"},
	"ListModelMixin":       {"list"},
	"CreateModelMixin":     {"create"},
	"RetrieveModelMixin":   {"retrieve"},
	"UpdateModelMixin":     {"update", "partial_update"},
	"DestroyModelMixin":    {"destroy"},
	// Bases that provide no action by themselves.
	"ViewSet": nil, "GenericViewSet": nil, "ViewSetMixin": nil, "GenericAPIView": nil, "APIView": nil, "object": nil,
}

// drfRoute is one action of a router: HTTP method and whether it takes the {pk}.
var drfActionRoute = map[string]struct {
	method string
	detail bool
}{
	"list":           {"GET", false},
	"create":         {"POST", false},
	"retrieve":       {"GET", true},
	"update":         {"PUT", true},
	"partial_update": {"PATCH", true},
	"destroy":        {"DELETE", true},
}

type drfRouteSpec struct {
	method string
	suffix string // "" for the collection, "/{pk}" or "/{pk}/name" ...
}

type drfViewSet struct {
	routes []drfRouteSpec
	file   string
	line   lex.Line
}

func applyDRFRouter(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, ".register(") {
		return nil
	}
	routers := map[string]bool{}
	for _, l := range f.Lines {
		if m := drfRouterDecl.FindStringSubmatch(l.Code); m != nil {
			routers[m[1]] = true
		}
	}
	if len(routers) == 0 {
		return nil
	}
	mount, ok := drfMount(f, routers)
	if !ok {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		m := drfRegister.FindStringSubmatch(callText(f.Lines, i, 6))
		if m == nil || !routers[m[1]] || !strings.Contains(l.Code, ".register(") {
			continue
		}
		vs, found := drfResolve(f, m[3])
		if !found {
			continue
		}
		var derived []truth.Evidence
		derived = append(derived, mount.evidence...)
		derived = append(derived, truth.Evidence{File: vs.file, Line: vs.line.Num, Text: strings.TrimSpace(vs.line.Raw)})
		base := joinPath(mount.prefix, m[2], false)
		for _, r := range vs.routes {
			out = append(out, endpointFact("drf.router", f, l, r.method, base+r.suffix, derived...))
		}
	}
	return out
}

type drfMountInfo struct {
	prefix   string
	evidence []truth.Evidence
}

// drfMount finds the URL prefix the file's routers are served under.
func drfMount(f *FileCtx, routers map[string]bool) (drfMountInfo, bool) {
	for i, l := range f.Lines {
		if !strings.Contains(l.Code, "include(") {
			continue
		}
		text := callText(f.Lines, i, 4)
		if m := drfIncludeRtr.FindStringSubmatch(text); m != nil && routers[m[1]] {
			return drfMountInfo{drfCleanPrefix(drfPathBefore(f.Lines, i)), []truth.Evidence{evidence(f, l)}}, true
		}
	}
	return drfMountVia(f, f.Path, 0)
}

// drfPathBefore returns the literal of the path()/url() call that encloses
// the include( on line i, looking back at most three lines for a call
// written over several lines.
func drfPathBefore(lines []lex.Line, i int) string {
	var sb strings.Builder
	for j := i - 3; j < i; j++ {
		if j >= 0 {
			sb.WriteString(lines[j].Code)
			sb.WriteByte(' ')
		}
	}
	code := lines[i].Code
	if k := strings.Index(code, "include("); k >= 0 {
		code = code[:k]
	}
	sb.WriteString(code)
	all := drfPathHead.FindAllStringSubmatch(sb.String(), -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1][1]
}

func drfCleanPrefix(p string) string {
	return strings.TrimSuffix(strings.TrimPrefix(p, "^"), "$")
}

// drfDotted turns a file path into its dotted module name.
func drfDotted(p string) string {
	p = strings.TrimSuffix(p, ".py")
	p = strings.TrimSuffix(p, "/__init__")
	return strings.ReplaceAll(p, "/", ".")
}

// drfUrlsFiles lists urls.py files at most three directories below the root.
func drfUrlsFiles(t Tree) []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if len(t.Siblings(dir, "urls.py")) > 0 {
			out = append(out, path.Join(dir, "urls.py"))
		}
		if depth >= 3 {
			return
		}
		subs := t.Subdirs(dir)
		sort.Strings(subs)
		for _, d := range subs {
			walk(path.Join(dir, d), depth+1)
		}
	}
	walk(".", 0)
	return out
}

// drfMountVia finds the urls.py files that include the module at file and
// adds the prefixes of their own mounts (at most three levels up). Several
// includers with different prefixes make the mount ambiguous.
func drfMountVia(f *FileCtx, file string, depth int) (drfMountInfo, bool) {
	if depth > 3 {
		return drfMountInfo{}, true
	}
	mod := drfDotted(file)
	var found []drfMountInfo
	for _, cand := range drfUrlsFiles(f.Tree) {
		if cand == file || IsTestPath(cand) || IsSamplePath(cand) {
			continue
		}
		raw, ok := f.Tree.ReadFile(cand)
		if !ok {
			continue
		}
		lines := lex.Scan(raw, lex.Python).Lines
		for i, l := range lines {
			if !strings.Contains(l.Code, "include(") {
				continue
			}
			text := callText(lines, i, 4)
			m := drfIncludeMod.FindStringSubmatch(text)
			if m == nil || !(mod == m[1] || strings.HasSuffix(mod, "."+m[1])) {
				continue
			}
			info := drfMountInfo{drfCleanPrefix(drfPathBefore(lines, i)), []truth.Evidence{{File: cand, Line: l.Num, Text: strings.TrimSpace(l.Raw)}}}
			up, ok := drfMountVia(&FileCtx{Path: cand, Tree: f.Tree}, cand, depth+1)
			if !ok {
				return drfMountInfo{}, false
			}
			info.prefix = joinPath(up.prefix, info.prefix, false)
			info.evidence = append(info.evidence, up.evidence...)
			found = append(found, info)
		}
	}
	if len(found) == 0 {
		return drfMountInfo{}, true // served at the root as far as the tree shows
	}
	for _, x := range found[1:] {
		if x.prefix != found[0].prefix {
			return drfMountInfo{}, false
		}
	}
	return found[0], true
}

// ---- viewset resolution ----

type pyModule struct {
	path  string
	lines []lex.Line
}

// drfLoadModule reads a python module by dotted name, relative to dir for
// leading dots and to the tree root (or dir) otherwise.
func drfLoadModule(f *FileCtx, dir, mod string) (pyModule, bool) {
	rel := 0
	for strings.HasPrefix(mod, ".") {
		rel++
		mod = mod[1:]
	}
	var bases []string
	if rel > 0 {
		d := dir
		for k := 1; k < rel; k++ {
			d = path.Dir(d)
		}
		bases = []string{d}
	} else {
		bases = []string{".", dir}
	}
	if mod == "" {
		return pyModule{}, false
	}
	rest := strings.ReplaceAll(mod, ".", "/")
	for _, b := range bases {
		for _, c := range []string{path.Join(b, rest+".py"), path.Join(b, rest, "__init__.py")} {
			if raw, ok := f.Tree.ReadFile(c); ok {
				return pyModule{c, lex.Scan(raw, lex.Python).Lines}, true
			}
		}
	}
	return pyModule{}, false
}

// drfImport resolves a local name to (module, original name) through the
// import statements of lines. For `import a.b as n` the original is "".
func drfImport(lines []lex.Line, name string) (mod, orig string, ok bool) {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.Code)
		sb.WriteByte('\n')
	}
	text := sb.String()
	for _, m := range pyFromImport.FindAllStringSubmatch(text, -1) {
		names := strings.NewReplacer("(", " ", ")", " ", ",", " ", "\\", " ").Replace(m[2])
		w := strings.Fields(names)
		for i := 0; i < len(w); i++ {
			if i+2 < len(w) && w[i+1] == "as" {
				if w[i+2] == name {
					return m[1], w[i], true
				}
				i += 2
				continue
			}
			if w[i] == name {
				return m[1], w[i], true
			}
		}
	}
	for _, m := range pyImportAs.FindAllStringSubmatch(text, -1) {
		if m[2] == name {
			return m[1], "", true
		}
	}
	return "", "", false
}

// drfResolve finds the ViewSet class named ref ("X" or "views.X") and derives
// the routes it provides.
func drfResolve(f *FileCtx, ref string) (drfViewSet, bool) {
	return drfResolveIn(f, pyModule{f.Path, f.Lines}, ref, 0)
}

func drfResolveIn(f *FileCtx, cur pyModule, ref string, depth int) (drfViewSet, bool) {
	if depth > 6 {
		return drfViewSet{}, false
	}
	dir := path.Dir(cur.path)
	mod, name := cur, ref
	if i := strings.LastIndex(ref, "."); i >= 0 {
		// views.X: views is an imported module (from . import views / import a.views as views).
		qual, last := ref[:i], ref[i+1:]
		if strings.Contains(qual, ".") {
			qual = qual[strings.LastIndex(qual, ".")+1:]
		}
		im, orig, ok := drfImport(cur.lines, qual)
		if !ok {
			return drfViewSet{}, false
		}
		target := im
		if orig != "" {
			target = strings.TrimSuffix(im, ".") + "." + orig
			if strings.HasSuffix(im, ".") {
				target = im + orig
			}
		}
		m, ok := drfLoadModule(f, dir, target)
		if !ok {
			return drfViewSet{}, false
		}
		mod, name = m, last
	}
	if cls, ok := drfClass(mod.lines, name); ok {
		return drfBuild(f, mod, cls, depth)
	}
	im, orig, ok := drfImport(mod.lines, name)
	if !ok || orig == "" {
		return drfViewSet{}, false
	}
	m, ok := drfLoadModule(f, path.Dir(mod.path), im)
	if !ok {
		return drfViewSet{}, false
	}
	cls, ok := drfClass(m.lines, orig)
	if !ok {
		return drfViewSet{}, false
	}
	return drfBuild(f, m, cls, depth)
}

type pyClass struct {
	idx    int // index of the class line
	bases  []string
	body   []lex.Line
	indent int
}

// drfClass finds `class name(...)` and its body (lines indented deeper).
func drfClass(lines []lex.Line, name string) (pyClass, bool) {
	for i, l := range lines {
		m := pyClassDef.FindStringSubmatch(l.Code)
		if m == nil || m[2] != name {
			continue
		}
		c := pyClass{idx: i, indent: len(m[1])}
		for _, b := range strings.Split(m[3], ",") {
			b = strings.TrimSpace(b)
			if b == "" || strings.Contains(b, "=") {
				continue
			}
			if k := strings.LastIndex(b, "."); k >= 0 {
				b = b[k+1:]
			}
			c.bases = append(c.bases, b)
		}
		for j := i + 1; j < len(lines); j++ {
			code := lines[j].Code
			if strings.TrimSpace(code) == "" {
				continue
			}
			if len(code)-len(strings.TrimLeft(code, " \t")) <= c.indent {
				break
			}
			c.body = append(c.body, lines[j])
		}
		return c, true
	}
	return pyClass{}, false
}

// drfBuild collects the actions of a class: inherited from bases, defined in
// its body, plus @action routes; http_method_names narrows the result.
func drfBuild(f *FileCtx, mod pyModule, cls pyClass, depth int) (drfViewSet, bool) {
	actions := map[string]bool{}
	var extra []drfRouteSpec
	var collect func(mod pyModule, cls pyClass, depth int)
	collect = func(mod pyModule, cls pyClass, depth int) {
		if depth > 6 {
			return
		}
		for _, b := range cls.bases {
			if acts, known := drfBaseActions[b]; known {
				for _, a := range acts {
					actions[a] = true
				}
				continue
			}
			if bc, ok := drfClass(mod.lines, b); ok {
				collect(mod, bc, depth+1)
				continue
			}
			if im, orig, ok := drfImport(mod.lines, b); ok && orig != "" {
				if bm, ok := drfLoadModule(f, path.Dir(mod.path), im); ok {
					if bc, ok := drfClass(bm.lines, orig); ok {
						collect(bm, bc, depth+1)
					}
				}
			}
		}
		bodyIndent := -1
		for i, l := range cls.body {
			ind := len(l.Code) - len(strings.TrimLeft(l.Code, " \t"))
			if bodyIndent < 0 {
				bodyIndent = ind
			}
			if ind != bodyIndent {
				continue
			}
			if m := pyDefName.FindStringSubmatch(l.Code); m != nil {
				if _, isAct := drfActionRoute[m[2]]; isAct {
					actions[m[2]] = true
				}
			}
			if strings.HasPrefix(strings.TrimSpace(l.Code), "@action(") && depth == 0 {
				if r, ok := drfActionSpecs(cls.body, i); ok {
					extra = append(extra, r...)
				}
			}
		}
	}
	collect(mod, cls, depth)
	var routes []drfRouteSpec
	for _, a := range []string{"list", "create", "retrieve", "update", "partial_update", "destroy"} {
		if !actions[a] {
			continue
		}
		r := drfActionRoute[a]
		s := ""
		if r.detail {
			s = "/{pk}"
		}
		routes = append(routes, drfRouteSpec{r.method, s})
	}
	routes = append(routes, extra...)
	for _, l := range cls.body {
		if m := drfHTTPNames.FindStringSubmatch(l.Code); m != nil {
			allowed := map[string]bool{}
			for _, q := range pyQuoted.FindAllStringSubmatch(m[1], -1) {
				allowed[strings.ToUpper(q[1])] = true
			}
			kept := routes[:0]
			for _, r := range routes {
				if allowed[r.method] {
					kept = append(kept, r)
				}
			}
			routes = kept
		}
	}
	if len(routes) == 0 {
		return drfViewSet{}, false
	}
	return drfViewSet{routes: routes, file: mod.path, line: lexLineOf(mod, cls)}, true
}

func lexLineOf(mod pyModule, cls pyClass) lex.Line { return mod.lines[cls.idx] }

// drfActionSpecs reads `@action(detail=..., methods=[...], url_path="...")`
// at body[i] and the def below it.
func drfActionSpecs(body []lex.Line, i int) ([]drfRouteSpec, bool) {
	text := callText(body, i, 6)
	d := drfActionKwD.FindStringSubmatch(text)
	if d == nil {
		return nil, false
	}
	name := ""
	for j := i; j < len(body) && j < i+8; j++ {
		if m := pyDefName.FindStringSubmatch(body[j].Code); m != nil {
			name = m[2]
			break
		}
	}
	if name == "" {
		return nil, false
	}
	seg := name
	if u := drfActionKwU.FindStringSubmatch(text); u != nil {
		seg = strings.Trim(u[1], "/")
	}
	methods := []string{"GET"}
	if mm := pyMethodsKw.FindStringSubmatch(text); mm != nil {
		methods = methods[:0]
		for _, q := range pyQuoted.FindAllStringSubmatch(mm[1], -1) {
			methods = append(methods, strings.ToUpper(q[1]))
		}
	}
	suffix := "/" + seg
	if d[1] == "True" {
		suffix = "/{pk}/" + seg
	}
	var out []drfRouteSpec
	for _, m := range methods {
		out = append(out, drfRouteSpec{m, suffix})
	}
	return out, true
}

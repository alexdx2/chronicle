package extract

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/alexdx2/chronicle-core/truth"
)

// Dependency rules. Lockfile rules list DIRECT dependencies only; manifest
// rules (rules_deps_manifest.go) step aside when a lockfile of their
// ecosystem sits in the same directory. Deps rules ignore IsTest.

func init() {
	Rules = append(Rules,
		funcRule{"lock.nuget", func(p string) bool { return baseName(p) == "packages.lock.json" }, applyLockNuget},
		funcRule{"lock.npm", func(p string) bool { return baseName(p) == "package-lock.json" }, applyLockNpm},
		funcRule{"lock.pnpm", func(p string) bool { return baseName(p) == "pnpm-lock.yaml" }, applyLockPnpm},
		funcRule{"lock.yarn", func(p string) bool { return baseName(p) == "yarn.lock" }, applyLockYarn},
		funcRule{"lock.poetry", func(p string) bool { return baseName(p) == "poetry.lock" }, applyLockPoetry},
		funcRule{"lock.uv", func(p string) bool { return baseName(p) == "uv.lock" }, applyLockUV},
	)
}

// depSet collects facts of one rule run, dropping repeated keys.
type depSet struct {
	rule  string
	f     *FileCtx
	seen  map[string]bool
	facts []truth.Fact
}

func newDepSet(rule string, f *FileCtx) *depSet {
	return &depSet{rule: rule, f: f, seen: map[string]bool{}}
}

// add records a dependency; anchors narrow the line search (see findDepLine).
func (s *depSet) add(eco, name, version, section string, anchors ...string) *truth.Fact {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	key := truth.DepKey(eco, name)
	if s.seen[key] {
		return nil
	}
	s.seen[key] = true
	s.facts = append(s.facts, truth.Fact{
		Class:   truth.ClassDeps,
		Key:     key,
		Attrs:   map[string]string{"name": name, "ecosystem": eco, "version": version, "section": section},
		Primary: evidence(s.f, s.f.Lines[findDepLine(s.f, name, anchors)]),
		Sources: []string{s.rule},
	})
	return &s.facts[len(s.facts)-1]
}

func nameBoundary(s string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(s) + `($|[^A-Za-z0-9_.-])`)
}

func anchorBoundary(s string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(s) + `($|[^A-Za-z0-9_-])`)
}

// findDepLine returns the 0-based index into f.Lines of the first line that
// mentions name after the given anchors (anchors that cannot be found are
// ignored). It falls back to the first line.
func findDepLine(f *FileCtx, name string, anchors []string) int {
	if len(f.Lines) == 0 {
		return 0
	}
	start := 0
	for _, a := range anchors {
		re := anchorBoundary(a)
		for i := start; i < len(f.Lines); i++ {
			if re.MatchString(f.Lines[i].Code) {
				start = i
				break
			}
		}
	}
	re := nameBoundary(name)
	for _, from := range []int{start, 0} {
		for i := from; i < len(f.Lines); i++ {
			if re.MatchString(f.Lines[i].Code) {
				return i
			}
		}
	}
	return 0
}

func isLocalSpec(v string) bool {
	return strings.HasPrefix(v, "file:") || strings.HasPrefix(v, "link:") || strings.HasPrefix(v, "workspace:")
}

func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- NuGet -----------------------------------------------------------------

func applyLockNuget(f *FileCtx) []truth.Fact {
	var doc struct {
		Dependencies map[string]map[string]struct {
			Type      string `json:"type"`
			Requested string `json:"requested"`
			Resolved  string `json:"resolved"`
		} `json:"dependencies"`
	}
	if json.Unmarshal(f.Raw, &doc) != nil {
		return nil
	}
	s := newDepSet("lock.nuget", f)
	for _, fw := range sortedKeys(doc.Dependencies) {
		for _, name := range sortedKeys(doc.Dependencies[fw]) {
			e := doc.Dependencies[fw][name]
			ver := e.Requested
			if ver == "" {
				ver = e.Resolved
			}
			switch e.Type {
			case "Direct":
				s.add("nuget", name, ver, "direct", fw)
			case "Project":
				s.add("project", name, "", "project", fw)
			}
		}
	}
	return s.facts
}

// ---- npm -------------------------------------------------------------------

var npmSections = []struct{ field, section string }{
	{"dependencies", "dependencies"},
	{"devDependencies", "devDependencies"},
	{"optionalDependencies", "optional"},
	{"peerDependencies", "peer"},
}

// packageJSONDeps returns name -> (version, section) from a package.json body.
func packageJSONDeps(raw []byte) (map[string][2]string, bool) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return nil, false
	}
	out := map[string][2]string{}
	for _, sec := range npmSections {
		var m map[string]string
		if json.Unmarshal(doc[sec.field], &m) != nil {
			continue
		}
		for n, v := range m {
			if _, dup := out[n]; !dup {
				out[n] = [2]string{v, sec.section}
			}
		}
	}
	return out, true
}

func npmLockBeside(f *FileCtx) bool {
	for _, g := range []string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock"} {
		if len(f.Tree.Siblings(f.Dir, g)) > 0 {
			return true
		}
	}
	return false
}

func applyLockNpm(f *FileCtx) []truth.Fact {
	var doc struct {
		Packages     map[string]map[string]json.RawMessage `json:"packages"`
		Dependencies map[string]struct {
			Dev bool `json:"dev"`
		} `json:"dependencies"`
	}
	if json.Unmarshal(f.Raw, &doc) != nil {
		return nil
	}
	s := newDepSet("lock.npm", f)
	if doc.Packages != nil {
		root := doc.Packages[""]
		for _, sec := range npmSections {
			var m map[string]string
			if json.Unmarshal(root[sec.field], &m) != nil {
				continue
			}
			for _, n := range sortedKeys(m) {
				if !isLocalSpec(m[n]) {
					s.add("npm", n, m[n], sec.section, `""`, sec.field)
				}
			}
		}
		return s.facts
	}
	// lockfile v1 lists transitive packages too; package.json selects the direct ones
	pj, ok := f.Tree.ReadFile(path.Join(f.Dir, "package.json"))
	if !ok {
		return nil
	}
	direct, ok := packageJSONDeps(pj)
	if !ok {
		return nil
	}
	for _, n := range sortedKeys(doc.Dependencies) {
		d, isDirect := direct[n]
		if !isDirect || isLocalSpec(d[0]) {
			continue
		}
		section := "dependencies"
		if doc.Dependencies[n].Dev {
			section = "devDependencies"
		}
		s.add("npm", n, d[0], section)
	}
	return s.facts
}

func applyLockPnpm(f *FileCtx) []truth.Fact {
	var doc struct {
		Importers    map[string]map[string]yaml.Node `yaml:"importers"`
		Dependencies yaml.Node                       `yaml:"dependencies"`
		DevDeps      yaml.Node                       `yaml:"devDependencies"`
		OptionalDeps yaml.Node                       `yaml:"optionalDependencies"`
	}
	if yaml.Unmarshal(f.Raw, &doc) != nil {
		return nil
	}
	s := newDepSet("lock.pnpm", f)
	sections := []struct{ field, section string }{
		{"dependencies", "dependencies"}, {"devDependencies", "devDependencies"}, {"optionalDependencies", "optional"},
	}
	emit := func(importer, field, section string, n *yaml.Node, anchors []string) {
		if n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			name, val := n.Content[i].Value, n.Content[i+1]
			ver := ""
			switch val.Kind {
			case yaml.ScalarNode:
				ver = val.Value
			case yaml.MappingNode:
				for j := 0; j+1 < len(val.Content); j += 2 {
					switch val.Content[j].Value {
					case "specifier":
						ver = val.Content[j+1].Value
					case "version":
						if ver == "" {
							ver = val.Content[j+1].Value
						}
					}
				}
			}
			if isLocalSpec(ver) {
				continue
			}
			if ft := s.add("npm", name, ver, section, anchors...); ft != nil && importer != "." && importer != "" {
				ft.Attrs["importer"] = importer
			}
		}
	}
	if doc.Importers != nil {
		for _, imp := range sortedKeys(doc.Importers) {
			for _, sec := range sections {
				n, ok := doc.Importers[imp][sec.field]
				if !ok {
					continue
				}
				anchors := []string{"importers"}
				if imp != "." {
					anchors = append(anchors, imp)
				}
				emit(imp, sec.field, sec.section, &n, append(anchors, sec.field))
			}
		}
		return s.facts
	}
	// lockfile v5/v6 of a single project: top-level maps
	emit(".", "dependencies", "dependencies", &doc.Dependencies, []string{"dependencies"})
	emit(".", "devDependencies", "devDependencies", &doc.DevDeps, []string{"devDependencies"})
	emit(".", "optionalDependencies", "optional", &doc.OptionalDeps, []string{"optionalDependencies"})
	return s.facts
}

var yarnHeaderRe = regexp.MustCompile(`^("?)(@?[^@\s"][^@\s"]*)@`)

func applyLockYarn(f *FileCtx) []truth.Fact {
	pj, ok := f.Tree.ReadFile(path.Join(f.Dir, "package.json"))
	if !ok {
		return nil
	}
	direct, ok := packageJSONDeps(pj)
	if !ok {
		return nil
	}
	header := map[string]int{} // package name -> 0-based line of its first header
	for i, l := range f.Lines {
		c := strings.TrimRight(l.Code, " \r")
		if c == "" || c[0] == ' ' || c[0] == '#' || !strings.HasSuffix(c, ":") {
			continue
		}
		for _, part := range strings.Split(strings.TrimSuffix(c, ":"), ",") {
			if m := yarnHeaderRe.FindStringSubmatch(strings.TrimSpace(part)); m != nil {
				if _, seen := header[m[2]]; !seen {
					header[m[2]] = i
				}
			}
		}
	}
	s := newDepSet("lock.yarn", f)
	for _, n := range sortedKeys(direct) {
		i, ok := header[n]
		if !ok || isLocalSpec(direct[n][0]) {
			continue
		}
		s.add("npm", n, direct[n][0], direct[n][1])
		s.facts[len(s.facts)-1].Primary = evidence(f, f.Lines[i])
	}
	return s.facts
}

// ---- Python ----------------------------------------------------------------

type pyDep struct{ name, version, section string }

var pyNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// pep508 splits a requirement into name and version spec.
func pep508(s string) (name, version string, ok bool) {
	s = strings.TrimSpace(s)
	i := strings.IndexAny(s, " \t[=<>!~;(@")
	if i < 0 {
		i = len(s)
	}
	name = s[:i]
	if !pyNameRe.MatchString(name) {
		return "", "", false
	}
	rest := strings.TrimSpace(s[i:])
	if strings.HasPrefix(rest, "[") {
		if j := strings.Index(rest, "]"); j >= 0 {
			rest = strings.TrimSpace(rest[j+1:])
		}
	}
	if j := strings.Index(rest, ";"); j >= 0 {
		rest = rest[:j]
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "@") {
		rest = ""
	}
	rest = strings.TrimSpace(strings.Trim(rest, "()"))
	return name, strings.ReplaceAll(rest, " ", ""), true
}

func anySlice(v any) []any {
	l, _ := v.([]any)
	return l
}

func anyMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// pyprojectDeps lists declared dependencies of a pyproject.toml document.
func pyprojectDeps(raw []byte) ([]pyDep, bool) {
	var doc map[string]any
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return nil, false
	}
	var out []pyDep
	pep := func(list []any, section string) {
		for _, e := range list {
			if s, ok := e.(string); ok {
				if n, v, ok := pep508(s); ok {
					out = append(out, pyDep{n, v, section})
				}
			}
		}
	}
	poetry := func(tbl map[string]any, section string) {
		for _, n := range sortedKeys(tbl) {
			if strings.EqualFold(n, "python") {
				continue
			}
			ver := ""
			switch v := tbl[n].(type) {
			case string:
				ver = v
			case map[string]any:
				if _, local := v["path"]; local {
					continue
				}
				ver, _ = v["version"].(string)
			}
			out = append(out, pyDep{n, ver, section})
		}
	}
	project := anyMap(doc["project"])
	pep(anySlice(project["dependencies"]), "dependencies")
	opt := anyMap(project["optional-dependencies"])
	for _, g := range sortedKeys(opt) {
		pep(anySlice(opt[g]), "optional")
	}
	p := anyMap(anyMap(doc["tool"])["poetry"])
	poetry(anyMap(p["dependencies"]), "dependencies")
	poetry(anyMap(p["dev-dependencies"]), "devDependencies")
	groups := anyMap(p["group"])
	for _, g := range sortedKeys(groups) {
		poetry(anyMap(anyMap(groups[g])["dependencies"]), "devDependencies")
	}
	return out, true
}

// tomlPackageLines maps canonical package name -> 0-based line of its
// `name = "…"` inside a [[package]] table.
func tomlPackageLines(f *FileCtx) map[string]int {
	nameRe := regexp.MustCompile(`^name\s*=\s*"([^"]+)"`)
	out := map[string]int{}
	in := false
	for i, l := range f.Lines {
		t := strings.TrimSpace(l.Code)
		switch {
		case strings.HasPrefix(t, "[["):
			in = t == "[[package]]"
		case strings.HasPrefix(t, "["):
			in = false
		case in:
			if m := nameRe.FindStringSubmatch(t); m != nil {
				k := truth.DepKey("python", m[1])
				if _, seen := out[k]; !seen {
					out[k] = i
				}
				in = false
			}
		}
	}
	return out
}

func pyLockBeside(f *FileCtx) bool {
	return len(f.Tree.Siblings(f.Dir, "poetry.lock")) > 0 || len(f.Tree.Siblings(f.Dir, "uv.lock")) > 0
}

func applyLockPoetry(f *FileCtx) []truth.Fact {
	pp, ok := f.Tree.ReadFile(path.Join(f.Dir, "pyproject.toml"))
	if !ok {
		return nil
	}
	deps, ok := pyprojectDeps(pp)
	if !ok {
		return nil
	}
	direct := map[string]pyDep{}
	for _, d := range deps {
		k := truth.DepKey("python", d.name)
		if _, dup := direct[k]; !dup {
			direct[k] = d
		}
	}
	var lock struct {
		Package []struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
		} `toml:"package"`
	}
	if _, err := toml.Decode(string(f.Raw), &lock); err != nil {
		return nil
	}
	lines := tomlPackageLines(f)
	s := newDepSet("lock.poetry", f)
	for _, p := range lock.Package {
		k := truth.DepKey("python", p.Name)
		d, ok := direct[k]
		if !ok {
			continue
		}
		if ft := s.add("python", p.Name, p.Version, d.section); ft != nil {
			if i, ok := lines[k]; ok {
				ft.Primary = evidence(f, f.Lines[i])
			}
		}
	}
	return s.facts
}

func applyLockUV(f *FileCtx) []truth.Fact {
	type ref struct {
		Name string `toml:"name"`
	}
	var lock struct {
		Package []struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
			Source  struct {
				Editable string `toml:"editable"`
				Virtual  string `toml:"virtual"`
			} `toml:"source"`
			Dependencies    []ref            `toml:"dependencies"`
			DevDependencies map[string][]ref `toml:"dev-dependencies"`
			Metadata        struct {
				RequiresDist []ref `toml:"requires-dist"`
			} `toml:"metadata"`
		} `toml:"package"`
	}
	if _, err := toml.Decode(string(f.Raw), &lock); err != nil {
		return nil
	}
	root := -1
	for i, p := range lock.Package {
		if p.Source.Editable == "." || p.Source.Virtual == "." {
			root = i
			break
		}
	}
	if root < 0 {
		return nil
	}
	versions := map[string]string{}
	for i, p := range lock.Package {
		if i == root {
			continue
		}
		k := truth.DepKey("python", p.Name)
		if _, dup := versions[k]; !dup {
			versions[k] = p.Version
		}
	}
	lines := tomlPackageLines(f)
	s := newDepSet("lock.uv", f)
	emit := func(refs []ref, section string) {
		for _, r := range refs {
			k := truth.DepKey("python", r.Name)
			ver, ok := versions[k]
			if !ok {
				continue
			}
			if ft := s.add("python", r.Name, ver, section); ft != nil {
				if i, ok := lines[k]; ok {
					ft.Primary = evidence(f, f.Lines[i])
				}
			}
		}
	}
	r := lock.Package[root]
	emit(r.Dependencies, "dependencies")
	emit(r.Metadata.RequiresDist, "dependencies")
	for _, g := range sortedKeys(r.DevDependencies) {
		emit(r.DevDependencies[g], "devDependencies")
	}
	return s.facts
}

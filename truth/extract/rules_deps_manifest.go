package extract

import (
	"path"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules,
		funcRule{"manifest.csproj", func(p string) bool {
			return hasExt(p, ".csproj", ".fsproj") || baseName(p) == "Directory.Packages.props"
		}, applyManifestCsproj},
		funcRule{"manifest.packages_config", func(p string) bool { return baseName(p) == "packages.config" }, applyManifestPackagesConfig},
		funcRule{"manifest.npm", func(p string) bool { return baseName(p) == "package.json" }, applyManifestNpm},
		funcRule{"manifest.pyproject", func(p string) bool { return baseName(p) == "pyproject.toml" }, applyManifestPyproject},
		funcRule{"manifest.requirements", isRequirementsPath, applyManifestRequirements},
	)
}

// ---- .NET ------------------------------------------------------------------

var (
	xmlComment    = regexp.MustCompile(`(?s)<!--.*?-->`)
	pkgRefRe      = regexp.MustCompile(`(?s)<(?:PackageReference|GlobalPackageReference)\b([^>]*?)(?:/>|>(.*?)</(?:PackageReference|GlobalPackageReference)>)`)
	pkgVersionRe  = regexp.MustCompile(`(?s)<PackageVersion\b([^>]*?)/?>`)
	projRefRe     = regexp.MustCompile(`(?s)<ProjectReference\b([^>]*?)/?>`)
	xmlAttrRe     = regexp.MustCompile(`([A-Za-z:]+)\s*=\s*"([^"]*)"`)
	childVersion  = regexp.MustCompile(`(?s)<Version>\s*([^<]*?)\s*</Version>`)
	centralDocCap = "Directory.Packages.props"
)

func xmlAttrs(s string) map[string]string {
	m := map[string]string{}
	for _, a := range xmlAttrRe.FindAllStringSubmatch(s, -1) {
		if _, dup := m[a[1]]; !dup {
			m[a[1]] = a[2]
		}
	}
	return m
}

// blankComments replaces XML comments by spaces, keeping newlines and offsets.
func blankComments(s string) string {
	return xmlComment.ReplaceAllStringFunc(s, func(c string) string {
		b := []byte(c)
		for i, ch := range b {
			if ch != '\n' {
				b[i] = ' '
			}
		}
		return string(b)
	})
}

func lineAt(f *FileCtx, text string, off int) int {
	i := strings.Count(text[:off], "\n")
	if i >= len(f.Lines) {
		i = len(f.Lines) - 1
	}
	return i
}

// centralVersions reads PackageVersion entries of the nearest ancestor
// Directory.Packages.props (the file's own directory first).
func centralVersions(f *FileCtx) map[string]string {
	dir := f.Dir
	for {
		if b, ok := f.Tree.ReadFile(path.Join(dir, centralDocCap)); ok {
			m := map[string]string{}
			for _, x := range pkgVersionRe.FindAllStringSubmatch(blankComments(string(b)), -1) {
				a := xmlAttrs(x[1])
				n := a["Include"]
				if n == "" {
					n = a["Update"]
				}
				if n != "" {
					m[strings.ToLower(n)] = a["Version"]
				}
			}
			return m
		}
		if dir == "." || dir == "" || dir == "/" {
			return nil
		}
		dir = path.Dir(dir)
	}
}

func applyManifestCsproj(f *FileCtx) []truth.Fact {
	if len(f.Tree.Siblings(f.Dir, "packages.lock.json")) > 0 {
		return nil
	}
	text := blankComments(string(f.Raw))
	var central map[string]string
	loaded := false
	s := newDepSet("manifest.csproj", f)
	for _, m := range pkgRefRe.FindAllStringSubmatchIndex(text, -1) {
		a := xmlAttrs(text[m[2]:m[3]])
		name := a["Include"]
		if name == "" {
			name = a["Update"]
		}
		if name == "" {
			continue
		}
		ver := a["Version"]
		if ver == "" {
			ver = a["VersionOverride"]
		}
		if ver == "" && m[4] >= 0 {
			if c := childVersion.FindStringSubmatch(text[m[4]:m[5]]); c != nil {
				ver = c[1]
			}
		}
		if ver == "" {
			if !loaded {
				central, loaded = centralVersions(f), true
			}
			ver = central[strings.ToLower(name)]
		}
		if ft := s.add("nuget", name, ver, "dependencies"); ft != nil {
			ft.Primary = evidence(f, f.Lines[lineAt(f, text, m[0]+strings.Index(text[m[0]:m[1]], name))])
		}
	}
	for _, m := range projRefRe.FindAllStringSubmatchIndex(text, -1) {
		inc := xmlAttrs(text[m[2]:m[3]])["Include"]
		if inc == "" {
			continue
		}
		base := path.Base(strings.ReplaceAll(inc, "\\", "/"))
		stem := strings.TrimSuffix(base, path.Ext(base))
		if ft := s.add("project", stem, "", "project"); ft != nil {
			ft.Primary = evidence(f, f.Lines[lineAt(f, text, m[0])])
		}
	}
	return s.facts
}

// ---- legacy packages.config ------------------------------------------------

var (
	pkgConfigRoot = regexp.MustCompile(`(?s)<packages\b`)
	pkgConfigItem = regexp.MustCompile(`(?s)<package\b([^>]*?)/?>`)
)

// applyManifestPackagesConfig reads the pre-PackageReference NuGet manifest:
// <packages><package id="X" version="1.0" /></packages>. A packages.lock.json
// beside it wins, as for csproj.
func applyManifestPackagesConfig(f *FileCtx) []truth.Fact {
	if len(f.Tree.Siblings(f.Dir, "packages.lock.json")) > 0 {
		return nil
	}
	text := blankComments(string(f.Raw))
	if !pkgConfigRoot.MatchString(text) {
		return nil
	}
	s := newDepSet("manifest.packages_config", f)
	for _, m := range pkgConfigItem.FindAllStringSubmatchIndex(text, -1) {
		a := xmlAttrs(text[m[2]:m[3]])
		name := a["id"]
		if name == "" {
			continue
		}
		if ft := s.add("nuget", name, a["version"], "dependencies"); ft != nil {
			ft.Primary = evidence(f, f.Lines[lineAt(f, text, m[0])])
		}
	}
	return s.facts
}

// ---- npm -------------------------------------------------------------------

func applyManifestNpm(f *FileCtx) []truth.Fact {
	if npmLockBeside(f) {
		return nil
	}
	deps, ok := packageJSONDeps(f.Raw)
	if !ok {
		return nil
	}
	s := newDepSet("manifest.npm", f)
	for _, sec := range npmSections {
		for _, n := range sortedKeys(deps) {
			if d := deps[n]; d[1] == sec.section && !isLocalSpec(d[0]) {
				s.add("npm", n, d[0], d[1], sec.field)
			}
		}
	}
	return s.facts
}

// ---- Python ----------------------------------------------------------------

func applyManifestPyproject(f *FileCtx) []truth.Fact {
	if pyLockBeside(f) {
		return nil
	}
	deps, ok := pyprojectDeps(f.Raw)
	if !ok {
		return nil
	}
	s := newDepSet("manifest.pyproject", f)
	for _, d := range deps {
		s.add("python", d.name, d.version, d.section, "dependencies")
	}
	return s.facts
}

func isRequirementsPath(p string) bool {
	b := strings.ToLower(baseName(p))
	if strings.HasPrefix(b, "requirements") && strings.HasSuffix(b, ".txt") {
		return true
	}
	return strings.HasSuffix(b, ".txt") && strings.EqualFold(baseName(path.Dir(p)), "requirements")
}

func applyManifestRequirements(f *FileCtx) []truth.Fact {
	section := baseName(f.Path)
	s := newDepSet("manifest.requirements", f)
	for i, l := range f.Lines {
		t := strings.TrimSpace(l.Raw)
		if j := strings.Index(t, " #"); j >= 0 {
			t = t[:j]
		}
		t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "\\"))
		if t == "" || t[0] == '#' || t[0] == '-' || t[0] == '.' || t[0] == '/' || strings.Contains(t, "://") {
			continue
		}
		name, ver, ok := pep508(t)
		if !ok {
			continue
		}
		if ft := s.add("python", name, ver, section); ft != nil {
			ft.Primary = evidence(f, f.Lines[i])
		}
	}
	return s.facts
}

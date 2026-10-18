package extract

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/alexdx2/chronicle-core/truth"
)

// Packages: what this repository publishes, read from manifests only. A
// private workspace package still counts (other code in the fleet may import
// it); a workspace root and anything under node_modules does not. Keys use
// CanonDep so a packages fact here and a deps fact in another repository
// share one key.

func init() {
	Rules = append(Rules,
		funcRule{"npm.name", func(p string) bool { return baseName(p) == "package.json" }, applyPackageNpm},
		funcRule{"csproj.package", func(p string) bool { return hasExt(p, ".csproj", ".fsproj") }, applyPackageCsproj},
		funcRule{"pyproject.name", func(p string) bool { return baseName(p) == "pyproject.toml" }, applyPackagePyproject},
	)
}

func inNodeModules(p string) bool {
	for _, s := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		if s == "node_modules" {
			return true
		}
	}
	return false
}

func packageFact(rule string, f *FileCtx, eco, name string, line int) *truth.Fact {
	name = strings.TrimSpace(name)
	if name == "" || !literalDepName(name) || len(f.Lines) == 0 {
		return nil
	}
	if line < 0 || line >= len(f.Lines) {
		line = 0
	}
	return &truth.Fact{
		Class:   truth.ClassPackages,
		Key:     truth.CanonDep(eco, name),
		Attrs:   map[string]string{"name": name, "ecosystem": eco, "manifest": f.Path},
		Primary: evidence(f, f.Lines[line]),
		Sources: []string{rule},
	}
}

// firstLine returns the index of the first line matching re (0 when none).
func firstLine(f *FileCtx, re *regexp.Regexp, from int) int {
	for i := from; i < len(f.Lines); i++ {
		if re.MatchString(f.Lines[i].Raw) {
			return i
		}
	}
	return 0
}

// ---- npm ----

func applyPackageNpm(f *FileCtx) []truth.Fact {
	if inNodeModules(f.Path) {
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(f.Raw, &doc) != nil {
		return nil
	}
	if _, root := doc["workspaces"]; root {
		return nil
	}
	if len(f.Tree.Siblings(f.Dir, "pnpm-workspace.yaml")) > 0 {
		return nil
	}
	var name string
	if json.Unmarshal(doc["name"], &name) != nil || strings.TrimSpace(name) == "" {
		return nil
	}
	q, _ := json.Marshal(name)
	re := regexp.MustCompile(`"name"\s*:\s*` + regexp.QuoteMeta(string(q)))
	if ft := packageFact("npm.name", f, "npm", name, firstLine(f, re, 0)); ft != nil {
		return []truth.Fact{*ft}
	}
	return nil
}

// ---- .NET ----

var (
	csPackageID   = regexp.MustCompile(`(?s)<PackageId>\s*([^<]*?)\s*</PackageId>`)
	csAssemblyNm  = regexp.MustCompile(`(?s)<AssemblyName>\s*([^<]*?)\s*</AssemblyName>`)
	csIsPackable  = regexp.MustCompile(`(?is)<IsPackable>\s*([^<]*?)\s*</IsPackable>`)
	csPackOnBuild = regexp.MustCompile(`(?is)<GeneratePackageOnBuild>\s*([^<]*?)\s*</GeneratePackageOnBuild>`)
)

func applyPackageCsproj(f *FileCtx) []truth.Fact {
	text := blankComments(string(f.Raw))
	truthy := func(re *regexp.Regexp) bool {
		m := re.FindStringSubmatch(text)
		return m != nil && strings.EqualFold(m[1], "true")
	}
	// An explicit IsPackable=false means "pack nothing", whatever else is set.
	if m := csIsPackable.FindStringSubmatch(text); m != nil && strings.EqualFold(m[1], "false") {
		return nil
	}
	var name string
	var at int
	if m := csPackageID.FindStringSubmatchIndex(text); m != nil {
		name, at = text[m[2]:m[3]], m[0]
	} else if truthy(csIsPackable) || truthy(csPackOnBuild) {
		if a := csAssemblyNm.FindStringSubmatchIndex(text); a != nil && strings.TrimSpace(text[a[2]:a[3]]) != "" {
			name, at = text[a[2]:a[3]], a[0]
		} else {
			base := baseName(f.Path)
			name = strings.TrimSuffix(base, base[strings.LastIndex(base, "."):])
			at = -1
		}
	} else {
		return nil
	}
	line := 0
	if at >= 0 {
		line = lineAt(f, text, at)
	}
	if ft := packageFact("csproj.package", f, "nuget", name, line); ft != nil {
		return []truth.Fact{*ft}
	}
	return nil
}

// ---- Python ----

var pyNameLine = regexp.MustCompile(`^\s*name\s*=`)

func applyPackagePyproject(f *FileCtx) []truth.Fact {
	var doc struct {
		Project struct {
			Name string `toml:"name"`
		} `toml:"project"`
		Tool struct {
			Poetry struct {
				Name string `toml:"name"`
			} `toml:"poetry"`
		} `toml:"tool"`
	}
	if _, err := toml.Decode(string(f.Raw), &doc); err != nil {
		return nil
	}
	name, header := doc.Project.Name, "[project]"
	if strings.TrimSpace(name) == "" {
		name, header = doc.Tool.Poetry.Name, "[tool.poetry]"
	}
	if strings.TrimSpace(name) == "" {
		return nil
	}
	h := 0
	for i, l := range f.Lines {
		if strings.TrimSpace(l.Raw) == header {
			h = i
			break
		}
	}
	if ft := packageFact("pyproject.name", f, "python", name, firstLine(f, pyNameLine, h)); ft != nil {
		return []truth.Fact{*ft}
	}
	return nil
}

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

// nonPublishDirs hold sample or test manifests, not published packages.
var nonPublishDirs = map[string]bool{
	"test": true, "tests": true, "fixtures": true, "__fixtures__": true,
	"examples": true, "example": true, "templates": true,
}

func inNonPublishDir(p string) bool {
	parts := strings.Split(strings.ReplaceAll(p, "\\", "/"), "/")
	for _, s := range parts[:len(parts)-1] {
		if nonPublishDirs[strings.ToLower(s)] {
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
	if inNodeModules(f.Path) || inNonPublishDir(f.Path) {
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(f.Raw, &doc) != nil {
		return nil
	}
	if _, root := doc["workspaces"]; root {
		return nil
	}
	if len(f.Tree.Siblings(f.Dir, "pnpm-workspace.yaml")) > 0 || len(f.Tree.Siblings(f.Dir, "lerna.json")) > 0 {
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

// Elements may carry attributes (Condition="...").
var (
	csPackageID   = regexp.MustCompile(`(?s)<PackageId(\s[^>]*)?>\s*([^<]*?)\s*</PackageId>`)
	csAssemblyNm  = regexp.MustCompile(`(?s)<AssemblyName(\s[^>]*)?>\s*([^<]*?)\s*</AssemblyName>`)
	csIsPackable  = regexp.MustCompile(`(?is)<IsPackable(\s[^>]*)?>\s*([^<]*?)\s*</IsPackable>`)
	csPackOnBuild = regexp.MustCompile(`(?is)<GeneratePackageOnBuild(\s[^>]*)?>\s*([^<]*?)\s*</GeneratePackageOnBuild>`)
	csCondition   = regexp.MustCompile(`(?i)\bCondition\s*=`)
)

func applyPackageCsproj(f *FileCtx) []truth.Fact {
	text := blankComments(string(f.Raw))
	// truthy: any occurrence (conditional or not) set to true.
	truthy := func(re *regexp.Regexp) bool {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if strings.EqualFold(m[2], "true") {
				return true
			}
		}
		return false
	}
	// An unconditional IsPackable=false means "pack nothing", whatever else
	// is set. A conditional false only applies to some configurations.
	for _, m := range csIsPackable.FindAllStringSubmatch(text, -1) {
		if strings.EqualFold(m[2], "false") && !csCondition.MatchString(m[1]) {
			return nil
		}
	}
	assembly := func() (string, int) {
		for _, a := range csAssemblyNm.FindAllStringSubmatchIndex(text, -1) {
			if v := strings.TrimSpace(text[a[4]:a[5]]); v != "" && !strings.Contains(v, "$(") {
				return v, a[0]
			}
		}
		base := baseName(f.Path)
		return strings.TrimSuffix(base, base[strings.LastIndex(base, "."):]), -1
	}
	var name string
	var at int
	if m := csPackageID.FindStringSubmatchIndex(text); m != nil {
		name, at = strings.TrimSpace(text[m[4]:m[5]]), m[0]
		if name == "$(AssemblyName)" {
			name, at = assembly()
		}
	} else if truthy(csIsPackable) || truthy(csPackOnBuild) {
		name, at = assembly()
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
				Name        string `toml:"name"`
				PackageMode *bool  `toml:"package-mode"`
			} `toml:"poetry"`
		} `toml:"tool"`
	}
	if _, err := toml.Decode(string(f.Raw), &doc); err != nil {
		return nil
	}
	if pm := doc.Tool.Poetry.PackageMode; pm != nil && !*pm {
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

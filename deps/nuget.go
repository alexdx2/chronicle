package deps

import (
	"regexp"
	"strings"
)

type nuget struct{}

var (
	nugetRefRe     = regexp.MustCompile(`(PackageReference|PackageVersion|ProjectReference)\s+Include="([^"]+)"`)
	nugetVersionRe = regexp.MustCompile(`\bVersion="([^"]*)"`)
	nugetIDRe      = regexp.MustCompile(`<PackageId>\s*([^<]*?)\s*</PackageId>`)
	nugetAsmRe     = regexp.MustCompile(`<AssemblyName>\s*([^<]*?)\s*</AssemblyName>`)
	nugetUnpackRe  = regexp.MustCompile(`<IsPackable>\s*false\s*</IsPackable>`)
)

func (nuget) matches(p string) bool {
	b := base(p)
	if b == "Directory.Packages.props" || b == "Directory.Build.props" {
		return true
	}
	for _, ext := range []string{".csproj", ".fsproj", ".vbproj"} {
		if strings.HasSuffix(b, ext) {
			return true
		}
	}
	return false
}

func (nuget) parse(_, rel string, body []byte) (Report, error) {
	var r Report
	isProps := strings.HasSuffix(base(rel), ".props")
	var packageID, assembly string
	var idLine, asmLine int
	var idText, asmText string
	unpackable := false
	for i, line := range strings.Split(string(body), "\n") {
		n := i + 1
		if m := nugetRefRe.FindStringSubmatch(line); m != nil {
			d := Dependency{Ecosystem: "nuget", File: rel, Line: n, Section: "dependencies", Text: m[0]}
			if m[1] == "ProjectReference" {
				d.Name = Normalize("nuget", strings.ReplaceAll(m[2], `\`, "/"))
				d.Internal = true
			} else {
				d.Name = Normalize("nuget", m[2])
				if v := nugetVersionRe.FindStringSubmatch(line); v != nil {
					d.Version = v[1]
				}
			}
			r.Depends = append(r.Depends, d)
		}
		if m := nugetIDRe.FindStringSubmatch(line); m != nil && packageID == "" {
			packageID, idLine, idText = m[1], n, m[0]
		}
		if m := nugetAsmRe.FindStringSubmatch(line); m != nil && assembly == "" {
			assembly, asmLine, asmText = m[1], n, m[0]
		}
		if nugetUnpackRe.MatchString(line) {
			unpackable = true
		}
	}
	if isProps || unpackable {
		return r, nil
	}
	name, line, text := packageID, idLine, idText
	if name == "" {
		name, line, text = assembly, asmLine, asmText
	}
	if name == "" {
		name, line = strings.TrimSuffix(base(rel), pathExt(rel)), 1
		text = strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(body), "\n", 2)[0], "\ufeff"))
	}
	r.Publishes = append(r.Publishes, Package{Name: Normalize("nuget", name), Ecosystem: "nuget", File: rel, Line: line, Text: text})
	return r, nil
}

func pathExt(p string) string {
	b := base(p)
	if i := strings.LastIndex(b, "."); i >= 0 {
		return b[i:]
	}
	return ""
}

// fillCentralNuGetVersions gives PackageReference entries without a Version
// the version centrally managed by a Directory.Packages.props PackageVersion.
func fillCentralNuGetVersions(ds []Dependency) {
	central := map[string]string{}
	for _, d := range ds {
		if d.Ecosystem == "nuget" && d.Version != "" && base(d.File) == "Directory.Packages.props" {
			if _, ok := central[d.Name]; !ok {
				central[d.Name] = d.Version
			}
		}
	}
	for i := range ds {
		d := &ds[i]
		if d.Ecosystem == "nuget" && !d.Internal && d.Version == "" {
			d.Version = central[d.Name]
		}
	}
}

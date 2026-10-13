package deps

import (
	"bytes"
	"encoding/xml"
	"path"
	"regexp"
	"strings"
)

type jvm struct{}

func (jvm) matches(p string) bool {
	b := base(p)
	return b == "pom.xml" || b == "build.gradle" || b == "build.gradle.kts"
}

func (jvm) parse(root, rel string, body []byte) (Report, error) {
	if base(rel) == "pom.xml" {
		return parsePom(rel, body)
	}
	return parseGradle(root, rel, body), nil
}

type pomDoc struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Parent     struct {
		GroupID string `xml:"groupId"`
	} `xml:"parent"`
	Dependencies []struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
		Scope      string `xml:"scope"`
	} `xml:"dependencies>dependency"`
}

func parsePom(rel string, body []byte) (Report, error) {
	var doc pomDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return Report{}, err
	}
	var r Report
	ownLine, depLines := pomLines(body)
	group := strings.TrimSpace(doc.GroupID)
	if group == "" {
		group = strings.TrimSpace(doc.Parent.GroupID)
	}
	if art := strings.TrimSpace(doc.ArtifactID); art != "" && group != "" {
		r.Publishes = append(r.Publishes, Package{
			Name: Normalize("maven", group+":"+art), Ecosystem: "maven", File: rel, Line: ownLine, Text: "artifactId",
		})
	}
	for i, d := range doc.Dependencies {
		art := strings.TrimSpace(d.ArtifactID)
		scope := strings.TrimSpace(d.Scope)
		if scope == "" {
			scope = "compile"
		}
		dep := Dependency{
			Name: Normalize("maven", strings.TrimSpace(d.GroupID)+":"+art), Version: strings.TrimSpace(d.Version),
			Section: scope, Ecosystem: "maven", File: rel, Text: "<artifactId>" + art + "</artifactId>",
		}
		if i < len(depLines) {
			dep.Line = depLines[i]
		}
		r.Depends = append(r.Depends, dep)
	}
	return r, nil
}

// pomLines walks the document and returns the line of the project's own
// artifactId and, in document order, of the artifactId of every
// project/dependencies/dependency (not dependencyManagement, plugins or
// exclusions).
func pomLines(body []byte) (own int, deps []int) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var stack []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return own, deps
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			path := strings.Join(stack, "/")
			switch path {
			case "project/artifactId":
				own = lineOf(body, int(dec.InputOffset()))
			case "project/dependencies/dependency":
				deps = append(deps, 0)
			case "project/dependencies/dependency/artifactId":
				deps[len(deps)-1] = lineOf(body, int(dec.InputOffset()))
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
}

var (
	gradleDepRe     = regexp.MustCompile(`(implementation|api|compileOnly|runtimeOnly|testImplementation)\s*\(?\s*['"]([^'"]+)['"]`)
	gradleGroupRe   = regexp.MustCompile(`^\s*group\s*=\s*['"]([^'"]+)['"]`)
	gradleRootNameR = regexp.MustCompile(`rootProject\.name\s*=\s*['"]([^'"]+)['"]`)
)

func parseGradle(root, rel string, body []byte) Report {
	var r Report
	group, groupLine := "", 0
	for i, line := range strings.Split(string(body), "\n") {
		n := i + 1
		if m := gradleDepRe.FindStringSubmatch(line); m != nil {
			parts := strings.Split(m[2], ":")
			if len(parts) >= 2 {
				d := Dependency{
					Name: Normalize("maven", parts[0]+":"+parts[1]), Section: m[1],
					Ecosystem: "maven", File: rel, Line: n, Text: m[0],
				}
				if len(parts) >= 3 {
					d.Version = parts[2]
				}
				r.Depends = append(r.Depends, d)
			}
		}
		if m := gradleGroupRe.FindStringSubmatch(line); m != nil && group == "" {
			group, groupLine = m[1], n
		}
	}
	if group != "" {
		r.Publishes = append(r.Publishes, Package{
			Name: Normalize("maven", group+":"+gradleProjectName(root, rel, body)), Ecosystem: "maven", File: rel, Line: groupLine, Text: "group",
		})
	}
	return r
}

// gradleProjectName is rootProject.name from the build script or the
// settings script next to it, else the name of the containing directory.
func gradleProjectName(root, rel string, body []byte) string {
	if m := gradleRootNameR.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	dir := path.Dir(strings.ReplaceAll(rel, `\`, "/"))
	for _, settings := range []string{"settings.gradle", "settings.gradle.kts"} {
		if b, err := osReadFile(joinRoot(root, path.Join(dir, settings))); err == nil {
			if m := gradleRootNameR.FindSubmatch(b); m != nil {
				return string(m[1])
			}
		}
	}
	if dir == "." {
		return path.Base(strings.ReplaceAll(root, `\`, "/"))
	}
	return path.Base(dir)
}

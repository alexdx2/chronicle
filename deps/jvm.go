package deps

import (
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
	text := string(body)
	artifactTag := func(a string) string { return "<artifactId>" + a + "</artifactId>" }
	// The project's own artifactId comes before the dependencies; the cursor
	// then follows the dependencies in document order.
	cursor := 0
	group := strings.TrimSpace(doc.GroupID)
	if group == "" {
		group = strings.TrimSpace(doc.Parent.GroupID)
	}
	if doc.ArtifactID != "" {
		own := strings.Index(text, artifactTag(strings.TrimSpace(doc.ArtifactID)))
		if own >= 0 {
			cursor = own + 1
		}
		if group != "" {
			r.Publishes = append(r.Publishes, Package{
				Name: Normalize("maven", group+":"+strings.TrimSpace(doc.ArtifactID)), Ecosystem: "maven",
				File: rel, Line: lineOf(body, own),
			})
		}
	}
	for _, d := range doc.Dependencies {
		art := strings.TrimSpace(d.ArtifactID)
		scope := strings.TrimSpace(d.Scope)
		if scope == "" {
			scope = "compile"
		}
		dep := Dependency{
			Name: Normalize("maven", strings.TrimSpace(d.GroupID)+":"+art), Version: strings.TrimSpace(d.Version),
			Section: scope, Ecosystem: "maven", File: rel, Text: artifactTag(art),
		}
		if i := strings.Index(text[cursor:], artifactTag(art)); i >= 0 {
			dep.Line = lineOf(body, cursor+i)
			cursor += i + 1
		}
		r.Depends = append(r.Depends, dep)
	}
	return r, nil
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
			Name: Normalize("maven", group+":"+gradleProjectName(root, rel, body)), Ecosystem: "maven", File: rel, Line: groupLine,
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

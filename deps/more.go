package deps

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Manifest parsers for Ruby (Gemfile), PHP (composer.json), Rust (Cargo.toml)
// and legacy NuGet (packages.config). Direct dependencies only; the lock files
// list the transitive closure and are not read.

type ruby struct{}
type php struct{}
type rust struct{}
type nugetConfig struct{}

func (ruby) matches(p string) bool        { return base(p) == "Gemfile" }
func (php) matches(p string) bool         { return base(p) == "composer.json" }
func (rust) matches(p string) bool        { return base(p) == "Cargo.toml" }
func (nugetConfig) matches(p string) bool { return strings.EqualFold(base(p), "packages.config") }

var (
	gemLineRe   = regexp.MustCompile(`^\s*gem\s+['"]([^'"]+)['"](?:\s*,\s*['"]([^'"]*)['"])?`)
	gemGroupRe  = regexp.MustCompile(`^\s*group\s+(.*?)\s+do\b`)
	rubyBlockRe = regexp.MustCompile(`\bdo\s*(\|[^|]*\|)?\s*$`)
	pkgConfigRe = regexp.MustCompile(`<package\s[^>]*\bid="([^"]+)"(?:[^>]*\bversion="([^"]*)")?`)
)

func (ruby) parse(_, rel string, body []byte) (Report, error) {
	var r Report
	var groups []bool // per open do-block: is it a dev group
	for i, line := range strings.Split(string(body), "\n") {
		code := line
		if j := strings.Index(code, " #"); j >= 0 {
			code = code[:j]
		}
		if strings.HasPrefix(strings.TrimSpace(code), "#") {
			continue
		}
		if m := gemGroupRe.FindStringSubmatch(code); m != nil {
			groups = append(groups, strings.Contains(m[1], ":test") || strings.Contains(m[1], ":development"))
			continue
		}
		if rubyBlockRe.MatchString(code) {
			groups = append(groups, false)
			continue
		}
		if strings.TrimSpace(code) == "end" && len(groups) > 0 {
			groups = groups[:len(groups)-1]
			continue
		}
		if m := gemLineRe.FindStringSubmatch(code); m != nil {
			section := "dependencies"
			for _, dev := range groups {
				if dev {
					section = "dev"
				}
			}
			r.Depends = append(r.Depends, Dependency{Name: Normalize("gem", m[1]), Version: m[2], Section: section, Ecosystem: "gem", File: rel, Line: i + 1, Text: strings.TrimSpace(m[0])})
		}
	}
	return r, nil
}

func (php) parse(_, rel string, body []byte) (Report, error) {
	var doc struct {
		Name       string            `json:"name"`
		Type       string            `json:"type"`
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
	}
	if err := json.Unmarshal(stripBOM(body), &doc); err != nil {
		return Report{}, err
	}
	lines := strings.Split(string(body), "\n")
	find := func(name string) (int, string) {
		needle := `"` + name + `"`
		for i, l := range lines {
			if strings.Contains(l, needle) {
				return i + 1, needle
			}
		}
		return 1, ""
	}
	var r Report
	add := func(m map[string]string, section string) {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if n == "php" || strings.HasPrefix(n, "ext-") || strings.HasPrefix(n, "lib-") {
				continue
			}
			ln, text := find(n)
			r.Depends = append(r.Depends, Dependency{Name: Normalize("composer", n), Version: m[n], Section: section, Ecosystem: "composer", File: rel, Line: ln, Text: text})
		}
	}
	add(doc.Require, "dependencies")
	add(doc.RequireDev, "dev")
	if doc.Name != "" {
		// type "project" is an application skeleton (Laravel, Symfony): it runs
		ln, text := find(doc.Name)
		r.Publishes = append(r.Publishes, Package{Name: Normalize("composer", doc.Name), Ecosystem: "composer", File: rel, Line: ln, Text: text, App: doc.Type == "project"})
	}
	return r, nil
}

func (rust) parse(_, rel string, body []byte) (Report, error) {
	var doc struct {
		Package struct {
			Name    string `toml:"name"`
			Publish any    `toml:"publish"`
		} `toml:"package"`
		Dependencies      map[string]any `toml:"dependencies"`
		DevDependencies   map[string]any `toml:"dev-dependencies"`
		BuildDependencies map[string]any `toml:"build-dependencies"`
		Workspace         struct {
			Dependencies map[string]any `toml:"dependencies"`
		} `toml:"workspace"`
	}
	if _, err := toml.Decode(string(body), &doc); err != nil {
		return Report{}, err
	}
	lines := strings.Split(string(body), "\n")
	find := func(name string) (int, string) {
		re := regexp.MustCompile(`^\s*"?` + regexp.QuoteMeta(name) + `"?\s*=|^\s*\[[\w.\-]*dependencies\.` + regexp.QuoteMeta(name) + `\]`)
		for i, l := range lines {
			if re.MatchString(l) {
				return i + 1, strings.TrimSpace(l)
			}
		}
		return 1, ""
	}
	var r Report
	add := func(m map[string]any, section string) {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			ver, internal := "", false
			switch v := m[n].(type) {
			case string:
				ver = v
			case map[string]any:
				if s, ok := v["version"].(string); ok {
					ver = s
				}
				if _, ok := v["path"]; ok && ver == "" {
					internal = true
				}
				if s, ok := v["package"].(string); ok && s != "" {
					n = s
				}
			}
			if internal {
				continue
			}
			ln, text := find(n)
			r.Depends = append(r.Depends, Dependency{Name: Normalize("cargo", n), Version: ver, Section: section, Ecosystem: "cargo", File: rel, Line: ln, Text: text})
		}
	}
	add(doc.Dependencies, "dependencies")
	add(doc.Workspace.Dependencies, "dependencies")
	add(doc.BuildDependencies, "build")
	add(doc.DevDependencies, "dev")
	if doc.Package.Name != "" {
		if b, ok := doc.Package.Publish.(bool); !ok || b {
			ln, text := find("name")
			r.Publishes = append(r.Publishes, Package{Name: Normalize("cargo", doc.Package.Name), Ecosystem: "cargo", File: rel, Line: ln, Text: text})
		}
	}
	return r, nil
}

func (nugetConfig) parse(_, rel string, body []byte) (Report, error) {
	var r Report
	for i, line := range strings.Split(string(body), "\n") {
		if m := pkgConfigRe.FindStringSubmatch(line); m != nil {
			r.Depends = append(r.Depends, Dependency{Name: Normalize("nuget", m[1]), Version: m[2], Section: "dependencies", Ecosystem: "nuget", File: rel, Line: i + 1, Text: m[0]})
		}
	}
	return r, nil
}

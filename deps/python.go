package deps

import (
	"regexp"
	"strings"
)

type python struct{}

func (python) matches(p string) bool {
	b := base(p)
	return b == "pyproject.toml" || b == "setup.cfg" || (strings.HasPrefix(b, "requirements") && strings.HasSuffix(b, ".txt"))
}

func (python) parse(_, rel string, body []byte) (Report, error) {
	switch base(rel) {
	case "pyproject.toml":
		return parsePyproject(rel, body), nil
	case "setup.cfg":
		return parseSetupCfg(rel, body), nil
	}
	return parseRequirements(rel, body), nil
}

var (
	pyNameRe   = regexp.MustCompile(`^name\s*=\s*(?:"([^"]+)"|'([^']+)')`)
	pyQuotedRe = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
	// A poetry entry: `name = "^1"` or `name = { version = "^1", ... }`.
	poetryVersionRe = regexp.MustCompile(`^(?:"([^"]*)"|'([^']*)')`)
	poetryTableVer  = regexp.MustCompile(`version\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

func parsePyproject(rel string, body []byte) Report {
	var r Report
	var section string // the current table header, e.g. "[project]"
	listSection := ""  // set while inside a multi-line list
	for i, raw := range strings.Split(string(body), "\n") {
		n := i + 1
		line := strings.TrimSpace(raw)
		if listSection != "" {
			list, closed := line, strings.Contains(line, "]")
			if closed {
				list = line[:strings.Index(line, "]")]
			}
			r.addPyDeps(rel, n, list, listSection)
			if closed {
				listSection = ""
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		key, value, hasEq := strings.Cut(line, "=")
		key, value = strings.Trim(strings.TrimSpace(key), `"'`), strings.TrimSpace(value)
		if !hasEq || key == "" || strings.HasPrefix(key, "#") {
			continue
		}
		inProject := section == "[project]"
		switch {
		case (inProject || section == "[tool.poetry]") && key == "name":
			if m := pyNameRe.FindStringSubmatch(line); m != nil {
				r.Publishes = append(r.Publishes, Package{Name: Normalize("python", firstOf(m[1], m[2])), Ecosystem: "python", File: rel, Line: n, Text: "name"})
			}
		case section == "[tool.poetry.dependencies]":
			if key == "python" {
				continue
			}
			version := ""
			if m := poetryVersionRe.FindStringSubmatch(value); m != nil {
				version = firstOf(m[1], m[2])
			} else if m := poetryTableVer.FindStringSubmatch(value); m != nil {
				version = firstOf(m[1], m[2])
			}
			r.Depends = append(r.Depends, Dependency{
				Name: Normalize("python", key), Version: version, Section: "dependencies",
				Ecosystem: "python", File: rel, Line: n, Text: line,
			})
		case (inProject && key == "dependencies") || section == "[project.optional-dependencies]":
			listName := "dependencies"
			if section == "[project.optional-dependencies]" {
				listName = key
			}
			rest, ok := strings.CutPrefix(value, "[")
			if !ok {
				continue
			}
			closed := strings.Contains(rest, "]")
			if closed {
				rest = rest[:strings.Index(rest, "]")]
			}
			r.addPyDeps(rel, n, rest, listName)
			if !closed {
				listSection = listName
			}
		}
	}
	return r
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseSetupCfg reads [metadata] name and the [options] install_requires list.
func parseSetupCfg(rel string, body []byte) Report {
	var r Report
	section, inRequires := "", false
	for i, raw := range strings.Split(strings.ReplaceAll(string(body), "\r", ""), "\n") {
		n := i + 1
		trimmed := strings.TrimSpace(raw)
		indented := raw != "" && (raw[0] == ' ' || raw[0] == '\t')
		if inRequires {
			if indented && trimmed != "" {
				if !strings.HasPrefix(trimmed, "#") {
					if d, ok := pyDependency(trimmed, "dependencies", rel, n); ok {
						r.Depends = append(r.Depends, d)
					}
				}
				continue
			}
			if trimmed == "" {
				continue
			}
			inRequires = false
		}
		if strings.HasPrefix(trimmed, "[") {
			section = trimmed
			continue
		}
		key, value, hasEq := strings.Cut(trimmed, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !hasEq || indented {
			continue
		}
		switch {
		case section == "[metadata]" && key == "name" && value != "":
			r.Publishes = append(r.Publishes, Package{Name: Normalize("python", value), Ecosystem: "python", File: rel, Line: n, Text: "name"})
		case section == "[options]" && key == "install_requires":
			inRequires = true
			if value != "" {
				if d, ok := pyDependency(value, "dependencies", rel, n); ok {
					r.Depends = append(r.Depends, d)
				}
			}
		}
	}
	return r
}

func (r *Report) addPyDeps(rel string, line int, list, section string) {
	for _, m := range pyQuotedRe.FindAllStringSubmatch(list, -1) {
		if d, ok := pyDependency(firstOf(m[1], m[2]), section, rel, line); ok {
			r.Depends = append(r.Depends, d)
		}
	}
}

func parseRequirements(rel string, body []byte) Report {
	var r Report
	section := strings.TrimSuffix(base(rel), ".txt")
	for i, raw := range strings.Split(string(body), "\n") {
		line, _, _ := strings.Cut(raw, " #")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") || strings.Contains(line, "://") {
			continue
		}
		if d, ok := pyDependency(line, section, rel, i+1); ok {
			r.Depends = append(r.Depends, d)
		}
	}
	return r
}

// pyDependency splits a requirement string such as "fastapi[all]>=0.100; python_version>'3'".
func pyDependency(spec, section, rel string, line int) (Dependency, bool) {
	spec = strings.TrimSpace(spec)
	end := strings.IndexAny(spec, "<>=!~[ ;")
	if end < 0 {
		end = len(spec)
	}
	name := spec[:end]
	if name == "" {
		return Dependency{}, false
	}
	rest := strings.TrimSpace(spec[end:])
	if strings.HasPrefix(rest, "[") {
		if j := strings.Index(rest, "]"); j >= 0 {
			rest = strings.TrimSpace(rest[j+1:])
		}
	}
	version, _, _ := strings.Cut(rest, ";")
	return Dependency{
		Name: Normalize("python", name), Version: strings.TrimSpace(version), Section: section,
		Ecosystem: "python", File: rel, Line: line, Text: spec,
	}, true
}

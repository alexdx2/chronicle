package deps

import (
	"regexp"
	"strings"
)

type python struct{}

func (python) matches(p string) bool {
	b := base(p)
	return b == "pyproject.toml" || (strings.HasPrefix(b, "requirements") && strings.HasSuffix(b, ".txt"))
}

func (python) parse(_, rel string, body []byte) (Report, error) {
	if base(rel) == "pyproject.toml" {
		return parsePyproject(rel, body), nil
	}
	return parseRequirements(rel, body), nil
}

var (
	pyNameRe   = regexp.MustCompile(`^name\s*=\s*"([^"]+)"`)
	pyQuotedRe = regexp.MustCompile(`"([^"]*)"`)
)

func parsePyproject(rel string, body []byte) Report {
	var r Report
	inProject, inDeps := false, false
	for i, raw := range strings.Split(string(body), "\n") {
		n := i + 1
		line := strings.TrimSpace(raw)
		if inDeps {
			list, closed := line, strings.Contains(line, "]")
			if closed {
				list = line[:strings.Index(line, "]")]
			}
			r.addPyDeps(rel, n, list, "dependencies")
			inDeps = !closed
			continue
		}
		if strings.HasPrefix(line, "[") {
			inProject = line == "[project]"
			continue
		}
		if !inProject {
			continue
		}
		if m := pyNameRe.FindStringSubmatch(line); m != nil {
			r.Publishes = append(r.Publishes, Package{Name: Normalize("python", m[1]), Ecosystem: "python", File: rel, Line: n})
		} else if rest, ok := strings.CutPrefix(line, "dependencies"); ok {
			rest = strings.TrimSpace(rest)
			rest, ok = strings.CutPrefix(rest, "=")
			rest, ok2 := strings.CutPrefix(strings.TrimSpace(rest), "[")
			if !ok || !ok2 {
				continue
			}
			closed := strings.Contains(rest, "]")
			if closed {
				rest = rest[:strings.Index(rest, "]")]
			}
			r.addPyDeps(rel, n, rest, "dependencies")
			inDeps = !closed
		}
	}
	return r
}

func (r *Report) addPyDeps(rel string, line int, list, section string) {
	for _, m := range pyQuotedRe.FindAllStringSubmatch(list, -1) {
		if d, ok := pyDependency(m[1], section, rel, line); ok {
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

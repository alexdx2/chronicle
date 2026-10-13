package deps

import "strings"

type gomod struct{}

func (gomod) matches(p string) bool { return base(p) == "go.mod" }

func (gomod) parse(_, rel string, body []byte) (Report, error) {
	var r Report
	inRequire, inOther := false, false
	for i, raw := range strings.Split(string(body), "\n") {
		n := i + 1
		code, comment, _ := strings.Cut(raw, "//")
		line := strings.TrimSpace(code)
		if line == "" {
			continue
		}
		switch {
		case inRequire || inOther:
			if line == ")" {
				inRequire, inOther = false, false
				continue
			}
			if inRequire {
				r.addGoRequire(rel, n, line, comment)
			}
			continue
		case strings.HasPrefix(line, "module"):
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "module" {
				r.Publishes = append(r.Publishes, Package{Name: strings.Trim(f[1], `"`), Ecosystem: "go", File: rel, Line: n})
			}
		case strings.HasPrefix(line, "require"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "require"))
			if rest == "(" {
				inRequire = true
			} else {
				r.addGoRequire(rel, n, rest, comment)
			}
		case strings.HasSuffix(line, "("):
			inOther = true // replace, exclude, retract, tool blocks
		}
	}
	return r, nil
}

func (r *Report) addGoRequire(rel string, line int, spec, comment string) {
	f := strings.Fields(spec)
	if len(f) < 2 {
		return
	}
	section := "dependencies"
	if strings.Contains(comment, "indirect") {
		section = "indirect"
	}
	r.Depends = append(r.Depends, Dependency{
		Name: Normalize("go", f[0]), Version: f[1], Section: section,
		Ecosystem: "go", File: rel, Line: line, Text: f[0] + " " + f[1],
	})
}

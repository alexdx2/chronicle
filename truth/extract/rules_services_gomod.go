package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// go.mod module line: the last path element names the service (own:<name>);
// a trailing major-version element (v2, v3, ...) is skipped.

func init() {
	Rules = append(Rules,
		funcRule{"manifest.gomod_module", func(p string) bool { return baseName(p) == "go.mod" }, applyGoModModule},
	)
}

var (
	goModModule = regexp.MustCompile(`^module\s+("[^"]+"|\S+)$`)
	goMajorElem = regexp.MustCompile(`^v[0-9]+$`)
)

func applyGoModModule(f *FileCtx) []truth.Fact {
	for _, l := range f.Lines {
		code, _ := goModLine(l.Raw)
		m := goModModule.FindStringSubmatch(code)
		if m == nil {
			continue
		}
		parts := strings.Split(strings.Trim(strings.Trim(m[1], `"`), "/"), "/")
		name := parts[len(parts)-1]
		if goMajorElem.MatchString(name) && len(parts) > 1 {
			name = parts[len(parts)-2]
		}
		if fc, ok := serviceFact("manifest.gomod_module", f, "own", name, "", "", l); ok {
			return []truth.Fact{fc}
		}
		return nil
	}
	return nil
}

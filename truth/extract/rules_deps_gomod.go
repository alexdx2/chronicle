package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// go.mod: direct requirements only. A requirement marked "// indirect" is
// pulled in by another module and is not a declared dependency of this one.
// go.mod is not lexed (no comment family), so comments are cut here.

func init() {
	Rules = append(Rules,
		funcRule{"manifest.gomod", func(p string) bool { return baseName(p) == "go.mod" }, applyManifestGoMod},
	)
}

var (
	goModReqOne   = regexp.MustCompile(`^require\s+("[^"]+"|\S+)\s+(\S+)$`)
	goModReqBlock = regexp.MustCompile(`^require\s*\($`)
	goModReqLine  = regexp.MustCompile(`^("[^"]+"|\S+)\s+(\S+)$`)
)

// goModLine splits a go.mod line into its code and whether the comment says
// "indirect".
func goModLine(raw string) (code string, indirect bool) {
	code = raw
	if i := strings.Index(raw, "//"); i >= 0 {
		code = raw[:i]
		indirect = strings.Contains(strings.ToLower(raw[i:]), "indirect")
	}
	return strings.TrimSpace(code), indirect
}

func applyManifestGoMod(f *FileCtx) []truth.Fact {
	s := newDepSet("manifest.gomod", f)
	inBlock := false
	for _, l := range f.Lines {
		code, indirect := goModLine(l.Raw)
		var m []string
		switch {
		case code == "":
			continue
		case inBlock:
			if code == ")" {
				inBlock = false
				continue
			}
			m = goModReqLine.FindStringSubmatch(code)
		case goModReqBlock.MatchString(code):
			inBlock = true
			continue
		default:
			m = goModReqOne.FindStringSubmatch(code)
		}
		if m == nil || indirect {
			continue
		}
		if fc := s.add("go", strings.Trim(m[1], `"`), m[2], "require"); fc != nil {
			fc.Primary = evidence(f, l) // the require line itself, not the first mention of the path
		}
	}
	return s.facts
}

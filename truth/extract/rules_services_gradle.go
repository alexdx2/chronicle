package extract

import (
	"regexp"

	"github.com/alexdx2/chronicle-core/truth"
)

// settings.gradle(.kts): rootProject.name = "x" names the service.

func init() {
	Rules = append(Rules,
		funcRule{"gradle.root_project", func(p string) bool {
			b := baseName(p)
			return b == "settings.gradle" || b == "settings.gradle.kts"
		}, applyGradleRootProject},
	)
}

var gradleRootName = regexp.MustCompile(`\brootProject\.name\s*=\s*["']([^"'$]+)["']`)

func applyGradleRootProject(f *FileCtx) []truth.Fact {
	for _, l := range f.Lines {
		if m := gradleRootName.FindStringSubmatch(l.Code); m != nil {
			if fc, ok := serviceFact("gradle.root_project", f, "own", m[1], "", "", l); ok {
				return []truth.Fact{fc}
			}
		}
	}
	return nil
}

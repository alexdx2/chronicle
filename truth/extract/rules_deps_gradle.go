package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// Gradle build scripts: dependency configurations with a literal
// "group:artifact[:version]" coordinate, or group/name named arguments.
// Version catalogs (libs.x), project(...) and files(...) yield nothing.

func init() {
	Rules = append(Rules,
		funcRule{"manifest.gradle", func(p string) bool {
			b := baseName(p)
			return b == "build.gradle" || b == "build.gradle.kts"
		}, applyManifestGradle},
	)
}

var (
	gradleConfigs = `implementation|api|compileOnly|runtimeOnly|compileOnlyApi|testImplementation|testApi|testCompileOnly|testRuntimeOnly|kapt|ksp|annotationProcessor|developmentOnly|androidTestImplementation|debugImplementation|releaseImplementation|integrationTestImplementation`
	gradleCfgRe   = regexp.MustCompile(`(?:^|[^\w.])(` + gradleConfigs + `)\b`)
	gradleCoord   = regexp.MustCompile(`^["']([^"'$:\s]+):([^"'$:\s]+)(?::([^"':\s@]*))?[^"']*["']$`)
	gradleNamed   = regexp.MustCompile(`\b(group|name|version)\s*[:=]\s*["']([^"'$]*)["']`)
	gradlePlat    = regexp.MustCompile(`^(?:platform|enforcedPlatform)\s*\((.*)\)$`)
)

func applyManifestGradle(f *FileCtx) []truth.Fact {
	s := newDepSet("manifest.gradle", f)
	for i, l := range f.Lines {
		m := gradleCfgRe.FindStringSubmatchIndex(l.Code)
		if m == nil || ktInString(l.Code, m[2]) {
			continue
		}
		cfg := l.Code[m[2]:m[3]]
		rest := l.Code[m[3]:]
		// Collect the argument text: "(...)" balanced over lines, or the rest of the line.
		var arg string
		if t := strings.TrimLeft(rest, " \t"); strings.HasPrefix(t, "(") {
			text := callText(f.Lines, i, 8)
			from := strings.Index(text, cfg)
			if from < 0 {
				continue
			}
			text = text[from+len(cfg):]
			open := strings.Index(text, "(")
			end := ktMatchParen(text, open)
			if open < 0 || end < 0 {
				continue
			}
			arg = strings.TrimSpace(text[open+1 : end])
		} else {
			arg = strings.TrimSpace(rest)
		}
		section := "dependencies"
		if strings.HasPrefix(strings.ToLower(cfg), "test") || strings.Contains(strings.ToLower(cfg), "androidtest") {
			section = "test"
		}
		parts := ktSplitArgs(arg)
		if gradleNamed.MatchString(arg) && strings.Contains(arg, "group") && strings.Contains(arg, "name") {
			parts = []string{arg} // group/name/version are one dependency
		}
		for _, a := range parts {
			if p := gradlePlat.FindStringSubmatch(a); p != nil {
				a = strings.TrimSpace(p[1])
			}
			group, name, ver := "", "", ""
			if c := gradleCoord.FindStringSubmatch(a); c != nil {
				group, name, ver = c[1], c[2], c[3]
			} else if strings.Contains(a, "group") && strings.Contains(a, "name") {
				for _, n := range gradleNamed.FindAllStringSubmatch(a, -1) {
					switch n[1] {
					case "group":
						group = n[2]
					case "name":
						name = n[2]
					case "version":
						ver = n[2]
					}
				}
			}
			if group == "" || name == "" {
				continue
			}
			if ft := s.add("maven", group+":"+name, ver, section); ft != nil {
				ft.Primary = evidence(f, l)
			}
		}
	}
	return s.facts
}

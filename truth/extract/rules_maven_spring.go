package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

func init() {
	Rules = append(Rules,
		funcRule{"manifest.maven", func(p string) bool { return baseName(p) == "pom.xml" }, applyManifestMaven},
		funcRule{"maven.service", func(p string) bool { return baseName(p) == "pom.xml" }, applyMavenService},
		funcRule{"spring.config_url", isSpringConfigPath, applySpringConfig},
	)
}

// ---- maven ----

var (
	pomComment = regexp.MustCompile(`<!--.*?-->`)
	xmlTag     = regexp.MustCompile(`<(/?)(\w+)[^>]*>([^<]*)`)
)

type mavenScan struct {
	deps       []mavenDep
	svcName    string
	svcLine    lex.Line
	haveSvcArt bool
}

type mavenDep struct {
	group, artifact string
	line            lex.Line
}

// scanPom walks the pom line by line, tracking the open tags. Dependencies
// count under <project><dependencies> only (not dependencyManagement, build
// plugins or the parent); the project artifactId is the one directly under
// <project>.
func scanPom(f *FileCtx) mavenScan {
	var ms mavenScan
	var stack []string
	inComment := false
	var group, artifact string
	var artLine lex.Line
	for _, l := range f.Lines {
		s := pomComment.ReplaceAllString(l.Raw, "")
		if inComment {
			if i := strings.Index(s, "-->"); i >= 0 {
				s, inComment = s[i+3:], false
			} else {
				continue
			}
		}
		if i := strings.Index(s, "<!--"); i >= 0 {
			s, inComment = s[:i], true
		}
		for _, m := range xmlTag.FindAllStringSubmatch(s, -1) {
			closing, tag, text := m[1] == "/", m[2], strings.TrimSpace(m[3])
			if closing {
				if tag == "dependency" && pomPath(stack) == "dependencies/dependency" && artifact != "" {
					ms.deps = append(ms.deps, mavenDep{group, artifact, artLine})
				}
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				if tag == "dependency" {
					group, artifact = "", ""
				}
				continue
			}
			if strings.HasSuffix(strings.TrimSpace(m[0]), "/>") {
				continue
			}
			stack = append(stack, tag)
			switch pomPath(stack) {
			case "artifactId":
				if !ms.haveSvcArt && text != "" {
					ms.svcName, ms.svcLine, ms.haveSvcArt = text, l, true
				}
			case "dependencies/dependency/groupId":
				group = text
			case "dependencies/dependency/artifactId":
				artifact, artLine = text, l
			}
		}
	}
	return ms
}

func applyManifestMaven(f *FileCtx) []truth.Fact {
	var out []truth.Fact
	seen := map[string]bool{}
	for _, d := range scanPom(f).deps {
		if strings.ContainsAny(d.group+d.artifact, "$@%{}") || d.group == "" {
			continue
		}
		key := truth.DepKey("maven", d.group+":"+d.artifact)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, truth.Fact{
			Class:   truth.ClassDeps,
			Key:     key,
			Attrs:   map[string]string{"name": d.group + ":" + d.artifact, "ecosystem": "maven", "section": "dependencies"},
			Primary: evidence(f, d.line),
			Sources: []string{"manifest.maven"},
		})
	}
	return out
}

// applyMavenService names a deployable service after the project artifactId
// when the pom builds an application (spring-boot plugin or parent) or the
// packaging is not pom/jar-library.
func applyMavenService(f *FileCtx) []truth.Fact {
	ms := scanPom(f)
	if !ms.haveSvcArt || !pomIsApplication(string(f.Raw)) {
		return nil
	}
	if strings.Contains(string(f.Raw), "<packaging>pom</packaging>") {
		return nil
	}
	if fc, ok := serviceFact("maven.service", f, "own", ms.svcName, "java", "", ms.svcLine); ok {
		return []truth.Fact{fc}
	}
	return nil
}

// ---- spring config ----

func isSpringConfigPath(p string) bool {
	b := strings.ToLower(baseName(p))
	return (strings.HasPrefix(b, "application") || strings.HasPrefix(b, "bootstrap")) &&
		(strings.HasSuffix(b, ".yml") || strings.HasSuffix(b, ".yaml") || strings.HasSuffix(b, ".properties"))
}

var propLine = regexp.MustCompile(`^\s*([A-Za-z0-9_.\-\[\]]+)\s*[=:]\s*(.*?)\s*$`)

// applySpringConfig reads absolute service URLs from application.yml /
// application.properties, with the same key policy as appsettings.json.
func applySpringConfig(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	if strings.HasSuffix(strings.ToLower(f.Path), ".properties") {
		for _, l := range f.Lines {
			m := propLine.FindStringSubmatch(l.Code)
			if m == nil || !anyServiceURL(m[1], m[2]) {
				continue
			}
			if fc, ok := httpFact("spring.config_url", f, l, m[2], m[1]); ok {
				out = append(out, fc)
			}
		}
		return out
	}
	// YAML: scan "key: value" lines with the dotted path of the indentation.
	type frame struct {
		indent int
		key    string
	}
	var stack []frame
	for _, l := range f.Lines {
		code := strings.TrimRight(l.Code, " \t\r")
		trim := strings.TrimLeft(code, " ")
		if trim == "" || strings.HasPrefix(trim, "-") && !strings.Contains(trim, ": ") {
			continue
		}
		indent := len(code) - len(trim)
		k, v, ok := strings.Cut(trim, ":")
		if !ok {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		v = strings.TrimSpace(v)
		if v == "" {
			stack = append(stack, frame{indent, strings.TrimPrefix(k, "- ")})
			continue
		}
		v = unquote(v)
		if strings.HasPrefix(v, "${") {
			if i := strings.Index(v, ":"); i > 0 && strings.HasSuffix(v, "}") {
				v = v[i+1 : len(v)-1]
			}
		}
		full := k
		if len(stack) > 0 {
			var parts []string
			for _, fr := range stack {
				parts = append(parts, fr.key)
			}
			full = strings.Join(parts, ".") + "." + k
		}
		if !anyServiceURL(full, v) {
			continue
		}
		if fc, ok := httpFact("spring.config_url", f, l, v, full); ok {
			out = append(out, fc)
		}
	}
	return out
}

// pomPath is the open-tag path below <project>. A multi-line <project ...>
// start tag is not matched by the line regex, so the root may be missing.
func pomPath(stack []string) string {
	if len(stack) > 0 && stack[0] == "project" {
		stack = stack[1:]
	}
	return strings.Join(stack, "/")
}

// pomIsApplication: a pom builds something that runs when it uses an
// application framework (Spring Boot, Kafka Streams, Quarkus, Micronaut, Vert.x,
// Ktor, Javalin) or names a main class.
func pomIsApplication(raw string) bool {
	for _, marker := range []string{"spring-boot", "kafka-streams", "io.quarkus", "micronaut", "io.vertx", "ktor-server", "io.javalin", "<mainClass>", "<main.class>"} {
		if strings.Contains(raw, marker) {
			return true
		}
	}
	return false
}

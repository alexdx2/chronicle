package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"gopkg.in/yaml.v3"
)

func init() {
	Rules = append(Rules,
		funcRule{"python.env_default", func(p string) bool { return hasExt(p, ".py") }, applyPyEnvDefault},
	)
}

// pyEnvDefault matches a configuration read whose default is a string literal:
// os.getenv("X", "http://h:1"), os.environ.get("X", "..."), Airflow
// Variable.get("x", default_var="..."), and the settings-style helpers
// env("X", default="...") / config("X", default="..."). Capture 1 is the
// variable name, capture 2 the default. A receiver-less env/config call must
// not be a method of something else (app.config(...)).
var pyEnvDefault = regexp.MustCompile(`(?:\bos\.getenv|(?:^|[^\w.])getenv|\bos\.environ\.get|\benviron\.get|\bVariable\.get|(?:^|[^\w.])(?:env|config)(?:\.str)?)\(\s*["']([A-Za-z_][\w.\-]*)["']\s*,\s*(?:default(?:_var)?\s*=\s*)?["']([^"']*)["']`)

// applyPyEnvDefault: the default of an environment/Variable read is the host
// the service talks to when nothing overrides it. Only absolute http(s)/grpc
// URLs with a literal, non-local host count; the call may span lines.
func applyPyEnvDefault(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		text := codeWindow(f.Lines, i, 3)
		for _, ix := range pyEnvDefault.FindAllStringSubmatchIndex(text, -1) {
			if ix[0] >= len(l.Code) { // the call must start on this line
				continue
			}
			key, val := text[ix[2]:ix[3]], text[ix[4]:ix[5]]
			if !anyServiceURL(key, val) {
				continue
			}
			if fc, ok := httpFact("python.env_default", f, l, val, key); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

func init() {
	Rules = append(Rules,
		funcRule{"helm.values_url", isHelmValuesPath, applyHelmValues},
	)
}

var helmValuesName = regexp.MustCompile(`^values(?:[.-][\w.-]*)?\.ya?ml$`)

func isHelmValuesPath(p string) bool { return helmValuesName.MatchString(strings.ToLower(baseName(p))) }

// helmNotService marks keys that point at chart or image repositories.
var helmNotService = regexp.MustCompile(`(?i)repo|registry|chart|helm|mirror`)

// applyHelmValues: any scalar of a Helm values file that is an absolute
// http(s) URL with a non-local host is an upstream the release talks to.
// Keys naming documentation, schemas or chart repositories are skipped.
func applyHelmValues(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	var walk func(n *yaml.Node, key string)
	walk = func(n *yaml.Node, key string) {
		n = yamlResolve(n)
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				walk(n.Content[i+1], n.Content[i].Value)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c, key)
			}
		case yaml.ScalarNode:
			if helmNotService.MatchString(key) || !anyServiceURL(key, n.Value) {
				return
			}
			if fc, ok := yamlFact("helm.values_url", f, n, key); ok {
				out = append(out, fc)
			}
		}
	}
	yamlDocs(f.Raw, func(root *yaml.Node) { walk(root, "") })
	return out
}

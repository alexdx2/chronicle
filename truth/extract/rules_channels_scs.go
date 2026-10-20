package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"gopkg.in/yaml.v3"
)

// Spring Cloud Stream: spring.cloud.stream.bindings.<name>.destination in
// application*.yml / .yaml / .properties. Functional bindings are named
// <function>-in-<n> (consume) and <function>-out-<n> (produce); a binding
// whose name carries neither gives no fact. The destination may list several
// comma separated names. Placeholders give no fact.

func init() {
	Rules = append(Rules,
		funcRule{"spring.cloud.stream", isSpringAppConfig, applySpringCloudStream},
	)
}

func isSpringAppConfig(p string) bool {
	b := strings.ToLower(baseName(p))
	return strings.HasPrefix(b, "application") && hasExt(b, ".yml", ".yaml", ".properties") ||
		strings.HasPrefix(b, "bootstrap") && hasExt(b, ".yml", ".yaml", ".properties")
}

var scsDest = regexp.MustCompile(`^spring\.cloud\.stream\.bindings\.(.+)\.destination$`)

func scsSide(binding string) string {
	switch {
	case strings.Contains(binding, "-in-"):
		return "consume"
	case strings.Contains(binding, "-out-"):
		return "produce"
	}
	return ""
}

func applySpringCloudStream(f *FileCtx) []truth.Fact {
	if f.IsTest || !strings.Contains(string(f.Raw), "cloud") {
		return nil
	}
	var out []truth.Fact
	emit := func(binding, dest string, line int) {
		side := scsSide(binding)
		l, ok := yamlLine(f, line)
		if side == "" || !ok {
			return
		}
		for _, n := range strings.Split(dest, ",") {
			if fc, ok := channelFact("spring.cloud.stream", f, l, "topic", n, side); ok {
				out = append(out, fc)
			}
		}
	}
	if hasExt(f.Path, ".properties") {
		for _, l := range f.Lines {
			t := strings.TrimSpace(l.Raw)
			if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") {
				continue
			}
			k, v, ok := strings.Cut(t, "=")
			if !ok {
				continue
			}
			if m := scsDest.FindStringSubmatch(strings.TrimSpace(k)); m != nil {
				emit(m[1], strings.TrimSpace(v), l.Num)
			}
		}
		return out
	}
	yamlDocs(f.Raw, func(root *yaml.Node) {
		var walk func(n *yaml.Node, path string)
		walk = func(n *yaml.Node, path string) {
			if n == nil || n.Kind != yaml.MappingNode {
				return
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				p := k.Value
				if path != "" {
					p = path + "." + k.Value
				}
				if v.Kind == yaml.ScalarNode {
					if m := scsDest.FindStringSubmatch(p); m != nil {
						emit(m[1], v.Value, v.Line)
					}
					continue
				}
				walk(v, p)
			}
		}
		walk(root, "")
	})
	return out
}

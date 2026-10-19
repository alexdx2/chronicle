package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"gopkg.in/yaml.v3"
)

// Ktor application configuration: HOCON application*.conf and
// application*.yaml. Absolute http(s) URLs under any key except
// documentation/schema keys; local hosts yield no fact later (CanonHost).

func init() {
	Rules = append(Rules,
		funcRule{"ktor.config_hocon", isKtorConf, applyKtorHocon},
		funcRule{"ktor.config_yaml", isKtorYaml, applyKtorYaml},
	)
}

func isKtorConf(p string) bool {
	b := strings.ToLower(baseName(p))
	return b == "application.conf" || (strings.HasPrefix(b, "application-") && strings.HasSuffix(b, ".conf"))
}

func isKtorYaml(p string) bool {
	b := strings.ToLower(baseName(p))
	if !(strings.HasSuffix(b, ".yaml") || strings.HasSuffix(b, ".yml")) {
		return false
	}
	return strings.HasPrefix(b, "application.") || strings.HasPrefix(b, "application-")
}

var hoconURL = regexp.MustCompile(`(?:([A-Za-z0-9_.\-"]+)\s*[=:]\s*\[?\s*)?"(https?://[^"]*)"`)

// hoconCode cuts a trailing // comment (outside quotes) from a code line.
func hoconCode(s string) string {
	in := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && in:
			i++
		case s[i] == '"':
			in = !in
		case !in && s[i] == '/' && i+1 < len(s) && s[i+1] == '/':
			return s[:i]
		}
	}
	return s
}

func applyKtorHocon(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for _, l := range f.Lines {
		code := hoconCode(l.Code)
		for _, m := range hoconURL.FindAllStringSubmatch(code, -1) {
			key := strings.Trim(m[1], `"`)
			if i := strings.LastIndex(key, "."); i >= 0 {
				key = key[i+1:]
			}
			if !anyServiceURL(key, m[2]) {
				continue
			}
			if fc, ok := httpFact("ktor.config_hocon", f, l, m[2], key); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

func applyKtorYaml(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	yamlDocs(f.Raw, func(root *yaml.Node) {
		var walk func(n *yaml.Node, key string)
		walk = func(n *yaml.Node, key string) {
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
				if anyServiceURL(key, n.Value) {
					if fc, ok := yamlFact("ktor.config_yaml", f, n, key); ok {
						out = append(out, fc)
					}
				}
			}
		}
		walk(root, "")
	})
	return out
}

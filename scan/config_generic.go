package scan

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/alexdx2/chronicle-core/graph"
)

// The config pass reads the places where a service address is configured, in
// whatever technology the repository uses: Spring/Ktor/Helm/generic YAML,
// TOML, .properties, HOCON, and the default written next to an environment
// variable read in code (os.getenv("X", "http://host"), ENV.fetch, env(),
// Variable.get, unwrap_or, ${X:http://host}). Every value that is an absolute
// http(s) URL becomes an entry keyed by its config key or variable name; the
// resolver and the prompt treat all entries alike.

// isGenericConfigFile: application*/bootstrap* config (yml, yaml, properties,
// conf), Helm values*.yaml, config/settings files, any .properties file.
func isGenericConfigFile(p string) bool {
	lower := strings.ToLower(path.Base(p))
	ext := path.Ext(lower)
	stem := strings.TrimSuffix(lower, ext)
	switch ext {
	case ".properties":
		return true
	case ".conf":
		return strings.HasPrefix(stem, "application") || strings.HasPrefix(stem, "reference")
	case ".yml", ".yaml":
		return strings.HasPrefix(stem, "application") || strings.HasPrefix(stem, "bootstrap") ||
			strings.HasPrefix(stem, "values") || genericConfigStem(stem) || inConfigDir(p)
	case ".toml", ".json":
		return genericConfigStem(stem) && !strings.HasPrefix(stem, "package") && !strings.HasPrefix(stem, "tsconfig") ||
			inConfigDir(p) && !strings.HasPrefix(stem, "package") && !strings.HasPrefix(stem, "tsconfig")
	}
	return false
}

func genericConfigStem(stem string) bool {
	for _, p := range []string{"config", "settings", "services", "endpoints", "local.settings"} {
		if stem == p || strings.HasPrefix(stem, p+".") || strings.HasPrefix(stem, p+"-") || strings.HasPrefix(stem, p+"_") {
			return true
		}
	}
	return false
}

func inConfigDir(p string) bool {
	d := strings.ToLower(path.Dir(p))
	for _, c := range []string{"config", "configs", "conf", "settings", "etc"} {
		if d == c || strings.HasSuffix(d, "/"+c) {
			return true
		}
	}
	return false
}

// isEnvDefaultSource is a source file in a language the env-default reader knows.
func isEnvDefaultSource(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".py", ".go", ".rb", ".php", ".rs", ".java", ".kt", ".kts", ".scala", ".cs", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
	default:
		return false
	}
	lower := strings.ToLower(p)
	return !strings.HasSuffix(lower, ".d.ts") && !strings.Contains(lower, "node_modules/") &&
		!strings.Contains(lower, "_test.") && !strings.Contains(lower, ".spec.") && !strings.Contains(lower, ".test.") &&
		!strings.Contains(lower, "/test/") && !strings.Contains(lower, "/tests/")
}

var (
	// ${KEY:default} / ${KEY:-default} with a URL default, in config and in code
	placeholderURLRe = regexp.MustCompile(`\$\{\s*([A-Za-z0-9_.\-]+)\s*:-?\s*(https?://[^}\s"']+)\s*\}`)
	// an environment read: the name is a quoted UPPER_SNAKE literal or process.env.NAME
	envReadRe = regexp.MustCompile(`(?i:getenv|environ|\bENV\b|\benv\s*\(|\bconfig\s*\(|Variable\.get|GetEnvironmentVariable|env::var)[^"'` + "`" + `]{0,40}["'` + "`" + `]([A-Z][A-Z0-9_]{2,})["'` + "`" + `]|process\.env\.([A-Z][A-Z0-9_]{2,})|process\.env\[\s*["']([A-Z][A-Z0-9_]{2,})["']\s*\]`)
	urlLitRe  = regexp.MustCompile("[\"'`](https?://[^\"'`\\s)]+)[\"'`]")
	envHintRe = regexp.MustCompile(`(?i)getenv|environ|\bENV\b|\benv\s*\(|\bconfig\s*\(|Variable\.get|process\.env|env::var`)
)

// parseGenericConfig reads one config file by extension.
func parseGenericConfig(t *graph.ConfigHosts, file string, body []byte) {
	switch strings.ToLower(path.Ext(file)) {
	case ".yml", ".yaml":
		dec := yaml.NewDecoder(bytes.NewReader(body))
		for i := 0; i < 8; i++ { // a multi-document file; bound it
			var v any
			if err := dec.Decode(&v); err != nil {
				break
			}
			flattenConfig(t, file, "", v)
		}
	case ".json":
		parseAppsettings(t, file, body)
	case ".toml":
		var v map[string]any
		if _, err := toml.Decode(string(body), &v); err == nil {
			flattenConfig(t, file, "", v)
		}
	case ".properties":
		parseProperties(t, file, body)
	case ".conf":
		parseHocon(t, file, body)
	}
	for _, m := range placeholderURLRe.FindAllSubmatch(body, -1) {
		t.Add(string(m[1]), string(m[2]), file)
	}
}

// flattenConfig walks decoded YAML/TOML and adds "a.b.c" -> URL entries; a
// "${VAR:default}" value is added under the variable name too.
func flattenConfig(t *graph.ConfigHosts, file, prefix string, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenConfig(t, file, key, x[k])
		}
	case map[any]any:
		m := map[string]any{}
		for k, val := range x {
			m[fmt.Sprint(k)] = val
		}
		flattenConfig(t, file, prefix, m)
	case []any:
		for i, e := range x {
			flattenConfig(t, file, fmt.Sprintf("%s.%d", prefix, i), e)
		}
	case string:
		t.Add(prefix, unwrapPlaceholder(x), file)
	}
}

// unwrapPlaceholder turns "${VAR:default}" / "${VAR:-default}" into the default.
func unwrapPlaceholder(s string) string {
	s = strings.TrimSpace(s)
	if m := placeholderURLRe.FindStringSubmatch(s); m != nil && strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		return m[2]
	}
	return s
}

// parseProperties reads key=value and key: value lines of a .properties file.
func parseProperties(t *graph.ConfigHosts, file string, body []byte) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		i := strings.IndexAny(line, "=:")
		if i <= 0 {
			continue
		}
		k, v := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		t.Add(k, unwrapPlaceholder(strings.Trim(v, `"'`)), file)
	}
}

// parseHocon reads a HOCON/.conf file line by line, tracking the nesting of
// `name {` blocks, so a Ktor/Akka/Play style config gives dotted keys.
func parseHocon(t *graph.ConfigHosts, file string, body []byte) {
	var stack []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if i := strings.Index(line, "//"); i == 0 {
			continue
		}
		if line == "" || line[0] == '#' {
			continue
		}
		if line == "}" || strings.HasPrefix(line, "}") {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if strings.HasSuffix(line, "{") {
			name := strings.Trim(strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "{"), "=")), `"`)
			stack = append(stack, name)
			continue
		}
		i := strings.IndexAny(line, "=:")
		if i <= 0 {
			continue
		}
		k, v := strings.Trim(strings.TrimSpace(line[:i]), `"`), strings.TrimSpace(line[i+1:])
		v = strings.TrimSuffix(v, ",")
		v = strings.Trim(v, `"`)
		key := k
		if len(stack) > 0 {
			key = strings.Join(stack, ".") + "." + k
		}
		t.Add(key, unwrapPlaceholder(v), file)
	}
}

// parseEnvDefaults finds an environment read followed by an absolute URL
// literal (its default) within two lines, in any language, and adds the
// variable name as the key.
func parseEnvDefaults(t *graph.ConfigHosts, file string, body []byte) {
	for _, m := range placeholderURLRe.FindAllSubmatch(body, -1) {
		t.Add(string(m[1]), string(m[2]), file) // @Value("${catalog.url:http://host}")
	}
	if !envHintRe.Match(body) {
		return
	}
	lines := strings.Split(string(body), "\n")
	for i, l := range lines {
		if !envHintRe.MatchString(l) {
			continue
		}
		win := l
		if i+1 < len(lines) {
			win += " " + lines[i+1]
		}
		m := envReadRe.FindStringSubmatchIndex(win)
		if m == nil {
			continue
		}
		name := ""
		for g := 1; g <= 3; g++ {
			if m[2*g] >= 0 {
				name = win[m[2*g]:m[2*g+1]]
				break
			}
		}
		rest := win[m[1]:]
		if u := urlLitRe.FindStringSubmatch(rest); u != nil && name != "" {
			t.Add(name, u[1], file)
		}
	}
}

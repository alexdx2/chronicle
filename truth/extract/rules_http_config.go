package extract

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
	"gopkg.in/yaml.v3"
)

func init() {
	Rules = append(Rules,
		funcRule{"appsettings.url", func(p string) bool {
			b := strings.ToLower(baseName(p))
			return strings.HasPrefix(b, "appsettings") && strings.HasSuffix(b, ".json")
		}, applyAppSettings},
		funcRule{"webconfig.url", isDotnetConfigPath, applyWebConfig},
		funcRule{"ts.config_url", isTSConfigPath, applyTSConfig},
		funcRule{"dotenv.url", func(p string) bool {
			b := strings.ToLower(baseName(p))
			return b == ".env" || strings.HasPrefix(b, ".env.")
		}, applyDotenv},
		funcRule{"compose.env", isComposePath, applyCompose},
		funcRule{"k8s.env", func(p string) bool { return hasExt(p, ".yaml", ".yml") }, applyK8s},
	)
}

var (
	cfgURLKey   = regexp.MustCompile(`(?:^|[_.\-])(?i:url|uri|baseaddress|endpoint|host)$|[a-z0-9](?:Url|URL|Uri|URI|BaseAddress|Endpoint|ENDPOINT|Host|HOST)$`)
	dotenvLine  = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_.]*)\s*=\s*(.*?)\s*$`)
	k8sKindLine = regexp.MustCompile(`(?m)^\s*kind:\s*(?:Deployment|StatefulSet|DaemonSet|CronJob|Job|Pod|ConfigMap)\b`)
)

// lineOf finds the first code line that contains every needle (key first).
func lineOf(lines []lex.Line, needles ...string) (lex.Line, bool) {
	for len(needles) > 0 {
		for _, l := range lines {
			ok := true
			for _, n := range needles {
				if !strings.Contains(l.Code, n) {
					ok = false
					break
				}
			}
			if ok {
				return l, true
			}
		}
		needles = needles[:len(needles)-1] // fall back to the key alone
	}
	return lex.Line{}, false
}

func applyAppSettings(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var root any
	lines := f.Lines
	if err := json.Unmarshal(f.Raw, &root); err != nil {
		// .NET accepts comments in appsettings; blank them and retry.
		var sb strings.Builder
		lines = lex.Scan(f.Raw, lex.CLike).Lines
		for _, l := range lines {
			sb.WriteString(l.Code)
			sb.WriteByte('\n')
		}
		if err := json.Unmarshal([]byte(sb.String()), &root); err != nil {
			return nil
		}
	}
	var out []truth.Fact
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if s, ok := x[k].(string); ok {
					if (cfgURLKey.MatchString(k) && !(httpsURL.MatchString(s) && notServiceKey.MatchString(k))) || anyServiceURL(k, s) {
						if l, found := lineOf(lines, `"`+k+`"`, s); found {
							if fc, ok := httpFact("appsettings.url", f, l, s, k); ok {
								out = append(out, fc)
							}
						}
					}
					continue
				}
				walk(x[k])
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(root)
	return out
}

// notServiceKey marks documentation and schema pointers, never services.
var notServiceKey = regexp.MustCompile(`(?i)doc|help|schema|license|homepage|swagger`)

// schemaHosts are well-known schema/spec hosts that appear as values.
var schemaHosts = map[string]bool{"json-schema.org": true, "schemastore.org": true, "www.schemastore.org": true, "json.schemastore.org": true, "www.w3.org": true, "aka.ms": true}

// anyServiceURL accepts an absolute http(s) URL under any key except
// documentation/schema keys and well-known schema hosts. Local hosts yield no
// fact later (CanonHost), which also covers the app's own Kestrel binding.
func anyServiceURL(key, value string) bool {
	v := strings.TrimSpace(value)
	if !httpsURL.MatchString(v) || notServiceKey.MatchString(key) {
		return false
	}
	return !schemaHosts[truth.CanonHost(v)]
}

var httpsURL = regexp.MustCompile(`(?i)^https?://`)

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func applyDotenv(f *FileCtx) []truth.Fact {
	if f.IsTest || strings.HasSuffix(strings.ToLower(baseName(f.Path)), ".test") {
		return nil
	}
	var out []truth.Fact
	for _, l := range f.Lines {
		m := dotenvLine.FindStringSubmatch(l.Code)
		if m == nil {
			continue
		}
		if fc, ok := httpFact("dotenv.url", f, l, unquote(m[2]), m[1]); ok {
			out = append(out, fc)
		}
	}
	return out
}

// yamlMap returns the value node of key in a mapping node, or nil.
func yamlMap(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func yamlDoc(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// yamlFact builds a fact from a scalar node (its own line is the evidence).
func yamlFact(rule string, f *FileCtx, val *yaml.Node, key string) (truth.Fact, bool) {
	if val == nil || val.Kind != yaml.ScalarNode {
		return truth.Fact{}, false
	}
	l, ok := yamlLine(f, val.Line)
	if !ok {
		return truth.Fact{}, false
	}
	return httpFact(rule, f, l, val.Value, key)
}

func applyCompose(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(f.Raw, &doc); err != nil {
		return nil
	}
	services := yamlMap(yamlDoc(&doc), "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil
	}
	var out []truth.Fact
	for i := 1; i < len(services.Content); i += 2 {
		env := yamlMap(services.Content[i], "environment")
		if env == nil {
			continue
		}
		switch env.Kind {
		case yaml.SequenceNode:
			for _, it := range env.Content {
				if it.Kind != yaml.ScalarNode {
					continue
				}
				k, v, found := strings.Cut(it.Value, "=")
				if !found {
					continue
				}
				if it.Line < 1 || it.Line > len(f.Lines) {
					continue
				}
				if fc, ok := httpFact("compose.env", f, f.Lines[it.Line-1], unquote(v), strings.TrimSpace(k)); ok {
					out = append(out, fc)
				}
			}
		case yaml.MappingNode:
			for j := 0; j+1 < len(env.Content); j += 2 {
				if fc, ok := yamlFact("compose.env", f, env.Content[j+1], env.Content[j].Value); ok {
					out = append(out, fc)
				}
			}
		}
	}
	return out
}

func applyK8s(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var code strings.Builder
	for _, l := range f.Lines {
		code.WriteString(l.Code)
		code.WriteByte('\n')
	}
	if !k8sKindLine.MatchString(code.String()) {
		return nil
	}
	var out []truth.Fact
	yamlDocs(f.Raw, func(root *yaml.Node) {
		if k := yamlMap(root, "kind"); k != nil && k8sKinds[k.Value] {
			out = append(out, k8sWalk(f, root, k8sKindIsConfigMap(root))...)
		}
	})
	return out
}

var k8sKinds = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "CronJob": true, "Job": true, "Pod": true, "ConfigMap": true}

func k8sKindIsConfigMap(root *yaml.Node) bool {
	k := yamlMap(root, "kind")
	return k != nil && k.Value == "ConfigMap"
}

func k8sWalk(f *FileCtx, root *yaml.Node, configMap bool) []truth.Fact {
	var out []truth.Fact
	if configMap {
		if data := yamlMap(root, "data"); data != nil && data.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(data.Content); i += 2 {
				if fc, ok := yamlFact("k8s.env", f, data.Content[i+1], data.Content[i].Value); ok {
					out = append(out, fc)
				}
			}
		}
		return out
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value == "env" && v.Kind == yaml.SequenceNode {
					for _, it := range v.Content {
						name, val := yamlMap(it, "name"), yamlMap(it, "value")
						if name == nil || val == nil {
							continue
						}
						if fc, ok := yamlFact("k8s.env", f, val, name.Value); ok {
							out = append(out, fc)
						}
					}
					continue
				}
				walk(v)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c)
			}
		}
	}
	walk(root)
	return out
}

// isTSConfigPath: environment*.ts, config*.ts and *.config.ts files.
func isTSConfigPath(p string) bool {
	b := strings.ToLower(baseName(p))
	if !(strings.HasSuffix(b, ".ts") || strings.HasSuffix(b, ".js")) || strings.HasSuffix(b, ".d.ts") {
		return false
	}
	return strings.HasPrefix(b, "environment") || strings.HasPrefix(b, "config") ||
		strings.HasSuffix(b, ".config.ts") || strings.HasSuffix(b, ".config.js")
}

var tsConfigProp = regexp.MustCompile(`(?:^|[\s{,])['"]?([A-Za-z_$][A-Za-z0-9_$]*)['"]?\s*:\s*['"` + "`" + `]((?i:https?)://[^'"` + "`" + `\s]*)['"` + "`" + `]`)

// applyTSConfig: an object property whose value is an absolute http(s)
// string literal. The path is the literal's own path (use sites live in other files).
func applyTSConfig(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for _, l := range f.Lines {
		for _, m := range tsConfigProp.FindAllStringSubmatch(l.Code, -1) {
			if !anyServiceURL(m[1], m[2]) {
				continue
			}
			if fc, ok := httpFact("ts.config_url", f, l, m[2], m[1]); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

// ---- .NET web.config / app.config appSettings ----

// isDotnetConfigPath matches web.config, app.config and their transforms
// (Web.Release.config, App.Staging.config).
func isDotnetConfigPath(p string) bool {
	b := strings.ToLower(baseName(p))
	if !strings.HasSuffix(b, ".config") {
		return false
	}
	return b == "web.config" || b == "app.config" || strings.HasPrefix(b, "web.") || strings.HasPrefix(b, "app.")
}

var (
	cfgAppSettingsOpen  = regexp.MustCompile(`(?i)<appSettings\b[^>]*?(/?)>`)
	cfgAppSettingsClose = regexp.MustCompile(`(?i)</appSettings\s*>`)
	cfgAddRe            = regexp.MustCompile(`(?is)<add\b([^>]*?)/?>`)
	// sqsHost: queue URLs belong to the channels class, not to http.
	sqsHost = regexp.MustCompile(`(?i)^sqs[.-]([a-z0-9-]+\.)?amazonaws\.com(\.cn)?$`)
)

// applyWebConfig reads <add key="K" value="V" /> entries inside <appSettings>
// with the same key/value criteria as appsettings.json.
func applyWebConfig(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	text := blankComments(string(f.Raw))
	var out []truth.Fact
	for _, o := range cfgAppSettingsOpen.FindAllStringSubmatchIndex(text, -1) {
		if o[2] >= 0 && o[3] > o[2] { // <appSettings ... /> is empty
			continue
		}
		end := len(text)
		if c := cfgAppSettingsClose.FindStringIndex(text[o[1]:]); c != nil {
			end = o[1] + c[0]
		}
		block := text[o[1]:end]
		for _, m := range cfgAddRe.FindAllStringSubmatchIndex(block, -1) {
			a := xmlAttrs(block[m[2]:m[3]])
			k, v := a["key"], strings.TrimSpace(a["value"])
			if k == "" || v == "" {
				continue
			}
			if !((cfgURLKey.MatchString(k) && !(httpsURL.MatchString(v) && notServiceKey.MatchString(k))) || anyServiceURL(k, v)) {
				continue
			}
			if host := truth.CanonHost(v); sqsHost.MatchString(host) {
				continue
			}
			if fc, ok := httpFact("webconfig.url", f, f.Lines[lineAt(f, text, o[1]+m[0])], v, k); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

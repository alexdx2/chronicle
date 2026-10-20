package scan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/internal/srccomment"
	"github.com/alexdx2/chronicle-core/store"
)

// configExtractorID labels the coverage rows the config pass writes.
const (
	configExtractorID      = "chronicle-config"
	configExtractorVersion = "1"
	maxConfigBytes         = 262144
)

// IsConfigFile says whether p is a file the config pass reads: appsettings*.json,
// .env and .env.*, docker-compose files, environment*.ts / config*.ts /
// *.config.ts.
func IsConfigFile(p string) bool {
	base := path.Base(p)
	lower := strings.ToLower(base)
	switch {
	case strings.HasPrefix(lower, "appsettings") && strings.HasSuffix(lower, ".json"):
		return true
	case lower == ".env" || strings.HasPrefix(lower, ".env."):
		return true
	case (strings.HasPrefix(lower, "docker-compose") || strings.HasPrefix(lower, "compose.")) &&
		(strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")):
		return true
	case isTSConfigFile(lower):
		return true
	case isGenericConfigFile(p):
		return true
	}
	return false
}

func isTSConfigFile(lower string) bool {
	ext := path.Ext(lower)
	switch ext {
	case ".ts", ".js", ".mjs", ".cjs":
	default:
		return false
	}
	if strings.HasSuffix(lower, ".d.ts") || strings.Contains(lower, ".spec.") || strings.Contains(lower, ".test.") {
		return false
	}
	stem := strings.TrimSuffix(lower, ext)
	return strings.HasPrefix(lower, "environment") || strings.HasPrefix(lower, "config") || strings.HasSuffix(stem, ".config")
}

// isRegistrationFile is a C# file that may register typed HTTP clients.
func isRegistrationFile(p string) bool { return strings.HasSuffix(strings.ToLower(p), ".cs") }

// isScriptSource is a JS/TS source file that may hand a config value to a
// client constructor. Config files, declarations and tests are not.
func isScriptSource(p string) bool {
	lower := strings.ToLower(p)
	switch path.Ext(lower) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
	default:
		return false
	}
	if strings.HasSuffix(lower, ".d.ts") || strings.Contains(lower, ".spec.") || strings.Contains(lower, ".test.") ||
		strings.Contains(lower, "node_modules/") || IsConfigFile(p) {
		return false
	}
	return true
}

// ReadConfigHosts reads every config file among files (repo-relative, tracked)
// and every C# client registration, and returns the table plus the config files
// it parsed. A file it cannot read is skipped: the table is best effort.
func ReadConfigHosts(root string, files []string) (*graph.ConfigHosts, []string) {
	t := &graph.ConfigHosts{Root: root}
	var parsed []string
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	tooBig := 0
	var namedRegs []namedReg
	var namedUses []namedUse
	classDecls := map[string][]classDecl{}
	sc := LoadScanConfig(root)
	var useFiles []string // sources a client may be built in; read again once the table is complete
	defer func() {
		if tooBig > 0 {
			log.Printf("scan: config: skipped %d files larger than %d bytes", tooBig, maxConfigBytes)
		}
	}()
	for _, p := range sorted {
		cfg := IsConfigFile(p)
		reg := isRegistrationFile(p)
		src := isScriptSource(p)
		envSrc := !cfg && !reg && !src && isEnvDefaultSource(p)
		tf := strings.EqualFold(path.Ext(p), ".tf")
		maybeConnector := strings.EqualFold(path.Ext(p), ".json")
		if !cfg && !reg && !src && !envSrc && !tf && !maybeConnector {
			continue
		}
		fp := filepath.Join(root, filepath.FromSlash(p))
		fi, err := os.Lstat(fp)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if fi.Size() > maxConfigBytes {
			tooBig++
			continue
		}
		body, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		if tf {
			parseTerraformChannels(t, p, body)
			continue
		}
		if maybeConnector && isConnectorJSON(p, body) {
			parseConnectorChannels(t, p, body)
			parsed = append(parsed, p)
			continue
		}
		if maybeConnector && !cfg {
			continue
		}
		if envSrc || src || reg || (cfg && isEnvDefaultSource(p)) {
			parseCDKChannels(t, p, body)
		}
		if envSrc || reg || src || (cfg && isEnvDefaultSource(p)) {
			if sc.Config.EnvDefaultsOn() {
				parseEnvDefaults(t, p, body)
			} // the default beside an env read, any language (a config module too)
			useFiles = append(useFiles, p)
		}
		if envSrc {
			continue // an env-default source is not a config file: no coverage row
		}
		if (reg || src) && !mayUseConfig(body) {
			continue
		}
		if reg {
			parseClientRegistrations(t, p, body)
			scanClassDecls(p, body, classDecls)
			rs, us := scanNamedClients(p, body)
			namedRegs, namedUses = append(namedRegs, rs...), append(namedUses, us...)
			continue
		}
		if src {
			parseConfigUses(t, p, body)
			continue
		}
		lower := strings.ToLower(path.Base(p))
		switch {
		case isGenericConfigFile(p) && !strings.HasPrefix(lower, "appsettings") && !strings.HasPrefix(lower, ".env") &&
			!strings.HasPrefix(lower, "docker-compose") && !strings.HasPrefix(lower, "compose."):
			parseGenericConfig(t, p, body)
		case strings.HasSuffix(lower, ".json"):
			parseAppsettings(t, p, body)
		case strings.HasPrefix(lower, ".env"):
			parseDotenv(t, p, body)
		case strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml"):
			parseCompose(t, p, body)
		default:
			parseTSConfig(t, p, body)
		}
		parsed = append(parsed, p)
	}
	joinNamedClients(t, namedRegs, namedUses)
	joinTypedClients(t, classDecls)
	parseKeyLiteralUses(t, root, useFiles)
	applyScanConfig(t, root, sorted, sc, &parsed)
	return t, parsed
}

// parseAppsettings flattens an appsettings JSON file; every string value that is
// an absolute http(s) URL becomes an entry keyed "A:B:C".
func parseAppsettings(t *graph.ConfigHosts, file string, body []byte) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		// appsettings tolerates comments and trailing commas
		clean := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAll(body, nil)
		clean = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAll(clean, nil)
		clean = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAll(clean, []byte("$1"))
		if err := json.Unmarshal(clean, &v); err != nil {
			return
		}
	}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
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
					key = prefix + ":" + k
				}
				walk(key, x[k])
			}
		case []any:
			for i, e := range x {
				walk(fmt.Sprintf("%s:%d", prefix, i), e)
			}
		case string:
			t.Add(prefix, x, file)
		}
	}
	walk("", v)
}

// parseDotenv reads KEY=VALUE lines (optionally "export KEY=VALUE", quoted).
func parseDotenv(t *graph.ConfigHosts, file string, body []byte) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.Index(v, " #"); i >= 0 && !strings.HasPrefix(v, "\"") && !strings.HasPrefix(v, "'") {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		t.Add(strings.TrimSpace(k), v, file)
	}
}

// parseCompose reads services.*.environment (map or KEY=VALUE list form).
func parseCompose(t *graph.ConfigHosts, file string, body []byte) {
	var doc struct {
		Services map[string]struct {
			Environment any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return
	}
	names := make([]string, 0, len(doc.Services))
	for n := range doc.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		switch env := doc.Services[n].Environment.(type) {
		case map[string]any:
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if s, ok := env[k].(string); ok {
					t.Add(k, composeDefault(s), file)
				}
			}
		case []any:
			for _, e := range env {
				if s, ok := e.(string); ok {
					if k, v, ok := strings.Cut(s, "="); ok {
						t.Add(strings.TrimSpace(k), composeDefault(v), file)
					}
				}
			}
		}
	}
}

// composeDefault unwraps "${VAR:-http://x}" to its default.
func composeDefault(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		if _, def, ok := strings.Cut(v[2:len(v)-1], ":-"); ok {
			return def
		}
	}
	return v
}

// tsToken is one meaningful token of a TS config object: a property holding an
// absolute URL literal, a property opening an object, a bare brace, a closing brace.
var tsToken = regexp.MustCompile("([A-Za-z_$][\\w$]*)[\"']?\\s*:\\s*(?:[^,{}\"'`\\n]*?(?:\\|\\||\\?\\?)\\s*)?[\"'`](https?://[^\"'`\\s]+)[\"'`]|([A-Za-z_$][\\w$]*)[\"']?\\s*:\\s*\\{|(\\{)|(\\})")

// parseTSConfig reads object properties with absolute URL literals in
// environment*.ts / config*.ts. The key is the property path inside the
// exported object ("apiUrl", "api.url"); the object's own name is dropped.
func parseTSConfig(t *graph.ConfigHosts, file string, body []byte) {
	src := string(srccomment.Strip(file, body))
	var stack []string
	for _, m := range tsToken.FindAllStringSubmatch(src, -1) {
		switch {
		case m[1] != "":
			var parts []string
			for _, s := range stack {
				if s != "" {
					parts = append(parts, s)
				}
			}
			t.Add(strings.Join(append(parts, m[1]), "."), m[2], file)
		case m[3] != "":
			stack = append(stack, m[3])
		case m[4] != "":
			stack = append(stack, "")
		case m[5] != "":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

var (
	clientRegRe = regexp.MustCompile(`Add(?:Http|Refit|Typed)Client\s*<\s*([\w.]+)\s*(?:,\s*([\w.]+))?\s*>`)
	baseAddrRe  = regexp.MustCompile(`BaseAddress\s*=\s*new\s+Uri\s*\(`)
)

// parseClientRegistrations finds AddHttpClient<T>(c => c.BaseAddress = new Uri(X))
// and AddRefitClient<T>().ConfigureHttpClient(c => c.BaseAddress = new Uri(X))
// and binds the class (and its interface) to X: a config key or a literal URL.
func parseClientRegistrations(t *graph.ConfigHosts, file string, body []byte) {
	src := string(srccomment.Strip(file, body))
	if !strings.Contains(src, "Client") {
		return
	}
	locs := clientRegRe.FindAllStringSubmatchIndex(src, -1)
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if end > loc[1]+1500 {
			end = loc[1] + 1500
		}
		seg := src[loc[1]:end]
		bm := baseAddrRe.FindStringIndex(seg)
		if bm == nil {
			continue
		}
		expr := balancedArg(seg[bm[1]:])
		if expr == "" {
			continue
		}
		var key, lit string
		if q := strings.TrimSpace(expr); len(q) > 1 && (q[0] == '"' || q[0] == '\'') {
			if end := strings.IndexByte(q[1:], q[0]); end >= 0 {
				lit = q[1 : 1+end]
			}
		} else if kind, k := graph.ClassifyCallBase(strings.TrimSpace(strings.TrimSuffix(expr, "!"))); (kind == graph.SymConfig || kind == graph.SymEnv) && k != "" {
			key = k
		}
		for _, g := range []int{2, 4} {
			if loc[g] >= 0 {
				name := lastIdent(src[loc[g]:loc[g+1]])
				t.AddClient(graph.ClientBinding{Class: name, Key: key, URL: lit, File: file})
			}
		}
	}
}

func lastIdent(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// configMarkers are the substrings a .cs/.ts/.js file must contain to be worth
// parsing for client registrations or config uses.
var configMarkers = [][]byte{[]byte("Client"), []byte("process.env"), []byte("environment"), []byte("Configuration"), []byte("config."), []byte("onfig"), []byte("baseURL"), []byte("baseUrl")}

func mayUseConfig(body []byte) bool {
	for _, m := range configMarkers {
		if bytes.Contains(body, m) {
			return true
		}
	}
	return false
}

// maxBalancedArg bounds how far balancedArg looks for the closing parenthesis.
const maxBalancedArg = 2048

// balancedArg returns the text up to the parenthesis that closes an already
// opened one, or "" when it is not closed within maxBalancedArg bytes.
func balancedArg(s string) string {
	if len(s) > maxBalancedArg {
		s = s[:maxBalancedArg]
	}
	depth := 1
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return ""
}

// readConfig builds the domain's config table, makes the graph use it for http
// calls, records a coverage row for each config file it read (they are not sent
// to the model, but they are covered) and returns the table.
func (r *fullRun) readConfig(d domainRef) (*graph.ConfigHosts, error) {
	out, err := gitutil.RunRaw(r.o.RepoRoot, "-c", "core.quotePath=false", "ls-files", "-z")
	if err != nil {
		return nil, fmt.Errorf("scan: list tracked files: %w", err)
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		if d.multi && r.m.DomainForFile(p) != d.key {
			continue
		}
		files = append(files, p)
	}
	table, parsed := ReadConfigHosts(r.o.RepoRoot, files)
	r.g.SetConfigHosts(d.key, table)
	if r.configs == nil {
		r.configs = map[string]*graph.ConfigHosts{}
	}
	r.configs[d.key] = table
	st := r.g.Store()
	for _, p := range parsed {
		blob, _ := gitutil.FileBlobSHA(r.o.RepoRoot, p)
		if err := st.UpsertCoverage(store.CoverageRow{
			DomainKey: d.key, FilePath: p, ExtractorID: configExtractorID,
			ExtractorVersion: configExtractorVersion, Status: "config_only", BlobSHA: blob,
		}); err != nil {
			return nil, fmt.Errorf("scan: record config coverage: %w", err)
		}
	}
	return table, nil
}

// domainRef is what readConfig needs of a domain.
type domainRef struct {
	key   string
	multi bool
}

var (
	// new NotifyClient(  /  createClient(  /  makeApiClient(  /  buildSdk(
	ctorCallRe    = regexp.MustCompile(`\bnew\s+([A-Z][A-Za-z0-9_]*)\s*\(`)
	factoryCallRe = regexp.MustCompile(`\b((?:create|make|build|init|get|new)[A-Za-z0-9_]*(?:Client|Api|Sdk|SDK|Service|Gateway|Connector)[A-Za-z0-9_]*)\s*\(`)
	// trailing fallbacks and casts that do not change which value is passed
	argTailRe = regexp.MustCompile(`(?s)\s*(?:\?\?|\|\|)\s*.*$|\s+as\s+\w+$|!$`)
)

// builtinCtors never take a service address worth a call edge.
var builtinCtors = map[string]bool{
	"URL": true, "Error": true, "Map": true, "Set": true, "Date": true, "Promise": true,
	"RegExp": true, "Headers": true, "Request": true, "Response": true, "URLSearchParams": true,
	"Proxy": true, "Array": true, "Object": true, "WebSocket": true, "Worker": true,
}

// parseConfigUses finds `new X(<config value>)` and client-factory calls whose
// argument is process.env.X / config.x / environment.x, one ConfigUse per
// argument. Resolving the symbol to a host is the resolver's job.
func parseConfigUses(t *graph.ConfigHosts, file string, body []byte) {
	src := string(srccomment.Strip(file, body))
	if !strings.Contains(src, "env") && !strings.Contains(src, "onfig") && !strings.Contains(src, "ettings") {
		return
	}
	line := 1
	pos := 0
	lineAt := func(off int) int {
		line += strings.Count(src[pos:off], "\n")
		pos = off
		return line
	}
	type hit struct {
		off    int
		callee string
		args   string
	}
	var hits []hit
	aliases := localConfigAliases(src)
	for _, re := range []*regexp.Regexp{ctorCallRe, factoryCallRe, httpLibFactoryRe} {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			callee := src[m[2]:m[3]]
			if builtinCtors[callee] {
				continue
			}
			hits = append(hits, hit{off: m[0], callee: callee, args: balancedArg(src[m[1]:])})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].off < hits[j].off })
	for _, h := range hits {
		for _, arg := range splitTopLevel(h.args) {
			for _, expr := range addressArgs(arg) {
				symbol := expr
				if a, ok := aliases[expr]; ok { // const apiUrl = Constants.expoConfig.extra.apiUrl
					symbol = a
				}
				kind, _ := graph.ClassifyCallBase(symbol)
				if kind != graph.SymEnv && kind != graph.SymConfig {
					continue
				}
				t.AddUse(graph.ConfigUse{File: file, Line: lineAt(h.off), Text: expr, Callee: h.callee, Symbol: symbol})
			}
		}
	}
}

// httpLibFactoryRe: axios.create({ baseURL }), got.extend({ prefixUrl }), ky.create ...
var httpLibFactoryRe = regexp.MustCompile(`\b((?:axios|got|ky|ofetch|superagent)\.(?:create|extend))\s*\(`)

var (
	localAliasRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::\s*[\w<>\[\]|. ]+)?=\s*([^;\n]+)`)
	addressKeyRe = regexp.MustCompile(`(?i)^(?:base_?url|prefix_?url|url|uri|endpoint|host|address|api_?url|[a-z]*url)$`)
)

// localConfigAliases finds `const name = <config value>` so a client built from
// `name` is known to be built from the value.
func localConfigAliases(src string) map[string]string {
	out := map[string]string{}
	for _, m := range localAliasRe.FindAllStringSubmatch(src, -1) {
		expr := strings.TrimSpace(argTailRe.ReplaceAllString(strings.TrimSpace(m[2]), ""))
		if kind, _ := graph.ClassifyCallBase(expr); kind == graph.SymEnv || kind == graph.SymConfig {
			out[m[1]] = expr
		}
	}
	return out
}

// addressArgs gives the expressions of one call argument that may hold an
// address: the argument itself, or the address-named properties of an options
// object ({ baseURL: apiUrl, timeout: 5 } gives apiUrl).
func addressArgs(arg string) []string {
	a := strings.TrimSpace(arg)
	if strings.HasPrefix(a, "{") && strings.HasSuffix(a, "}") {
		var out []string
		for _, prop := range splitTopLevel(a[1 : len(a)-1]) {
			k, v, ok := strings.Cut(prop, ":")
			if !ok || !addressKeyRe.MatchString(strings.Trim(strings.TrimSpace(k), `"'`)) {
				continue
			}
			out = append(out, strings.TrimSpace(argTailRe.ReplaceAllString(strings.TrimSpace(v), "")))
		}
		return out
	}
	return []string{strings.TrimSpace(argTailRe.ReplaceAllString(a, ""))}
}

// splitTopLevel splits call arguments at commas outside brackets and quotes.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"' || r == '`':
			quote = r
		case r == '(' || r == '[' || r == '{':
			depth++
		case r == ')' || r == ']' || r == '}':
			depth--
		case r == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

package graph

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ConfigEntry is one configuration key whose value is an absolute http(s) URL,
// as the scan read it out of a config file. URL is sanitized: no userinfo, no
// query, no fragment.
type ConfigEntry struct {
	Key  string // "Services:PricingApi", "NOTIFY_SVC_URL", "apiUrl", "api.url"
	URL  string // "http://pricing-api:8080"
	Host string // "pricing-api" (no port)
	File string // repo-relative path of the config file
	Dir  string // directory of File ("." for the root)
	// Routing marks an address that sits under a routing key (reverse proxy
	// destinations, gateway routes, upstreams): the configuration is itself the
	// integration, no call site names it.
	Routing bool
}

// ClientBinding says a typed HTTP client class (AddHttpClient<T>,
// AddRefitClient<T>) gets its base address from a config key or a literal.
type ClientBinding struct {
	Class string
	Key   string // config key, when the address is read from configuration
	URL   string // sanitized literal address, when it is written in the code
	File  string // registration file
	Dir   string
}

// ConfigHosts is the per-repo table of config key -> URL the scan builds
// deterministically before extraction. The resolver uses it to give a host to
// a call whose target is only a symbol; the prompt gets keys and hosts.
type ConfigHosts struct {
	// Root is the repository root the scan read the files from; source files
	// named by repo-relative paths are read under it. Empty = ProjectRoot().
	Root    string
	Entries []ConfigEntry
	Clients []ClientBinding
	Uses    []ConfigUse
}

// ConfigUse is a source line that hands a configuration value (process.env.X,
// config.x, environment.x) to a constructor or client factory call:
// `new NotifyClient(process.env.NOTIFY_SVC_URL)`. The scan reads it from the
// text; the resolver turns it into a call to the value's host.
type ConfigUse struct {
	File   string
	Line   int    // 1-based
	Text   string // the argument expression as written, the evidence anchor
	Callee string // constructor or factory name
	Symbol string // the base the argument was classified from
}

// AddUse records a config value passed to a constructor / factory call.
func (c *ConfigHosts) AddUse(u ConfigUse) {
	if u.File == "" || u.Text == "" || u.Symbol == "" {
		return
	}
	c.Uses = append(c.Uses, u)
}

// SanitizeConfigURL reduces an absolute http(s) URL to scheme://host[:port]:
// userinfo, path, query and fragment never enter the table (a path can hold a
// token: Slack hooks, Telegram /bot<token>/). The call's own path comes from
// the call site. ok is false for anything
// that is not an absolute http(s) URL with a host.
func SanitizeConfigURL(raw string) (clean, host string, ok bool) {
	raw = strings.TrimSpace(raw)
	low := strings.ToLower(raw)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), "${}") {
		return "", "", false
	}
	host = strings.ToLower(u.Hostname())
	clean = u.Scheme + "://" + strings.ToLower(u.Host)
	return clean, host, true
}

// Add records a key/URL pair read from file; values that are not absolute
// http(s) URLs are ignored.
func (c *ConfigHosts) Add(key, rawURL, file string) {
	clean, host, ok := SanitizeConfigURL(rawURL)
	if !ok || key == "" {
		return
	}
	c.Entries = append(c.Entries, ConfigEntry{Key: key, URL: clean, Host: host, File: file, Dir: dirOf(file), Routing: isRoutingKey(key)})
}

// AddClient records a typed client registration.
func (c *ConfigHosts) AddClient(b ClientBinding) {
	if b.Class == "" || (b.Key == "" && b.URL == "") {
		return
	}
	b.Dir = dirOf(b.File)
	c.Clients = append(c.Clients, b)
}

// Empty reports whether the table holds nothing.
func (c *ConfigHosts) Empty() bool { return c == nil || (len(c.Entries) == 0 && len(c.Clients) == 0) }

func dirOf(file string) string {
	if i := strings.LastIndex(file, "/"); i >= 0 {
		return file[:i]
	}
	return "."
}

func isUnder(dir, file string) bool {
	if dir == "." || dir == "" {
		return true
	}
	return strings.HasPrefix(file, dir+"/")
}

// normConfigKey makes the spellings of one key compare equal: "A:B", "A__B",
// "a.b" and "A_B"-style names differ only in separator and case.
func normConfigKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.ReplaceAll(k, "__", ":")
	k = strings.ReplaceAll(k, ".", ":")
	return k
}

// lookup finds the entry for key as seen from the file at filePath. The config
// file nearest to filePath (deepest ancestor directory) wins; among equals the
// file named without an environment suffix wins, then the path order. A key no
// ancestor holds resolves only when exactly one entry in the repo has it.
func (c *ConfigHosts) lookup(filePath, key string) (ConfigEntry, bool) {
	if c == nil {
		return ConfigEntry{}, false
	}
	want := normConfigKey(key)
	var near, any []ConfigEntry
	for _, e := range c.Entries {
		if normConfigKey(e.Key) != want {
			continue
		}
		any = append(any, e)
		if isUnder(e.Dir, filePath) {
			near = append(near, e)
		}
	}
	pick := func(es []ConfigEntry) ConfigEntry {
		sort.SliceStable(es, func(i, j int) bool {
			if len(es[i].Dir) != len(es[j].Dir) {
				return len(es[i].Dir) > len(es[j].Dir)
			}
			vi, vj := variantFile(es[i].File), variantFile(es[j].File)
			if vi != vj {
				return !vi
			}
			return es[i].File < es[j].File
		})
		return es[0]
	}
	if len(near) > 0 {
		return pick(near), true
	}
	hosts := map[string]bool{}
	for _, e := range any {
		hosts[e.URL] = true
	}
	if len(hosts) == 1 {
		return any[0], true
	}
	return ConfigEntry{}, false
}

// variantFile is true for config files that override a base one for a
// particular environment (appsettings.Development.json, .env.local,
// environment.prod.ts): their values are second choice.
func variantFile(file string) bool {
	base := file[strings.LastIndex(file, "/")+1:]
	switch {
	case strings.HasPrefix(base, "appsettings."):
		return strings.Count(base, ".") > 1
	case strings.HasPrefix(base, ".env."):
		return true
	case strings.HasPrefix(base, "environment."), strings.HasPrefix(base, "config."):
		return strings.Count(base, ".") > 1
	}
	return false
}

var (
	// Configuration["A:B"], config["A:B"], _config["A:B"], settings["A"]
	cfgIndexRe = regexp.MustCompile(`\[\s*["']?([A-Za-z0-9_:.\-]+)["']?\s*\]`)
	// GetValue<string>("A:B"), GetSection("A:B"), GetConnectionString("x"), GetString("A")
	cfgGetRe = regexp.MustCompile(`Get(?:Value|Section|String|ConnectionString)\s*(?:<[^>]*>)?\s*\(\s*["']([^"')]+)["']`)
	// process.env.X, process.env['X'], env.X, os.environ['X'], os.getenv('X'), Environment.GetEnvironmentVariable("X")
	envAccessRe = regexp.MustCompile(`(?:process\.env\.|(?:^|[^A-Za-z.])env\.|ENV\.)([A-Za-z_][A-Za-z0-9_]*)|(?:process\.env|os\.environ|ENV)\s*\[\s*["']([^"']+)["']\s*\]|(?:os\.getenv|Environment\.GetEnvironmentVariable|System\.getenv)\s*\(\s*["']([^"']+)["']`)
	// environment.apiUrl, config.api.url, settings.x (a TS/JS config object)
	objPathRe    = regexp.MustCompile(`^(?:this\.)?(?:[A-Za-z_$][\w$]*\.)*?(?:environment|environments|config|settings|appsettings|appConfig|AppConfig|extra)\.([A-Za-z_][A-Za-z0-9_.]*)$`)
	lowerIdentRe = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	// Spring / compose style: ${VAR}, ${VAR:-default}
	placeholderRe = regexp.MustCompile(`^\$\{\s*([^}:]+?)\s*(?::-[^}]*)?\}$`)
)

// SymbolKind classifies the base part of a call target.
type SymbolKind int

const (
	SymNone    SymbolKind = iota // a literal host or URL: nothing to resolve
	SymEnv                       // an environment variable
	SymConfig                    // a configuration key or config-object property
	SymUnknown                   // some other expression we cannot read
)

// SplitCallTarget separates a call target into its symbolic base and the path
// after it ("${environment.apiUrl}/orders" -> "${environment.apiUrl}", "/orders").
// The split is at the first "/" outside brackets, braces and parentheses.
func SplitCallTarget(target string) (base, rest string) {
	s := cleanCallTarget(target)
	s = strings.TrimLeft(s, "$@")
	depth := 0
	for i, r := range s {
		switch r {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case '/':
			if depth == 0 {
				return s[:i], s[i:]
			}
		}
	}
	return s, ""
}

// ClassifyCallBase reads the base of a call target. key is the config key, env
// variable name or object path it refers to.
func ClassifyCallBase(base string) (kind SymbolKind, key string) {
	b := strings.TrimSpace(base)
	if b == "" {
		return SymNone, ""
	}
	low := strings.ToLower(b)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") ||
		strings.HasPrefix(low, "grpc://") || strings.HasPrefix(low, "grpcs://") {
		return SymNone, ""
	}
	inner := b
	wrapped := false
	for {
		switch {
		case strings.HasPrefix(inner, "${") && strings.HasSuffix(inner, "}"):
			inner, wrapped = strings.TrimSpace(inner[2:len(inner)-1]), true
			continue
		case strings.HasPrefix(inner, "{") && strings.HasSuffix(inner, "}"):
			inner, wrapped = strings.TrimSpace(inner[1:len(inner)-1]), true
			continue
		case strings.HasPrefix(inner, "$") && len(inner) > 1:
			inner, wrapped = inner[1:], true
			continue
		}
		break
	}
	if m := envAccessRe.FindStringSubmatch(inner); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				return SymEnv, g
			}
		}
	}
	if m := cfgGetRe.FindStringSubmatch(inner); m != nil {
		return SymConfig, m[1]
	}
	if strings.ContainsAny(inner, "[(") {
		// Configuration["A:B"], _config["Services"]["X"] ...
		if ms := cfgIndexRe.FindAllStringSubmatch(inner, -1); len(ms) > 0 {
			parts := make([]string, len(ms))
			for i, m := range ms {
				parts[i] = m[1]
			}
			return SymConfig, strings.Join(parts, ":")
		}
		return SymUnknown, ""
	}
	if m := objPathRe.FindStringSubmatch(strings.ReplaceAll(inner, "?.", ".")); m != nil {
		return SymConfig, m[1]
	}
	if m := placeholderRe.FindStringSubmatch(b); m != nil && envVarRe.MatchString(m[1]) {
		return SymEnv, m[1]
	}
	if wrapped && envVarRe.MatchString(inner) {
		return SymEnv, inner
	}
	if wrapped && lowerIdentRe.MatchString(inner) {
		return SymUnknown, "" // ${base}: a local variable, not a name for a service
	}
	if envVarRe.MatchString(inner) && strings.Contains(inner, "_") {
		return SymEnv, inner
	}
	return SymNone, ""
}

// ResolveSymbol gives the sanitized base URL a symbolic base stands for.
func (c *ConfigHosts) ResolveSymbol(filePath, base string) (kind SymbolKind, key string, e ConfigEntry, ok bool) {
	kind, key = ClassifyCallBase(base)
	if kind == SymNone || kind == SymUnknown || key == "" {
		return kind, key, ConfigEntry{}, false
	}
	e, ok = c.lookup(filePath, key)
	return kind, key, e, ok
}

// ClientBase gives the base URL a typed client is registered with. classes are
// the names the calling code goes by (file name, owning class, interface).
func (c *ConfigHosts) ClientBase(filePath string, classes ...string) (ConfigEntry, bool) {
	if c == nil {
		return ConfigEntry{}, false
	}
	var cand []ClientBinding
	for _, b := range c.Clients {
		for _, n := range classes {
			if n != "" && strings.EqualFold(b.Class, n) {
				cand = append(cand, b)
				break
			}
		}
	}
	sort.SliceStable(cand, func(i, j int) bool {
		di, dj := isUnder(cand[i].Dir, filePath), isUnder(cand[j].Dir, filePath)
		if di != dj {
			return di
		}
		return cand[i].File < cand[j].File
	})
	for _, b := range cand {
		if b.URL != "" {
			if clean, host, ok := SanitizeConfigURL(b.URL); ok {
				return ConfigEntry{Key: b.Class, URL: clean, Host: host, File: b.File, Dir: b.Dir}, true
			}
		}
		if b.Key != "" {
			if e, ok := c.lookup(b.File, b.Key); ok {
				return e, true
			}
		}
	}
	return ConfigEntry{}, false
}

// PromptSection renders the table as a short block for the extraction prompt:
// keys and hosts only. The URL's path, userinfo and query never appear.
func (c *ConfigHosts) PromptSection(filePath string) string {
	if c.Empty() {
		return ""
	}
	type row struct{ key, host, class string }
	seen := map[string]bool{}
	var rows []row
	for _, e := range c.Entries {
		if e.File == filePath {
			// the file that defines the value shows it to the model itself;
			// telling it "the host is filled in for you" there makes it skip
			// the very line that holds the address
			continue
		}
		if !isUnder(e.Dir, filePath) && len(c.Entries) > 60 {
			continue
		}
		k := e.Key + "\x00" + e.Host
		if seen[k] {
			continue
		}
		seen[k] = true
		rows = append(rows, row{key: e.Key, host: e.Host})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].key < rows[j].key || (rows[i].key == rows[j].key && rows[i].host < rows[j].host)
	})
	if len(rows) == 0 && len(c.Clients) == 0 {
		return ""
	}
	if len(rows) > 40 {
		rows = rows[:40]
	}
	var b strings.Builder
	b.WriteString("\n\n## Known config values\nThese configuration keys of this repository hold the hosts below. When a call's address is one of these keys (a configuration key, an environment variable, a settings property: Configuration[\"..\"], process.env.X, os.getenv(\"X\"), @Value, environment.x), report the call with the key as the target; the host is filled in for you.\n")
	for _, r := range rows {
		b.WriteString("- " + r.key + " -> " + r.host + "\n")
	}
	if len(c.Clients) > 0 {
		b.WriteString("Typed HTTP clients registered with a base address from configuration: ")
		var names []string
		seenC := map[string]bool{}
		for _, cl := range c.Clients {
			if !seenC[cl.Class] {
				seenC[cl.Class] = true
				names = append(names, cl.Class)
			}
		}
		sort.Strings(names)
		b.WriteString(strings.Join(names, ", ") + ". A call made through one of them with only a path (\"/prices/{sku}\") is an http_call with that path as the target.\n")
	}
	return b.String()
}

// routingKeyWords are config key parts that say the value is where traffic is
// sent on behalf of callers: reverse proxies, API gateways, load balancers.
var routingKeyWords = []string{"reverseproxy", "destinations", "upstream", "proxy_pass", "proxypass", "downstream", "gateway.routes", "gateway:routes", "backends"}

func isRoutingKey(key string) bool {
	k := strings.ToLower(key)
	for _, w := range routingKeyWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

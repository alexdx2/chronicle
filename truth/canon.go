package truth

import (
	"regexp"
	"sort"
	"strings"
)

// Canonical forms shared by extraction (keys) and comparison (graph side).

var (
	angleParam = regexp.MustCompile(`^<[^<>]+>$`)
	braceParam = regexp.MustCompile(`^\{[^{}]*\}$`)
	pep503     = regexp.MustCompile(`[-_.]+`)
	camelSplit = regexp.MustCompile(`([a-z0-9])([A-Z])`)

	multiUnderscore = regexp.MustCompile(`_+`)
)

// CanonEndpoint returns "METHOD /canonical/path"; an empty method means GET.
func CanonEndpoint(method, path string) string {
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		m = "GET"
	}
	return m + " " + CanonPath(path)
}

// CanonPath lowercases the path, ensures a leading "/", drops the query string
// and trailing "/", and replaces every parameter segment with "{}".
func CanonPath(path string) string {
	p := strings.TrimSpace(path)
	p = cutQuery(p)
	p = strings.ToLower(p)
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if s == "" {
			continue
		}
		if isParamSegment(s) {
			s = "{}"
		}
		out = append(out, s)
	}
	return "/" + strings.Join(out, "/")
}

// cutQuery drops a "?query" or "#fragment" tail, but not a '?' inside braces
// (ASP.NET optional parameter "{id?}").
func cutQuery(p string) string {
	depth := 0
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case '?', '#':
			if depth == 0 {
				return p[:i]
			}
		}
	}
	return p
}

func isParamSegment(s string) bool {
	switch {
	case strings.HasPrefix(s, ":") && len(s) > 1:
		return true
	case strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}"):
		return true
	case braceParam.MatchString(s), angleParam.MatchString(s):
		return true
	case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") && len(s) > 2:
		return true
	}
	return false
}

// CanonHost lowercases a URL or host and strips scheme, userinfo, port, path
// and trailing "/". Local hosts yield "".
func CanonHost(raw string) string {
	h := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	if strings.HasPrefix(h, "[") { // IPv6 literal
		if i := strings.Index(h, "]"); i >= 0 {
			h = h[:i+1]
		}
	} else if i := strings.Index(h, ":"); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	switch h {
	case "localhost", "127.0.0.1", "0.0.0.0", "[::1]":
		return ""
	}
	return h
}

var hostVarSuffixes = []string{"_BASE_URL", "_URL", "_URI", "_ENDPOINT", "_HOST", "_ADDRESS"}

// HostFromVar derives a service host name from a config variable name:
// "TOM_API_URL" -> "tom-api", "PaymentsBaseUrl" -> "payments".
func HostFromVar(name string) string {
	s := camelSplit.ReplaceAllString(strings.TrimSpace(name), "${1}_${2}")
	s = strings.ToUpper(strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(s))
	for _, suf := range hostVarSuffixes {
		if strings.HasSuffix(s, suf) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	s = strings.ToLower(multiUnderscore.ReplaceAllString(s, "_"))
	return strings.Trim(strings.ReplaceAll(s, "_", "-"), "-")
}

// CanonName is the tolerant form for topic/service/package names: lowercase
// with ". - _" and spaces removed (mirrors the graph's flattenName).
func CanonName(s string) string {
	return strings.ToLower(strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(s))
}

// StripScope removes an npm scope ("@acme/catalog" -> "catalog"); any other
// name is returned unchanged.
func StripScope(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "@") {
		if i := strings.Index(s, "/"); i > 0 && i < len(s)-1 {
			return s[i+1:]
		}
	}
	return s
}

// CanonDep returns "ecosystem:name" in the ecosystem's canonical case.
func CanonDep(ecosystem, name string) string {
	eco := strings.ToLower(strings.TrimSpace(ecosystem))
	n := strings.ToLower(strings.TrimSpace(name))
	if eco == "pypi" {
		eco = "python"
	}
	if eco == "python" {
		n = pep503.ReplaceAllString(n, "-")
	}
	return eco + ":" + n
}

// EndpointKey is the fact key of an endpoint.
func EndpointKey(method, path string) string { return CanonEndpoint(method, path) }

// ChannelKey is the fact key of a channel, e.g. "topic:order-created#produce".
func ChannelKey(kind, name, side string) string {
	return strings.ToLower(kind) + ":" + strings.ToLower(strings.TrimSpace(name)) + "#" + strings.ToLower(side)
}

// DepKey is the fact key of a dependency.
func DepKey(ecosystem, name string) string { return CanonDep(ecosystem, name) }

// ServiceKey is the fact key of a service, e.g. "own:orders-api".
func ServiceKey(kind, name string) string {
	return strings.ToLower(kind) + ":" + strings.ToLower(strings.TrimSpace(name))
}

var (
	noisePrefixes = []string{"system.", "microsoft.", "runtime.", "@types/", "eslint", "xunit", "nunit", "coverlet", "pytest", "@vitejs/", "@testing-library", "@babel/", "babel-", "@swc/", "postcss", "autoprefixer", "tailwindcss", "org.junit", "junit:", "org.mockito", "org.jetbrains.kotlin:kotlin-test", "io.ktor:ktor-server-test-host", "org.springframework.boot:spring-boot-starter-test"}
	noiseExact    = map[string]bool{
		"netstandard.library": true, "typescript": true, "prettier": true, "jest": true,
		"vitest": true, "mocha": true, "moq": true, "fluentassertions": true,
		"black": true, "ruff": true, "mypy": true, "flake8": true,
		"react": true, "react-dom": true, "next": true, "vue": true, "angular": true, "vite": true, "webpack": true,
		"rollup": true, "esbuild": true, "tsx": true, "ts-node": true, "tsup": true, "supertest": true, "nock": true,
	}
)

// The ecosystem parameter is accepted for symmetry with CanonDep and is
// intentionally unused: spec §4.4 has one shared noise list.
//
// Noise reports whether a dependency is on the reference's own noise list
// (framework and tooling packages that get status "ignored" when absent from
// the graph). Matching is case-insensitive.
func Noise(ecosystem, name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if noiseExact[n] {
		return true
	}
	for _, p := range noisePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

var genericHostTokens = map[string]bool{
	"service": true, "svc": true, "srv": true, "server": true, "api": true, "client": true,
	"http": true, "https": true, "base": true, "internal": true, "external": true,
	"url": true, "gateway": true, "backend": true, "host": true,
}

// LooseTokens returns the sorted, "|"-joined content tokens of a service name
// or var-derived host: camelCase and "-_. " split, generic tokens (service,
// api, url, gateway, ...) dropped, one trailing plural "s" trimmed. It returns
// "" when no content token is left, so generic-only names never match.
func LooseTokens(s string) string {
	s = camelSplit.ReplaceAllString(strings.TrimSpace(s), "${1}_${2}")
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	})
	seen := map[string]bool{}
	var toks []string
	for _, p := range parts {
		if genericHostTokens[p] {
			continue
		}
		if len(p) > 3 && strings.HasSuffix(p, "s") && !strings.HasSuffix(p, "ss") && !strings.HasSuffix(p, "us") && !strings.HasSuffix(p, "is") {
			p = p[:len(p)-1]
		}
		if !seen[p] {
			seen[p] = true
			toks = append(toks, p)
		}
	}
	sort.Strings(toks)
	return strings.Join(toks, "|")
}

// GraphQLEndpointKey is the fact key of a GraphQL operation: kind is "query"
// or "mutation", name is the field name, whose case is kept (GraphQL names
// are case-sensitive), e.g. "MUTATION renameProduct".
func GraphQLEndpointKey(kind, name string) string {
	return strings.ToUpper(strings.TrimSpace(kind)) + " " + strings.TrimSpace(name)
}

// DevSection reports whether a dependency section only matters while
// developing or testing: npm devDependencies, Maven test scope, Gradle
// testImplementation, Composer require-dev, Cargo dev-dependencies, a Gemfile
// development/test group, requirements-dev.txt. Such a dependency is not a
// fact the running system has, so the comparison ignores it when the graph
// leaves it out.
func DevSection(section string) bool {
	s := strings.ToLower(strings.TrimSpace(section))
	switch {
	case s == "":
		return false
	case strings.HasPrefix(s, "dev"), strings.HasPrefix(s, "test"):
		return true
	case strings.HasSuffix(s, "-dev"), strings.HasSuffix(s, "_dev"), strings.Contains(s, "requirements-dev"), strings.Contains(s, "dev-requirements"):
		return true
	}
	return false
}

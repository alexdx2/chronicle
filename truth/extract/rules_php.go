package extract

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// PHP: Laravel routes, php-rdkafka / laravel-kafka channels, env() default
// URLs and composer.lock packages.

func init() {
	isPHP := func(p string) bool { return hasExt(p, ".php") }
	Rules = append(Rules,
		funcRule{"laravel.routes", isLaravelRoutesPath, applyLaravelRoutes},
		funcRule{"php.kafka", isPHP, applyPHPKafka},
		funcRule{"php.http", isPHP, applyPHPHTTP},
		funcRule{"lock.composer", func(p string) bool { return baseName(p) == "composer.lock" }, applyLockComposer},
	)
}

func isLaravelRoutesPath(p string) bool {
	if !hasExt(p, ".php") {
		return false
	}
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		if d == "routes" {
			return true
		}
	}
	return false
}

// ---- Laravel routes --------------------------------------------------------

type phpCall struct {
	name string
	args string
}

// phpChain parses `Route::a(...)->b(...)` (or `$router->a(...)`) from the
// start of a statement. mk is the literal-masked twin of text.
func phpChain(text, mk string) []phpCall {
	i := 0
	if strings.HasPrefix(mk, "Route::") {
		i = len("Route::")
	} else if strings.HasPrefix(mk, "$router->") {
		i = len("$router->")
	} else {
		return nil
	}
	var calls []phpCall
	for i < len(mk) {
		j := i
		for j < len(mk) && isIdentByte(mk[j]) {
			j++
		}
		if j == i {
			break
		}
		name := mk[i:j]
		for j < len(mk) && (mk[j] == ' ' || mk[j] == '\n' || mk[j] == '\t' || mk[j] == '\r') {
			j++
		}
		if j >= len(mk) || mk[j] != '(' {
			break
		}
		depth, k := 0, j
		for ; k < len(mk); k++ {
			if mk[k] == '(' {
				depth++
			} else if mk[k] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if k >= len(mk) { // unterminated (group opener): keep what we have
			calls = append(calls, phpCall{name, text[j+1:]})
			break
		}
		calls = append(calls, phpCall{name, text[j+1 : k]})
		k++
		for k < len(mk) && (mk[k] == ' ' || mk[k] == '\n' || mk[k] == '\t' || mk[k] == '\r') {
			k++
		}
		if strings.HasPrefix(mk[k:], "->") {
			i = k + 2
			for i < len(mk) && (mk[i] == ' ' || mk[i] == '\n' || mk[i] == '\t' || mk[i] == '\r') {
				i++
			}
			continue
		}
		break
	}
	return calls
}

var (
	phpFirstStr  = regexp.MustCompile(`^\s*['"]([^'"]*)['"]`)
	phpAllStr    = regexp.MustCompile(`['"]([^'"]*)['"]`)
	phpPrefixKey = regexp.MustCompile(`['"]prefix['"]\s*=>\s*['"]([^'"]*)['"]`)
	phpResKey    = regexp.MustCompile(`['"]([^'"]+)['"]\s*=>`)
	phpVerbs     = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "options": true}
)

func phpCountDelta(s, open, close string) int {
	return strings.Count(s, open) - strings.Count(s, close)
}

// phpGroupPrefix collects the prefix of a chain up to (and including the
// array argument of) a group call.
func phpGroupPrefix(calls []phpCall) string {
	pre := ""
	for _, c := range calls {
		switch c.name {
		case "prefix":
			if m := phpFirstStr.FindStringSubmatch(c.args); m != nil {
				pre = joinPath(pre, m[1], false)
			}
		case "group":
			if m := phpPrefixKey.FindStringSubmatch(c.args); m != nil {
				pre = joinPath(pre, m[1], false)
			}
		}
	}
	if pre == "/" {
		return ""
	}
	return pre
}

func phpResourceActions(name string, api bool, calls []phpCall) []string {
	acts := []string{"index", "store", "show", "update", "destroy"}
	if !api {
		acts = []string{"index", "create", "store", "show", "edit", "update", "destroy"}
	}
	for _, c := range calls {
		if c.name != "only" && c.name != "except" {
			continue
		}
		listed := map[string]bool{}
		for _, m := range phpAllStr.FindAllStringSubmatch(c.args, -1) {
			listed[m[1]] = true
		}
		var kept []string
		for _, a := range acts {
			if (c.name == "only") == listed[a] {
				kept = append(kept, a)
			}
		}
		acts = kept
	}
	return acts
}

func applyLaravelRoutes(f *FileCtx) []truth.Fact {
	if isTestLike(f) {
		return nil
	}
	type frame struct {
		prefix string
		depth  int
		line   lex.Line
	}
	var stack []frame
	var out []truth.Fact
	depth := 0
	stackPrefix := func() (string, []truth.Evidence) {
		p := ""
		var ev []truth.Evidence
		for _, fr := range stack {
			if fr.prefix != "" {
				p = joinPath(p, fr.prefix, false)
				ev = append(ev, evidence(f, fr.line))
			}
		}
		return p, ev
	}
	for i := 0; i < len(f.Lines); i++ {
		for len(stack) > 0 && depth < stack[len(stack)-1].depth {
			stack = stack[:len(stack)-1]
		}
		first := f.Lines[i]
		m := maskedCode(first.Code, false)
		t := strings.TrimSpace(m)
		if !strings.HasPrefix(t, "Route::") && !strings.HasPrefix(t, "$router->") {
			depth += phpCountDelta(m, "{", "}")
			continue
		}
		var real, mk strings.Builder
		opener := false
		for start := i; i < len(f.Lines) && i-start < 60; i++ {
			c := f.Lines[i].Code
			real.WriteString(c + "\n")
			mm := maskedCode(c, false)
			mk.WriteString(mm + "\n")
			depth += phpCountDelta(mm, "{", "}")
			tr := strings.TrimSpace(mk.String())
			if strings.Contains(tr, "group(") && strings.HasSuffix(tr, "{") {
				opener = true
				break
			}
			if phpCountDelta(tr, "(", ")") <= 0 {
				if i+1 < len(f.Lines) && strings.HasPrefix(strings.TrimSpace(maskedCode(f.Lines[i+1].Code, false)), "->") {
					continue
				}
				break
			}
		}
		text, masked := strings.TrimSpace(real.String()), strings.TrimSpace(mk.String())
		calls := phpChain(text, masked)
		if len(calls) == 0 {
			continue
		}
		if opener {
			stack = append(stack, frame{phpGroupPrefix(calls), depth, first})
			continue
		}
		base, derived := stackPrefix()
		if p := phpGroupPrefix(calls); p != "" {
			base = joinPath(base, p, false)
			derived = append(derived, evidence(f, first))
		}
		emit := func(method, route string) {
			out = append(out, endpointFact("laravel.routes", f, first, method, joinPath(base, route, false), derived...))
		}
		for k, c := range calls {
			switch {
			case phpVerbs[c.name]:
				if m := phpFirstStr.FindStringSubmatch(c.args); m != nil {
					emit(c.name, m[1])
				}
			case c.name == "view":
				if m := phpFirstStr.FindStringSubmatch(c.args); m != nil {
					emit("get", m[1])
				}
			case c.name == "match":
				verbs := phpArrayHead(c.args)
				rest := c.args[len(verbs):]
				if m := phpFirstStr.FindStringSubmatch(strings.TrimPrefix(strings.TrimSpace(rest), ",")); m != nil {
					for _, v := range phpAllStr.FindAllStringSubmatch(verbs, -1) {
						if phpVerbs[strings.ToLower(v[1])] {
							emit(strings.ToLower(v[1]), m[1])
						}
					}
				}
			case c.name == "resource" || c.name == "apiResource":
				if m := phpFirstStr.FindStringSubmatch(c.args); m != nil {
					phpEmitResource(emit, m[1], c.name == "apiResource", calls[k+1:])
				}
			case c.name == "resources" || c.name == "apiResources":
				for _, m := range phpResKey.FindAllStringSubmatch(c.args, -1) {
					phpEmitResource(emit, m[1], c.name == "apiResources", nil)
				}
			}
		}
	}
	return out
}

// phpArrayHead returns the leading [...] of s (or "").
func phpArrayHead(s string) string {
	s = strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(s, "[") {
		return ""
	}
	if j := strings.Index(s, "]"); j >= 0 {
		return s[:j+1]
	}
	return ""
}

func phpEmitResource(emit func(method, route string), name string, api bool, mods []phpCall) {
	segs := strings.Split(name, ".")
	path := ""
	for k, s := range segs {
		path += "/" + s
		if k < len(segs)-1 {
			path += "/{" + strings.TrimSuffix(s, "s") + "}"
		}
	}
	for _, a := range phpResourceActions(name, api, mods) {
		switch a {
		case "index":
			emit("get", path)
		case "create":
			emit("get", path+"/create")
		case "store":
			emit("post", path)
		case "show":
			emit("get", path+"/{id}")
		case "edit":
			emit("get", path+"/{id}/edit")
		case "update":
			emit("put", path+"/{id}")
			emit("patch", path+"/{id}")
		case "destroy":
			emit("delete", path+"/{id}")
		}
	}
}

// ---- Kafka -----------------------------------------------------------------

var (
	phpSubscribe  = regexp.MustCompile(`->subscribe\(\s*\[([^\]]*)\]`)
	phpNewTopic   = regexp.MustCompile(`->newTopic\(\s*['"]([^'"]+)['"]`)
	phpOnTopic    = regexp.MustCompile(`(?:->onTopic|::publishOn)\(\s*['"]([^'"]+)['"]`)
	phpCreateCons = regexp.MustCompile(`::createConsumer\(\s*(?:topics:\s*)?\[([^\]]*)\]`)
)

func applyPHPKafka(f *FileCtx) []truth.Fact {
	if isTestLike(f) || !codeContains(f, "kafka") {
		return nil
	}
	newTopicSide := "produce"
	if codeContains(f, "consumestart") && !codeContains(f, "->produce(") {
		newTopicSide = "consume"
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		text := codeWindow(f.Lines, i, 3)
		mask := litMask(text, false)
		each := func(re *regexp.Regexp, list bool, side string) {
			loc := re.FindStringSubmatchIndex(text)
			if loc == nil || loc[0] >= len(l.Code) || mask[loc[0]] {
				return
			}
			frag := text[loc[2]:loc[3]]
			names := []string{frag}
			if list {
				names = literalItems(frag)
			}
			for _, n := range names {
				if fc, ok := channelFact("php.kafka", f, l, "topic", n, side); ok {
					out = append(out, fc)
				}
			}
		}
		each(phpSubscribe, true, "consume")
		each(phpCreateCons, true, "consume")
		each(phpNewTopic, false, newTopicSide)
		each(phpOnTopic, false, "produce")
	}
	return out
}

// ---- HTTP ------------------------------------------------------------------

var (
	phpEnvURL   = regexp.MustCompile(`\b(?:env|getenv)\(\s*['"]([^'"]+)['"]\s*(?:,\s*|\)\s*(?:\?:|\?\?)\s*)['"](https?://[^'"]+)['"]`)
	phpSuperEnv = regexp.MustCompile(`\$_(?:ENV|SERVER)\[\s*['"]([^'"]+)['"]\s*\]\s*(?:\?\?|\?:)\s*['"](https?://[^'"]+)['"]`)
	phpCfgURL   = regexp.MustCompile(`['"]([A-Za-z0-9_.\-]+)['"]\s*=>\s*['"](https?://[^'"]+)['"]`)
	phpDirect   = regexp.MustCompile(`(?:\bHttp::\w+|->(?:get|post|put|patch|delete|head|request|baseUrl)|\bcurl_init|\bfile_get_contents)\(\s*(?:['"][A-Za-z]+['"]\s*,\s*)?['"](https?://[^'"]+)['"]`)
	phpClient   = []string{"guzzle", "http::", "curl_init", "file_get_contents", "symfony\\component\\httpclient", "httpclient"}
)

func applyPHPHTTP(f *FileCtx) []truth.Fact {
	if isTestLike(f) {
		return nil
	}
	client := codeContains(f, phpClient...)
	var out []truth.Fact
	for i, l := range f.Lines {
		win := codeWindow(f.Lines, i, 2)
		mask := litMask(win, false)
		add := func(url string) {
			if fc, ok := httpFact("php.http", f, l, url, ""); ok {
				out = append(out, fc)
			}
		}
		for _, m := range phpEnvURL.FindAllStringSubmatchIndex(win, -1) {
			if m[0] < len(l.Code) && !mask[m[0]] && envURLAllowed(client, win[m[2]:m[3]]) {
				add(win[m[4]:m[5]])
			}
		}
		for _, m := range phpSuperEnv.FindAllStringSubmatchIndex(win, -1) {
			if m[0] < len(l.Code) && !mask[m[0]] && envURLAllowed(client, win[m[2]:m[3]]) {
				add(win[m[4]:m[5]])
			}
		}
		for _, m := range phpCfgURL.FindAllStringSubmatchIndex(l.Code, -1) {
			key := l.Code[m[2]:m[3]]
			if !cfgURLKey.MatchString(key) || selfURLKey.MatchString(key) {
				continue
			}
			add(l.Code[m[4]:m[5]])
		}
		if client {
			for _, m := range phpDirect.FindAllStringSubmatchIndex(win, -1) {
				if m[0] < len(l.Code) && !mask[m[0]] {
					add(win[m[2]:m[3]])
				}
			}
		}
	}
	return out
}

// ---- composer.lock ---------------------------------------------------------

func applyLockComposer(f *FileCtx) []truth.Fact {
	var doc struct {
		Packages    []struct{ Name, Version string } `json:"packages"`
		PackagesDev []struct{ Name, Version string } `json:"packages-dev"`
	}
	if json.Unmarshal(f.Raw, &doc) != nil {
		return nil
	}
	s := newDepSet("lock.composer", f)
	// The lock lists the transitive closure; the direct dependencies are the
	// require / require-dev keys of the composer.json beside it.
	direct := map[string]bool{}
	if raw, ok := f.Tree.ReadFile(path.Join(f.Dir, "composer.json")); ok {
		var cj struct {
			Require    map[string]json.RawMessage `json:"require"`
			RequireDev map[string]json.RawMessage `json:"require-dev"`
		}
		if json.Unmarshal(raw, &cj) == nil {
			for n := range cj.Require {
				direct[strings.ToLower(n)] = true
			}
			for n := range cj.RequireDev {
				direct[strings.ToLower(n)] = true
			}
		}
	}
	find := func(name, section string) *lex.Line {
		from := 0
		for k, l := range f.Lines {
			if strings.Contains(l.Code, `"`+section+`"`) {
				from = k
				break
			}
		}
		re := regexp.MustCompile(`^\s*"name"\s*:\s*"` + regexp.QuoteMeta(name) + `"`)
		for k := from; k < len(f.Lines); k++ {
			if re.MatchString(f.Lines[k].Code) {
				return &f.Lines[k]
			}
		}
		return nil
	}
	add := func(section string, name, version string) {
		if len(direct) > 0 && !direct[strings.ToLower(name)] {
			return
		}
		if fp := s.add("composer", name, version, section); fp != nil {
			if l := find(name, section); l != nil {
				fp.Primary = evidence(f, *l)
			}
		}
	}
	for _, p := range doc.Packages {
		add("packages", p.Name, p.Version)
	}
	for _, p := range doc.PackagesDev {
		add("packages-dev", p.Name, p.Version)
	}
	return s.facts
}

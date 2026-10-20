package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Ruby: Rails routes, ruby-kafka / waterdrop channels, HTTP base URLs from
// ENV defaults and Gemfile.lock specs.

func init() {
	isRb := func(p string) bool { return hasExt(p, ".rb") }
	Rules = append(Rules,
		funcRule{"rails.routes", isRailsRoutesPath, applyRailsRoutes},
		funcRule{"ruby.kafka", isRb, applyRubyKafka},
		funcRule{"ruby.http", isRb, applyRubyHTTP},
		funcRule{"lock.gem", func(p string) bool { return baseName(p) == "Gemfile.lock" }, applyLockGem},
	)
}

func isRailsRoutesPath(p string) bool {
	if !hasExt(p, ".rb") {
		return false
	}
	return strings.HasSuffix(p, "config/routes.rb") || strings.Contains(p, "config/routes/")
}

// ---- Rails routes ----------------------------------------------------------

type rbFrame struct {
	kind string // plain, scope, res, member, collection, skip
	full string // path prefix for routes directly inside
	// resource frames
	coll, member string
	line         lex.Line
}

var (
	rbBlockOpen  = regexp.MustCompile(`\bdo\s*(\|[^|]*\|)?\s*$`)
	rbKeywordOpn = map[string]bool{"if": true, "unless": true, "case": true, "while": true, "until": true, "begin": true, "def": true, "class": true, "module": true, "for": true}
	rbFirstWord  = regexp.MustCompile(`^\s*([a-z_]\w*)\b`)
	rbFirstArg   = regexp.MustCompile(`^\s*\(?\s*(?::(\w+)|"([^"]*)"|'([^']*)')`)
	rbPathOpt    = regexp.MustCompile(`\bpath:\s*(?:"([^"]*)"|'([^']*)'|:(\w+))`)
	rbOnOpt      = regexp.MustCompile(`\bon:\s*:(\w+)`)
	rbViaOpt     = regexp.MustCompile(`\bvia:\s*(\[[^\]]*\]|:\w+)`)
	rbOnlyOpt    = regexp.MustCompile(`\b(only|except):\s*(\[[^\]]*\]|%[iwIW][\[({][^\])}]*[\])}]|:\w+)`)
	rbSymbol     = regexp.MustCompile(`:(\w+)|\b(\w+)\b`)
	rbOptional   = regexp.MustCompile(`\([^)]*\)`)
	rbVerbs      = map[string]string{"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH", "delete": "DELETE"}
)

// rbArgPath returns the first string or symbol argument of a routing call.
func rbArgPath(args string) (string, bool) {
	m := rbFirstArg.FindStringSubmatch(args)
	if m == nil {
		return "", false
	}
	for _, g := range m[1:] {
		if g != "" {
			return g, true
		}
	}
	return "", true
}

func rbOptPath(opts string) (string, bool) {
	m := rbPathOpt.FindStringSubmatch(opts)
	if m == nil {
		return "", false
	}
	for _, g := range m[1:] {
		if g != "" {
			return g, true
		}
	}
	return "", true
}

// rbActionSet applies only:/except: to the default action list.
func rbActionSet(opts string, all []string) []string {
	m := rbOnlyOpt.FindStringSubmatch(opts)
	if m == nil {
		return all
	}
	listed := map[string]bool{}
	body := m[2]
	if i := strings.IndexAny(body, "[({"); i >= 0 {
		body = body[i+1:]
	}
	for _, s := range rbSymbol.FindAllStringSubmatch(body, -1) {
		n := s[1]
		if n == "" {
			n = s[2]
		}
		listed[n] = true
	}
	var out []string
	for _, a := range all {
		if (m[1] == "only") == listed[a] {
			out = append(out, a)
		}
	}
	return out
}

func rbClean(seg string) string {
	return strings.Trim(rbOptional.ReplaceAllString(seg, ""), "/ ")
}

func applyRailsRoutes(f *FileCtx) []truth.Fact {
	if isTestLike(f) {
		return nil
	}
	var out []truth.Fact
	var stack []rbFrame
	cur := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1].full
	}
	skipping := func() bool {
		for _, fr := range stack {
			if fr.kind == "skip" {
				return true
			}
		}
		return false
	}
	derived := func() []truth.Evidence {
		var ev []truth.Evidence
		for _, fr := range stack {
			if fr.kind == "scope" || fr.kind == "res" {
				ev = append(ev, evidence(f, fr.line))
			}
		}
		return ev
	}
	nearestRes := func() *rbFrame {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].kind == "res" {
				return &stack[i]
			}
		}
		return nil
	}
	emit := func(l lex.Line, method, path string) {
		if skipping() {
			return
		}
		out = append(out, endpointFact("rails.routes", f, l, method, path, derived()...))
	}

	for i := 0; i < len(f.Lines); i++ {
		first := f.Lines[i]
		stmt := strings.TrimSpace(first.Code)
		if stmt == "" {
			continue
		}
		// continuation: trailing comma, backslash or an unclosed bracket
		for n := 0; n < 8 && i+1 < len(f.Lines) && rbContinues(stmt); n++ {
			i++
			stmt += " " + strings.TrimSpace(f.Lines[i].Code)
		}
		wm := rbFirstWord.FindStringSubmatch(stmt)
		if stmt == "end" || strings.HasPrefix(stmt, "end ") || strings.HasPrefix(stmt, "end.") {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		opener := rbBlockOpen.MatchString(stmt)
		if wm == nil {
			if opener {
				stack = append(stack, rbFrame{kind: "plain", full: cur()})
			}
			continue
		}
		word := wm[1]
		args := stmt[len(wm[0]):]
		push := func(fr rbFrame) {
			if opener {
				stack = append(stack, fr)
			}
		}
		switch {
		case rbKeywordOpn[word] && !opener:
			stack = append(stack, rbFrame{kind: "plain", full: cur()})
		case word == "namespace" || word == "scope":
			seg, _ := rbArgPath(args)
			if p, ok := rbOptPath(args); ok {
				seg = p
			}
			fr := rbFrame{kind: "scope", full: cur(), line: first}
			if seg = rbClean(seg); seg != "" {
				fr.full = joinPath(cur(), seg, false)
			}
			push(fr)
		case word == "resources" || word == "resource":
			if skipping() {
				push(rbFrame{kind: "skip", full: cur()})
				break
			}
			names, opts := rbResourceNames(args)
			if len(names) == 0 {
				push(rbFrame{kind: "plain", full: cur()})
				break
			}
			var first1 rbFrame
			for k, name := range names {
				seg := name
				if p, ok := rbOptPath(opts); ok && p != "" {
					seg = p
				}
				base := joinPath(cur(), rbClean(seg), false)
				plural := word == "resources"
				acts := []string{"index", "create", "new", "show", "edit", "update", "destroy"}
				if !plural {
					acts = []string{"create", "new", "show", "edit", "update", "destroy"}
				}
				idp := base
				if plural {
					idp = base + "/:id"
				}
				for _, a := range rbActionSet(opts, acts) {
					switch a {
					case "index":
						emit(first, "GET", base)
					case "create":
						emit(first, "POST", base)
					case "new":
						emit(first, "GET", base+"/new")
					case "show":
						emit(first, "GET", idp)
					case "edit":
						emit(first, "GET", idp+"/edit")
					case "update":
						emit(first, "PATCH", idp)
						emit(first, "PUT", idp)
					case "destroy":
						emit(first, "DELETE", idp)
					}
				}
				if k == 0 {
					nested := base
					if plural {
						nested = base + "/:" + strings.TrimSuffix(name, "s") + "_id"
					}
					first1 = rbFrame{kind: "res", full: nested, coll: base, member: idp, line: first}
				}
			}
			push(first1)
		case word == "member" || word == "collection":
			fr := rbFrame{kind: word, full: cur(), line: first}
			if r := nearestRes(); r != nil {
				fr.full = r.coll
				if word == "member" {
					fr.full = r.member
				}
			}
			push(fr)
		case word == "concern" || word == "concerns_for":
			push(rbFrame{kind: "skip", full: cur()})
		case rbVerbs[word] != "" || word == "match":
			seg, ok := rbArgPath(args)
			if !ok || seg == "" {
				if opener {
					stack = append(stack, rbFrame{kind: "plain", full: cur()})
				}
				break
			}
			var methods []string
			if word == "match" {
				if vm := rbViaOpt.FindStringSubmatch(args); vm != nil {
					for _, s := range rbSymbol.FindAllStringSubmatch(strings.Trim(vm[1], "[]"), -1) {
						n := strings.ToUpper(strings.TrimPrefix(s[0], ":"))
						if _, isVerb := rbVerbs[strings.ToLower(n)]; isVerb {
							methods = append(methods, n)
						}
					}
				}
			} else {
				methods = []string{rbVerbs[word]}
			}
			base := cur()
			if on := rbOnOpt.FindStringSubmatch(args); on != nil {
				if r := nearestRes(); r != nil {
					base = r.coll
					if on[1] == "member" {
						base = r.member
					}
				}
			}
			path := joinPath(base, rbClean(seg), false)
			for _, m := range methods {
				emit(first, m, path)
			}
			if opener {
				stack = append(stack, rbFrame{kind: "plain", full: cur()})
			}
		case word == "root":
			emit(first, "GET", joinPath(cur(), "", false))
		default:
			if opener {
				stack = append(stack, rbFrame{kind: "plain", full: cur()})
			}
		}
	}
	return out
}

func rbContinues(s string) bool {
	if strings.HasSuffix(s, ",") || strings.HasSuffix(s, "\\") {
		return true
	}
	return strings.Count(s, "[") > strings.Count(s, "]") || strings.Count(s, "(") > strings.Count(s, ")")
}

// rbResourceNames reads the leading symbol/string names of resources and
// returns the rest of the argument text as options.
func rbResourceNames(args string) (names []string, opts string) {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "("))
	for {
		m := rbFirstArg.FindStringSubmatch(rest)
		if m == nil {
			break
		}
		n := m[1] + m[2] + m[3]
		after := strings.TrimSpace(rest[len(m[0]):])
		if strings.HasPrefix(after, "=>") {
			break
		}
		names = append(names, n)
		rest = after
		if !strings.HasPrefix(rest, ",") {
			break
		}
		rest = strings.TrimSpace(rest[1:])
	}
	return names, rest
}

// ---- Kafka -----------------------------------------------------------------

var (
	rbSendCall = regexp.MustCompile(`\b(?:deliver_message|deliver_messages|produce|produce_sync|produce_async|produce_many_sync|produce_many_async)\b`)
	rbTopicOpt = regexp.MustCompile(`(?:\btopic:|:topic\s*=>|["']topic["']\s*=>)\s*["']([^"']+)["']`)
)

func isRubyKafkaFile(f *FileCtx) bool {
	return codeContains(f, "kafka", "waterdrop", "karafka")
}

func applyRubyKafka(f *FileCtx) []truth.Fact {
	if isTestLike(f) || !isRubyKafkaFile(f) {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		masked := maskedCode(l.Code, false)
		if loc := rbSendCall.FindStringIndex(masked); loc != nil {
			text := callText(f.Lines, i, 6)
			mt := maskedCode(text, false)
			// the call name must be outside literals; so must the topic: key
			if loc2 := rbTopicOpt.FindStringSubmatchIndex(text); loc2 != nil && mt[loc2[0]] != ' ' {
				if fc, ok := channelFact("ruby.kafka", f, l, "topic", text[loc2[2]:loc2[3]], "produce"); ok {
					out = append(out, fc)
				}
			}
		}
		if j := strings.Index(masked, ".subscribe"); j >= 0 {
			rest := callText(f.Lines, i, 4)[j+len(".subscribe"):]
			rest = strings.TrimPrefix(strings.TrimSpace(rest), "(")
			for _, n := range rbLeadingLiterals(rest) {
				if fc, ok := channelFact("ruby.kafka", f, l, "topic", n, "consume"); ok {
					out = append(out, fc)
				}
			}
		}
	}
	return out
}

// rbLeadingLiterals returns the leading plain string literals of an argument
// list ("a", 'b', key: ...); it stops at the first non-literal argument.
func rbLeadingLiterals(s string) []string {
	var out []string
	s = strings.TrimSpace(s)
	for len(s) > 1 && (s[0] == '"' || s[0] == '\'') {
		j := strings.IndexByte(s[1:], s[0])
		if j < 0 {
			break
		}
		lit := s[1 : j+1]
		rest := strings.TrimSpace(s[j+2:])
		if rest != "" && rest[0] != ',' && rest[0] != ')' {
			break // "a" + x, "a" if y ...
		}
		out = append(out, lit)
		if rest == "" || rest[0] == ')' {
			break
		}
		s = strings.TrimSpace(rest[1:])
	}
	return out
}

// ---- HTTP ------------------------------------------------------------------

var (
	rbEnvFetch = regexp.MustCompile(`\bENV\.fetch\(\s*["']([^"']+)["']\s*(?:,\s*["'](https?://[^"']+)["']|\)\s*\{\s*["'](https?://[^"']+)["']\s*\})`)
	rbEnvOr    = regexp.MustCompile(`\bENV\[\s*["']([^"']+)["']\s*\]\s*\|\|\s*["'](https?://[^"']+)["']`)
	rbDirect   = regexp.MustCompile(`(?:\b(?:Faraday(?:\.new)?|HTTParty(?:\.\w+)?|RestClient(?:\.\w+)?|Typhoeus(?:\.\w+)?|HTTP(?:\.\w+)?|Net::HTTP(?:\.\w+)?|URI(?:\.parse)?|Excon(?:\.\w+)?)\s*\(?\s*|\bbase_uri\s+|\burl:\s*)["'](https?://[^"']+)["']`)
	rbClient   = []string{"faraday", "net::http", "httparty", "restclient", "typhoeus", "excon", "httpx", "http.rb", "open-uri", "open_uri"}
	selfURLKey = regexp.MustCompile(`(?i)^(app|application|site|asset|assets|cdn|frontend|public|self|mail|mailer|redirect|callback)(_|$)`)
)

// envURLAllowed decides whether an ENV default URL is an outbound target: the
// file uses an HTTP client, or the variable name itself reads like a service
// URL (CATALOG_API_URL) and not like the app's own address.
func envURLAllowed(clientInFile bool, key string) bool {
	if clientInFile {
		return true
	}
	return cfgURLKey.MatchString(key) && !selfURLKey.MatchString(key)
}

func applyRubyHTTP(f *FileCtx) []truth.Fact {
	if isTestLike(f) {
		return nil
	}
	client := codeContains(f, rbClient...)
	var out []truth.Fact
	for i, l := range f.Lines {
		mask := litMask(l.Code, false)
		win := l.Code
		if i+1 < len(f.Lines) {
			win += " " + f.Lines[i+1].Code
		}
		wmask := litMask(win, false)
		add := func(url string) {
			if fc, ok := httpFact("ruby.http", f, l, url, ""); ok {
				out = append(out, fc)
			}
		}
		for _, m := range rbEnvFetch.FindAllStringSubmatchIndex(win, -1) {
			if m[0] >= len(l.Code) || wmask[m[0]] {
				continue
			}
			key := win[m[2]:m[3]]
			url := ""
			if m[4] >= 0 {
				url = win[m[4]:m[5]]
			} else if m[6] >= 0 {
				url = win[m[6]:m[7]]
			}
			if envURLAllowed(client, key) {
				add(url)
			}
		}
		for _, m := range rbEnvOr.FindAllStringSubmatchIndex(l.Code, -1) {
			if !mask[m[0]] && envURLAllowed(client, l.Code[m[2]:m[3]]) {
				add(l.Code[m[4]:m[5]])
			}
		}
		for _, m := range rbDirect.FindAllStringSubmatchIndex(win, -1) {
			if m[0] >= len(l.Code) || wmask[m[0]] {
				continue
			}
			add(win[m[2]:m[3]])
		}
	}
	return out
}

// ---- Gemfile.lock ----------------------------------------------------------

func applyLockGem(f *FileCtx) []truth.Fact {
	s := newDepSet("lock.gem", f)
	// DEPENDENCIES lists what the Gemfile asks for; specs is the whole closure.
	inDeps := false
	for _, l := range f.Lines {
		raw := strings.TrimRight(l.Raw, "\r")
		switch {
		case raw == "DEPENDENCIES":
			inDeps = true
			continue
		case strings.TrimSpace(raw) == "" || !strings.HasPrefix(raw, " "):
			inDeps = false
			continue
		}
		if !inDeps {
			continue
		}
		if m := gemDirect.FindStringSubmatch(raw); m != nil {
			if fp := s.add("gem", m[1], m[2], "dependencies"); fp != nil {
				fp.Primary = evidence(f, l)
			}
		}
	}
	return s.facts
}

var gemDirect = regexp.MustCompile(`^  ([A-Za-z0-9_.\-]+)!?(?: \(([^)]*)\))?\s*$`)

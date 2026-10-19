package extract

import (
	"regexp"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// Ktor server routing: routing { get("/x") { } route("/p") { post { } } }.
// Prefixes come from nested route("...") blocks; a path or prefix that is not
// a plain string literal makes everything under it unknown (no fact).

func init() {
	Rules = append(Rules,
		funcRule{"ktor.routing", func(p string) bool { return hasExt(p, ".kt") }, applyKtorRouting},
	)
}

var (
	ktorRoutingImport = regexp.MustCompile(`\bio\.ktor\.(?:server\.)?(?:routing|locations)\b`)
	ktPlainLit        = regexp.MustCompile(`^"([^"\\]*)"$`)
	ktNamedPath       = regexp.MustCompile(`^(?:path|route)\s*=\s*`)
	ktHTTPMethodArg   = regexp.MustCompile(`^(?:method\s*=\s*)?(?:io\.ktor\.http\.)?HttpMethod\.(\w+)$`)
)

var ktVerbs = map[string]bool{"get": true, "post": true, "put": true, "delete": true, "patch": true, "head": true, "options": true}

type ktFrame struct {
	prefix  string
	unknown bool
	ev      []truth.Evidence
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// ktSkipString returns the index after the string literal starting at t[i].
func ktSkipString(t string, i int) int {
	if strings.HasPrefix(t[i:], `"""`) {
		j := strings.Index(t[i+3:], `"""`)
		if j < 0 {
			return len(t)
		}
		e := i + 3 + j + 3
		for e < len(t) && t[e] == '"' {
			e++
		}
		return e
	}
	i++
	for i < len(t) {
		switch t[i] {
		case '\\':
			i += 2
		case '"', '\n':
			return i + 1
		case '$':
			if i+1 < len(t) && t[i+1] == '{' {
				i = ktSkipTemplate(t, i+2)
			} else {
				i++
			}
		default:
			i++
		}
	}
	return len(t)
}

// ktSkipTemplate skips a "${ ... }" body; i points after the "{".
func ktSkipTemplate(t string, i int) int {
	depth := 1
	for i < len(t) && depth > 0 {
		switch t[i] {
		case '"':
			i = ktSkipString(t, i)
			continue
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	return i
}

// ktSkipChar skips a char literal at t[i]=='\'' (or just the quote).
func ktSkipChar(t string, i int) int {
	if i+1 < len(t) && t[i+1] == '\\' {
		if j := strings.IndexByte(t[i+2:], '\''); j >= 0 && j < 8 {
			return i + 2 + j + 1
		}
		return i + 1
	}
	if i+2 < len(t) && t[i+2] == '\'' {
		return i + 3
	}
	return i + 1
}

// ktMatchParen returns the index of the ")" closing the "(" at t[i], or -1.
func ktMatchParen(t string, i int) int {
	depth := 0
	for i < len(t) {
		switch t[i] {
		case '"':
			i = ktSkipString(t, i)
			continue
		case '\'':
			i = ktSkipChar(t, i)
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// ktSplitArgs splits an argument list at top-level commas.
func ktSplitArgs(a string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(a); {
		switch a[i] {
		case '"':
			i = ktSkipString(a, i)
			continue
		case '\'':
			i = ktSkipChar(a, i)
			continue
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(a[start:i]))
				start = i + 1
			}
		}
		i++
	}
	if s := strings.TrimSpace(a[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// ktPrevIsMember reports whether the identifier at t[i:] is a member access
// (x.get, x?.get, ::get) or a declaration (fun get).
func ktPrevIsMember(t string, i int) bool {
	j := i - 1
	for j >= 0 && (t[j] == ' ' || t[j] == '\t' || t[j] == '\n' || t[j] == '\r') {
		j--
	}
	if j < 0 {
		return false
	}
	if t[j] == '.' || (t[j] == ':' && j > 0 && t[j-1] == ':') {
		return true
	}
	if j >= 2 && t[j-2:j+1] == "fun" && (j < 3 || !isIdentPart(t[j-3])) {
		return true
	}
	return false
}

func applyKtorRouting(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	imp := false
	for _, l := range f.Lines {
		if ktorRoutingImport.MatchString(l.Code) {
			imp = true
			break
		}
	}
	if !imp {
		return nil
	}
	var sb strings.Builder
	starts := make([]int, len(f.Lines))
	for i, l := range f.Lines {
		starts[i] = sb.Len()
		sb.WriteString(l.Code)
		sb.WriteByte('\n')
	}
	t := sb.String()
	lineAtOff := func(off int) int {
		return sort.Search(len(starts), func(i int) bool { return starts[i] > off }) - 1
	}
	skipWS := func(i int) int {
		for i < len(t) && (t[i] == ' ' || t[i] == '\t' || t[i] == '\n' || t[i] == '\r') {
			i++
		}
		return i
	}

	stack := []ktFrame{{}}
	pending := map[int]ktFrame{}
	var out []truth.Fact
	emit := func(off int, method string, fr ktFrame, lit string, hasLit bool) {
		if fr.unknown {
			return
		}
		p := fr.prefix
		if hasLit {
			p = joinPath(fr.prefix, lit, false)
		} else if p == "" {
			p = "/"
		}
		out = append(out, endpointFact("ktor.routing", f, f.Lines[lineAtOff(off)], method, p, fr.ev...))
	}

	for i := 0; i < len(t); {
		c := t[i]
		switch {
		case c == '"':
			i = ktSkipString(t, i)
		case c == '\'':
			i = ktSkipChar(t, i)
		case c == '{':
			fr := stack[len(stack)-1]
			if p, ok := pending[i]; ok {
				fr = p
			}
			stack = append(stack, fr)
			i++
		case c == '}':
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			i++
		case isIdentStart(c) && (i == 0 || !isIdentPart(t[i-1])):
			j := i
			for j < len(t) && isIdentPart(t[j]) {
				j++
			}
			id := t[i:j]
			isVerb := ktVerbs[id]
			if !(isVerb || id == "route" || id == "method") || ktPrevIsMember(t, i) {
				i = j
				break
			}
			top := stack[len(stack)-1]
			k := skipWS(j)
			if k >= len(t) {
				i = j
				break
			}
			if t[k] == '{' && isVerb { // get { ... }: the route is the prefix itself
				emit(i, strings.ToUpper(id), top, "", false)
				i = j
				break
			}
			if t[k] != '(' {
				i = j
				break
			}
			m := ktMatchParen(t, k)
			if m < 0 {
				i = j
				break
			}
			args := ktSplitArgs(t[k+1 : m])
			brace := skipWS(m + 1)
			hasBrace := brace < len(t) && t[brace] == '{'
			lit, hasLit, bad := "", false, false
			method := ""
			for n, a := range args {
				if mm := ktHTTPMethodArg.FindStringSubmatch(a); mm != nil {
					method = strings.ToUpper(mm[1])
					continue
				}
				if n != 0 {
					continue
				}
				a = ktNamedPath.ReplaceAllString(a, "")
				if mm := ktPlainLit.FindStringSubmatch(a); mm != nil && !strings.Contains(mm[1], "$") {
					lit, hasLit = mm[1], true
				} else {
					bad = true // constant, template or expression
				}
			}
			switch id {
			case "route":
				fr := ktFrame{prefix: top.prefix, unknown: top.unknown || bad || !hasLit, ev: top.ev}
				if hasLit && !fr.unknown {
					fr.prefix = joinPath(top.prefix, lit, false)
					fr.ev = append(append([]truth.Evidence(nil), top.ev...), evidence(f, f.Lines[lineAtOff(i)]))
				}
				if hasBrace {
					pending[brace] = fr
					if method != "" {
						emit(i, method, ktFrame{prefix: fr.prefix, unknown: fr.unknown, ev: top.ev}, "", false)
					}
				}
			case "method":
				if method != "" && hasBrace {
					emit(i, method, top, "", false)
				}
			default: // verb
				if bad || (hasLit && !hasBrace && !strings.HasPrefix(lit, "/")) {
					break
				}
				if !hasLit && !hasBrace {
					break
				}
				emit(i, strings.ToUpper(id), top, lit, hasLit)
			}
			i = m + 1
		default:
			i++
		}
	}
	return out
}

package extract

import (
	"regexp"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Channel names that are not literals at the call but come from configuration:
// appsettings.json keys (.NET), process.env defaults (TypeScript), os.getenv
// defaults (Python), Spring placeholders (application.yml / .properties).
// The call line is the primary evidence, the configuration line derived. A
// name that cannot be resolved yields no fact.

// codeText is the comment-free code of a file as one string, with a mask of
// the bytes inside quoted literals and the offset where each line starts.
type codeText struct {
	text   string
	mask   []bool
	starts []int
}

func newCodeText(lines []lex.Line) *codeText {
	var sb strings.Builder
	var mask []bool
	ct := &codeText{}
	for _, l := range lines {
		ct.starts = append(ct.starts, sb.Len())
		sb.WriteString(l.Code)
		sb.WriteByte('\n')
		mask = append(mask, litMask(l.Code, false)...)
		mask = append(mask, false)
	}
	ct.text, ct.mask = sb.String(), mask
	return ct
}

// lineIdx returns the index of the line holding offset off.
func (c *codeText) lineIdx(off int) int {
	i := sort.Search(len(c.starts), func(i int) bool { return c.starts[i] > off })
	if i == 0 {
		return 0
	}
	return i - 1
}

type span struct{ s, e int }

// splitArgs splits text from `from` (just after an opening bracket) at
// depth-0 commas until the matching closer. Spans are trimmed.
func splitArgs(text string, from int) []span {
	var out []span
	depth, start := 0, from
	var quote byte
	limit := from + 3000
	if limit > len(text) {
		limit = len(text)
	}
	push := func(e int) {
		s := start
		for s < e && (text[s] == ' ' || text[s] == '\t' || text[s] == '\n' || text[s] == '\r') {
			s++
		}
		for e > s && (text[e-1] == ' ' || text[e-1] == '\t' || text[e-1] == '\n' || text[e-1] == '\r') {
			e--
		}
		if e > s {
			out = append(out, span{s, e})
		}
	}
	for i := from; i < limit; i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth == 0 {
				push(i)
				return out
			}
			depth--
		case c == ',' && depth == 0:
			push(i)
			start = i + 1
		}
	}
	push(limit)
	return out
}

// topColon returns the index of the first depth-0 ':' of text[s:e], or -1.
func topColon(text string, s, e int) int {
	depth := 0
	var quote byte
	for i := s; i < e; i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ':' && depth == 0:
			return i
		}
	}
	return -1
}

func trimSpan(text string, s, e int) span {
	for s < e && strings.ContainsRune(" \t\r\n", rune(text[s])) {
		s++
	}
	for e > s && strings.ContainsRune(" \t\r\n", rune(text[e-1])) {
		e--
	}
	return span{s, e}
}

var containerHead = regexp.MustCompile(`^(?:new\s+[\w.<>\[\], ?]*?|listOf|arrayOf|setOf|List\.of|Set\.of|Arrays\.asList)\s*[\[({]`)

// itemSpans expands an expression that may hold several names: `[a, b]`,
// `{a, b}`, `new[] { a }`, `listOf(a)`. Anything else is one item.
func itemSpans(text string, sp span) []span {
	t := text[sp.s:sp.e]
	open := -1
	if t[0] == '[' || t[0] == '{' {
		open = sp.s
	} else if m := containerHead.FindStringIndex(t); m != nil {
		open = sp.s + m[1] - 1
	}
	if open < 0 {
		return []span{sp}
	}
	return splitArgs(text, open+1)
}

// cfgCall is one call shape whose channel argument is resolved from config.
type cfgCall struct {
	re     *regexp.Regexp // ends right after the opening bracket; group 1 is an optional receiver
	kind   string
	side   string
	sideFn func(prefix string) string
	kws    []string // keyword / object-key names that carry the channel
	pos    int      // positional index of the channel argument
	obj    bool     // the first argument is an object literal holding kws
	all    bool     // every positional argument is a channel
	gate   func(f *FileCtx, recv string) bool
	annot  bool // an annotation: arguments are `name = value` pairs
}

var kwArg = regexp.MustCompile(`^(\w+)\s*=[^=]`)

// channelExprs picks the argument expressions of one call match.
func (c cfgCall) channelExprs(text string, open int) []span {
	args := splitArgs(text, open)
	if c.obj {
		if len(args) == 0 || text[args[0].s] != '{' {
			return nil
		}
		var out []span
		for _, a := range splitArgs(text, args[0].s+1) {
			key, val := "", a
			if i := topColon(text, a.s, a.e); i >= 0 {
				key = strings.Trim(strings.TrimSpace(text[a.s:i]), `'"`)
				val = trimSpan(text, i+1, a.e)
			} else {
				key = text[a.s:a.e] // shorthand { topic }
			}
			for _, k := range c.kws {
				if key == k && val.e > val.s {
					out = append(out, itemSpans(text, val)...)
				}
			}
		}
		return out
	}
	var out []span
	pos := 0
	for _, a := range args {
		t := text[a.s:a.e]
		if m := kwArg.FindStringSubmatchIndex(t); m != nil {
			name := t[m[2]:m[3]]
			for _, k := range c.kws {
				if name == k {
					out = append(out, itemSpans(text, trimSpan(text, a.s+strings.Index(t, "=")+1, a.e))...)
				}
			}
			continue
		}
		if c.all || pos == c.pos {
			out = append(out, itemSpans(text, a)...)
		}
		pos++
	}
	return out
}

type cfgVal struct {
	val string
	ev  *truth.Evidence
}

// cfgResolver turns one channel expression into a name.
type cfgResolver interface {
	resolve(expr string) (cfgVal, bool)
}

func cfgChannelRule(name string, pre func(f *FileCtx) bool, calls []cfgCall, newRes func(f *FileCtx, ct *codeText) cfgResolver) func(f *FileCtx) []truth.Fact {
	return func(f *FileCtx) []truth.Fact {
		if f.IsTest || (pre != nil && !pre(f)) {
			return nil
		}
		ct := newCodeText(f.Lines)
		var res cfgResolver
		var out []truth.Fact
		seen := map[string]bool{}
		for _, c := range calls {
			for _, m := range c.re.FindAllStringSubmatchIndex(ct.text, -1) {
				if ct.mask[m[0]] {
					continue
				}
				recv := ""
				if len(m) >= 4 && m[2] >= 0 {
					recv = ct.text[m[2]:m[3]]
				}
				if c.gate != nil && !c.gate(f, recv) {
					continue
				}
				side := c.side
				if c.sideFn != nil {
					lo := m[0] - 300
					if lo < 0 {
						lo = 0
					}
					side = c.sideFn(ct.text[lo:m[0]])
				}
				for _, sp := range c.channelExprs(ct.text, m[1]) {
					if res == nil {
						res = newRes(f, ct)
					}
					v, ok := res.resolve(ct.text[sp.s:sp.e])
					if !ok {
						continue
					}
					li := ct.lineIdx(sp.s)
					var derived []truth.Evidence
					if v.ev != nil && !(v.ev.File == f.Path && v.ev.Line == f.Lines[li].Num) {
						derived = append(derived, *v.ev)
					}
					fc, ok := channelFact(name, f, f.Lines[li], c.kind, v.val, side, derived...)
					if !ok || seen[fc.Key+"@"+f.Lines[li].Raw] {
						continue
					}
					seen[fc.Key+"@"+f.Lines[li].Raw] = true
					out = append(out, fc)
				}
			}
		}
		return out
	}
}

// distinctVal returns the common value of the resolved candidates; differing
// values are ambiguous and yield nothing.
func distinctVal(vs []cfgVal) (cfgVal, bool) {
	if len(vs) == 0 {
		return cfgVal{}, false
	}
	for _, v := range vs[1:] {
		if v.val != vs[0].val {
			return cfgVal{}, false
		}
	}
	return vs[0], true
}

// splitCfgTop splits s at the top-level occurrences of any separator.
func splitCfgTop(s string, seps ...string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case depth == 0:
			for _, sep := range seps {
				if strings.HasPrefix(s[i:], sep) {
					out = append(out, s[start:i])
					i += len(sep) - 1
					start = i + 1
					break
				}
			}
		}
	}
	return append(out, s[start:])
}

// quotedLit returns the content of s when it is exactly one plain quoted
// literal ('x', "x" or `x` without interpolation).
func quotedLit(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || (s[0] != '\'' && s[0] != '"' && s[0] != '`') || s[len(s)-1] != s[0] {
		return "", false
	}
	body := s[1 : len(s)-1]
	if strings.ContainsRune(body, rune(s[0])) || strings.Contains(body, "${") {
		return "", false
	}
	return body, true
}

// evAt builds derived evidence for line num of file g.
func evAt(g *FileCtx, num int) *truth.Evidence {
	if num < 1 || num > len(g.Lines) {
		return nil
	}
	e := evidence(g, g.Lines[num-1])
	return &e
}

// loadFileCtx lexes another file of the tree; test files are refused.
func loadFileCtx(f *FileCtx, p string) *FileCtx {
	if IsTestPath(p) {
		return nil
	}
	raw, ok := f.Tree.ReadFile(p)
	if !ok {
		return nil
	}
	return &FileCtx{Path: p, Dir: pathDir(p), Lines: lex.Scan(raw, lex.FamilyFor(p)).Lines, Raw: raw, Tree: f.Tree}
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

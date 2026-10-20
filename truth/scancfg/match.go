package scancfg

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// PerFileBudget bounds the time spent matching one file.
const PerFileBudget = 100 * time.Millisecond

// Resolver turns a non-literal channel expression (a constant, a config read)
// into the name it holds. via names where the value came from.
type Resolver func(file, expr string) (name, via string, ok bool)

// Compiled is a template turned into a regexp.
type Compiled struct {
	re     *regexp.Regexp
	named  bool // {name}
	hasID  bool // {id}: the identifier a definition binds
	hasRef bool // {ref}: an identifier looked up in the definitions
}

var litToken = regexp.MustCompile(`[\w$@]+(?:\.[\w$@]+)*|\S`)

func wordTok(t string) bool {
	c := t[0]
	return c == '_' || c == '$' || c == '@' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

const (
	strLit  = `(?:"(?P<s1>[^"\\\n]*)"|'(?P<s2>[^'\\\n]*)'|` + "`(?P<s3>[^`\\n]*)`" + `)`
	exprLit = `(?P<e>[A-Za-z_@$][\w.$]*(?:\(\))?)`
	identRe = `(?P<%s>[A-Za-z_]\w*)`
	anyExpr = `(?:[^,()]|\((?:[^()]|\([^()]*\))*\))*?`
)

// compileTemplate: literal text matches literally with whitespace optional
// around punctuation; {name} captures a string literal or an identifier
// expression, {any} skips an expression, {Word} (capitalised) captures a type
// name, {addr} captures like {name}.
func compileTemplate(tpl string) (*Compiled, error) {
	tpl = strings.TrimSpace(tpl)
	if tpl == "" {
		return nil, fmt.Errorf("call template is empty")
	}
	var b strings.Builder
	named, hasID, hasRef := false, false, false
	last := 0
	locs := placeholder.FindAllStringSubmatchIndex(tpl, -1)
	lit := func(s string) {
		// Whitespace is optional between every pair of tokens except two words,
		// which stay apart ("new Client"): `{ baseURL:` and `{baseURL:` are one thing.
		idx := litToken.FindAllStringIndex(s, -1)
		for i, loc := range idx {
			tk := s[loc[0]:loc[1]]
			b.WriteString(regexp.QuoteMeta(tk))
			if i+1 < len(idx) && wordTok(tk) && wordTok(s[idx[i+1][0]:idx[i+1][1]]) && idx[i+1][0] > loc[1] {
				b.WriteString(`\s+`)
			} else {
				b.WriteString(`\s*`)
			}
		}
		if len(idx) == 0 {
			b.WriteString(`\s*`)
		}
	}
	if first := tpl[0]; first == '_' || first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z' {
		b.WriteString(`\b`)
	}
	for _, m := range locs {
		lit(tpl[last:m[0]])
		w := tpl[m[2]:m[3]]
		switch {
		case w == "name" || w == "addr":
			if named {
				return nil, fmt.Errorf("template %q uses {%s} twice", tpl, w)
			}
			named = true
			b.WriteString(`(?:` + strLit + `|` + exprLit + `)`)
		case w == "id":
			if hasID {
				return nil, fmt.Errorf("template %q uses {id} twice", tpl)
			}
			hasID = true
			b.WriteString(fmt.Sprintf(identRe, "id"))
		case w == "ref":
			if hasRef {
				return nil, fmt.Errorf("template %q uses {ref} twice", tpl)
			}
			hasRef = true
			b.WriteString(fmt.Sprintf(identRe, "ref"))
		case w == "any":
			b.WriteString(anyExpr)
		case w[0] >= 'A' && w[0] <= 'Z':
			b.WriteString(`[A-Za-z_][\w.<>\[\]]*`)
		default:
			return nil, fmt.Errorf("template %q: unknown placeholder {%s} (use {name}, {any} or {Type})", tpl, w)
		}
		b.WriteString(`\s*`)
		last = m[1]
	}
	lit(tpl[last:])
	if !named && !hasRef {
		return nil, fmt.Errorf("template %q has no {name} or {ref}", tpl)
	}
	if named && hasRef {
		return nil, fmt.Errorf("template %q has both {name} and {ref}", tpl)
	}
	re, err := regexp.Compile(strings.TrimSuffix(b.String(), `\s*`))
	if err != nil {
		return nil, fmt.Errorf("template %q: %v", tpl, err)
	}
	return &Compiled{re: re, named: named, hasID: hasID, hasRef: hasRef}, nil
}

// captured returns (literal, expr): exactly one is non-empty.
func (c *Compiled) captured(m []int, code string) (lit, expr string, ok bool) {
	for _, n := range []string{"s1", "s2", "s3"} {
		if g := c.re.SubexpIndex(n); g >= 0 && m[2*g] >= 0 {
			return code[m[2*g]:m[2*g+1]], "", true
		}
	}
	if g := c.re.SubexpIndex("e"); g >= 0 && m[2*g] >= 0 {
		return "", code[m[2*g]:m[2*g+1]], true
	}
	return "", "", false
}

func (c *Compiled) group(m []int, code, name string) string {
	if g := c.re.SubexpIndex(name); g >= 0 && m[2*g] >= 0 {
		return code[m[2*g]:m[2*g+1]]
	}
	return ""
}

// globToRegexp: * within a segment, ** across segments, ? one char.
func globToRegexp(g string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		switch g[i] {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				i++
				if i+1 < len(g) && g[i+1] == '/' {
					i++
					b.WriteString(`(?:.*/)?`)
				} else {
					b.WriteString(`.*`)
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(g[i])))
		}
	}
	b.WriteString("$")
	return b.String()
}

// GlobMatch reports whether name (slash-separated) matches the glob.
func GlobMatch(glob, name string) bool {
	re, err := regexp.Compile(globToRegexp(glob))
	return err == nil && re.MatchString(name)
}

func (w When) ok(file, code string) bool {
	if len(w.Ext) > 0 {
		e := strings.ToLower(filepath.Ext(file))
		hit := false
		for _, x := range w.Ext {
			if strings.ToLower(strings.TrimPrefix("."+strings.TrimPrefix(x, "."), "")) == e {
				hit = true
			}
		}
		if !hit {
			return false
		}
	}
	if len(w.Glob) > 0 {
		hit := false
		for _, g := range w.Glob {
			if GlobMatch(g, file) {
				hit = true
			}
		}
		if !hit {
			return false
		}
	}
	if len(w.Contains) > 0 {
		hit := false
		for _, s := range w.Contains {
			if strings.Contains(code, s) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(w.Imports) > 0 && !importsAny(code, w.Imports) {
		return false
	}
	return true
}

var importLineRe = regexp.MustCompile(`(?m)^\s*(?:import\b|from\b|using\b|#include\b|use\b|require\b|(?:const|let|var)\s+[^=\n]*=\s*require\s*\(|\w+\s*=\s*require\b|@import\b|load\b)[^\n]*$|\bimport\s*\(|\brequire\s*\(\s*['"][^\n]*`)

// importsAny: a token appears in an import-like line (import, from, using,
// require, #include, use). A token that only occurs in a string or an
// identifier somewhere else does not open the gate.
func importsAny(code string, tokens []string) bool {
	for _, ln := range importLineRe.FindAllString(code, -1) {
		for _, t := range tokens {
			if strings.Contains(ln, t) {
				return true
			}
		}
	}
	// Go-style grouped imports: import ( ... ) spans lines
	for _, blk := range goImportBlock.FindAllString(code, -1) {
		for _, t := range tokens {
			if strings.Contains(blk, t) {
				return true
			}
		}
	}
	return false
}

var goImportBlock = regexp.MustCompile(`(?s)\bimport\s*\(.*?\)`)

// stringMask marks bytes of code that sit inside a string literal (quotes
// included). Comments are already blanked; Python triple quotes are blanked as
// docstrings by the lexer.
func stringMask(code string) []bool {
	mask := make([]bool, len(code))
	for i := 0; i < len(code); i++ {
		c := code[i]
		if c != '"' && c != '\'' && c != '`' {
			continue
		}
		j := i + 1
		for ; j < len(code); j++ {
			if code[j] == '\\' && c != '`' {
				j++
				continue
			}
			if code[j] == c {
				break
			}
			if code[j] == '\n' && c != '`' {
				break
			}
		}
		if j >= len(code) || code[j] != c {
			continue // unterminated on its line: an apostrophe, not a string
		}
		for k := i; k <= j; k++ {
			mask[k] = true
		}
		i = j
	}
	return mask
}

type compiledRule struct {
	rule ChannelRule
	pats []*Compiled
}

// Engine runs channel rules over source text.
type Engine struct {
	rules []compiledRule
}

// NewEngine compiles the channel rules of cfg (already validated).
func NewEngine(cfg *Config) (*Engine, error) {
	e := &Engine{}
	for _, r := range cfg.Channels {
		cr := compiledRule{rule: r}
		for _, p := range r.Patterns {
			c, err := compileTemplate(p.Call)
			if err != nil {
				return nil, fmt.Errorf("rule %s: %w", r.ID, err)
			}
			cr.pats = append(cr.pats, c)
		}
		e.rules = append(e.rules, cr)
	}
	return e, nil
}

func ignored(names []string, name string) bool {
	for _, g := range names {
		if re, err := regexp.Compile(globToRegexp(g)); err == nil && re.MatchString(name) {
			return true
		}
	}
	return false
}

// Run applies every rule to one file. file is the repo-relative slash path.
// A rule that exceeds the per-file budget is cut off, never the scan.
func (e *Engine) Run(file string, src []byte, resolve Resolver) []Fact {
	return e.RunWith(file, src, resolve, nil)
}

// HasDefs reports whether any rule binds identifiers (define patterns).
func (e *Engine) HasDefs() bool {
	for _, r := range e.rules {
		for _, p := range r.rule.Patterns {
			if p.Side == "define" {
				return true
			}
		}
	}
	return false
}

// Defs collects the definitions of one file: patterns with side "define" bind
// the identifier {id} to the channel name {name} (`orders = app.topic("orders-v1")`).
// Merge the results of all files and hand them to RunWith: {ref} patterns
// (`@app.agent({ref})`, `{ref}.send(`) resolve their identifier through them,
// across files.
func (e *Engine) Defs(file string, src []byte, resolve Resolver) map[string]string {
	out := map[string]string{}
	for _, f := range e.scanFile(file, src, resolve, nil, true) {
		out[f.Via] = f.Name // Via carries the bound identifier for define facts
	}
	return out
}

// RunWith is Run with the repository's definitions for {ref} patterns.
func (e *Engine) RunWith(file string, src []byte, resolve Resolver, defs map[string]string) []Fact {
	return e.scanFile(file, src, resolve, defs, false)
}

func (e *Engine) scanFile(file string, src []byte, resolve Resolver, defs map[string]string, defining bool) []Fact {
	res := lex.Scan(src, lex.FamilyFor(file))
	var sb strings.Builder
	starts := make([]int, 0, len(res.Lines))
	for _, l := range res.Lines {
		starts = append(starts, sb.Len())
		sb.WriteString(l.Code)
		sb.WriteByte('\n')
	}
	code := sb.String()
	lineAt := func(off int) int {
		lo, hi := 0, len(starts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if starts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return res.Lines[lo].Num
	}
	var inStr []bool
	var out []Fact
	deadline := time.Now().Add(PerFileBudget)
	seen := map[string]bool{}
	for _, cr := range e.rules {
		if !cr.rule.When.ok(file, code) {
			continue
		}
		for i, c := range cr.pats {
			if time.Now().After(deadline) {
				return out
			}
			p := cr.rule.Patterns[i]
			if (p.Side == "define") != defining {
				continue
			}
			for _, m := range c.re.FindAllStringSubmatchIndex(code, -1) {
				lit, expr, ok := c.captured(m, code)
				if c.hasRef {
					ref := c.group(m, code, "ref")
					n, found := defs[ref]
					if !found {
						continue
					}
					lit, expr, ok = n, "", true
				}
				if !ok {
					continue
				}
				if inStr == nil {
					inStr = stringMask(code)
				}
				if inStr[m[0]] {
					continue // the call is text inside a string (a log line, a docstring)
				}
				name, via := lit, ""
				if expr != "" {
					if resolve == nil {
						continue
					}
					n, v, ok := resolve(file, expr)
					if !ok || n == "" {
						continue
					}
					name, via = n, v
				}
				if name == "" || ignored(cr.rule.IgnoreNames, name) {
					continue
				}
				kind := p.Kind
				if kind == "" {
					kind = "topic"
				}
				line := lineAt(m[0])
				if c.hasRef {
					via = "definition " + c.group(m, code, "ref")
				}
				f := Fact{Rule: cr.rule.ID, Source: cr.rule.Source, Reviewed: cr.rule.Reviewed, Kind: kind,
					Side: p.Side, Transport: p.Transport, Name: name, File: file, Line: line,
					Text: strings.TrimSpace(strings.Join(strings.Fields(code[m[0]:m[1]]), " ")), Via: via}
				if defining {
					f.Via = c.group(m, code, "id")
					if f.Via == "" {
						continue
					}
				}
				k := f.Rule + "|" + f.Key() + "|" + fmt.Sprint(line)
				if seen[k] {
					continue
				}
				seen[k] = true
				out = append(out, f)
			}
		}
	}
	SortFacts(out)
	return out
}

// Allowed reports whether file passes scope.include/exclude.
func (s Scope) Allowed(file string) bool {
	file = path.Clean(filepath.ToSlash(file))
	if len(s.Include) > 0 {
		hit := false
		for _, g := range s.Include {
			if GlobMatch(g, file) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	for _, g := range s.Exclude {
		if GlobMatch(g, file) {
			return false
		}
	}
	return true
}

// ClientEngine runs client rules: calls that build an HTTP client from an
// address. The address is returned raw (expression text or literal) so the
// caller can resolve it through its own tables.
type ClientEngine struct{ e *Engine }

// NewClientEngine compiles cfg.Clients; nil when there are none.
func NewClientEngine(cfg *Config) (*ClientEngine, error) {
	if len(cfg.Clients) == 0 {
		return nil, nil
	}
	synth := &Config{}
	for i, c := range cfg.Clients {
		synth.Channels = append(synth.Channels, ChannelRule{
			ID: fmt.Sprintf("client-%d", i), When: c.When, Source: c.Source,
			Patterns: []Pattern{{Call: strings.ReplaceAll(c.Call, "{addr}", "{name}"), Side: "produce"}},
		})
	}
	e, err := NewEngine(synth)
	if err != nil {
		return nil, err
	}
	return &ClientEngine{e: e}, nil
}

// RunClients returns one Fact per client construction whose address is an
// expression; Name is the expression text.
func (c *ClientEngine) RunClients(file string, src []byte) []Fact {
	return c.e.Run(file, src, func(_, expr string) (string, string, bool) { return expr, "expr", true })
}

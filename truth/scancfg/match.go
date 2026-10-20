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
	re    *regexp.Regexp
	named bool
}

var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

const (
	strLit  = `(?:"([^"\\\n]*)"|'([^'\\\n]*)'|` + "`([^`\\n]*)`" + `)`
	exprLit = `([A-Za-z_@$][\w.$]*(?:\(\))?)`
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
	named := false
	last := 0
	locs := placeholder.FindAllStringSubmatchIndex(tpl, -1)
	lit := func(s string) {
		if s == "" {
			return
		}
		for _, f := range strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ", ",", " , ").Replace(s)) {
			switch f {
			case "(":
				b.WriteString(`\(\s*`)
			case ")":
				b.WriteString(`\s*\)`)
			case ",":
				b.WriteString(`\s*,\s*`)
			default:
				b.WriteString(regexp.QuoteMeta(f))
			}
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
	if !named {
		return nil, fmt.Errorf("template %q has no {name}", tpl)
	}
	re, err := regexp.Compile(strings.TrimSuffix(b.String(), `\s*`))
	if err != nil {
		return nil, fmt.Errorf("template %q: %v", tpl, err)
	}
	return &Compiled{re: re, named: named}, nil
}

// captured returns (literal, expr): exactly one is non-empty.
func (c *Compiled) captured(m []int, code string) (lit, expr string, ok bool) {
	// groups: 1..3 string literal forms, 4 expression
	for g := 1; g <= 3; g++ {
		if m[2*g] >= 0 {
			return code[m[2*g]:m[2*g+1]], "", true
		}
	}
	if m[8] >= 0 {
		return "", code[m[8]:m[9]], true
	}
	return "", "", false
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
	for _, list := range [][]string{w.Contains, w.Imports} {
		if len(list) == 0 {
			continue
		}
		hit := false
		for _, s := range list {
			if strings.Contains(code, s) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
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
			for _, m := range c.re.FindAllStringSubmatchIndex(code, -1) {
				lit, expr, ok := c.captured(m, code)
				if !ok {
					continue
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
				f := Fact{Rule: cr.rule.ID, Source: cr.rule.Source, Reviewed: cr.rule.Reviewed, Kind: kind,
					Side: p.Side, Transport: p.Transport, Name: name, File: file, Line: line,
					Text: strings.TrimSpace(strings.Join(strings.Fields(code[m[0]:m[1]]), " ")), Via: via}
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

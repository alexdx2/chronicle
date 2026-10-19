package extract

import (
	"path"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Go messaging: franz-go (kgo.ConsumeTopics consumes, kgo.Record{Topic:}
// and kgo.DefaultProduceTopic produce) and Shopify/IBM sarama
// (ProducerMessage{Topic:} produces, ConsumePartition and Consume(ctx,
// []string{...}) consume). A topic given as an identifier is resolved through
// a string const or var of the same package (same file first, then the
// sibling .go files of the directory); the fact then points at the const line
// and keeps the call as derived evidence. Anything else yields no fact.

func init() {
	isGo := func(p string) bool { return hasExt(p, ".go") }
	Rules = append(Rules,
		funcRule{"kafka.go_franz", isGo, applyGoFranz},
		funcRule{"kafka.go_sarama", isGo, applyGoSarama},
	)
}

var (
	goConstOne   = regexp.MustCompile(`^\s*(?:const|var)\s+(\w+)(?:\s+[\w.]+)?\s*=\s*"([^"]*)"`)
	goConstBlock = regexp.MustCompile(`^\s*(?:const|var)\s*\(\s*$`)
	goConstInner = regexp.MustCompile(`^\s*(\w+)(?:\s+[\w.]+)?\s*=\s*"([^"]*)"`)
	goIdent      = regexp.MustCompile(`^[A-Za-z_]\w*$`)
	goStringLit  = regexp.MustCompile(`^"([^"]*)"$`)
)

type goConst struct {
	val  string
	file string
	line lex.Line
}

// goConsts collects the string consts and vars declared with a literal.
func goConsts(file string, lines []lex.Line) map[string]goConst {
	out := map[string]goConst{}
	inBlock := false
	for _, l := range lines {
		switch {
		case inBlock:
			if strings.HasPrefix(strings.TrimSpace(l.Code), ")") {
				inBlock = false
			} else if m := goConstInner.FindStringSubmatch(l.Code); m != nil {
				out[m[1]] = goConst{m[2], file, l}
			}
		case goConstBlock.MatchString(l.Code):
			inBlock = true
		default:
			if m := goConstOne.FindStringSubmatch(l.Code); m != nil {
				out[m[1]] = goConst{m[2], file, l}
			}
		}
	}
	return out
}

// goPkgConsts is the const table of the file plus its sibling non-test .go
// files; the file's own declarations win.
func goPkgConsts(f *FileCtx) map[string]goConst {
	out := map[string]goConst{}
	for _, name := range f.Tree.Siblings(f.Dir, "*.go") {
		p := path.Join(f.Dir, name)
		if p == f.Path || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if b, ok := f.Tree.ReadFile(p); ok {
			for k, v := range goConsts(p, lex.Scan(b, lex.CLike).Lines) {
				out[k] = v
			}
		}
	}
	for k, v := range goConsts(f.Path, f.Lines) {
		out[k] = v
	}
	return out
}

type goChanCtx struct {
	f      *FileCtx
	rule   string
	consts map[string]goConst // loaded on first identifier
}

// emit turns one argument expression (a literal or an identifier) into a fact.
func (c *goChanCtx) emit(l lex.Line, expr, side string) []truth.Fact {
	expr = strings.TrimSpace(expr)
	if m := goStringLit.FindStringSubmatch(expr); m != nil {
		if fc, ok := channelFact(c.rule, c.f, l, "topic", m[1], side); ok {
			return []truth.Fact{fc}
		}
		return nil
	}
	if !goIdent.MatchString(expr) {
		return nil
	}
	if c.consts == nil {
		c.consts = goPkgConsts(c.f)
	}
	k, ok := c.consts[expr]
	if !ok {
		return nil
	}
	fc, ok := channelFact(c.rule, c.f, k.line, "topic", k.val, side)
	if !ok {
		return nil
	}
	fc.Primary.File = k.file
	fc.Derived = append(fc.Derived, evidence(c.f, l))
	return []truth.Fact{fc}
}

// goSplitArgs splits the text between the outer parentheses or braces at top
// level commas. text must be masked-structure-safe: strings are skipped here.
func goSplitArgs(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// goCallArgs returns the argument text of the call whose "(" is at s[open],
// up to the matching ")". ok is false when it never closes.
func goCallArgs(s string, open int) (string, bool) {
	if open >= len(s) || s[open] != '(' {
		return "", false
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '`':
			for i++; i < len(s) && s[i] != '`'; i++ {
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i], true
			}
		}
	}
	return "", false
}

// goWindow joins the code of n lines from i; offsets of line i stay valid.
func goWindow(f *FileCtx, i, n int) string { return codeWindow(f.Lines, i, n) }

// goCalls runs fn for every call of the form re (which must end right before
// the "(") whose match starts on line i; fn gets the argument list.
func goCalls(f *FileCtx, re *regexp.Regexp, fn func(l lex.Line, args []string)) {
	for i, l := range f.Lines {
		text := goWindow(f, i, 8)
		for _, ix := range re.FindAllStringIndex(text, -1) {
			if ix[0] >= len(l.Code) {
				continue
			}
			if args, ok := goCallArgs(text, ix[1]); ok {
				fn(l, goSplitArgs(args))
			}
		}
	}
}

// goComposites runs fn with the value expression of the field in every
// composite literal opened by re (which must end right before "{").
func goComposites(f *FileCtx, re *regexp.Regexp, field string, fn func(l lex.Line, expr string)) {
	fieldRe := regexp.MustCompile(`^\s*` + field + `\s*:\s*(.*)$`)
	for i, l := range f.Lines {
		text := goWindow(f, i, 12)
		for _, ix := range re.FindAllStringIndex(text, -1) {
			if ix[0] >= len(l.Code) {
				continue
			}
			body, ok := goBraceBody(text, ix[1]-1)
			if !ok {
				continue
			}
			for _, kv := range goSplitArgs(body) {
				if m := fieldRe.FindStringSubmatch(kv); m != nil {
					fn(l, m[1])
				}
			}
		}
	}
}

func goBraceBody(s string, open int) (string, bool) {
	if open >= len(s) || s[open] != '{' {
		return "", false
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '`':
			for i++; i < len(s) && s[i] != '`'; i++ {
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[open+1 : i], true
			}
		}
	}
	return "", false
}

var (
	kgoConsume = regexp.MustCompile(`\bkgo\.ConsumeTopics`)
	kgoDefault = regexp.MustCompile(`\bkgo\.DefaultProduceTopic`)
	kgoRecord  = regexp.MustCompile(`\bkgo\.Record\{`)

	saramaMsg       = regexp.MustCompile(`\bsarama\.ProducerMessage\{`)
	saramaPartition = regexp.MustCompile(`\.ConsumePartition`)
	saramaConsume   = regexp.MustCompile(`\.Consume`)
)

func applyGoFranz(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "twmb/franz-go") {
		return nil
	}
	c := &goChanCtx{f: f, rule: "kafka.go_franz"}
	var out []truth.Fact
	// the "(" must follow the name directly: regex ends before it, so allow it
	call := func(re *regexp.Regexp, side string) {
		goCalls(f, regexp.MustCompile(re.String()+`\s*`), func(l lex.Line, args []string) {
			for _, a := range args {
				out = append(out, c.emit(l, a, side)...)
			}
		})
	}
	call(kgoConsume, "consume")
	call(kgoDefault, "produce")
	goComposites(f, kgoRecord, "Topic", func(l lex.Line, expr string) {
		out = append(out, c.emit(l, expr, "produce")...)
	})
	return out
}

func applyGoSarama(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "shopify/sarama", "ibm/sarama") {
		return nil
	}
	c := &goChanCtx{f: f, rule: "kafka.go_sarama"}
	var out []truth.Fact
	goComposites(f, saramaMsg, "Topic", func(l lex.Line, expr string) {
		out = append(out, c.emit(l, expr, "produce")...)
	})
	goCalls(f, regexp.MustCompile(saramaPartition.String()+`\s*`), func(l lex.Line, args []string) {
		if len(args) > 0 {
			out = append(out, c.emit(l, args[0], "consume")...)
		}
	})
	// ConsumerGroup.Consume(ctx, []string{"a", "b"}, handler)
	goCalls(f, regexp.MustCompile(saramaConsume.String()+`\s*`), func(l lex.Line, args []string) {
		if len(args) < 2 {
			return
		}
		t := strings.TrimSpace(args[1])
		if !strings.HasPrefix(t, "[]string{") || !strings.HasSuffix(t, "}") {
			return
		}
		for _, a := range goSplitArgs(t[len("[]string{") : len(t)-1]) {
			if strings.TrimSpace(a) != "" {
				out = append(out, c.emit(l, a, "consume")...)
			}
		}
	})
	return out
}

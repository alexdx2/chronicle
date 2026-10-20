package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Shared machinery of the call-shaped messaging rules (NATS, Redis, MQTT):
// find `recv.method(args)` in comment-free code with string contents masked,
// so a log message that looks like a call never matches, and hand the rule
// the original text of each top-level argument.

// chCall is one method call found in a file.
type chCall struct {
	line lex.Line // line the call starts on
	recv string   // identifier chain before the dot ("" after a call result)
	name string   // method name as written
	args []string // trimmed original text of each top-level argument
}

// chMask blanks quoted literals (quotes included; ", ' and `) so structure can
// be scanned; offsets stay the same. A quote that never closes is left alone.
func chMask(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		q := b[i]
		if q != '"' && q != '\'' && q != '`' {
			continue
		}
		j := i + 1
		for j < len(b) && b[j] != q {
			if b[j] == '\\' && q != '`' && j+1 < len(b) {
				j++
			}
			j++
		}
		if j >= len(b) {
			continue
		}
		for k := i; k <= j; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
		i = j
	}
	return string(b)
}

// chSplit splits a masked argument list at top-level commas and returns the
// matching slices of orig. masked and orig have the same length.
func chSplit(masked, orig string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(masked); i++ {
		switch masked[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(orig[start:i]))
				start = i + 1
			}
		}
	}
	if last := strings.TrimSpace(orig[start:]); last != "" {
		out = append(out, last)
	}
	return out
}

// chCalls finds every call `.name(` where names is a regexp alternation (no
// groups needed). The call must start on its line; its arguments may span up
// to 10 lines.
func chCalls(f *FileCtx, names string) []chCall {
	re := regexp.MustCompile(`\.\s*(` + names + `)\s*\(`)
	var out []chCall
	for i, l := range f.Lines {
		if !strings.Contains(l.Code, "(") && !strings.Contains(l.Code, ".") {
			continue
		}
		text := codeWindow(f.Lines, i, 10)
		masked := chMask(text)
		for _, ix := range re.FindAllStringSubmatchIndex(masked, -1) {
			if ix[0] >= len(l.Code) {
				break
			}
			open := ix[1] - 1
			depth, end := 0, -1
			for k := open; k < len(masked) && end < 0; k++ {
				switch masked[k] {
				case '(':
					depth++
				case ')':
					depth--
					if depth == 0 {
						end = k
					}
				}
			}
			if end < 0 {
				continue
			}
			rs := ix[0]
			for rs > 0 && (isIdentByte(masked[rs-1]) || masked[rs-1] == '.' || masked[rs-1] == '$') {
				rs--
			}
			out = append(out, chCall{
				line: l,
				recv: text[rs:ix[0]],
				name: text[ix[2]:ix[3]],
				args: chSplit(masked[open+1:end], text[open+1:end]),
			})
		}
	}
	return out
}

// chLit returns the contents of an argument that is exactly one plain string
// literal ("", false for anything else: variables, concatenations, f-strings).
func chLit(arg string) (string, bool) {
	arg = strings.TrimSpace(arg)
	if len(arg) < 3 {
		return "", false
	}
	q := arg[0]
	if (q != '"' && q != '\'' && q != '`') || arg[len(arg)-1] != q {
		return "", false
	}
	in := arg[1 : len(arg)-1]
	if strings.Contains(strings.ReplaceAll(in, `\`+string(q), ""), string(q)) {
		return "", false
	}
	return in, true
}

var chKwRe = regexp.MustCompile(`^(\w+)\s*=\s*(.+)$`)

// chLitKw is chLit that also accepts `kw="x"` (Python keyword argument).
func chLitKw(arg, kw string) (string, bool) {
	if m := chKwRe.FindStringSubmatch(strings.TrimSpace(arg)); m != nil && m[1] == kw {
		return chLit(m[2])
	}
	return chLit(arg)
}

// chAnyKw reports whether arg is a keyword argument (kw=value).
func chAnyKw(arg string) bool { return chKwRe.MatchString(strings.TrimSpace(arg)) }

// chLeadLits returns the literals of the leading arguments, stopping at the
// first one that is not a plain literal (ioredis subscribe('a', 'b', cb)).
func chLeadLits(args []string) []string {
	var out []string
	for _, a := range args {
		s, ok := chLit(a)
		if !ok {
			break
		}
		out = append(out, s)
	}
	return out
}

// chSpec is one call shape: the method names, the channel kind and side, and
// how to read the names out of the call.
type chSpec struct {
	names string
	kind  string
	side  string
	pick  func(c chCall) []string
}

// chRule builds a rule body from call specs. gate decides per file (import
// check); test files give nothing.
func chRule(rule string, gate func(f *FileCtx) bool, specs ...chSpec) func(f *FileCtx) []truth.Fact {
	return func(f *FileCtx) []truth.Fact {
		if f.IsTest || !gate(f) {
			return nil
		}
		var out []truth.Fact
		for _, s := range specs {
			for _, c := range chCalls(f, s.names) {
				for _, n := range s.pick(c) {
					if fc, ok := channelFact(rule, f, c.line, s.kind, n, s.side); ok {
						out = append(out, fc)
					}
				}
			}
		}
		return out
	}
}

// chImports returns a gate that is true when some code line matches re.
func chImports(re *regexp.Regexp) func(f *FileCtx) bool {
	return func(f *FileCtx) bool {
		for _, l := range f.Lines {
			if re.MatchString(l.Code) {
				return true
			}
		}
		return false
	}
}

// chFirst picks the first argument (keyword form kw allowed when kw != "").
func chFirst(kw string) func(c chCall) []string {
	return func(c chCall) []string {
		if len(c.args) == 0 {
			return nil
		}
		if s, ok := chLitKw(c.args[0], kw); ok && (kw != "" || !chAnyKw(c.args[0])) {
			return []string{s}
		}
		return nil
	}
}

// chJSImport builds the import regexp of a JS/TS module name alternation.
func chJSImport(mods string) *regexp.Regexp {
	return regexp.MustCompile(`(?:\bfrom|\brequire\s*\(|\bimport\s*\(?)\s*['"](?:` + mods + `)['"]`)
}

// chPyImport builds the import regexp of a Python module name alternation.
func chPyImport(mods string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*(?:import|from)\s+(?:` + mods + `)\b`)
}

package extract

import (
	"regexp"
	"strings"
)

// Shared helpers for the Java rules (Spring). They work on the comment-free
// code of a file joined into one string, plus a "masked" copy of the same
// length in which the content of string and char literals is blanked, so
// structure (annotations, class declarations, braces) is never read from
// inside a literal while arguments are still read from the original text.

type javaText struct {
	f      *FileCtx
	code   string // comment-free code, lines joined with '\n'
	masked string // same length, literal contents blanked
	starts []int  // offset of each line start
}

func newJavaText(f *FileCtx) *javaText {
	var sb strings.Builder
	starts := make([]int, 0, len(f.Lines))
	for _, l := range f.Lines {
		starts = append(starts, sb.Len())
		sb.WriteString(l.Code)
		sb.WriteByte('\n')
	}
	code := sb.String()
	m := []byte(code)
	for i := 0; i < len(m); i++ {
		switch c := m[i]; {
		case c == '"' && i+2 < len(m) && m[i+1] == '"' && m[i+2] == '"': // text block
			j := strings.Index(code[i+3:], `"""`)
			end := len(m)
			if j >= 0 {
				end = i + 3 + j
			}
			for k := i + 3; k < end; k++ {
				if m[k] != '\n' {
					m[k] = ' '
				}
			}
			i = end + 2
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(m) && m[j] != c && m[j] != '\n' {
				if m[j] == '\\' && j+1 < len(m) && m[j+1] != '\n' {
					m[j] = ' '
					j++
				}
				m[j] = ' '
				j++
			}
			i = j
		}
	}
	return &javaText{f: f, code: code, masked: string(m), starts: starts}
}

// lineIdx returns the index in f.Lines of the line holding offset off.
func (t *javaText) lineIdx(off int) int {
	lo, hi := 0, len(t.starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if t.starts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// balanced returns the offset just past the ")" matching the "(" at open
// (masked text), or -1.
func (t *javaText) balanced(open int) int {
	depth := 0
	for k := open; k < len(t.masked); k++ {
		switch t.masked[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return k + 1
			}
		}
	}
	return -1
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

var javaAnnoName = regexp.MustCompile(`^@\s*([A-Za-z_]\w*(?:\s*\.\s*[A-Za-z_]\w*)*)`)

// javaAnno is one annotation use.
type javaAnno struct {
	name    string // simple name (qualifier dropped)
	pos     int    // offset of '@'
	args    string // original text between the parentheses, "" when none
	hasArgs bool
	end     int // offset past the annotation
	classKw int // offset of the class/interface keyword the annotation is attached to, -1 otherwise
}

var javaModifiers = map[string]bool{"public": true, "protected": true, "private": true, "abstract": true,
	"static": true, "final": true, "sealed": true, "strictfp": true, "non": true}

// javaAnnotations lists every annotation use in order. An annotation counts as
// class-level (classKw >= 0) when only further annotations and modifiers sit
// between it and a class or interface keyword.
func (t *javaText) annotations() []javaAnno {
	var out []javaAnno
	i := 0
	for i < len(t.masked) {
		at := strings.IndexByte(t.masked[i:], '@')
		if at < 0 {
			break
		}
		a, ok := t.annoAt(i + at)
		if !ok {
			i += at + 1
			continue
		}
		a.classKw = t.classAfter(a.end)
		out = append(out, a)
		i = a.end
	}
	return out
}

// annoAt parses the annotation whose '@' is at pos.
func (t *javaText) annoAt(pos int) (javaAnno, bool) {
	m := javaAnnoName.FindStringSubmatch(t.masked[pos:])
	if m == nil || m[1] == "interface" {
		return javaAnno{}, false
	}
	name := strings.ReplaceAll(strings.ReplaceAll(m[1], " ", ""), "\n", "")
	if k := strings.LastIndex(name, "."); k >= 0 {
		name = name[k+1:]
	}
	a := javaAnno{name: name, pos: pos, end: pos + len(m[0]), classKw: -1}
	j := skipSpace(t.masked, a.end)
	if j < len(t.masked) && t.masked[j] == '(' {
		e := t.balanced(j)
		if e < 0 {
			return javaAnno{}, false
		}
		a.args, a.hasArgs, a.end = t.code[j+1:e-1], true, e
	}
	return a, true
}

var javaWord = regexp.MustCompile(`^[A-Za-z_]\w*`)

// classAfter walks over annotations and modifiers from off and returns the
// offset of a following class/interface keyword, or -1.
func (t *javaText) classAfter(off int) int {
	i := off
	for {
		i = skipSpace(t.masked, i)
		if i >= len(t.masked) {
			return -1
		}
		if t.masked[i] == '@' {
			a, ok := t.annoAt(i)
			if !ok {
				return -1
			}
			i = a.end
			continue
		}
		w := javaWord.FindString(t.masked[i:])
		switch {
		case w == "class" || w == "interface":
			return i
		case javaModifiers[w]:
			i += len(w)
			if w == "non" && i+7 <= len(t.masked) && t.masked[i:i+7] == "-sealed" {
				i += 7
			}
		default:
			return -1
		}
	}
}

var javaClassDecl = regexp.MustCompile(`(?:^|[^@\w.])(class|interface)\s+[A-Za-z_]\w*`)

// classDecls returns the keyword offset of every class/interface declaration
// together with its brace depth.
func (t *javaText) classDecls() (pos, depth []int) {
	depthAt := make([]int, len(t.masked)+1)
	d := 0
	for i := 0; i < len(t.masked); i++ {
		depthAt[i] = d
		switch t.masked[i] {
		case '{':
			d++
		case '}':
			if d > 0 {
				d--
			}
		}
	}
	for _, m := range javaClassDecl.FindAllStringSubmatchIndex(t.masked, -1) {
		pos = append(pos, m[2])
		depth = append(depth, depthAt[m[2]])
	}
	return
}

// splitTop splits s on commas outside quotes and brackets.
func splitTop(s string) []string {
	var parts []string
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
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '{' || c == '[':
			depth++
		case c == ')' || c == '}' || c == ']':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

var javaStrLit = regexp.MustCompile(`^"([^"\\]*)"$`)

// javaLiterals reads expr as one string literal or a {"a", "b"} array of
// literals. ok is false for constants, concatenations and placeholders.
func javaLiterals(expr string) (vals []string, ok bool) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "{") && strings.HasSuffix(expr, "}") {
		inner := strings.TrimSpace(expr[1 : len(expr)-1])
		inner = strings.TrimSuffix(inner, ",")
		if strings.TrimSpace(inner) == "" {
			return []string{""}, true
		}
		for _, p := range splitTop(inner) {
			v, ok := javaLiterals(p)
			if !ok || len(v) != 1 {
				return nil, false
			}
			vals = append(vals, v[0])
		}
		return vals, true
	}
	m := javaStrLit.FindStringSubmatch(expr)
	if m == nil || strings.Contains(m[1], "${") || strings.Contains(m[1], "#{") {
		return nil, false
	}
	return []string{m[1]}, true
}

var javaNamedArg = regexp.MustCompile(`^\s*([A-Za-z_]\w*)\s*=\s*([\s\S]*)$`)

// javaArg returns the expression of the named attribute in an annotation's
// argument text. A leading positional argument counts as "value".
func javaArg(args string, names ...string) (expr string, found bool) {
	for i, p := range splitTop(args) {
		if m := javaNamedArg.FindStringSubmatch(p); m != nil {
			for _, n := range names {
				if m[1] == n {
					return m[2], true
				}
			}
			continue
		}
		if i == 0 && strings.TrimSpace(p) != "" {
			for _, n := range names {
				if n == "value" {
					return p, true
				}
			}
		}
	}
	return "", false
}

// blockEnd returns the offset just past the '}' closing the first '{' at or
// after pos (masked text), or the end of the text.
func (t *javaText) blockEnd(pos int) int {
	depth := 0
	for k := pos; k < len(t.masked); k++ {
		switch t.masked[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return k + 1
			}
		}
	}
	return len(t.masked)
}

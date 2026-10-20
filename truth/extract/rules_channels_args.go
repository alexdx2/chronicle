package extract

import (
	"regexp"
	"strings"
)

// Argument parsing shared by the cloud messaging rules (Service Bus, Pub/Sub,
// CDK). The lexer already blanked comments; strings are still in Code.

// splitCallArgs returns the top-level arguments of the call whose opening
// parenthesis is text[open]. ok is false when the call does not close inside
// text. Brackets and quotes are respected; an empty trailing argument (a
// trailing comma) is dropped.
func splitCallArgs(text string, open int) (args []string, ok bool) {
	depth, start, quote := 0, open+1, byte(0)
	for i := open; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				if a := strings.TrimSpace(text[start:i]); a != "" {
					args = append(args, a)
				}
				return args, true
			}
		case ',':
			if depth == 1 {
				args = append(args, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	return nil, false
}

var argKeyword = regexp.MustCompile(`^([A-Za-z_]\w*)\s*[:=]\s*([^=].*)$`)

// parsedArgs holds the arguments of one call: positional ones in order and
// keyword ones (`name=value` in Python and C# named arguments `name: value`).
type parsedArgs struct {
	pos []string          // raw positional arguments
	kw  map[string]string // raw keyword arguments
}

func parseArgs(args []string) parsedArgs {
	p := parsedArgs{kw: map[string]string{}}
	for _, a := range args {
		if m := argKeyword.FindStringSubmatch(a); m != nil && !strings.HasPrefix(a, "==") {
			p.kw[m[1]] = strings.TrimSpace(m[2])
			continue
		}
		p.pos = append(p.pos, a)
	}
	return p
}

// positional returns positional argument i, or keyword argument kwName when
// the call names it (kwName "" means positional only).
func (p parsedArgs) arg(i int, kwNames ...string) (string, bool) {
	for _, k := range kwNames {
		if v, ok := p.kw[k]; ok {
			return v, true
		}
	}
	if i >= 0 && i < len(p.pos) {
		return p.pos[i], true
	}
	return "", false
}

// plainLiteral returns the content of an argument that is exactly one string
// literal ("a", 'a' or a `a` without interpolation). Concatenations, calls and
// prefixed strings (f"", $"", @"") give "".
func plainLiteral(arg string) string {
	arg = strings.TrimSpace(arg)
	if len(arg) < 3 {
		return ""
	}
	q := arg[0]
	if q != '"' && q != '\'' && q != '`' {
		return ""
	}
	if arg[len(arg)-1] != q || strings.IndexByte(arg[1:len(arg)-1], q) >= 0 || strings.Contains(arg, "${") {
		return ""
	}
	return arg[1 : len(arg)-1]
}

// foundCall is one call of a method found on a code line.
type foundCall struct {
	idx    int    // 0-based line index of the method name
	name   string // capture 1 of the matching regex
	recv   string // capture 2 when the regex has one (receiver)
	text   string // code window starting at the line
	open   int    // index of the opening parenthesis in text
	args   parsedArgs
	closed bool
}

// findCalls runs re over every code line. re must end with the opening
// parenthesis of the call and capture the method name as group 1 (optionally
// the receiver as group 2). The argument text may span up to window lines.
func findCalls(f *FileCtx, re *regexp.Regexp, window int) []foundCall {
	var out []foundCall
	for i, l := range f.Lines {
		for _, loc := range re.FindAllStringSubmatchIndex(l.Code, -1) {
			text := codeWindow(f.Lines, i, window)
			open := loc[1] - 1
			args, ok := splitCallArgs(text, open)
			c := foundCall{idx: i, text: text, open: open, args: parseArgs(args), closed: ok}
			c.name = l.Code[loc[2]:loc[3]]
			if len(loc) >= 6 && loc[4] >= 0 {
				c.recv = l.Code[loc[4]:loc[5]]
			}
			out = append(out, c)
		}
	}
	return out
}

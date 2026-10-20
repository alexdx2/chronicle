package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// RabbitMQ / AMQP channels shared by the per-language rules: exchanges and
// queues with a literal name. A language rule turns its calls into amqpOp and
// amqpBind values; amqpFacts then applies the common semantics:
//
//   - declare/produce facts are emitted as they are;
//   - consuming queue q gives queue:q#consume, and when the same file binds q
//     to a literal exchange x (queue_bind, QueueBind, bindQueue) also
//     exchange:x#consume, whose primary evidence is the binding line and whose
//     derived evidence is the consuming call.

type amqpOp struct {
	kind string // exchange | queue
	side string // declare | produce | consume
	name string
	line lex.Line
}

type amqpBind struct {
	queue, exchange string
	line            lex.Line
}

func amqpFacts(rule string, f *FileCtx, ops []amqpOp, binds []amqpBind) []truth.Fact {
	var out []truth.Fact
	for _, op := range ops {
		if fc, ok := channelFact(rule, f, op.line, op.kind, op.name, op.side); ok {
			out = append(out, fc)
		}
		if op.kind != "queue" || op.side != "consume" {
			continue
		}
		for _, b := range binds {
			if b.queue != op.name {
				continue
			}
			if fc, ok := channelFact(rule, f, b.line, "exchange", b.exchange, "consume", evidence(f, op.line)); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

// amqpArg is one call argument; name is set for `name: x` / `name=x`.
type amqpArg struct{ name, text string }

type amqpCall struct {
	line     lex.Line
	receiver string
	method   string
	args     []amqpArg
}

var amqpNamedArg = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::|=)\s*([^=:][\s\S]*)$`)

// amqpCalls finds calls matching re (group 1 receiver, group 2 method; the
// match must end at the opening parenthesis) and parses their arguments over
// at most 8 lines. named enables `name: x` / `name=x` arguments.
func amqpCalls(f *FileCtx, re *regexp.Regexp, named bool) []amqpCall {
	var out []amqpCall
	for i, l := range f.Lines {
		for _, loc := range re.FindAllStringSubmatchIndex(maskQuoted(l.Code), -1) {
			text := l.Code[loc[1]:]
			if i+1 < len(f.Lines) {
				text += " " + codeWindow(f.Lines, i+1, 7)
			}
			c := amqpCall{line: l, receiver: l.Code[loc[2]:loc[3]], method: l.Code[loc[4]:loc[5]]}
			for _, a := range splitCallArgs(text) {
				a = strings.TrimSpace(a)
				arg := amqpArg{text: a}
				if named {
					if m := amqpNamedArg.FindStringSubmatch(a); m != nil {
						arg = amqpArg{name: m[1], text: strings.TrimSpace(m[2])}
					}
				}
				c.args = append(c.args, arg)
			}
			out = append(out, c)
		}
	}
	return out
}

// splitCallArgs splits the text after an opening parenthesis into top-level
// arguments, up to the matching closing parenthesis.
func splitCallArgs(s string) []string {
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
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(' || c == '{' || c == '[':
			depth++
		case c == ')' || c == '}' || c == ']':
			if depth == 0 {
				if strings.TrimSpace(s[start:i]) != "" || len(parts) > 0 {
					parts = append(parts, s[start:i])
				}
				return parts
			}
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return parts // unbalanced: arguments are incomplete, keep what we have
}

// strLiteral reads s as exactly one plain string literal ('x', "x", `x`,
// C# @"x"). ok is false for concatenations, interpolations and non-literals.
func strLiteral(s string) (val string, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	if len(s) < 2 || (s[0] != '\'' && s[0] != '"' && s[0] != '`') || s[len(s)-1] != s[0] {
		return "", false
	}
	body := s[1 : len(s)-1]
	if strings.IndexByte(body, s[0]) >= 0 || strings.Contains(body, "\\") {
		return "", false
	}
	return body, true
}

// param returns the literal of the argument called one of names, else of the
// unnamed argument at position idx (idx < 0: by name only).
func (c amqpCall) param(idx int, names ...string) (string, bool) {
	for _, a := range c.args {
		for _, n := range names {
			if a.name != "" && strings.EqualFold(a.name, n) {
				return strLiteral(a.text)
			}
		}
	}
	if idx >= 0 && idx < len(c.args) && c.args[idx].name == "" {
		return strLiteral(c.args[idx].text)
	}
	return "", false
}

// publishOp builds the op for a publish to exchange (with routing key):
// a named exchange is produced to; the default exchange ("") routes by
// queue name, so the routing key names the queue.
func publishOp(l lex.Line, exchange string, exOK bool, key string, keyOK bool) (amqpOp, bool) {
	switch {
	case exOK && strings.TrimSpace(exchange) != "":
		return amqpOp{"exchange", "produce", exchange, l}, true
	case exOK && keyOK && strings.TrimSpace(key) != "":
		return amqpOp{"queue", "produce", key, l}, true
	}
	return amqpOp{}, false
}

// maskQuoted blanks the contents of string literals (quotes stay) so that a
// call named inside a log message or another string never matches. The result
// has the same length as the input, so offsets carry over to the original.
func maskQuoted(s string) string {
	if !strings.ContainsAny(s, "'\"`") {
		return s
	}
	b := []byte(s)
	var quote byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case quote != 0:
			if c == '\\' && i+1 < len(b) {
				b[i], b[i+1] = ' ', ' '
				i++
			} else if c == quote {
				quote = 0
			} else {
				b[i] = ' '
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		}
	}
	return string(b)
}

package extract

import (
	"regexp"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules,
		funcRule{"pika", func(p string) bool { return hasExt(p, ".py") }, applyPika},
	)
}

var amqpPyCall = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\.\s*(exchange_declare|queue_declare|queue_bind|basic_publish|basic_consume)\s*\(`)

// applyPika: pika (and py-amqp style) channel calls, positional or keyword.
// Old pika basic_consume(callback, queue=...) is read by keyword.
func applyPika(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "pika", "amqp") {
		return nil
	}
	var ops []amqpOp
	var binds []amqpBind
	for _, c := range amqpCalls(f, amqpPyCall, true) {
		switch c.method {
		case "exchange_declare":
			if n, ok := c.param(0, "exchange"); ok {
				ops = append(ops, amqpOp{"exchange", "declare", n, c.line})
			}
		case "queue_declare":
			if n, ok := c.param(0, "queue"); ok {
				ops = append(ops, amqpOp{"queue", "declare", n, c.line})
			}
		case "basic_consume":
			if n, ok := c.param(0, "queue"); ok {
				ops = append(ops, amqpOp{"queue", "consume", n, c.line})
			}
		case "queue_bind":
			q, qok := c.param(0, "queue")
			x, xok := c.param(1, "exchange")
			if qok && xok {
				binds = append(binds, amqpBind{q, x, c.line})
			}
		case "basic_publish":
			x, xok := c.param(0, "exchange")
			k, kok := c.param(1, "routing_key")
			if op, ok := publishOp(c.line, x, xok, k, kok); ok {
				ops = append(ops, op)
			}
		}
	}
	return amqpFacts("pika", f, ops, binds)
}

package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules,
		funcRule{"amqplib", func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }, applyAmqplib},
	)
}

var (
	amqpJSCall = regexp.MustCompile(`([A-Za-z_$][\w$.]*)\s*\??\.\s*(assertExchange|assertQueue|publish|sendToQueue|consume|bindQueue)\s*\(`)
	// a channel is created by createChannel/createConfirmChannel or declared
	// with an amqplib Channel type.
	amqpJSChanAssign = regexp.MustCompile(`([A-Za-z_$][\w$.]*)\s*=\s*(?:await\s+)?[\w$.]*\.\s*create(?:Confirm)?Channel\b`)
	amqpJSChanType   = regexp.MustCompile(`([A-Za-z_$][\w$.]*)\s*[:?]\s*[\w.]*\b(?:Confirm)?Channel\b`)
)

// amqpJSReceivers lists the receivers that are channels in this file; the
// conventional names channel/ch/chan count too.
func amqpJSReceiver(f *FileCtx) func(string) bool {
	known := map[string]bool{}
	for _, l := range f.Lines {
		for _, m := range amqpJSChanAssign.FindAllStringSubmatch(l.Code, -1) {
			known[strings.TrimPrefix(m[1], "this.")] = true
		}
		for _, m := range amqpJSChanType.FindAllStringSubmatch(l.Code, -1) {
			known[strings.TrimPrefix(m[1], "this.")] = true
		}
	}
	return func(recv string) bool {
		r := strings.TrimPrefix(recv, "this.")
		if known[r] {
			return true
		}
		last := strings.ToLower(r[strings.LastIndex(r, ".")+1:])
		return last == "ch" || last == "chan" || strings.Contains(last, "channel")
	}
}

// applyAmqplib: amqplib (and amqp-connection-manager) channel calls with a
// literal first argument. publish(”, 'q', ...) uses the default exchange and
// so produces to queue q.
func applyAmqplib(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "amqplib", "amqp-connection-manager") {
		return nil
	}
	isChan := amqpJSReceiver(f)
	var ops []amqpOp
	var binds []amqpBind
	for _, c := range amqpCalls(f, amqpJSCall, false) {
		if !isChan(c.receiver) {
			continue
		}
		a0, ok0 := c.param(0)
		switch c.method {
		case "assertExchange":
			ops = append(ops, amqpOp{"exchange", "declare", a0, c.line})
		case "assertQueue":
			ops = append(ops, amqpOp{"queue", "declare", a0, c.line})
		case "sendToQueue":
			ops = append(ops, amqpOp{"queue", "produce", a0, c.line})
		case "consume":
			ops = append(ops, amqpOp{"queue", "consume", a0, c.line})
		case "publish":
			key, keyOK := c.param(1)
			if op, ok := publishOp(c.line, a0, ok0, key, keyOK); ok {
				ops = append(ops, op)
			}
			continue
		case "bindQueue":
			if x, ok := c.param(1); ok0 && ok {
				binds = append(binds, amqpBind{a0, x, c.line})
			}
			continue
		}
		if !ok0 {
			ops = ops[:len(ops)-1]
		}
	}
	return amqpFacts("amqplib", f, ops, binds)
}

package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules,
		funcRule{"spring.amqp", func(p string) bool { return hasExt(p, ".java") }, applySpringAmqp},
	)
}

var (
	rabbitListenerRe = regexp.MustCompile(`@(?:[A-Za-z_]\w*\s*\.\s*)*RabbitListener\b`)
	// @Queue("q") / @Queue(value = "q") / @Queue(name = "q"), same for @Exchange.
	annoQueueRe    = regexp.MustCompile(`@(?:\w+\.)*Queue\s*\(([^()]*)\)`)
	annoExchangeRe = regexp.MustCompile(`@(?:\w+\.)*Exchange\s*\(([^()]*)\)`)

	amqpTemplateDecl = regexp.MustCompile(`\b(?:RabbitTemplate|AmqpTemplate|RabbitMessagingTemplate)\b\s+([A-Za-z_]\w*)`)
	amqpJavaSend     = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\.\s*(convertAndSend|convertSendAndReceive|send)\s*\(`)
	// beans declared in code: new Queue("q"), QueueBuilder.durable("q"),
	// new TopicExchange("x"), ExchangeBuilder.topicExchange("x").
	amqpNewQueue    = regexp.MustCompile(`\bnew\s+Queue\s*\(`)
	amqpQueueBuild  = regexp.MustCompile(`\bQueueBuilder\s*\.\s*(?:durable|nonDurable)\s*\(`)
	amqpNewExchange = regexp.MustCompile(`\bnew\s+(?:Topic|Direct|Fanout|Headers|Custom)Exchange\s*\(`)
	amqpExchBuild   = regexp.MustCompile(`\bExchangeBuilder\s*\.\s*(?:topic|direct|fanout|headers)Exchange\s*\(`)
)

// amqpTemplateReceiver accepts a receiver declared with a RabbitTemplate /
// AmqpTemplate type in this file.
func amqpTemplateReceiver(f *FileCtx, recv string) bool {
	recv = strings.TrimPrefix(recv, "this.")
	for _, l := range f.Lines {
		for _, m := range amqpTemplateDecl.FindAllStringSubmatch(l.Code, -1) {
			if m[1] == recv {
				return true
			}
		}
	}
	return false
}

// applySpringAmqp: @RabbitListener(queues = ...) and its @QueueBinding form
// consume; RabbitTemplate.convertAndSend(exchange, key, msg) produces;
// Queue/Exchange beans declare. Names must be string literals.
func applySpringAmqp(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "amqp", "rabbit") {
		return nil
	}
	var ops []amqpOp
	var binds []amqpBind
	t := newJavaText(f)
	for _, loc := range rabbitListenerRe.FindAllStringIndex(t.masked, -1) {
		a, ok := t.annoAt(loc[0])
		if !ok || !a.hasArgs {
			continue
		}
		l := f.Lines[t.lineIdx(a.pos)]
		if expr, found := javaArg(a.args, "queues"); found {
			if names, ok := javaLiterals(expr); ok {
				for _, n := range names {
					ops = append(ops, amqpOp{"queue", "consume", n, l})
				}
			}
		}
		if expr, found := javaArg(a.args, "queuesToDeclare"); found {
			for _, m := range annoQueueRe.FindAllStringSubmatch(expr, -1) {
				if n, ok := annoName(m[1]); ok {
					ops = append(ops, amqpOp{"queue", "declare", n, l}, amqpOp{"queue", "consume", n, l})
				}
			}
		}
		if expr, found := javaArg(a.args, "bindings"); found {
			var qs []string
			for _, m := range annoQueueRe.FindAllStringSubmatch(expr, -1) {
				if n, ok := annoName(m[1]); ok {
					qs = append(qs, n)
					ops = append(ops, amqpOp{"queue", "declare", n, l}, amqpOp{"queue", "consume", n, l})
				}
			}
			for _, m := range annoExchangeRe.FindAllStringSubmatch(expr, -1) {
				x, ok := annoName(m[1])
				if !ok {
					continue
				}
				ops = append(ops, amqpOp{"exchange", "declare", x, l})
				for _, q := range qs {
					binds = append(binds, amqpBind{q, x, l})
				}
			}
		}
	}
	for _, c := range amqpCalls(f, amqpJavaSend, false) {
		if len(c.args) < 3 || !amqpTemplateReceiver(f, c.receiver) {
			continue
		}
		// convertAndSend(routingKey, message, postProcessor) is not an exchange call.
		if c.method != "send" && (strings.Contains(c.args[2].text, "->") || strings.Contains(c.args[2].text, "::") || strings.Contains(c.args[2].text, "PostProcessor")) {
			continue
		}
		x, xok := c.param(0)
		k, kok := c.param(1)
		if op, ok := publishOp(c.line, x, xok, k, kok); ok {
			ops = append(ops, op)
		}
	}
	for _, d := range []struct {
		re   *regexp.Regexp
		kind string
	}{{amqpNewQueue, "queue"}, {amqpQueueBuild, "queue"}, {amqpNewExchange, "exchange"}, {amqpExchBuild, "exchange"}} {
		for i, l := range f.Lines {
			for _, loc := range d.re.FindAllStringIndex(maskQuoted(l.Code), -1) {
				args := splitCallArgs(l.Code[loc[1]:] + " " + codeWindow(f.Lines, i+1, 3))
				if len(args) == 0 {
					continue
				}
				if n, ok := strLiteral(args[0]); ok {
					ops = append(ops, amqpOp{d.kind, "declare", n, l})
				}
			}
		}
	}
	return amqpFacts("spring.amqp", f, ops, binds)
}

// annoName reads the name from @Queue/@Exchange arguments: the first
// positional argument or value=/name=.
func annoName(args string) (string, bool) {
	expr, found := javaArg(args, "value", "name")
	if !found {
		return "", false
	}
	names, ok := javaLiterals(expr)
	if !ok || len(names) != 1 {
		return "", false
	}
	return names[0], true
}

package extract

import (
	"regexp"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules,
		funcRule{"rabbitmq.dotnet", func(p string) bool { return hasExt(p, ".cs") }, applyRabbitDotnet},
	)
}

var amqpCSCall = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\??\.\s*(ExchangeDeclare|QueueDeclare|QueueBind|BasicPublish|BasicConsume)(?:Async|NoWait)?\s*\(`)

// applyRabbitDotnet: RabbitMQ.Client model/channel calls, positional or with
// named arguments (exchange:, queue:, routingKey:).
func applyRabbitDotnet(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "rabbitmq") {
		return nil
	}
	var ops []amqpOp
	var binds []amqpBind
	for _, c := range amqpCalls(f, amqpCSCall, true) {
		switch c.method {
		case "ExchangeDeclare":
			if n, ok := c.param(0, "exchange"); ok {
				ops = append(ops, amqpOp{"exchange", "declare", n, c.line})
			}
		case "QueueDeclare":
			if n, ok := c.param(0, "queue"); ok {
				ops = append(ops, amqpOp{"queue", "declare", n, c.line})
			}
		case "BasicConsume":
			if n, ok := c.param(0, "queue"); ok {
				ops = append(ops, amqpOp{"queue", "consume", n, c.line})
			}
		case "QueueBind":
			q, qok := c.param(0, "queue")
			x, xok := c.param(1, "exchange")
			if qok && xok {
				binds = append(binds, amqpBind{q, x, c.line})
			}
		case "BasicPublish":
			x, xok := c.param(0, "exchange")
			k, kok := c.param(1, "routingKey")
			if op, ok := publishOp(c.line, x, xok, k, kok); ok {
				ops = append(ops, op)
			}
		}
	}
	return amqpFacts("rabbitmq.dotnet", f, ops, binds)
}

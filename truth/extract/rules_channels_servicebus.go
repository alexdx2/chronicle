package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// Azure Service Bus clients: .NET Azure.Messaging.ServiceBus, JS
// @azure/service-bus and Python azure-servicebus. A receiver or processor on
// (topic, subscription) consumes the topic and the subscription; on a single
// name it consumes a queue. A sender produces; the SDK does not say whether
// the target is a queue or a topic, so a sender created with a plain name is a
// queue unless the call names it (topicName: / get_topic_sender).

func init() {
	Rules = append(Rules, funcRule{"servicebus", func(p string) bool { return hasExt(p, ".cs", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py") }, applyServiceBus})
}

var (
	sbCallNet = regexp.MustCompile(`\.\s*(CreateProcessor|CreateSessionProcessor|CreateReceiver|CreateSender)\s*(?:<[^()]*>)?\(`)
	sbCallJS  = regexp.MustCompile(`\.\s*(createReceiver|createSender)\s*\(`)
	sbCallPy  = regexp.MustCompile(`\.\s*(get_queue_receiver|get_subscription_receiver|get_topic_sender|get_queue_sender)\s*\(`)
)

func applyServiceBus(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "servicebus", "service-bus", "service_bus") {
		return nil
	}
	var out []truth.Fact
	emit := func(c foundCall, kind, name, side string) {
		if fc, ok := channelFact("servicebus", f, f.Lines[c.idx], kind, name, side); ok {
			out = append(out, fc)
		}
	}
	lit := func(c foundCall, i int, kws ...string) string {
		a, ok := c.args.arg(i, kws...)
		if !ok {
			return ""
		}
		return plainLiteral(a)
	}
	switch {
	case hasExt(f.Path, ".py"):
		for _, c := range findCalls(f, sbCallPy, 4) {
			if !c.closed {
				continue
			}
			switch c.name {
			case "get_queue_receiver":
				emit(c, "queue", lit(c, 0, "queue_name"), "consume")
			case "get_queue_sender":
				emit(c, "queue", lit(c, 0, "queue_name"), "produce")
			case "get_topic_sender":
				emit(c, "topic", lit(c, 0, "topic_name"), "produce")
			case "get_subscription_receiver":
				if t, s := lit(c, 0, "topic_name"), lit(c, 1, "subscription_name"); t != "" && s != "" {
					emit(c, "topic", t, "consume")
					emit(c, "subscription", s, "consume")
				}
			}
		}
	default:
		re := sbCallNet
		if !hasExt(f.Path, ".cs") {
			re = sbCallJS
		}
		for _, c := range findCalls(f, re, 4) {
			if !c.closed {
				continue
			}
			switch strings.ToLower(c.name[:1]) + c.name[1:] {
			case "createSender":
				switch {
				case lit(c, -1, "topicName") != "":
					emit(c, "topic", lit(c, -1, "topicName"), "produce")
				default:
					emit(c, "queue", lit(c, 0, "queueName"), "produce")
				}
			default: // processors and receivers
				if t := lit(c, -1, "topicName"); t != "" {
					if s := lit(c, 1, "subscriptionName"); s != "" {
						emit(c, "topic", t, "consume")
						emit(c, "subscription", s, "consume")
					}
					continue
				}
				if q := lit(c, -1, "queueName"); q != "" {
					emit(c, "queue", q, "consume")
					continue
				}
				first := lit(c, 0)
				if first == "" {
					continue
				}
				second, has := c.args.arg(1)
				if s := plainLiteral(second); has && s != "" {
					emit(c, "topic", first, "consume")
					emit(c, "subscription", s, "consume")
				} else if !has || sbOptionsArg(second) {
					emit(c, "queue", first, "consume")
				}
			}
		}
	}
	return out
}

// sbOptionsArg reports whether an argument is an options object rather than a
// subscription name held in a variable.
func sbOptionsArg(a string) bool {
	a = strings.TrimSpace(a)
	l := strings.ToLower(a)
	return strings.HasPrefix(a, "{") || strings.HasPrefix(l, "new ") || l == "null" || l == "undefined" || strings.Contains(l, "option")
}

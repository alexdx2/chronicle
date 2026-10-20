package extract

import (
	"regexp"

	"github.com/alexdx2/chronicle-core/truth"
)

// AWS CDK event buses: new events.EventBus(this, 'id', { eventBusName: 'b' })
// in TypeScript/JavaScript and events.EventBus(self, "id", event_bus_name="b")
// in Python declare a bus. (Queues and topics are in rules_channels_infra.go.)

func init() {
	Rules = append(Rules, funcRule{"cdk.eventbus", func(p string) bool { return hasExt(p, ".ts", ".js", ".py") }, applyCDKEventBus})
}

var cdkEventBusCall = regexp.MustCompile(`\b(?:new\s+)?(?:[A-Za-z_]*events\.)?(EventBus)\s*\(`)

var cdkNewEventBus = regexp.MustCompile(`\bnew\s+(?:[A-Za-z_]*events\.)?EventBus\s*\(`)

func applyCDKEventBus(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "aws-cdk-lib", "aws_cdk", "@aws-cdk/") {
		return nil
	}
	py := hasExt(f.Path, ".py")
	var out []truth.Fact
	for _, c := range findCalls(f, cdkEventBusCall, 8) {
		if !c.closed {
			continue
		}
		// Only the constructor: new EventBus(...) in TS, events.EventBus(...) in
		// Python. Static helpers (EventBus.fromEventBusName) never match the
		// regex because the name is followed by "(".
		code := f.Lines[c.idx].Code
		if !py && !cdkNewEventBus.MatchString(code) {
			continue
		}
		var name string
		if py {
			a, ok := c.args.arg(-1, "event_bus_name")
			if !ok {
				continue
			}
			name = plainLiteral(a)
		} else {
			if len(c.args.pos) < 3 {
				continue
			}
			name = objectPropLiteral(c.args.pos[2], "eventBusName")
		}
		if fc, ok := channelFact("cdk.eventbus", f, f.Lines[c.idx], "bus", name, "declare"); ok {
			out = append(out, fc)
		}
	}
	return out
}

// objectPropLiteral returns the string literal value of a top-level property
// of an object-literal argument `{ a: 1, key: 'v', nested: { key: 'x' } }`.
func objectPropLiteral(obj, key string) string {
	if len(obj) < 2 || obj[0] != '{' {
		return ""
	}
	args, ok := splitCloudCallArgs("("+obj[1:len(obj)-1]+")", 0)
	if !ok {
		return ""
	}
	if v, found := parseArgs(args).kw[key]; found {
		return plainLiteral(v)
	}
	return ""
}

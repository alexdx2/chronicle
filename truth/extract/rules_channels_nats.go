package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// NATS and JetStream. Subjects are kind "subject": publish/request produce,
// subscribe variants consume. A JetStream stream declaration (StreamConfig
// Name, add_stream, streams.add) is kind "stream", side declare. Only literal
// names count; the subjects listed inside a stream config are not facts.
// Go (nats.go and the jetstream package), JavaScript/TypeScript (nats.js) and
// Python (nats-py) are covered; a file must import the client.

var (
	natsGoImport = regexp.MustCompile(`nats-io/nats\.go`)
	natsJSImport = chJSImport(`nats|nats\.ws|@nats-io/[\w.-]+`)
	natsPyImport = chPyImport(`nats`)

	natsGoStream = regexp.MustCompile(`\b(?:nats|jetstream|jetstreamapi)\.StreamConfig\{`)
	natsGoMsg    = regexp.MustCompile(`\bnats\.Msg\{`)
	natsGoCtx    = regexp.MustCompile(`^(?:ctx|context\.\w+\(.*\)|\w*[cC]tx|\w*[cC]ontext)$`)
	natsJSName   = regexp.MustCompile(`\bname\s*:\s*(['"])([^'"]+)['"]`)
	natsPyName   = regexp.MustCompile(`\bname\s*=\s*(['"])([^'"]+)['"]`)
)

func init() {
	Rules = append(Rules,
		funcRule{"nats.go", func(p string) bool { return hasExt(p, ".go") }, applyNatsGo},
		funcRule{"nats.js", func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }, applyNatsJS},
		funcRule{"nats.py", func(p string) bool { return hasExt(p, ".py") }, applyNatsPy},
	)
}

// natsGoSubject reads the subject of a Go call: the first argument, or the
// second when the first is a context (jetstream package: js.Publish(ctx, "s", b)).
func natsGoSubject(c chCall) []string {
	for i := 0; i < len(c.args) && i < 2; i++ {
		if s, ok := chLit(c.args[i]); ok {
			return []string{s}
		}
		if i == 0 && natsGoCtx.MatchString(c.args[0]) {
			continue
		}
		break
	}
	return nil
}

func applyNatsGo(f *FileCtx) []truth.Fact {
	if f.IsTest || !chImports(natsGoImport)(f) {
		return nil
	}
	out := chRule("nats.go", chImports(natsGoImport),
		chSpec{names: `Publish|PublishAsync|Request|RequestWithContext`, kind: "subject", side: "produce", pick: natsGoSubject},
		chSpec{names: `Subscribe|SubscribeSync|QueueSubscribe|QueueSubscribeSync|ChanSubscribe|ChanQueueSubscribe|PullSubscribe`, kind: "subject", side: "consume", pick: natsGoSubject},
	)(f)
	// nats.NewMsg("s") builds a message for PublishMsg.
	for _, c := range chCalls(f, `NewMsg`) {
		if strings.HasSuffix(c.recv, "nats") {
			for _, n := range chFirst("")(c) {
				if fc, ok := channelFact("nats.go", f, c.line, "subject", n, "produce"); ok {
					out = append(out, fc)
				}
			}
		}
	}
	goComposites(f, natsGoStream, "Name", func(l lex.Line, expr string) {
		if m := goStringLit.FindStringSubmatch(strings.TrimSpace(expr)); m != nil {
			if fc, ok := channelFact("nats.go", f, l, "stream", m[1], "declare"); ok {
				out = append(out, fc)
			}
		}
	})
	goComposites(f, natsGoMsg, "Subject", func(l lex.Line, expr string) {
		if m := goStringLit.FindStringSubmatch(strings.TrimSpace(expr)); m != nil {
			if fc, ok := channelFact("nats.go", f, l, "subject", m[1], "produce"); ok {
				out = append(out, fc)
			}
		}
	})
	return out
}

// natsDeclared reads `name: 'X'` (re) out of the arguments of an add-stream call.
func natsDeclared(re *regexp.Regexp) func(c chCall) []string {
	return func(c chCall) []string {
		for _, a := range c.args {
			if m := re.FindStringSubmatch(a); m != nil {
				return []string{m[2]}
			}
		}
		return nil
	}
}

func applyNatsJS(f *FileCtx) []truth.Fact {
	return chRule("nats.js", chImports(natsJSImport),
		chSpec{names: `publish|request|requestMany`, kind: "subject", side: "produce", pick: chFirst("")},
		chSpec{names: `subscribe`, kind: "subject", side: "consume", pick: chFirst("")},
		chSpec{names: `add`, kind: "stream", side: "declare", pick: func(c chCall) []string {
			if !strings.HasSuffix(c.recv, "streams") {
				return nil
			}
			return natsDeclared(natsJSName)(c)
		}},
	)(f)
}

func applyNatsPy(f *FileCtx) []truth.Fact {
	return chRule("nats.py", chImports(natsPyImport),
		chSpec{names: `publish|request`, kind: "subject", side: "produce", pick: chFirst("subject")},
		chSpec{names: `subscribe|pull_subscribe`, kind: "subject", side: "consume", pick: chFirst("subject")},
		chSpec{names: `add_stream|update_stream`, kind: "stream", side: "declare", pick: natsDeclared(natsPyName)},
	)(f)
}

package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Google Cloud Pub/Sub clients. Python (google-cloud-pubsub): a topic path
// from topic_path("project", "t") or a "projects/p/topics/t" literal, passed
// to publish, produces the topic; a subscription path passed to subscribe or
// pull consumes the subscription. The primary evidence is the line that
// defines the path, the derived evidence the line that uses it. Node
// (@google-cloud/pubsub): topic('t').publish* produces, and
// subscription('s').on('message') consumes.

func init() {
	Rules = append(Rules, funcRule{"pubsub", func(p string) bool { return hasExt(p, ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }, applyPubSub})
}

func applyPubSub(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	if hasExt(f.Path, ".py") {
		if !codeContains(f, "pubsub") {
			return nil
		}
		return pubsubPython(f)
	}
	if !codeContains(f, "@google-cloud/pubsub") {
		return nil
	}
	return pubsubNode(f)
}

// psPath is a resolved Pub/Sub path: kind is topic or subscription.
type psPath struct {
	kind, name string
	line       int // 0-based line that defines it
}

var (
	psAssign   = regexp.MustCompile(`^\s*([A-Za-z_][\w.]*)\s*(?::[^=]+)?=\s*([^=].*)$`)
	psFullPath = regexp.MustCompile(`^[fF]?(["'])projects/[^/"']+/(topics|subscriptions)/([^/"']+)["']$`)
	psPathCall = regexp.MustCompile(`^(?:[A-Za-z_][\w.]*\.)?(topic|subscription)_path\s*\(`)
	psUse      = regexp.MustCompile(`\.\s*(publish|subscribe|pull|streaming_pull)\s*\(`)
	psReqKey   = regexp.MustCompile(`['"]subscription['"]\s*:\s*([^,}]+)`)
)

func psKind(plural string) string {
	if plural == "topics" {
		return "topic"
	}
	return "subscription"
}

// psResolve resolves an expression to a path: a full-path literal, a
// topic_path/subscription_path call, or a variable assigned earlier.
func psResolve(expr, window string, vars map[string]psPath, line int) (psPath, bool) {
	expr = strings.TrimSpace(expr)
	if m := psFullPath.FindStringSubmatch(expr); m != nil {
		return psPath{psKind(m[2]), m[3], line}, true
	}
	if m := psPathCall.FindStringSubmatch(expr); m != nil {
		open := strings.IndexByte(window, '(')
		if i := strings.Index(window, m[0]); i >= 0 {
			open = i + len(m[0]) - 1
		}
		args, ok := splitCloudCallArgs(window, open)
		if !ok {
			return psPath{}, false
		}
		p := parseArgs(args)
		a, found := p.arg(1, m[1])
		if n := plainLiteral(a); found && n != "" {
			return psPath{m[1], n, line}, true
		}
		return psPath{}, false
	}
	if p, ok := vars[expr]; ok {
		return p, true
	}
	return psPath{}, false
}

func pubsubPython(f *FileCtx) []truth.Fact {
	vars := map[string]psPath{}
	uses := map[int][]foundCall{}
	for _, c := range findCalls(f, psUse, 4) {
		uses[c.idx] = append(uses[c.idx], c)
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		// Uses on this line see the assignments of earlier lines (and of this
		// line's own right-hand side only through inline path calls).
		for _, c := range uses[i] {
			if !c.closed {
				continue
			}
			var expr string
			var ok bool
			switch c.name {
			case "publish":
				expr, ok = c.args.arg(0, "topic")
			case "subscribe", "streaming_pull":
				expr, ok = c.args.arg(0, "subscription")
			case "pull":
				if expr, ok = c.args.arg(-1, "subscription"); !ok {
					if req, has := c.args.arg(-1, "request"); has {
						if m := psReqKey.FindStringSubmatch(req); m != nil {
							expr, ok = m[1], true
						}
					}
				}
				if !ok {
					expr, ok = c.args.arg(0)
				}
			}
			if !ok {
				continue
			}
			p, found := psResolve(expr, c.text, vars, i)
			if !found {
				continue
			}
			side, kind := "produce", "topic"
			if c.name != "publish" {
				side, kind = "consume", "subscription"
			}
			if p.kind != kind {
				continue
			}
			var derived []truth.Evidence
			if p.line != i {
				derived = append(derived, evidence(f, l))
			}
			if fc, ok := channelFact("pubsub", f, f.Lines[p.line], kind, p.name, side, derived...); ok {
				out = append(out, fc)
			}
		}
		if m := psAssign.FindStringSubmatch(l.Code); m != nil {
			if p, ok := psResolve(m[2], codeWindow(f.Lines, i, 3)[strings.Index(l.Code, m[2]):], vars, i); ok {
				vars[m[1]] = p
			} else {
				delete(vars, m[1])
			}
		}
	}
	return out
}

var (
	psNodeChain  = regexp.MustCompile(`\.\s*(topic|subscription)\s*\(\s*[` + "`" + `'"]([^` + "`" + `'"]+)[` + "`" + `'"][^()]*\)\s*\.\s*(publish\w*|on\s*\(\s*['"]message['"])`)
	psNodeAssign = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*=\s*[\w.]*\.\s*(topic|subscription)\s*\(\s*[` + "`" + `'"]([^` + "`" + `'"]+)[` + "`" + `'"][^()]*\)\s*(?:;|[^.\s]|$)`)
	psNodeUse    = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\.\s*(publish\w*|on\s*\(\s*['"]message['"])`)
	psNodeName   = regexp.MustCompile(`^projects/[^/]+/(?:topics|subscriptions)/([^/]+)$`)
)

func psNodeSide(kind, verb string) (string, bool) {
	switch {
	case kind == "topic" && strings.HasPrefix(verb, "publish"):
		return "produce", true
	case kind == "subscription" && strings.HasPrefix(verb, "on"):
		return "consume", true
	}
	return "", false
}

func pubsubNode(f *FileCtx) []truth.Fact {
	type decl struct {
		kind, name string
		line       int
	}
	vars := map[string]decl{}
	var out []truth.Fact
	emit := func(kind, name, side string, def int, use lex.Line, useIdx int) {
		if m := psNodeName.FindStringSubmatch(name); m != nil {
			name = m[1]
		}
		var derived []truth.Evidence
		if def != useIdx {
			derived = append(derived, evidence(f, use))
		}
		if fc, ok := channelFact("pubsub", f, f.Lines[def], kind, name, side, derived...); ok {
			out = append(out, fc)
		}
	}
	for i, l := range f.Lines {
		win := codeWindow(f.Lines, i, 3)
		if m := psNodeChain.FindStringSubmatchIndex(win); m != nil && m[0] < len(l.Code) {
			kind, name, verb := win[m[2]:m[3]], win[m[4]:m[5]], win[m[6]:m[7]]
			if side, ok := psNodeSide(kind, verb); ok {
				emit(kind, name, side, i, l, i)
			}
		}
		for _, u := range psNodeUse.FindAllStringSubmatch(l.Code, -1) {
			if d, ok := vars[u[1]]; ok {
				if side, ok := psNodeSide(d.kind, u[2]); ok {
					emit(d.kind, d.name, side, d.line, l, i)
				}
			}
		}
		if loc := psNodeAssign.FindStringSubmatchIndex(win); loc != nil && loc[0] < len(l.Code) {
			vars[win[loc[2]:loc[3]]] = decl{win[loc[4]:loc[5]], win[loc[6]:loc[7]], i}
		}
	}
	return out
}

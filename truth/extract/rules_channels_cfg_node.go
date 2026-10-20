package extract

import (
	"path"
	"regexp"
	"strings"
)

// TypeScript / JavaScript: the channel is process.env.NAME || 'default', or a
// constant, field or config-object property assigned from that, in this file
// or in a module it imports (relative imports and src/ aliases, same repo).

var (
	jsEnvRead = regexp.MustCompile("^(?:process\\.env\\.\\w+|process\\.env\\s*\\[\\s*['\"`][^'\"`]+['\"`]\\s*\\]|import\\.meta\\.env\\.\\w+|Deno\\.env\\.get\\(\\s*['\"`][^'\"`]+['\"`]\\s*\\))$")
	// configService.get<string>('NAME', 'default')
	jsCfgGet    = regexp.MustCompile("^[\\w$.]*(?:[cC]onfig|[eE]nv)\\w*(?:\\??\\.get(?:OrThrow)?)(?:<[^>]*>)?\\(\\s*['\"`][^'\"`]+['\"`]\\s*(?:,\\s*(['\"`][^'\"`]*['\"`])\\s*)?\\)$")
	jsTSTail    = regexp.MustCompile(`\s+as\s+[\w.<>\[\] |]+$|!$|\.toString\(\)$`)
	jsWrapCall  = regexp.MustCompile(`^(?:String|\w+\.toString)\((.*)\)$`)
	jsChain     = regexp.MustCompile(`^(?:this\.)?[\w$]+(?:\??\.[\w$]+)*$`)
	jsImportRel = regexp.MustCompile("(?:\\bfrom\\s*|\\brequire\\s*\\(\\s*|\\bimport\\s*\\(\\s*|\\bimport\\s+)['\"]([^'\"]+)['\"]")
	jsImportAs  = regexp.MustCompile(`([\w$]+)\s+as\s+([\w$]+)`)
	jsDestruct  = regexp.MustCompile(`\b(?:const|let|var)\s*\{([^}]*)\}\s*=\s*(?:process\.env|import\.meta\.env)\b`)
)

type jsRes struct {
	f  *FileCtx
	ct *codeText
}

func (r *jsRes) resolve(expr string) (cfgVal, bool) {
	return jsEval(r.f, newCodeTextOf(r.f, r.ct), expr, 0, map[string]bool{})
}

func newCodeTextOf(f *FileCtx, ct *codeText) *codeText {
	if ct != nil {
		return ct
	}
	return newCodeText(f.Lines)
}

// jsEnvOperand reports whether op reads the environment or configuration and
// returns its default literal when it has one.
func jsEnvOperand(op string) (def string, hasDef, isEnv bool) {
	op = strings.TrimSpace(op)
	for strings.HasPrefix(op, "(") && strings.HasSuffix(op, ")") {
		op = strings.TrimSpace(op[1 : len(op)-1])
	}
	if jsEnvRead.MatchString(op) {
		return "", false, true
	}
	if m := jsCfgGet.FindStringSubmatch(op); m != nil {
		if d, ok := quotedLit(m[1]); ok && m[1] != "" {
			return d, true, true
		}
		return "", false, true
	}
	return "", false, false
}

func jsEval(f *FileCtx, ct *codeText, expr string, depth int, seen map[string]bool) (cfgVal, bool) {
	if depth > 4 {
		return cfgVal{}, false
	}
	expr = strings.TrimSpace(expr)
	for {
		n := jsTSTail.ReplaceAllString(expr, "")
		if m := jsWrapCall.FindStringSubmatch(n); m != nil {
			n = m[1]
		}
		n = strings.TrimSpace(n)
		if n == expr {
			break
		}
		expr = n
	}
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") && len(splitCfgTop(expr[1:len(expr)-1], "||", "??")) > 0 {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	ops := splitCfgTop(expr, "||", "??")
	if len(ops) > 1 {
		// env || env || 'default'
		var def string
		for i, op := range ops {
			d, has, isEnv := jsEnvOperand(op)
			switch {
			case i == len(ops)-1:
				lit, ok := quotedLit(op)
				switch {
				case ok:
					def = lit
				case has:
					def = d
				default:
					return cfgVal{}, false
				}
			case !isEnv:
				return cfgVal{}, false
			}
		}
		return cfgVal{val: def}, true
	}
	if d, has, isEnv := jsEnvOperand(expr); isEnv {
		if has {
			return cfgVal{val: d}, true
		}
		return cfgVal{}, false
	}
	if lit, ok := quotedLit(expr); ok && depth > 0 {
		// a name defined elsewhere as a plain string (eventsExchange: 'orders.events')
		return cfgVal{val: lit}, true
	}
	if !jsChain.MatchString(expr) {
		return cfgVal{}, false
	}
	segs := strings.Split(strings.ReplaceAll(expr, "?.", "."), ".")
	return jsName(f, ct, segs[len(segs)-1], len(segs) > 1 && segs[0] != "this", depth+1, seen)
}

// jsName resolves a name: its definitions in this file, then in imported
// modules of the same repo.
func jsName(f *FileCtx, ct *codeText, name string, member bool, depth int, seen map[string]bool) (cfgVal, bool) {
	if seen[f.Path+"#"+name] || depth > 4 {
		return cfgVal{}, false
	}
	seen[f.Path+"#"+name] = true
	if ct == nil {
		ct = newCodeText(f.Lines)
	}
	vs, found := jsLocalDefs(f, ct, name, depth, seen)
	// A member access (config.topic) whose local definitions resolve to
	// nothing may be defined in the module that exports the object.
	if found && (len(vs) > 0 || !member) {
		return distinctVal(vs)
	}
	var out []cfgVal
	for _, imp := range jsImports(f, ct, name) {
		g := loadFileCtx(f, imp.path)
		if g == nil {
			continue
		}
		if v, ok := jsName(g, nil, imp.orig, member, depth+1, seen); ok {
			out = append(out, v)
		}
	}
	return distinctVal(out)
}

// jsLocalDefs evaluates the definitions of name in the file. found tells
// whether any definition exists, resolvable or not.
func jsLocalDefs(f *FileCtx, ct *codeText, name string, depth int, seen map[string]bool) (vs []cfgVal, found bool) {
	q := regexp.QuoteMeta(name)
	assign := regexp.MustCompile(`(?:^|[^\w$.])(?:[\w$]+\s*\.\s*)*` + q + `\s*(?::\s*[^=,;{}()\n]+?)?\s*=(?:[^=>]|$)`)
	prop := regexp.MustCompile(`(?:^|[^\w$.?])` + q + `\s*:\s*`)
	add := func(rhsStart, defOff int) {
		found = true
		rhs := jsRHS(ct.text, rhsStart)
		v, ok := jsEval(f, ct, rhs, depth+1, seen)
		if !ok {
			return
		}
		if v.ev == nil {
			v.ev = evAt(f, f.Lines[ct.lineIdx(defOff)].Num)
		}
		vs = append(vs, v)
	}
	for _, m := range assign.FindAllStringIndex(ct.text, -1) {
		add(m[1]-1, m[0])
	}
	for _, m := range prop.FindAllStringIndex(ct.text, -1) {
		if i := m[0]; i < len(ct.text) && !ct.mask[m[1]-1] {
			add(m[1], i)
		}
	}
	for _, m := range jsDestruct.FindAllStringSubmatchIndex(ct.text, -1) {
		for _, a := range splitArgs(ct.text, m[2]) {
			ent := ct.text[a.s:a.e]
			local, def := ent, ""
			if i := strings.Index(ent, "="); i >= 0 {
				local, def = strings.TrimSpace(ent[:i]), strings.TrimSpace(ent[i+1:])
			}
			if i := strings.Index(local, ":"); i >= 0 {
				local = strings.TrimSpace(local[i+1:])
			}
			if local != name {
				continue
			}
			found = true
			if lit, ok := quotedLit(def); ok {
				vs = append(vs, cfgVal{val: lit, ev: evAt(f, f.Lines[ct.lineIdx(a.s)].Num)})
			}
		}
	}
	return vs, found
}

// jsRHS returns the expression starting at off: up to a depth-0 , ; or
// closing bracket, or a line end that does not continue the expression.
func jsRHS(text string, off int) string {
	depth := 0
	var quote byte
	i := off
	for ; i < len(text) && i < off+1500; i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth == 0 {
				return text[off:i]
			}
			depth--
		case depth == 0 && (c == ',' || c == ';'):
			return text[off:i]
		case depth == 0 && c == '\n':
			cur := strings.TrimSpace(text[off:i])
			rest := strings.TrimLeft(text[i:], " \t\r\n")
			if strings.HasSuffix(cur, "||") || strings.HasSuffix(cur, "??") ||
				strings.HasPrefix(rest, "||") || strings.HasPrefix(rest, "??") || cur == "" {
				continue
			}
			return text[off:i]
		}
	}
	return text[off:i]
}

type jsImport struct {
	path string // tree path of the module file
	orig string // name to look for in it
}

var jsExts = []string{".ts", ".tsx", ".js", ".mjs", ".cjs", ".jsx", "/index.ts", "/index.js"}

// jsImports lists the in-repo modules the file imports; a name imported under
// an alias is looked up under its original name.
func jsImports(f *FileCtx, ct *codeText, name string) []jsImport {
	orig := name
	for _, m := range regexp.MustCompile(`\{([^}]*)\}`).FindAllStringSubmatch(ct.text, -1) {
		for _, a := range jsImportAs.FindAllStringSubmatch(m[1], -1) {
			if strings.Contains(a[0], ":") { // destructuring rename `a: b`
				continue
			}
			if a[2] == name {
				orig = a[1]
			}
		}
	}
	var out []jsImport
	seen := map[string]bool{}
	for _, m := range jsImportRel.FindAllStringSubmatch(ct.text, -1) {
		spec := m[1]
		var bases []string
		switch {
		case strings.HasPrefix(spec, "."):
			bases = []string{path.Join(f.Dir, spec)}
		case strings.HasPrefix(spec, "@/"), strings.HasPrefix(spec, "~/"):
			bases = []string{spec[2:], path.Join("src", spec[2:])}
		case strings.HasPrefix(spec, "src/"):
			bases = []string{spec}
		default:
			continue
		}
		for _, b := range bases {
			for _, ext := range append([]string{""}, jsExts...) {
				p := path.Clean(b + ext)
				if _, ok := f.Tree.ReadFile(p); ok && !seen[p] && strings.Contains(path.Base(p), ".") {
					seen[p] = true
					out = append(out, jsImport{p, orig})
					break
				}
			}
		}
	}
	return out
}

var (
	jsHasKafka = func(f *FileCtx) bool { return codeContains(f, "kafka") }
	jsHasAmqp  = func(f *FileCtx) bool { return codeContains(f, "amqp") }
	jsHasNats  = func(f *FileCtx) bool { return codeContains(f, "nats") && !codeContains(f, "amqp") }
	jsHasNest  = func(f *FileCtx) bool { return codeContains(f, "@nestjs/microservices") }
)

func init() {
	isJS := func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }
	newRes := func(f *FileCtx, ct *codeText) cfgResolver { return &jsRes{f: f, ct: ct} }
	gateIf := func(g func(f *FileCtx) bool) func(f *FileCtx, recv string) bool {
		return func(f *FileCtx, _ string) bool { return g(f) }
	}
	call := func(re string, kind, side string, g func(f *FileCtx) bool) cfgCall {
		return cfgCall{re: regexp.MustCompile(re), kind: kind, side: side, gate: gateIf(g)}
	}
	kafka := []cfgCall{
		{re: regexp.MustCompile(`\.\s*subscribe\s*\(`), kind: "topic", side: "consume", obj: true, kws: []string{"topic", "topics"}, gate: gateIf(jsHasKafka)},
		{re: regexp.MustCompile(`\.\s*send\s*\(`), kind: "topic", side: "produce", obj: true, kws: []string{"topic"}, gate: gateIf(jsHasKafka)},
		call(`@(?:EventPattern|MessagePattern)\s*\(`, "topic", "consume", jsHasNest),
	}
	amqp := []cfgCall{
		call(`\.\s*assertQueue\s*\(`, "queue", "declare", jsHasAmqp),
		call(`\.\s*sendToQueue\s*\(`, "queue", "produce", jsHasAmqp),
		call(`\.\s*consume\s*\(`, "queue", "consume", jsHasAmqp),
		call(`\.\s*assertExchange\s*\(`, "exchange", "declare", jsHasAmqp),
		call(`\.\s*publish\s*\(`, "exchange", "produce", jsHasAmqp),
	}
	nats := []cfgCall{
		call(`\.\s*publish\s*\(`, "subject", "produce", jsHasNats),
		call(`\.\s*request\s*\(`, "subject", "produce", jsHasNats),
		call(`\.\s*subscribe\s*\(`, "subject", "consume", jsHasNats),
	}
	Rules = append(Rules,
		funcRule{"kafkajs.config", isJS, cfgChannelRule("kafkajs.config", nil, kafka, newRes)},
		funcRule{"amqp.js.config", isJS, cfgChannelRule("amqp.js.config", nil, amqp, newRes)},
		funcRule{"nats.js.config", isJS, cfgChannelRule("nats.js.config", nil, nats, newRes)},
	)
}

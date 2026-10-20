package extract

import (
	"path"
	"regexp"
	"strings"
)

// Python: os.getenv("NAME", "default"), os.environ.get(...), environ-style
// helpers, directly or through a module-level constant / settings attribute
// defined in this file or in an imported module of the same repo.

var (
	pyEnvGet = regexp.MustCompile(`^(?:os\.)?(?:environ\.get|getenv)\(\s*(['"])[^'"]+['"]\s*(?:,\s*(?:default\s*=\s*)?(None|'[^']*'|"[^"]*")\s*)?\)$`)
	pyEnvSub = regexp.MustCompile(`^(?:os\.)?environ\[\s*['"][^'"]+['"]\s*\]$`)
	pyEnvLib = regexp.MustCompile(`^(?:[\w.]*env|config)(?:\.str)?\(\s*['"][^'"]+['"]\s*(?:,\s*default\s*=\s*('[^']*'|"[^"]*")\s*)?\)$`)
	pyChain  = regexp.MustCompile(`^(?:self\.|cls\.)?\w+(?:\.\w+)*$`)
	pyStrFn  = regexp.MustCompile(`^str\((.*)\)$`)
	pyFrom   = regexp.MustCompile(`(?m)^[ \t]*from[ \t]+(\.*)([\w.]*)[ \t]+import[ \t]+(\(?[^\n]*)`)
	pyImport = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+([\w.]+)`)
)

type pyRes struct {
	f  *FileCtx
	ct *codeText
}

func (r *pyRes) resolve(expr string) (cfgVal, bool) {
	return pyEval(r.f, r.ct, expr, 0, map[string]bool{})
}

func pyEnvOperand(op string) (def string, hasDef, isEnv bool) {
	op = strings.TrimSpace(op)
	if m := pyEnvGet.FindStringSubmatch(op); m != nil {
		if d, ok := quotedLit(m[2]); ok && m[2] != "" {
			return d, true, true
		}
		return "", false, true
	}
	if pyEnvSub.MatchString(op) {
		return "", false, true
	}
	if m := pyEnvLib.FindStringSubmatch(op); m != nil {
		if d, ok := quotedLit(m[1]); ok && m[1] != "" {
			return d, true, true
		}
		return "", false, true
	}
	return "", false, false
}

func pyEval(f *FileCtx, ct *codeText, expr string, depth int, seen map[string]bool) (cfgVal, bool) {
	if depth > 4 {
		return cfgVal{}, false
	}
	expr = strings.TrimSpace(expr)
	if m := pyStrFn.FindStringSubmatch(expr); m != nil {
		expr = strings.TrimSpace(m[1])
	}
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	if ops := splitCfgTop(expr, " or "); len(ops) > 1 {
		var def string
		for i, op := range ops {
			d, has, isEnv := pyEnvOperand(op)
			switch {
			case i == len(ops)-1:
				if lit, ok := quotedLit(op); ok {
					def = lit
				} else if has {
					def = d
				} else {
					return cfgVal{}, false
				}
			case !isEnv:
				return cfgVal{}, false
			}
		}
		return cfgVal{val: def}, true
	}
	if d, has, isEnv := pyEnvOperand(expr); isEnv {
		if has {
			return cfgVal{val: d}, true
		}
		return cfgVal{}, false
	}
	if !pyChain.MatchString(expr) {
		return cfgVal{}, false
	}
	segs := strings.Split(expr, ".")
	return pyName(f, ct, segs[len(segs)-1], len(segs) > 1 && segs[0] != "self" && segs[0] != "cls", depth+1, seen)
}

func pyName(f *FileCtx, ct *codeText, name string, member bool, depth int, seen map[string]bool) (cfgVal, bool) {
	if seen[f.Path+"#"+name] || depth > 4 {
		return cfgVal{}, false
	}
	seen[f.Path+"#"+name] = true
	if ct == nil {
		ct = newCodeText(f.Lines)
	}
	def := regexp.MustCompile(`(?m)^[ \t]*(?:(?:self|cls|\w+)\.)*` + regexp.QuoteMeta(name) + `[ \t]*(?::[^=\n]+)?=[ \t]*([^=\n][^\n]*|\n)`)
	var vs []cfgVal
	found := false
	for _, m := range def.FindAllStringSubmatchIndex(ct.text, -1) {
		found = true
		rhs := pyRHS(ct.text, m[2])
		if v, ok := pyEval(f, ct, rhs, depth+1, seen); ok {
			if v.ev == nil {
				v.ev = evAt(f, f.Lines[ct.lineIdx(m[0])].Num)
			}
			vs = append(vs, v)
		}
	}
	if found && (len(vs) > 0 || !member) {
		return distinctVal(vs)
	}
	var out []cfgVal
	for _, p := range pyModules(f, ct, name) {
		g := loadFileCtx(f, p)
		if g == nil {
			continue
		}
		if v, ok := pyName(g, nil, name, member, depth+1, seen); ok {
			out = append(out, v)
		}
	}
	return distinctVal(out)
}

// pyRHS returns the expression at off, continued while brackets are open.
func pyRHS(text string, off int) string {
	depth := 0
	var quote byte
	for i := off; i < len(text) && i < off+1500; i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == '\n' && depth <= 0:
			return text[off:i]
		}
	}
	return text[off:]
}

// pyModules lists in-repo module files the file imports.
func pyModules(f *FileCtx, ct *codeText, name string) []string {
	var cands []string
	addMod := func(base, mod string) {
		m := strings.ReplaceAll(mod, ".", "/")
		cands = append(cands, path.Join(base, m)+".py", path.Join(base, m, "__init__.py"))
	}
	bases := []string{""}
	for d := f.Dir; d != "." && d != "/" && d != ""; d = path.Dir(d) {
		bases = append(bases, d)
	}
	for _, m := range pyFrom.FindAllStringSubmatch(ct.text, -1) {
		dots, mod, names := m[1], m[2], m[3]
		var bs []string
		if dots != "" {
			d := f.Dir
			for i := 1; i < len(dots); i++ {
				d = path.Dir(d)
			}
			bs = []string{d}
		} else {
			bs = bases
		}
		for _, b := range bs {
			if mod != "" {
				addMod(b, mod)
			}
			for _, n := range regexp.MustCompile(`\w+`).FindAllString(names, -1) {
				if mod == "" {
					addMod(b, n)
				} else {
					addMod(path.Join(b, strings.ReplaceAll(mod, ".", "/")), n)
				}
			}
		}
	}
	for _, m := range pyImport.FindAllStringSubmatch(ct.text, -1) {
		for _, b := range bases {
			addMod(b, m[1])
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range cands {
		c = path.Clean(c)
		if seen[c] {
			continue
		}
		seen[c] = true
		if _, ok := f.Tree.ReadFile(c); ok {
			out = append(out, c)
		}
	}
	return out
}

var pyAWSKafka = regexp.MustCompile(`(\w[\w.]*)\s*=\s*(?:await\s+)?(?:\w+\.)?(?:AIO)?(?:Kafka)?(?:Producer|Consumer|SerializingProducer|DeserializingConsumer)\(`)

// pyKafkaRecv accepts the receivers pyKafkaReceiver does, plus confluent
// Producer/Consumer instances.
func pyKafkaRecv(f *FileCtx, recv string) bool {
	if pyKafkaReceiver(f, recv) {
		return true
	}
	for _, l := range f.Lines {
		if m := pyAWSKafka.FindStringSubmatch(l.Code); m != nil && m[1] == recv {
			return true
		}
	}
	return false
}

func init() {
	isPy := func(p string) bool { return hasExt(p, ".py") }
	newRes := func(f *FileCtx, ct *codeText) cfgResolver { return &pyRes{f: f, ct: ct} }
	has := func(subs ...string) func(f *FileCtx, recv string) bool {
		return func(f *FileCtx, _ string) bool { return codeContains(f, subs...) }
	}
	kafka := []cfgCall{
		{re: regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\.\s*(?:send|produce)\s*\(`), kind: "topic", side: "produce", kws: []string{"topic"}, gate: pyKafkaRecv},
		{re: regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\.\s*subscribe\s*\(`), kind: "topic", side: "consume", kws: []string{"topics"}, gate: pyKafkaRecv},
		{re: regexp.MustCompile(`\bKafkaConsumer\s*\(`), kind: "topic", side: "consume", all: true, kws: []string{"topics"}},
	}
	pika := []cfgCall{
		{re: regexp.MustCompile(`\.\s*queue_declare\s*\(`), kind: "queue", side: "declare", kws: []string{"queue"}, gate: has("pika")},
		{re: regexp.MustCompile(`\.\s*basic_consume\s*\(`), kind: "queue", side: "consume", kws: []string{"queue"}, gate: has("pika")},
		{re: regexp.MustCompile(`\.\s*exchange_declare\s*\(`), kind: "exchange", side: "declare", kws: []string{"exchange"}, gate: has("pika")},
		{re: regexp.MustCompile(`\.\s*basic_publish\s*\(`), kind: "exchange", side: "produce", kws: []string{"exchange"}, gate: has("pika")},
	}
	natsGate := func(f *FileCtx, _ string) bool { return codeContains(f, "nats") && !codeContains(f, "kafka", "pika") }
	nats := []cfgCall{
		{re: regexp.MustCompile(`\.\s*publish\s*\(`), kind: "subject", side: "produce", kws: []string{"subject"}, gate: natsGate},
		{re: regexp.MustCompile(`\.\s*request\s*\(`), kind: "subject", side: "produce", kws: []string{"subject"}, gate: natsGate},
		{re: regexp.MustCompile(`\.\s*subscribe\s*\(`), kind: "subject", side: "consume", kws: []string{"subject"}, gate: natsGate},
	}
	Rules = append(Rules,
		funcRule{"kafka.py.config", isPy, cfgChannelRule("kafka.py.config", isKafkaPy, kafka, newRes)},
		funcRule{"pika.config", isPy, cfgChannelRule("pika.config", nil, pika, newRes)},
		funcRule{"nats.py.config", isPy, cfgChannelRule("nats.py.config", nil, nats, newRes)},
	)
}

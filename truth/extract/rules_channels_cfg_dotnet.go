package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// .NET: topic from IConfiguration (indexer, GetValue, GetSection) or from an
// options object, resolved through the appsettings*.json of the project.

var (
	csCfgRecv    = regexp.MustCompile(`(?i)conf|setting|option|cfg|opts`)
	csIndexer    = regexp.MustCompile(`^([\w.]+)\s*\[\s*"([^"]+)"\s*\]$`)
	csGetValue   = regexp.MustCompile(`^([\w.]+)\s*\.\s*GetValue\s*<[^>]*>\s*\(\s*"([^"]+)"\s*(?:,\s*"([^"]*)"\s*)?\)$`)
	csSectionVal = regexp.MustCompile(`^[\w.]+\s*((?:\.\s*Get(?:Required)?Section\s*\(\s*"[^"]+"\s*\))+)\s*(?:\.\s*(?:Value|Get\s*<\s*string\s*>\s*\(\s*\))|\[\s*"([^"]+)"\s*\])$`)
	csSectionKey = regexp.MustCompile(`"([^"]+)"`)
	csChain      = regexp.MustCompile(`^\w+(?:\s*\.\s*\w+)+$`)
	csIdent      = regexp.MustCompile(`^\w+$`)
	csTail       = regexp.MustCompile(`(?:\.ToString\(\)|!)+$`)
	csCast       = regexp.MustCompile(`^(?:\(\s*string\s*\)|\bstring\.Format\b)\s*`)
)

type dotnetRes struct {
	f     *FileCtx
	ct    *codeText
	files []*cfgFile
	init  bool
	depth int
}

func (r *dotnetRes) settings() []*cfgFile {
	if !r.init {
		r.files, r.init = appsettingsFiles(r.f), true
	}
	return r.files
}

func (r *dotnetRes) resolve(expr string) (cfgVal, bool) { return r.eval(expr, 0) }

func (r *dotnetRes) eval(expr string, depth int) (cfgVal, bool) {
	if depth > 3 {
		return cfgVal{}, false
	}
	expr = strings.TrimSpace(expr)
	// `a ?? "default"`: config first, the default literal otherwise.
	if parts := splitCfgTop(expr, "??"); len(parts) == 2 {
		if v, ok := r.eval(parts[0], depth+1); ok {
			return v, true
		}
		if d, ok := quotedLit(parts[1]); ok {
			return cfgVal{val: d}, true
		}
		return cfgVal{}, false
	}
	expr = csTail.ReplaceAllString(expr, "")
	expr = strings.TrimSpace(csCast.ReplaceAllString(expr, ""))
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	if m := csIndexer.FindStringSubmatch(expr); m != nil && csCfgRecv.MatchString(m[1]) {
		return r.key(m[2], "")
	}
	if m := csGetValue.FindStringSubmatch(expr); m != nil {
		return r.key(m[2], m[3])
	}
	if m := csSectionVal.FindStringSubmatch(expr); m != nil {
		var keys []string
		for _, k := range csSectionKey.FindAllStringSubmatch(m[1], -1) {
			keys = append(keys, k[1])
		}
		if m[2] != "" {
			keys = append(keys, m[2])
		}
		return r.key(strings.Join(keys, ":"), "")
	}
	if csIdent.MatchString(expr) {
		return r.ident(expr, depth)
	}
	if csChain.MatchString(expr) {
		return r.member(expr)
	}
	return cfgVal{}, false
}

func (r *dotnetRes) key(key, def string) (cfgVal, bool) {
	if e, ev, ok := lookupAll(r.settings(), key); ok && strings.TrimSpace(e.Value) != "" {
		return cfgVal{val: e.Value, ev: &ev}, true
	}
	if def != "" {
		return cfgVal{val: def}, true
	}
	return cfgVal{}, false
}

// ident resolves a local or field assigned from a configuration expression.
func (r *dotnetRes) ident(id string, depth int) (cfgVal, bool) {
	re := regexp.MustCompile(`(?:^|[^\w.=!<>])(?:this\s*\.\s*)?` + regexp.QuoteMeta(id) + `\s*=\s*([^=][^;]*);`)
	var vs []cfgVal
	for _, m := range re.FindAllStringSubmatchIndex(r.ct.text, -1) {
		if v, ok := r.eval(r.ct.text[m[2]:m[3]], depth+1); ok {
			vs = append(vs, v)
		}
	}
	return distinctVal(vs)
}

// member resolves `options.Topic` / `_opts.Value.Topics.Payments`: the last
// segment names a leaf of appsettings, and the receiver must look like a
// configuration object. The leaf must be unique, and the segment before it
// must appear in its path when there is more than one candidate.
func (r *dotnetRes) member(expr string) (cfgVal, bool) {
	segs := strings.Split(strings.ReplaceAll(expr, " ", ""), ".")
	if !csCfgRecv.MatchString(strings.Join(segs[:len(segs)-1], ".")) {
		return cfgVal{}, false
	}
	leaf := segs[len(segs)-1]
	type hit struct {
		e  cfgEntry
		ev truth.Evidence
	}
	var hits []hit
	for _, c := range r.settings() {
		for _, e := range c.Entries {
			p := e.Path
			if i := strings.LastIndex(p, ":"); i >= 0 {
				p = p[i+1:]
			}
			if strings.EqualFold(p, leaf) && strings.TrimSpace(e.Value) != "" {
				hits = append(hits, hit{e, c.evidence(e)})
			}
		}
		if len(hits) > 0 {
			break // base file wins
		}
	}
	if len(hits) > 1 && len(segs) >= 2 {
		var narrowed []hit
		for _, h := range hits {
			if strings.Contains(strings.ToLower(h.e.Path), strings.ToLower(segs[len(segs)-2])) {
				narrowed = append(narrowed, h)
			}
		}
		hits = narrowed
	}
	if len(hits) != 1 {
		return cfgVal{}, false
	}
	return cfgVal{val: hits[0].e.Value, ev: &hits[0].ev}, true
}

func init() {
	isCS := func(p string) bool { return hasExt(p, ".cs") }
	Rules = append(Rules, funcRule{"kafka.dotnet.config", isCS, cfgChannelRule("kafka.dotnet.config", isKafkaDotnet,
		[]cfgCall{
			{re: regexp.MustCompile(`\.\s*Subscribe\s*\(`), kind: "topic", side: "consume"},
			{re: regexp.MustCompile(`\.\s*Produce(?:Async)?\s*\(`), kind: "topic", side: "produce"},
			{re: regexp.MustCompile(`\bnew\s+TopicPartition\s*\(`), kind: "topic", sideFn: topicPartitionSide},
		},
		func(f *FileCtx, ct *codeText) cfgResolver { return &dotnetRes{f: f, ct: ct} })})
}

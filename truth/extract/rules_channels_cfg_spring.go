package extract

import (
	"regexp"
	"strings"
)

// Spring (Java / Kotlin): the channel is "${a.b.c}" or "${a.b.c:default}" in
// @KafkaListener / @RabbitListener, or in an @Value field passed to
// KafkaTemplate.send / ProducerRecord; the key is looked up in
// application*.yml / .yaml / .properties.

var (
	springPlaceholder = regexp.MustCompile(`^"\\?\$\{([^}:"]+)(?::([^}"]*))?\}"$`)
	springValueAnno   = regexp.MustCompile(`@Value\s*\(\s*(?:value\s*=\s*)?("\\?\$\{[^}"]+\}")\s*\)`)
	springAnnoAny     = regexp.MustCompile(`@\w+(?:\s*\([^)]*\))?\s*`)
	springGetProp     = regexp.MustCompile(`^[\w.]+\s*\.\s*get(?:Required)?Property\s*\(\s*("[^"]+")\s*(?:,\s*("[^"]*")\s*)?\)$`)
	springIdent       = regexp.MustCompile(`^(?:this\.)?(\w+)$`)
	springNested      = regexp.MustCompile(`^\$\{([^}:]+)(?::([^}]*))?\}$`)
	springTemplate    = regexp.MustCompile(`\bKafkaTemplate\b`)
)

type springRes struct {
	f     *FileCtx
	ct    *codeText
	files []*cfgFile
	init  bool
}

func (r *springRes) config() []*cfgFile {
	if !r.init {
		r.files, r.init = springConfigFiles(r.f), true
	}
	return r.files
}

func (r *springRes) resolve(expr string) (cfgVal, bool) {
	expr = strings.TrimSpace(expr)
	if m := springGetProp.FindStringSubmatch(expr); m != nil {
		key, _ := quotedLit(m[1])
		def, _ := quotedLit(m[2])
		return r.key(key, def, 0)
	}
	if m := springPlaceholder.FindStringSubmatch(expr); m != nil {
		return r.key(m[1], m[2], 0)
	}
	if m := springIdent.FindStringSubmatch(expr); m != nil {
		return r.field(m[1])
	}
	return cfgVal{}, false
}

// key looks a property up; a missing key falls back to the placeholder
// default. A value that is itself a placeholder is followed.
func (r *springRes) key(key, def string, depth int) (cfgVal, bool) {
	if depth > 3 {
		return cfgVal{}, false
	}
	if e, ev, ok := lookupAll(r.config(), key); ok {
		val := strings.TrimSpace(e.Value)
		if m := springNested.FindStringSubmatch(val); m != nil {
			if v, ok := r.key(m[1], m[2], depth+1); ok {
				if v.ev == nil {
					v.ev = &ev
				}
				return v, true
			}
			return cfgVal{}, false
		}
		if val != "" {
			return cfgVal{val: val, ev: &ev}, true
		}
	}
	if def != "" {
		return cfgVal{val: def}, true
	}
	return cfgVal{}, false
}

// field resolves an identifier declared with @Value("${...}") or assigned a
// placeholder literal.
func (r *springRes) field(name string) (cfgVal, bool) {
	var vs []cfgVal
	for _, m := range springValueAnno.FindAllStringSubmatchIndex(r.ct.text, -1) {
		decl := r.ct.text[m[1]:]
		end := strings.IndexAny(decl, ";={")
		if end < 0 || end > 400 {
			continue
		}
		decl = springAnnoAny.ReplaceAllString(decl[:end], "")
		if i := strings.IndexAny(decl, ",)"); i >= 0 {
			decl = decl[:i]
		}
		if springDeclName(decl) != name {
			continue
		}
		if p := springPlaceholder.FindStringSubmatch(r.ct.text[m[2]:m[3]]); p != nil {
			if v, ok := r.key(p[1], p[2], 0); ok {
				vs = append(vs, v)
			}
		}
	}
	if len(vs) == 0 {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*(?::\s*String\s*)?=\s*("\\?\$\{[^}"]+\}")`)
		for _, m := range re.FindAllStringSubmatch(r.ct.text, -1) {
			if p := springPlaceholder.FindStringSubmatch(m[1]); p != nil {
				if v, ok := r.key(p[1], p[2], 0); ok {
					vs = append(vs, v)
				}
			}
		}
	}
	return distinctVal(vs)
}

// springDeclName returns the declared name of `private String topic` or
// `private val topic: String`.
func springDeclName(decl string) string {
	decl = strings.TrimSpace(decl)
	if m := regexp.MustCompile(`\b(?:val|var)\s+(\w+)`).FindStringSubmatch(decl); m != nil {
		return m[1]
	}
	if i := strings.Index(decl, ":"); i >= 0 {
		decl = decl[:i]
	}
	fs := strings.Fields(decl)
	if len(fs) == 0 {
		return ""
	}
	return fs[len(fs)-1]
}

func springSendReceiver(f *FileCtx, recv string) bool {
	if kafkaTemplateReceiver(f, recv) {
		return true
	}
	// Kotlin declares `val kafkaTemplate: KafkaTemplate<..>`.
	re := regexp.MustCompile(`\b(?:val|var)\s+` + regexp.QuoteMeta(strings.TrimPrefix(recv, "this.")) + `\s*:\s*KafkaTemplate\b`)
	for _, l := range f.Lines {
		if re.MatchString(l.Code) {
			return true
		}
	}
	return false
}

func init() {
	isJVM := func(p string) bool { return hasExt(p, ".java", ".kt") }
	newRes := func(f *FileCtx, ct *codeText) cfgResolver { return &springRes{f: f, ct: ct} }
	kafka := []cfgCall{
		{re: regexp.MustCompile(`@(?:[A-Za-z_]\w*\s*\.\s*)*KafkaListener\s*\(`), kind: "topic", side: "consume", kws: []string{"topics"}},
		{re: regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\.\s*send\s*\(`), kind: "topic", side: "produce", gate: springSendReceiver},
		{re: regexp.MustCompile(`\bProducerRecord\s*(?:<[^;(){}]*>)?\s*\(`), kind: "topic", side: "produce"},
	}
	rabbit := []cfgCall{
		{re: regexp.MustCompile(`@(?:[A-Za-z_]\w*\s*\.\s*)*RabbitListener\s*\(`), kind: "queue", side: "consume", kws: []string{"queues"}},
	}
	Rules = append(Rules,
		funcRule{"spring.kafka.config", isJVM, cfgChannelRule("spring.kafka.config",
			func(f *FileCtx) bool { return codeContains(f, "kafka") }, kafka, newRes)},
		funcRule{"spring.rabbit.config", isJVM, cfgChannelRule("spring.rabbit.config",
			func(f *FileCtx) bool { return codeContains(f, "rabbit") }, rabbit, newRes)},
	)
}

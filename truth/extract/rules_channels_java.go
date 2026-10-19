package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules,
		funcRule{"spring.kafka", func(p string) bool { return hasExt(p, ".java") }, applySpringKafka},
	)
}

var (
	kafkaListenerRe = regexp.MustCompile(`@(?:[A-Za-z_]\w*\s*\.\s*)*KafkaListener\b`)
	// kafkaTemplateDecl finds the names declared with a KafkaTemplate type:
	// fields, constructor and method parameters (generic or raw).
	kafkaTemplateDecl = regexp.MustCompile(`\bKafkaTemplate\b\s*(?:<[^;(){}]*>)?\s+([A-Za-z_]\w*)`)
	javaSend          = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\.\s*send\(\s*("(?:[^"\\]|\\.)*")\s*[,)]`)
	producerRecordNew = regexp.MustCompile(`\bnew\s+ProducerRecord\s*(?:<[^;(){}]*>)?\s*\(\s*("(?:[^"\\]|\\.)*")\s*[,)]`)
)

// kafkaTemplateReceiver accepts a receiver declared with a KafkaTemplate type
// in this file (a leading "this." is ignored).
func kafkaTemplateReceiver(f *FileCtx, recv string) bool {
	recv = strings.TrimPrefix(recv, "this.")
	for _, l := range f.Lines {
		for _, m := range kafkaTemplateDecl.FindAllStringSubmatch(l.Code, -1) {
			if m[1] == recv {
				return true
			}
		}
	}
	return false
}

var springKafkaSend = specRule("spring.kafka", nil,
	chanSpec{re: javaSend, kind: "topic", side: "produce", list: true, win: 2, gate: kafkaTemplateReceiver},
	chanSpec{re: producerRecordNew, kind: "topic", side: "produce", list: true, win: 2},
)

// applySpringKafka: @KafkaListener(topics = ...) consumes, KafkaTemplate.send
// and new ProducerRecord produce. Topics must be string literals; constants,
// placeholders and topicPattern yield nothing.
func applySpringKafka(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "kafka") {
		return nil
	}
	t := newJavaText(f)
	var out []truth.Fact
	for _, loc := range kafkaListenerRe.FindAllStringIndex(t.masked, -1) {
		a, ok := t.annoAt(loc[0])
		if !ok || !a.hasArgs {
			continue
		}
		expr, found := javaArg(a.args, "topics")
		if !found {
			continue
		}
		names, ok := javaLiterals(expr)
		if !ok {
			continue
		}
		l := f.Lines[t.lineIdx(a.pos)]
		for _, n := range names {
			if fc, ok := channelFact("spring.kafka", f, l, "topic", n, "consume"); ok {
				out = append(out, fc)
			}
		}
	}
	return append(out, springKafkaSend(f)...)
}

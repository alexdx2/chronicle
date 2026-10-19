package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// Kafka clients in Kotlin: ProducerRecord("topic", ...) produces and
// consumer.subscribe(listOf("a", "b")) consumes. Only literal names count and
// the file must mention kafka.

func init() {
	Rules = append(Rules,
		funcRule{"kotlin.kafka", func(p string) bool { return hasExt(p, ".kt") }, applyKotlinKafka},
	)
}

var (
	ktProducerRecord = regexp.MustCompile(`\bProducerRecord\s*(?:<[^(]*?>)?\s*\(\s*"([^"$\\]*)"\s*[,)]`)
	ktSubscribe      = regexp.MustCompile(`\.subscribe\s*\(\s*(?:[\w.]+\.)?(?:listOf|setOf|mutableListOf|mutableSetOf|arrayListOf|singletonList|singleton|asList|of)\s*(?:<[^(]*?>)?\s*\(([^)]*)\)`)
)

// ktInString reports whether idx of a single code line lies inside a string literal.
func ktInString(code string, idx int) bool {
	in := false
	for i := 0; i < idx && i < len(code); i++ {
		switch code[i] {
		case '\\':
			if in {
				i++
			}
		case '"':
			in = !in
		}
	}
	return in
}

func applyKotlinKafka(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "kafka") {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		if strings.Contains(l.Code, "ProducerRecord") {
			text := callText(f.Lines, i, 6)
			if m := ktProducerRecord.FindStringSubmatchIndex(text); m != nil && m[0] < len(l.Code) && !ktInString(l.Code, m[0]) {
				if fc, ok := channelFact("kotlin.kafka", f, l, "topic", text[m[2]:m[3]], "produce"); ok {
					out = append(out, fc)
				}
			}
		}
		if strings.Contains(l.Code, "subscribe") {
			text := callText(f.Lines, i, 8)
			if m := ktSubscribe.FindStringSubmatchIndex(text); m != nil && m[0] < len(l.Code) && !ktInString(l.Code, m[0]) {
				for _, name := range literalItems(text[m[2]:m[3]]) {
					if strings.Contains(name, "$") {
						continue
					}
					if fc, ok := channelFact("kotlin.kafka", f, l, "topic", name, "consume"); ok {
						out = append(out, fc)
					}
				}
			}
		}
	}
	return out
}

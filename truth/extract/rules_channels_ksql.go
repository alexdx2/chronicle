package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// ksqlDB: CREATE STREAM / CREATE TABLE ... WITH (KAFKA_TOPIC='x') declares a
// source over an existing topic (consume). In CREATE ... AS SELECT the same
// property names the topic the persistent query writes (produce).

func init() {
	Rules = append(Rules,
		funcRule{"ksql.channel", func(p string) bool { return hasExt(p, ".sql", ".ksql") }, applyKsql},
	)
}

var (
	ksqlCreate = regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:SOURCE\s+)?(?:STREAM|TABLE)\b`)
	ksqlAsSel  = regexp.MustCompile(`(?i)\bAS\s+SELECT\b`)
	ksqlTopic  = regexp.MustCompile(`(?i)\bKAFKA_TOPIC\s*=\s*'((?:[^']|'')*)'`)
	sqlStrings = regexp.MustCompile(`'(?:[^']|'')*'|"[^"]*"`)
)

type ksqlStmt struct{ start, end int } // byte offsets into the joined code

// splitStatements splits text on semicolons that are outside quotes.
func splitStatements(text string) []ksqlStmt {
	var out []ksqlStmt
	start, quote := 0, byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ';':
			out = append(out, ksqlStmt{start, i})
			start = i + 1
		}
	}
	return append(out, ksqlStmt{start, len(text)})
}

func applyKsql(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var sb strings.Builder
	var starts []int // byte offset where each line begins
	for _, l := range f.Lines {
		starts = append(starts, sb.Len())
		sb.WriteString(l.Code)
		sb.WriteByte('\n')
	}
	text := sb.String()
	lineAt := func(off int) int {
		lo, hi := 0, len(starts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if starts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo
	}
	var out []truth.Fact
	for _, st := range splitStatements(text) {
		stmt := text[st.start:st.end]
		if !ksqlCreate.MatchString(stmt) {
			continue
		}
		side := "consume"
		if ksqlAsSel.MatchString(sqlStrings.ReplaceAllStringFunc(stmt, func(s string) string { return strings.Repeat(" ", len(s)) })) {
			side = "produce"
		}
		for _, m := range ksqlTopic.FindAllStringSubmatchIndex(stmt, -1) {
			name := strings.ReplaceAll(stmt[m[2]:m[3]], "''", "'")
			l := f.Lines[lineAt(st.start+m[0])]
			if fc, ok := channelFact("ksql.channel", f, l, "topic", name, side); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

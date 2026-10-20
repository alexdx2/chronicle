package extract

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Kafka Connect connector configs (JSON, as posted to the Connect REST API or
// kept in a repo). A Debezium source connector produces one topic per captured
// table, "<topic.prefix>.<entry of table.include.list>"; a sink connector
// consumes the comma separated "topics". The config may be the top-level
// object or sit under a "config" key. Regex entries and placeholders give no
// fact.

func init() {
	Rules = append(Rules,
		funcRule{"kafka.connect", func(p string) bool { return hasExt(p, ".json") }, applyKafkaConnect},
	)
}

type jsonStr struct {
	val  string
	line int
}

// jsonObjects returns every JSON object of raw (document order, nested ones
// included) as a map of its string-valued members with their line numbers.
func jsonObjects(raw []byte) []map[string]jsonStr {
	dec := json.NewDecoder(bytes.NewReader(raw))
	lineAt := func(off int64) int {
		if off > int64(len(raw)) {
			off = int64(len(raw))
		}
		return 1 + bytes.Count(raw[:off], []byte{'\n'})
	}
	var out []map[string]jsonStr
	var value func(tok json.Token) bool
	object := func() bool {
		idx := len(out)
		out = append(out, map[string]jsonStr{})
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return false
			}
			key, _ := kt.(string)
			line := lineAt(dec.InputOffset())
			vt, err := dec.Token()
			if err != nil {
				return false
			}
			if s, ok := vt.(string); ok {
				out[idx][key] = jsonStr{s, line}
				continue
			}
			if !value(vt) {
				return false
			}
		}
		_, err := dec.Token()
		return err == nil
	}
	value = func(tok json.Token) bool {
		d, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		if d == '{' {
			return object()
		}
		for dec.More() { // array
			t, err := dec.Token()
			if err != nil || !value(t) {
				return false
			}
		}
		_, err := dec.Token()
		return err == nil
	}
	if tok, err := dec.Token(); err == nil {
		value(tok)
	}
	return out
}

// plainTableName reports whether an include-list entry is a literal name
// (no regex syntax).
func plainTableName(s string) bool {
	return s != "" && !strings.ContainsAny(s, `*?+()[]{}|\^$`)
}

func applyKafkaConnect(f *FileCtx) []truth.Fact {
	if f.IsTest || !bytes.Contains(f.Raw, []byte("connector.class")) {
		return nil
	}
	var out []truth.Fact
	for _, obj := range jsonObjects(f.Raw) {
		class, ok := obj["connector.class"]
		if !ok {
			continue
		}
		lc := strings.ToLower(class.val)
		line := func(n int) (l lex.Line, ok bool) {
			if n < 1 || n > len(f.Lines) {
				return l, false
			}
			return f.Lines[n-1], true
		}
		switch {
		case strings.Contains(lc, "debezium"):
			prefix := obj["topic.prefix"]
			if prefix.val == "" {
				prefix = obj["database.server.name"] // Debezium 1.x
			}
			if prefix.val == "" {
				continue
			}
			for _, key := range []string{"table.include.list", "collection.include.list"} {
				inc, ok := obj[key]
				if !ok {
					continue
				}
				l, ok := line(inc.line)
				if !ok {
					continue
				}
				for _, entry := range strings.Split(inc.val, ",") {
					entry = strings.TrimSpace(entry)
					if !plainTableName(entry) {
						continue
					}
					if fc, ok := channelFact("kafka.connect", f, l, "topic", prefix.val+"."+entry, "produce"); ok {
						out = append(out, fc)
					}
				}
			}
		case strings.Contains(lc, "sink"):
			tp, ok := obj["topics"]
			if !ok {
				continue
			}
			l, ok := line(tp.line)
			if !ok {
				continue
			}
			for _, n := range strings.Split(tp.val, ",") {
				if fc, ok := channelFact("kafka.connect", f, l, "topic", n, "consume"); ok {
					out = append(out, fc)
				}
			}
		}
	}
	return out
}

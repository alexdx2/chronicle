package scancfg

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// flatten turns a decoded document into dotted-path -> scalar text.
func flatten(prefix string, v any, out map[string]string) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			flatten(join(k), x, out)
		}
	case map[any]any:
		for k, x := range t {
			flatten(join(toS(k)), x, out)
		}
	case []any:
		for i, x := range t {
			flatten(join(strconv.Itoa(i)), x, out)
		}
	case nil:
	default:
		out[prefix] = toS(t)
	}
}

func toS(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

// decodeDocument reads json, yaml or properties/dotenv into a flat map.
// DecodeDocument reads json, yaml or properties/dotenv into dotted-path -> value.
func DecodeDocument(file string, src []byte) (map[string]string, bool) {
	return decodeDocument(file, src)
}

func decodeDocument(file string, src []byte) (map[string]string, bool) {
	out := map[string]string{}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".json":
		var v any
		if json.Unmarshal(src, &v) != nil {
			return nil, false
		}
		flatten("", v, out)
	case ".yaml", ".yml":
		var v any
		if yaml.Unmarshal(src, &v) != nil {
			return nil, false
		}
		flatten("", v, out)
	case ".properties", ".env", ".conf", ".cfg", ".ini":
		for _, ln := range strings.Split(string(src), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || ln[0] == '#' || ln[0] == '!' || ln[0] == ';' {
				continue
			}
			i := strings.IndexAny(ln, "=:")
			if i < 0 {
				continue
			}
			out[strings.TrimSpace(ln[:i])] = strings.Trim(strings.TrimSpace(ln[i+1:]), `"'`)
		}
	default:
		return nil, false
	}
	return out, true
}

var tplVar = regexp.MustCompile(`\{([^{}]+)\}`)

func expand(tpl string, key, value string, doc map[string]string) []string {
	names := []string{tpl}
	for _, m := range tplVar.FindAllStringSubmatch(tpl, -1) {
		v := m[1]
		var vals []string
		switch {
		case v == "value":
			vals = []string{value}
		case v == "key":
			vals = []string{key}
		case strings.HasPrefix(v, "key:"):
			n, err := strconv.Atoi(v[4:])
			seg := strings.Split(key, ".")
			if err != nil || n < 0 || n >= len(seg) {
				return nil
			}
			vals = []string{seg[n]}
		case strings.HasPrefix(v, "doc:"):
			x, ok := doc[v[4:]]
			if !ok {
				return nil
			}
			vals = []string{x}
		case strings.HasPrefix(v, "each:doc:"):
			x, ok := doc[v[9:]]
			if !ok {
				return nil
			}
			for _, it := range strings.Split(x, ",") {
				if it = strings.TrimSpace(it); it != "" {
					vals = append(vals, it)
				}
			}
		default:
			return nil
		}
		var next []string
		for _, n := range names {
			for _, x := range vals {
				next = append(next, strings.Replace(n, m[0], x, 1))
			}
		}
		names = next
	}
	return names
}

// pathMatch matches a dotted path against a glob where * is one segment and ** any.
func pathMatch(glob, p string) bool {
	return GlobMatch(strings.ReplaceAll(glob, ".", "/"), strings.ReplaceAll(p, ".", "/"))
}

func lineOf(src []byte, value string) int {
	if value == "" {
		return 1
	}
	for i, ln := range strings.Split(string(src), "\n") {
		if strings.Contains(ln, value) {
			return i + 1
		}
	}
	return 1
}

// RunDocuments applies document rules to one structured file.
func RunDocuments(cfg *Config, file string, src []byte) []Fact {
	var out []Fact
	var doc map[string]string
	decoded := false
	for _, d := range cfg.Documents {
		if !GlobMatch(d.Glob, file) {
			continue
		}
		if !decoded {
			decoded = true
			doc, _ = decodeDocument(file, src)
		}
		if doc == nil {
			continue
		}
		okAll := true
		for kg, vg := range d.WhenKeys {
			hit := false
			for k, v := range doc {
				if pathMatch(kg, k) && (vg == "" || vg == "*" || GlobMatch(vg, v)) {
					hit = true
					break
				}
			}
			if !hit {
				okAll = false
				break
			}
		}
		if !okAll {
			continue
		}
		keys := make([]string, 0, len(doc))
		for k := range doc {
			if d.Select == "" || pathMatch(d.Select, k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if d.Select == "" {
			keys = []string{""}
		}
		for _, k := range keys {
			for _, e := range d.Emit {
				side := e.Side
				for sub, s := range e.SideByKey {
					if strings.Contains(k, sub) {
						side = s
					}
				}
				if side == "" {
					continue
				}
				kind := e.Kind
				if kind == "" {
					kind = "topic"
				}
				for _, n := range expand(e.Name, k, doc[k], doc) {
					if n == "" {
						continue
					}
					out = append(out, Fact{Rule: d.ID, Source: d.Source, Reviewed: d.Reviewed, Kind: kind, Side: side,
						Transport: e.Transport, Name: n, File: file, Line: lineOf(src, doc[k]), Text: k + ": " + doc[k]})
				}
			}
		}
	}
	SortFacts(out)
	return out
}

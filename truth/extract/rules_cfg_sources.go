package extract

import (
	"bytes"
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"gopkg.in/yaml.v3"
)

// Configuration files that hold channel names: appsettings*.json (.NET) and
// Spring's application*.yml / .yaml / .properties. Each is flattened to
// dotted-path entries that remember their 1-based line.

type cfgEntry struct {
	Path  string // segments joined with sep
	Value string
	Line  int
}

type cfgFile struct {
	File    string // tree path
	Raw     string
	Entries []cfgEntry
	spring  bool // relaxed key matching (kebab/camel/underscore)
}

func (c *cfgFile) evidence(e cfgEntry) truth.Evidence {
	text := ""
	ls := strings.Split(c.Raw, "\n")
	if e.Line >= 1 && e.Line <= len(ls) {
		text = strings.TrimSpace(ls[e.Line-1])
	}
	return truth.Evidence{File: c.File, Line: e.Line, Text: text}
}

func relaxKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "-", "")
	return strings.ReplaceAll(k, "_", "")
}

// lookup finds key: exactly (case-insensitive) first, then, for Spring,
// with relaxed binding.
func (c *cfgFile) lookup(key string) (cfgEntry, bool) {
	for _, e := range c.Entries {
		if strings.EqualFold(e.Path, key) {
			return e, true
		}
	}
	if c.spring {
		rk := relaxKey(key)
		for _, e := range c.Entries {
			if relaxKey(e.Path) == rk {
				return e, true
			}
		}
	}
	return cfgEntry{}, false
}

// stripJSONComments blanks // and /* */ comments (appsettings.json allows
// them) keeping offsets and newlines.
func stripJSONComments(b []byte) []byte {
	out := append([]byte(nil), b...)
	for i := 0; i < len(out); i++ {
		switch {
		case out[i] == '"':
			for i++; i < len(out) && out[i] != '"'; i++ {
				if out[i] == '\\' {
					i++
				}
			}
		case out[i] == '/' && i+1 < len(out) && out[i+1] == '/':
			for ; i < len(out) && out[i] != '\n'; i++ {
				out[i] = ' '
			}
		case out[i] == '/' && i+1 < len(out) && out[i+1] == '*':
			j := bytes.Index(out[i+2:], []byte("*/"))
			end := len(out)
			if j >= 0 {
				end = i + 2 + j + 2
			}
			for ; i < end; i++ {
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
			i--
		}
	}
	return out
}

// flattenJSON lists the string leaves of a JSON document with the line of the
// value. Segments are joined with ":" (the .NET configuration separator).
func flattenJSON(raw []byte) []cfgEntry {
	b := stripJSONComments(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")))
	dec := json.NewDecoder(bytes.NewReader(b))
	type frame struct {
		obj       bool
		key       string
		idx       int
		expectKey bool
	}
	var stack []frame
	var out []cfgEntry
	pathOf := func() string {
		var segs []string
		for _, fr := range stack {
			if fr.obj {
				segs = append(segs, fr.key)
			} else {
				segs = append(segs, strconv.Itoa(fr.idx))
			}
		}
		return strings.Join(segs, ":")
	}
	// valueDone is called after a whole value (scalar or container) ended.
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].obj {
			stack[n-1].expectKey = true
		}
	}
	startValue := func() {
		if n := len(stack); n > 0 && !stack[n-1].obj {
			stack[n-1].idx++
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		off := int(dec.InputOffset())
		if off > len(b) {
			off = len(b)
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				startValue()
				stack = append(stack, frame{obj: true, expectKey: true})
			case '[':
				startValue()
				stack = append(stack, frame{idx: -1})
			default:
				stack = stack[:len(stack)-1]
				valueDone()
			}
		case string:
			if n := len(stack); n > 0 && stack[n-1].obj && stack[n-1].expectKey {
				stack[n-1].key = v
				stack[n-1].expectKey = false
				continue
			}
			startValue()
			out = append(out, cfgEntry{Path: pathOf(), Value: v, Line: 1 + bytes.Count(b[:off], []byte("\n"))})
			valueDone()
		default:
			startValue()
			valueDone()
		}
	}
}

// flattenYAML lists the scalar leaves of every document, paths joined by ".".
func flattenYAML(raw []byte) []cfgEntry {
	var out []cfgEntry
	var walk func(n *yaml.Node, p string)
	walk = func(n *yaml.Node, p string) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.AliasNode:
			walk(n.Alias, p)
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i].Value
				if p != "" {
					k = p + "." + k
				}
				walk(n.Content[i+1], k)
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, p+"["+strconv.Itoa(i)+"]")
			}
		case yaml.ScalarNode:
			if p != "" {
				out = append(out, cfgEntry{Path: p, Value: n.Value, Line: n.Line})
			}
		}
	}
	yamlDocs(raw, func(root *yaml.Node) { walk(root, "") })
	return out
}

func flattenProperties(raw []byte) []cfgEntry {
	var out []cfgEntry
	for i, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || ln[0] == '#' || ln[0] == '!' {
			continue
		}
		cut := strings.IndexAny(ln, "=: \t")
		if cut <= 0 {
			continue
		}
		k := ln[:cut]
		v := strings.TrimLeft(ln[cut:], " \t")
		if v != "" && (v[0] == '=' || v[0] == ':') {
			v = strings.TrimLeft(v[1:], " \t")
		}
		out = append(out, cfgEntry{Path: k, Value: strings.TrimSpace(v), Line: i + 1})
	}
	return out
}

// loadCfgDir reads the config files of dir that match the given base names
// and globs (names first, in order; glob matches sorted after them).
func loadCfgDir(f *FileCtx, dir string, names, globs []string, flatten func(p string, raw []byte) []cfgEntry, spring bool) []*cfgFile {
	var out []*cfgFile
	seen := map[string]bool{}
	add := func(base string) {
		if seen[base] {
			return
		}
		seen[base] = true
		p := path.Join(dir, base)
		raw, ok := f.Tree.ReadFile(p)
		if !ok {
			return
		}
		out = append(out, &cfgFile{File: p, Raw: string(raw), Entries: flatten(p, raw), spring: spring})
	}
	for _, n := range names {
		add(n)
	}
	for _, g := range globs {
		sib := f.Tree.Siblings(dir, g)
		sort.Strings(sib)
		for _, s := range sib {
			add(s)
		}
	}
	return out
}

// appsettingsFiles returns the appsettings*.json of the nearest directory
// above the file (the file's own directory first) that has any.
func appsettingsFiles(f *FileCtx) []*cfgFile {
	for dir := f.Dir; ; dir = path.Dir(dir) {
		fs := loadCfgDir(f, dir, []string{"appsettings.json"}, []string{"appsettings.*.json"},
			func(_ string, raw []byte) []cfgEntry { return flattenJSON(raw) }, false)
		if len(fs) > 0 {
			return fs
		}
		if dir == "." || dir == "/" || dir == "" {
			return nil
		}
	}
}

// springConfigFiles returns application*.yml/yaml/properties (and bootstrap)
// from the nearest directory above the file that has any, looking also into
// src/main/resources, resources and config below each directory.
func springConfigFiles(f *FileCtx) []*cfgFile {
	flat := func(p string, raw []byte) []cfgEntry {
		if strings.HasSuffix(p, ".properties") {
			return flattenProperties(raw)
		}
		return flattenYAML(raw)
	}
	var names, globs []string
	for _, b := range []string{"application", "bootstrap"} {
		for _, e := range []string{".yml", ".yaml", ".properties"} {
			names = append(names, b+e)
			globs = append(globs, b+"-*"+e)
		}
	}
	for dir := f.Dir; ; dir = path.Dir(dir) {
		for _, sub := range []string{"", "src/main/resources", "resources", "config"} {
			if fs := loadCfgDir(f, path.Join(dir, sub), names, globs, flat, true); len(fs) > 0 {
				return fs
			}
		}
		if dir == "." || dir == "/" || dir == "" {
			return nil
		}
	}
}

// lookupAll searches the files in order and returns the first hit.
func lookupAll(files []*cfgFile, key string) (cfgEntry, truth.Evidence, bool) {
	for _, c := range files {
		if e, ok := c.lookup(key); ok {
			return e, c.evidence(e), true
		}
	}
	return cfgEntry{}, truth.Evidence{}, false
}

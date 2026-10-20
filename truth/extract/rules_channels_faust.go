package extract

import (
	"path"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Faust: a topic object created with app.topic('x') that an @app.agent(topic)
// reads consumes; the same object used with .send( produces. Topic objects are
// usually declared in the app module and imported by the agent modules, so the
// rule runs on the declaring file and looks for uses there and in the .py
// files of the same package tree that import the name from it. Evidence is the
// declaration line.

func init() {
	Rules = append(Rules,
		funcRule{"faust.channel", func(p string) bool { return hasExt(p, ".py") }, applyFaust},
	)
}

var (
	faustDecl   = regexp.MustCompile(`^\s*([A-Za-z_]\w*)\s*(?::[^=]+)?=\s*(?:await\s+)?[A-Za-z_][\w.]*\.topic\(`)
	faustInline = regexp.MustCompile(`\.agent\(\s*(?:channel\s*=\s*)?[A-Za-z_][\w.]*\.topic\(`)
	faustAgent  = regexp.MustCompile(`@\s*[A-Za-z_][\w.]*\.agent\(\s*(?:channel\s*=\s*)?([A-Za-z_]\w*)\s*[,)]`)
	faustImport = regexp.MustCompile(`(?s)\bfrom\s+(\.*[\w.]*)\s+import\s+(\([^)]*\)|[^\n]*)`)
)

type faustTopic struct {
	names []string
	line  lex.Line
}

func isFaustFile(f *FileCtx) bool { return codeContains(f, "faust") }

func applyFaust(f *FileCtx) []truth.Fact {
	if f.IsTest || !isFaustFile(f) {
		return nil
	}
	topics := map[string]faustTopic{}
	var out []truth.Fact
	for i, l := range f.Lines {
		if m := faustDecl.FindStringSubmatch(l.Code); m != nil {
			text := codeWindow(f.Lines, i, 4)
			idx := strings.Index(text, ".topic(")
			if idx < 0 {
				continue
			}
			args := topicArgs(text[idx+len(".topic("):])
			if names := literalItems(args); len(names) > 0 {
				topics[m[1]] = faustTopic{names, l}
			}
		}
		if faustInline.MatchString(l.Code) {
			text := codeWindow(f.Lines, i, 4)
			idx := strings.Index(text, ".topic(")
			for _, n := range literalItems(topicArgs(text[idx+len(".topic("):])) {
				if fc, ok := channelFact("faust.channel", f, l, "topic", n, "consume"); ok {
					out = append(out, fc)
				}
			}
		}
	}
	if len(topics) == 0 {
		return out
	}
	modBase := strings.TrimSuffix(path.Base(f.Path), ".py")
	if modBase == "__init__" {
		modBase = path.Base(f.Dir)
	}
	type use struct{ consume, produce bool }
	uses := map[string]*use{}
	scan := func(lines []lex.Line, name string, u *use) {
		for _, l := range lines {
			if m := faustAgent.FindStringSubmatch(l.Code); m != nil && m[1] == name {
				u.consume = true
			}
		}
		sendRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*\.\s*(?:send|send_soon)\(`)
		for _, l := range lines {
			if sendRe.MatchString(l.Code) {
				u.produce = true
			}
		}
	}
	for v := range topics {
		u := &use{}
		uses[v] = u
		scan(f.Lines, v, u)
	}
	for _, p := range faustPackageFiles(f) {
		if p == f.Path || IsTestPath(p) {
			continue
		}
		raw, ok := f.Tree.ReadFile(p)
		if !ok || !strings.Contains(string(raw), "faust") && !strings.Contains(string(raw), modBase) {
			continue
		}
		lines := lex.Scan(raw, lex.Python).Lines
		aliases := faustImported(lines, modBase)
		for v := range topics {
			for _, local := range aliases[v] {
				scan(lines, local, uses[v])
			}
		}
	}
	for _, v := range sortedKeys(topics) {
		t, u := topics[v], uses[v]
		for _, n := range t.names {
			if u.consume {
				if fc, ok := channelFact("faust.channel", f, t.line, "topic", n, "consume"); ok {
					out = append(out, fc)
				}
			}
			if u.produce {
				if fc, ok := channelFact("faust.channel", f, t.line, "topic", n, "produce"); ok {
					out = append(out, fc)
				}
			}
		}
	}
	return out
}

// topicArgs returns the argument text of a call whose opening parenthesis was
// just consumed, up to the matching closer (or the end of the window).
func topicArgs(s string) string {
	depth, quote := 0, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth == 0 {
				return s[:i]
			}
			depth--
		}
	}
	return s
}

// faustImported maps the original name to the local names under which lines
// import it from a module whose last component is mod.
func faustImported(lines []lex.Line, mod string) map[string][]string {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.Code)
		sb.WriteByte('\n')
	}
	out := map[string][]string{}
	for _, m := range faustImport.FindAllStringSubmatch(sb.String(), -1) {
		src := strings.TrimLeft(m[1], ".")
		if src != mod && !strings.HasSuffix(src, "."+mod) {
			continue
		}
		list := strings.Trim(strings.TrimSpace(m[2]), "()")
		for _, part := range strings.Split(list, ",") {
			f := strings.Fields(part)
			switch {
			case len(f) == 1:
				out[f[0]] = append(out[f[0]], f[0])
			case len(f) == 3 && f[1] == "as":
				out[f[0]] = append(out[f[0]], f[2])
			}
		}
	}
	return out
}

// faustPackageFiles lists .py files under the root of the package that holds f
// (the first ancestor directory without __init__.py, or the tree root), at most
// four levels deep.
func faustPackageFiles(f *FileCtx) []string {
	root := f.Dir
	for root != "." && root != "/" && root != "" {
		if _, ok := f.Tree.ReadFile(path.Join(root, "__init__.py")); !ok {
			break
		}
		root = path.Dir(root)
	}
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		for _, n := range f.Tree.Siblings(dir, "*.py") {
			out = append(out, path.Join(dir, n))
		}
		if depth >= 4 {
			return
		}
		for _, d := range f.Tree.Subdirs(dir) {
			walk(path.Join(dir, d), depth+1)
		}
	}
	walk(root, 0)
	return out
}

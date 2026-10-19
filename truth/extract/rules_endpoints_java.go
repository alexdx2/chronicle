package extract

import (
	"regexp"
	"sort"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules,
		funcRule{"spring.controller", func(p string) bool { return hasExt(p, ".java") }, applySpringController},
	)
}

var springVerb = map[string]string{
	"GetMapping": "GET", "PostMapping": "POST", "PutMapping": "PUT",
	"DeleteMapping": "DELETE", "PatchMapping": "PATCH",
}

var requestMethodRe = regexp.MustCompile(`\bRequestMethod\s*\.\s*(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE)\b`)

// springPaths reads the route paths of a mapping annotation: value/path as one
// literal or an array. No path attribute means the empty path. ok is false when
// a path is not a plain literal.
func springPaths(a javaAnno) ([]string, bool) {
	if !a.hasArgs {
		return []string{""}, true
	}
	expr, found := javaArg(a.args, "value", "path")
	if !found {
		return []string{""}, true
	}
	return javaLiterals(expr)
}

// applySpringController: methods of @RestController / @Controller classes
// annotated with @GetMapping & co. or @RequestMapping(method = ...), joined
// with the class-level @RequestMapping prefix.
func applySpringController(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "controller") {
		return nil
	}
	t := newJavaText(f)
	annos := t.annotations()

	type classInfo struct {
		ctrl     bool
		prefixes []string
		prefixOK bool
		line     int // line index of the class-level @RequestMapping, -1
	}
	classes := map[int]*classInfo{}
	for _, a := range annos {
		if a.classKw < 0 {
			continue
		}
		ci := classes[a.classKw]
		if ci == nil {
			ci = &classInfo{prefixes: []string{""}, prefixOK: true, line: -1}
			classes[a.classKw] = ci
		}
		switch a.name {
		case "RestController", "Controller":
			ci.ctrl = true
		case "RequestMapping":
			ps, ok := springPaths(a)
			ci.prefixes, ci.prefixOK, ci.line = ps, ok, t.lineIdx(a.pos)
		}
	}

	// Events in source order: class declarations and method-level mappings.
	type event struct {
		pos  int
		anno *javaAnno
	}
	var events []event
	declPos, declDepth := t.classDecls()
	depthOf := map[int]int{}
	for i, p := range declPos {
		events = append(events, event{pos: p})
		depthOf[p] = declDepth[i]
	}
	for i := range annos {
		a := &annos[i]
		if a.classKw >= 0 {
			continue
		}
		if _, ok := springVerb[a.name]; ok || a.name == "RequestMapping" {
			events = append(events, event{pos: a.pos, anno: a})
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].pos < events[j].pos })

	var out []truth.Fact
	var cur *classInfo
	skipUntil := 0 // a nested class that is not a controller owns no routes
	for _, ev := range events {
		if ev.anno == nil {
			if depthOf[ev.pos] == 0 { // a top-level declaration replaces the active controller
				cur = nil
				if ci := classes[ev.pos]; ci != nil && ci.ctrl && ci.prefixOK {
					cur = ci
				}
			} else if ci := classes[ev.pos]; ci == nil || !ci.ctrl {
				skipUntil = t.blockEnd(ev.pos)
			}
			continue
		}
		if cur == nil || ev.pos < skipUntil {
			continue
		}
		a := ev.anno
		var methods []string
		if m, ok := springVerb[a.name]; ok {
			methods = []string{m}
		} else {
			expr, found := javaArg(a.args, "method")
			if found {
				for _, m := range requestMethodRe.FindAllStringSubmatch(expr, -1) {
					methods = append(methods, m[1])
				}
			} else {
				methods = []string{"ANY"}
			}
		}
		paths, ok := springPaths(*a)
		if !ok {
			continue
		}
		l := f.Lines[t.lineIdx(a.pos)]
		for _, p := range cur.prefixes {
			for _, r := range paths {
				var derived []truth.Evidence
				if p != "" && cur.line >= 0 {
					derived = append(derived, evidence(f, f.Lines[cur.line]))
				}
				for _, m := range methods {
					out = append(out, endpointFact("spring.controller", f, l, m, joinPath(p, r, false), derived...))
				}
			}
		}
	}
	return out
}

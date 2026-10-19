package headless

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/internal/srccomment"
)

// manifestFile reports whether path is a package/build manifest, the only kind
// of file the fact schema lets emit declares_service and dependency.
func manifestFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "package.json", "go.mod", "pyproject.toml", "setup.py", "setup.cfg", "pipfile", "pom.xml",
		"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "cargo.toml",
		"composer.json", "gemfile", "mix.exs", "directory.packages.props":
		return true
	}
	if strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".csproj", ".fsproj", ".vbproj":
		return true
	}
	return false
}

var (
	tsDeclRe       = regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?(?:declare[ \t]+)?(?:interface|type)[ \t]+([A-Za-z_$][\w$]*)`)
	tsTypeImportRe = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+type[ \t]*\{([^}]*)\}`)
	tsInlineTypeRe = regexp.MustCompile(`\btype[ \t]+([A-Za-z_$][\w$]*)(?:[ \t]+as[ \t]+([A-Za-z_$][\w$]*))?`)
)

// tsTypeNames returns the names a JS/TS file declares or imports purely as types
// (interface, type alias, `import type`, `import { type X }`). A type is not a
// service, provider or persisted model, whatever the model says.
func tsTypeNames(content []byte) map[string]bool {
	out := map[string]bool{}
	for _, m := range tsDeclRe.FindAllSubmatch(content, -1) {
		out[string(m[1])] = true
	}
	addList := func(list string, inline bool) {
		for _, part := range strings.Split(list, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if inline {
				m := tsInlineTypeRe.FindStringSubmatch(part)
				if m == nil || !strings.HasPrefix(part, "type") {
					continue
				}
				if m[2] != "" {
					out[m[2]] = true
				} else {
					out[m[1]] = true
				}
				continue
			}
			fields := strings.Fields(part)
			out[fields[len(fields)-1]] = true // `X as Y` binds Y
		}
	}
	for _, m := range tsTypeImportRe.FindAllSubmatch(content, -1) {
		addList(string(m[1]), false)
	}
	for _, m := range regexp.MustCompile(`(?m)^[ \t]*import[ \t]*\{([^}]*)\}`).FindAllSubmatch(content, -1) {
		addList(string(m[1]), true)
	}
	return out
}

// typeFact reports whether a fact of a runtime kind points at a name the file
// only knows as a type.
func typeFact(kind string, f map[string]any, types map[string]bool) bool {
	switch kind {
	case "calls_service", "injects", "provides", "uses_model", "model":
		to, _ := f["to"].(string)
		return types[to]
	case "model_relation":
		to, _ := f["to"].(string)
		from, _ := f["from"].(string)
		return types[to] || types[from]
	}
	return false
}

func jsLike(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return true
	}
	return false
}

func startsLower(s string) bool {
	r := []rune(strings.TrimSpace(s))
	return len(r) > 0 && unicode.IsLower(r[0])
}

func absoluteURL(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// sanitizeFacts enforces the schema rules that models break regardless of how
// the prompt words them, so a wrong fact never reaches the graph:
//   - declares_service and dependency come from manifest files only;
//   - dependency and publishes_package from a manifest the deps pass reads
//     (package.json, csproj, go.mod, ...) are dropped: that pass owns them;
//   - an absolute URL is an outbound http_call, never an endpoint (inbound route)
//     or calls_endpoint (internal route path);
//   - calls_service, injects, provides, uses_model, model and model_relation
//     pointing at a TS interface or type alias declared or type-imported in the
//     same file are about a type, not a service or a persisted model;
//   - injects in a JS/TS file whose target starts with a lowercase letter names an
//     imported function or constant, not a provider (providers are classes);
//   - calls_service whose target is the called function itself (to == method) is
//     a plain function call, not a call to a service.
//
// It returns the facts to store and how many were dropped. Rewritten facts are
// not counted as dropped. Facts that need no change keep their original bytes.
func sanitizeFacts(path string, content []byte, facts json.RawMessage) (json.RawMessage, int) {
	var items []json.RawMessage
	if json.Unmarshal(facts, &items) != nil {
		return facts, 0
	}
	manifest := manifestFile(path)
	depsOwned := deps.IsManifest(path)
	var types map[string]bool
	if jsLike(path) {
		types = tsTypeNames(content)
	}
	commented, _ := srccomment.Mask(path, content)
	out := make([]json.RawMessage, 0, len(items))
	dropped, changed := 0, false
	for _, raw := range items {
		var f map[string]any
		if json.Unmarshal(raw, &f) != nil {
			out = append(out, raw)
			continue
		}
		kind, _ := f["kind"].(string)
		str := func(k string) string { s, _ := f[k].(string); return s }
		switch {
		case (kind == "dependency" || kind == "publishes_package") && depsOwned:
			// The deterministic deps pass owns what these manifests say about
			// dependencies and publications; a model fact would only duplicate it.
			dropped++
			changed = true
			continue
		case (kind == "declares_service" || kind == "dependency") && !manifest:
			dropped++
			changed = true
			continue
		case (kind == "endpoint" || kind == "calls_endpoint") && absoluteURL(str("target")):
			f["kind"] = "http_call"
			b, err := json.Marshal(f)
			if err == nil {
				raw = b
				changed = true
			}
		case types != nil && typeFact(kind, f, types):
			dropped++
			changed = true
			continue
		case kind == "injects" && jsLike(path) && startsLower(str("to")):
			dropped++
			changed = true
			continue
		case kind == "calls_service" && str("method") != "" && strings.EqualFold(str("to"), str("method")):
			dropped++
			changed = true
			continue
		case commented != nil && commentedOut(path, content, commented, kind, f):
			dropped++
			changed = true
			continue
		}
		out = append(out, raw)
	}
	if !changed {
		return facts, 0
	}
	b, err := json.Marshal(out)
	if err != nil {
		return facts, 0
	}
	return b, dropped
}

// commentedOut says whether the source lines a fact points at are all inside
// comments: a commented-out `// app.MapGet("/legacy/ping", ...)` is not a
// route. The fact carries no line, so it is found by its anchor text (the
// route path, the call's path, the topic string); a fact whose anchor is not in
// the file, or appears once outside a comment, is kept.
func commentedOut(path string, content []byte, mask []bool, kind string, f map[string]any) bool {
	str := func(k string) string { s, _ := f[k].(string); return strings.TrimSpace(s) }
	var anchor string
	switch kind {
	case "endpoint", "calls_endpoint":
		anchor = str("target")
	case "http_call":
		if i := strings.Index(str("target"), "/"); i >= 0 {
			anchor = str("target")[i:]
			if strings.HasPrefix(str("target"), "http") {
				if j := strings.Index(str("target")[8:], "/"); j >= 0 {
					anchor = str("target")[8+j:]
				}
			}
		}
	case "produces", "consumes":
		anchor = str("to")
	}
	if line, ok := f["line"].(float64); ok && line >= 1 {
		return lineInComment(content, mask, int(line))
	}
	// A route with parameters ("/orders/{id}") is written other ways in code;
	// judge only the literal head of it.
	if i := strings.IndexAny(anchor, "{:<["); i > 0 {
		anchor = strings.TrimRight(anchor[:i], "/")
	}
	// "/api/orders" may be written "api/orders" in a route attribute: any live
	// spelling keeps the fact.
	anchor = strings.TrimPrefix(anchor, "/")
	if len(anchor) < 3 {
		return false
	}
	text := string(content)
	found, live := false, false
	for from := 0; ; {
		i := strings.Index(text[from:], anchor)
		if i < 0 {
			break
		}
		at := from + i
		found = true
		if !mask[at] {
			live = true
			break
		}
		from = at + len(anchor)
	}
	return found && !live
}

// lineInComment is true when every non-blank byte of the 1-based line is
// inside a comment.
func lineInComment(content []byte, mask []bool, line int) bool {
	start, n := 0, 1
	for n < line {
		i := bytes.IndexByte(content[start:], '\n')
		if i < 0 {
			return false
		}
		start += i + 1
		n++
	}
	end := start
	for end < len(content) && content[end] != '\n' {
		end++
	}
	seen := false
	for i := start; i < end; i++ {
		if c := content[i]; c == ' ' || c == '\t' || c == '\r' {
			continue
		}
		seen = true
		if !mask[i] {
			return false
		}
	}
	return seen
}

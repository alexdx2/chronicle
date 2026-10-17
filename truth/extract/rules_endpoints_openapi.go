package extract

import (
	"encoding/json"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
	"gopkg.in/yaml.v3"
)

func init() {
	Rules = append(Rules, funcRule{"openapi.paths", matchOpenAPI, applyOpenAPI})
}

func matchOpenAPI(p string) bool {
	b := strings.ToLower(baseName(p))
	if !(strings.HasPrefix(b, "openapi") || strings.HasPrefix(b, "swagger")) {
		return false
	}
	return hasExt(b, ".json", ".yaml", ".yml")
}

var openAPIMethods = []string{"get", "post", "put", "delete", "patch"}

func applyOpenAPI(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var doc map[string]any
	var err error
	if hasExt(f.Path, ".json") {
		err = json.Unmarshal(f.Raw, &doc)
	} else {
		err = yaml.Unmarshal(f.Raw, &doc)
	}
	if err != nil {
		return nil
	}
	paths, _ := doc["paths"].(map[string]any)
	base, _ := doc["basePath"].(string)
	keys := sortedKeys(paths)
	var out []truth.Fact
	for _, p := range keys {
		if !strings.HasPrefix(p, "/") {
			continue // x-… extensions and anything that is not a path
		}
		ops, _ := paths[p].(map[string]any)
		for _, m := range openAPIMethods {
			if _, ok := ops[m]; !ok {
				continue
			}
			out = append(out, endpointFact("openapi.paths", f, openAPIPathLine(f, p), m, joinPath(base, p, false)))
		}
	}
	return out
}

// openAPIPathLine finds the line holding the path key; the first line when
// the text search finds nothing.
func openAPIPathLine(f *FileCtx, p string) (l lex.Line) {
	for _, ln := range f.Lines {
		c := ln.Code
		if strings.Contains(c, `"`+p+`"`) || strings.Contains(c, `'`+p+`'`) ||
			strings.HasPrefix(strings.TrimSpace(c), p+":") {
			return ln
		}
	}
	if len(f.Lines) == 0 {
		return lex.Line{Num: 1}
	}
	return f.Lines[0]
}

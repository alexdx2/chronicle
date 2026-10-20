package extract

import (
	"encoding/json"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules, funcRule{"npm.app", func(p string) bool { return baseName(p) == "package.json" }, applyNpmApp})
}

// applyNpmApp: a package.json that runs something is a deployable service: a
// start or serve script (node, nest, next, expo, ng serve ...), or a Vite
// project with a dev script. A workspace root is not one (it names the
// workspace), and neither is a package a Dockerfile-less library with only
// build scripts. The name is the package name without its scope.
func applyNpmApp(f *FileCtx) []truth.Fact {
	var doc map[string]json.RawMessage
	if json.Unmarshal(f.Raw, &doc) != nil {
		return nil
	}
	if _, ws := doc["workspaces"]; ws {
		return nil
	}
	var name string
	if json.Unmarshal(doc["name"], &name) != nil || strings.TrimSpace(name) == "" {
		return nil
	}
	var scripts map[string]string
	_ = json.Unmarshal(doc["scripts"], &scripts)
	runs := scripts["start"] != "" || scripts["serve"] != ""
	if !runs && scripts["dev"] != "" {
		for _, sec := range []string{"dependencies", "devDependencies"} {
			var deps map[string]json.RawMessage
			if json.Unmarshal(doc[sec], &deps) == nil {
				if _, ok := deps["vite"]; ok {
					runs = true
				}
			}
		}
	}
	if !runs {
		return nil
	}
	short := name[strings.LastIndex(name, "/")+1:]
	for _, l := range f.Lines {
		if strings.Contains(l.Code, `"name"`) {
			if fc, ok := serviceFact("npm.app", f, "own", short, "", "", l); ok {
				return []truth.Fact{fc}
			}
		}
	}
	return nil
}

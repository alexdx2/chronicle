package extract

import (
	"path"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// Env aliases: `export const API_URL = import.meta.env.VITE_API_URL` makes
// API_URL another name of the environment variable. A template use of the
// alias (fetch(`${API_URL}/x`)) yields a var:API_URL fact; when the variable
// itself resolves to a host in a config file (.env.production, app.config),
// the unresolved var fact is dropped, the host fact stands for it.

var jsEnvAlias = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?=\s*(?:import\.meta\.env|process\.env)\.([A-Za-z_][A-Za-z0-9_]*)\b`)

type envAlias struct{ name, env, dir string }

func collectEnvAliases(f *FileCtx) []envAlias {
	if f.IsTest || !hasExt(f.Path, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") {
		return nil
	}
	var out []envAlias
	for _, l := range f.Lines {
		for _, m := range jsEnvAlias.FindAllStringSubmatch(l.Code, -1) {
			out = append(out, envAlias{m[1], m[2], f.Dir})
		}
	}
	return out
}

// dropResolvedAliasVars removes var:ALIAS facts whose environment variable
// has a host fact (same config key) in a file at or above the alias file.
func dropResolvedAliasVars(all []truth.Fact, aliases []envAlias) []truth.Fact {
	if len(aliases) == 0 {
		return all
	}
	drop := map[string]bool{}
	for _, a := range aliases {
		for _, f := range all {
			if f.Class != truth.ClassHTTP || f.Attrs["host"] == "" || f.Attrs["config_key"] != a.env {
				continue
			}
			if under(a.dir, path.Dir(f.Primary.File)) {
				drop["var:"+a.name] = true
				break
			}
		}
	}
	out := all[:0:0]
	for _, f := range all {
		if f.Class == truth.ClassHTTP && drop[f.Key] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// under reports whether dir is base or below it.
func under(dir, base string) bool {
	return base == "." || dir == base || strings.HasPrefix(dir, base+"/")
}

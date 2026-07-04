package salience

import (
	pathpkg "path"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/registry"
)

// NoiseClassForPath returns the deterministic noise class ("generated",
// "test", "vendor") for a file path per the policy's noise_paths patterns, or
// "" when nothing matches. This is a pure path check — no LLM claim involved —
// so callers pass the result as Input.NoiseClass, which bypasses the demote
// confidence gate.
//
// Pattern forms (see registry.SaliencePolicy.NoisePaths):
//   - "dir/"  — matches a directory path segment ("generated/" hits
//     src/generated/x.ts but not src/generators.ts)
//   - other   — glob against the base name ("*.pb.go", "*.spec.*")
//
// Classes are checked in sorted order so multi-class matches resolve
// deterministically.
func NoiseClassForPath(p *registry.SaliencePolicy, path string) string {
	if p == nil || path == "" || len(p.NoisePaths) == 0 {
		return ""
	}
	norm := strings.ReplaceAll(path, "\\", "/")
	base := pathpkg.Base(norm)
	segs := strings.Split(norm, "/")
	dirs := segs[:len(segs)-1]

	classes := make([]string, 0, len(p.NoisePaths))
	for c := range p.NoisePaths {
		classes = append(classes, c)
	}
	sort.Strings(classes)

	for _, class := range classes {
		for _, pat := range p.NoisePaths[class] {
			if strings.HasSuffix(pat, "/") {
				want := strings.TrimSuffix(pat, "/")
				for _, d := range dirs {
					if d == want {
						return class
					}
				}
			} else if ok, _ := pathpkg.Match(pat, base); ok {
				return class
			}
		}
	}
	return ""
}

package manifest_test

import (
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
)

// TestMatchGlob_RealLeakedPaths pins the real leaked paths from the field
// (22,870 junk obligations) as a yes/no table. The old matchGlob split on
// the FIRST "**" only and matched the remainder against filepath.Base — so
// a trailing "**" (as in "**/__tests__/**") survived as literal text
// matched against a basename that can never contain "/": structurally
// incapable of matching. The bare "config.json" case pins the inverse
// defect: the old no-"**" fallback matched filepath.Base at ANY depth, so a
// bare exclude silently excluded every config.json in the tree.
func TestMatchGlob_RealLeakedPaths(t *testing.T) {
	yes := []struct{ path, pat string }{
		{"packages/support-core/src/__tests__/support/fake-store.ts", "**/__tests__/**"},
		{"voice-service/src/domain/__tests__/x.ts", "**/__tests__/**"},
		{"packages/ui/dist/index.js", "**/dist/**"},
		{"node_modules/lodash/index.js", "**/node_modules/**"},
		{"src/a/b/c.test.ts", "**/*.test.*"},
		{"arena-api/src/deep/nested/file.ts", "arena-api/**/*.ts"},
		{"Dockerfile", "Dockerfile"},
		{"src/main.ts", "src/**"},
	}
	no := []struct{ path, pat string }{
		{"src/tests_helper.ts", "**/__tests__/**"},
		{"src/distance.ts", "**/dist/**"},
		{"deep/config.json", "config.json"}, // bare name must NOT match at depth
		{"src/main.go", "src/**/*.ts"},
	}
	for _, c := range yes {
		if !manifest.MatchGlob(c.path, c.pat) {
			t.Errorf("MUST match: %q vs %q", c.path, c.pat)
		}
	}
	for _, c := range no {
		if manifest.MatchGlob(c.path, c.pat) {
			t.Errorf("must NOT match: %q vs %q", c.path, c.pat)
		}
	}
}

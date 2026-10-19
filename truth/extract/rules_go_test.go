package extract

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexdx2/chronicle-core/truth"
)

// goFacts extracts the given files (virtual path -> file under testdata/go)
// and returns the facts of one class as "source | key" lines, sorted.
func goFacts(t *testing.T, class truth.Class, files map[string]string) ([]truth.Fact, []string) {
	t.Helper()
	m := fstest.MapFS{}
	for vpath, disk := range files {
		data, err := os.ReadFile(filepath.Join("testdata", "go", disk))
		if err != nil {
			t.Fatal(err)
		}
		m[vpath] = &fstest.MapFile{Data: data}
	}
	rep, err := Extract(m)
	if err != nil {
		t.Fatal(err)
	}
	var facts []truth.Fact
	var lines []string
	for _, f := range rep.Facts {
		if f.Class == class {
			facts = append(facts, f)
			lines = append(lines, f.Sources[0]+" | "+f.Key)
		}
	}
	sort.Strings(lines)
	return facts, lines
}

func wantLines(src string, keys ...string) []string {
	var out []string
	for _, k := range keys {
		out = append(out, src+" | "+k)
	}
	sort.Strings(out)
	return out
}

func checkLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("facts mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestGoChiRoutes(t *testing.T) {
	facts, got := goFacts(t, truth.ClassEndpoints, map[string]string{
		"internal/api/router.go":      "chi_router.go",
		"internal/api/router_test.go": "chi_router_test.go",
	})
	checkLines(t, got, wantLines("go.chi",
		"GET /health",
		"GET /api/items", "POST /api/items", "PATCH /api/items",
		"GET /api/items/{}", "DELETE /api/items/{}",
		"PUT /api/settings",
		"GET /admin/stats",
		"GET /users", "GET /users/{}",
		"GET /v2/ping",
		"GET /multi",
	))
	for _, f := range facts {
		switch f.Key {
		case "GET /api/items/{}":
			// the primary is the Get line; the Route scopes that built the prefix are derived
			if f.Primary.Line != 16 || len(f.Derived) != 2 {
				t.Errorf("%s: primary %+v derived %v", f.Key, f.Primary, f.Derived)
			}
		case "GET /multi":
			if f.Primary.Line != 30 {
				t.Errorf("%s: primary line %d", f.Key, f.Primary.Line)
			}
		}
	}
}

func TestGoHTTPMux(t *testing.T) {
	_, got := goFacts(t, truth.ClassEndpoints, map[string]string{"cmd/srv/main.go": "http_mux.go"})
	checkLines(t, got, wantLines("go.http_mux",
		"GET /items", "POST /items/{}", "DELETE /items/{}/{}", "GET /", "GET /hosted", "PUT /default-mux"))
}

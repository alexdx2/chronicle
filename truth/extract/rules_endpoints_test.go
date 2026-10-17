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

type wantFact struct {
	src     string
	key     string
	derived bool // a class-level / prefix evidence line is expected
}

func endpointFacts(t *testing.T, vpath, disk string) []truth.Fact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "endpoints", disk))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Extract(fstest.MapFS{vpath: {Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	var out []truth.Fact
	for _, f := range rep.Facts {
		if f.Class == truth.ClassEndpoints {
			out = append(out, f)
		}
	}
	return out
}

func TestEndpointRules(t *testing.T) {
	cases := []struct {
		name  string
		vpath string
		disk  string
		want  []wantFact
	}{
		{"csharp controller", "src/OrdersController.cs", "csharp/OrdersController.cs", []wantFact{
			{"csharp.route_attr", "GET /api/orders", true},
			{"csharp.route_attr", "GET /api/orders/{}", true},
			{"csharp.route_attr", "POST /api/orders", true},
			{"csharp.route_attr", "PUT /api/orders/{}", true},
			{"csharp.route_attr", "DELETE /api/legacy/{}", false},
			{"csharp.route_attr", "GET /api/orders/export", true},
			{"csharp.route_attr", "POST /api/orders/search", true},
			{"csharp.route_attr", "GET /api/orders/summary", true},
			{"csharp.route_attr", "GET /api/orders/both", true},
			{"csharp.route_attr", "POST /api/orders/both", true},
			{"csharp.route_attr", "GET /health", false},
		}},
		{"csharp test file yields nothing", "tests/OrdersControllerTests.cs", "csharp/OrdersControllerTests.cs", nil},
		{"csharp minimal api", "Program.cs", "csharp/MinimalApi.cs", []wantFact{
			{"csharp.minimal_api", "GET /ping", false},
			{"csharp.minimal_api", "GET /api/v1/items", true},
			{"csharp.minimal_api", "POST /api/v1/items/{}", true},
		}},
		{"nest", "src/cats.controller.ts", "ts/cats.controller.ts", []wantFact{
			{"nest.controller", "GET /cats", true},
			{"nest.controller", "GET /cats/{}", true},
			{"nest.controller", "POST /cats/new", true},
			{"nest.controller", "DELETE /cats/{}", true},
			{"nest.controller", "GET /dogs/list", true},
			{"nest.controller", "GET /a/x", true},
			{"nest.controller", "GET /b/x", true},
			{"nest.controller", "GET /health", false},
		}},
		{"express", "src/routes.ts", "ts/routes.ts", []wantFact{
			{"express.router", "GET /users", false},
			{"express.router", "POST /users/{}/avatar", false},
			{"express.router", "DELETE /users/{}", false},
			{"express.router", "PATCH /p", false},
			{"express.router", "ALL /any", false},
		}},
		{"fastapi", "app/users.py", "py/users_router.py", []wantFact{
			{"fastapi.decorator", "GET /users", true},
			{"fastapi.decorator", "GET /users/{}", true},
			{"fastapi.decorator", "POST /users", true},
			{"fastapi.decorator", "GET /health", false},
			{"fastapi.decorator", "GET /users/multi", true},
			{"fastapi.decorator", "POST /users/multi", true},
			{"fastapi.decorator", "DELETE /admin/{}", true},
		}},
		{"flask", "app/main.py", "py/flask_app.py", []wantFact{
			{"flask.route", "GET /", false},
			{"flask.route", "GET /health", false},
			{"flask.route", "POST /orders/{}", true},
			{"flask.route", "PUT /orders/{}", true},
			{"flask.route", "GET /orders/list", true},
		}},
		{"django urls.py", "shop/urls.py", "py/django/urls.py", []wantFact{
			{"django.urls", "ANY /", false},
			{"django.urls", "ANY /orders/{}", false},
			{"django.urls", "ANY /legacy/{}", false},
			{"django.urls", "ANY /about", false},
		}},
		{"django non-urls file ignored", "shop/views.py", "py/django/urls.py", nil},
		{"openapi yaml", "docs/openapi.yaml", "openapi/openapi.yaml", []wantFact{
			{"openapi.paths", "GET /orders", false},
			{"openapi.paths", "POST /orders", false},
			{"openapi.paths", "GET /orders/{}", false},
		}},
		{"swagger json basePath", "swagger.json", "openapi/swagger.json", []wantFact{
			{"openapi.paths", "GET /v2/pets", false},
			{"openapi.paths", "PUT /v2/pets", false},
		}},
		{"openapi in tests dir ignored", "tests/openapi.yaml", "openapi/openapi.yaml", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := endpointFacts(t, c.vpath, c.disk)
			var g, w []string
			for _, f := range got {
				if len(f.Sources) != 1 {
					t.Errorf("%s: sources %v", f.Key, f.Sources)
					continue
				}
				g = append(g, f.Sources[0]+" | "+f.Key)
				if f.Primary.File != c.vpath || f.Primary.Line == 0 || f.Primary.Text == "" {
					t.Errorf("%s: bad primary %+v", f.Key, f.Primary)
				}
				if f.Attrs["method"] == "" || !strings.HasPrefix(f.Attrs["path"], "/") {
					t.Errorf("%s: bad attrs %v", f.Key, f.Attrs)
				}
				var wd *wantFact
				for i := range c.want {
					if c.want[i].key == f.Key {
						wd = &c.want[i]
					}
				}
				if wd != nil && (len(f.Derived) > 0) != wd.derived {
					t.Errorf("%s: derived=%v want %v", f.Key, f.Derived, wd.derived)
				}
			}
			for _, x := range c.want {
				w = append(w, x.src+" | "+x.key)
			}
			sort.Strings(g)
			sort.Strings(w)
			if strings.Join(g, "\n") != strings.Join(w, "\n") {
				t.Fatalf("facts mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(w, "\n  "))
			}
		})
	}
}

func TestEndpointPrimaryLines(t *testing.T) {
	var swagger, cs []truth.Fact
	swagger = endpointFacts(t, "openapi.yaml", "openapi/openapi.yaml")
	cs = endpointFacts(t, "Program.cs", "csharp/MinimalApi.cs")
	for _, f := range swagger {
		if f.Key == "GET /orders/{}" && f.Primary.Line != 12 {
			t.Errorf("openapi primary line %d", f.Primary.Line)
		}
	}
	for _, f := range cs {
		if f.Key == "GET /api/v1/items" && (f.Primary.Line != 4 || len(f.Derived) != 1 || f.Derived[0].Line != 3) {
			t.Errorf("minimal api evidence %+v %+v", f.Primary, f.Derived)
		}
	}
}

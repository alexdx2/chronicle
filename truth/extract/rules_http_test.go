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

func httpFacts(t *testing.T, vpath, disk string) []truth.Fact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "http", disk))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Extract(fstest.MapFS{vpath: {Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	var out []truth.Fact
	for _, f := range rep.Facts {
		if f.Class == truth.ClassHTTP {
			out = append(out, f)
		}
	}
	return out
}

func TestHTTPRules(t *testing.T) {
	cases := []struct {
		name  string
		vpath string
		disk  string
		want  []string // "source | key"
	}{
		{"csharp", "src/Clients.cs", "csharp/Clients.cs", []string{
			"csharp.httpclient_base | api.payments.io",
			"csharp.httpclient_base | tom-api.internal",
			"csharp.httpclient_base | raw.example.com",
			"csharp.http_literal | reports.example.com",
			"csharp.http_literal | hooks.example.com",
			"csharp.refit | ship.example.com",
		}},
		{"csharp test file", "tests/ClientsTests.cs", "csharp/ClientsTests.cs", nil},
		{"ts", "src/api.ts", "ts/api.ts", []string{
			"axios.create | api.payments.io",
			"fetch.literal | feed.example.com",
			"fetch.literal | var:HOST",
			"fetch.template | var:TOM_API_URL",
			"nest.httpservice | svc.example.com",
			"nest.httpservice | svc2.example.com",
		}},
		{"ts spec file", "src/api.spec.ts", "ts/api.spec.ts", nil},
		{"python", "app/client.py", "py/client.py", []string{
			"requests.literal | py.example.com",
			"requests.literal | httpx.example.com",
			"requests.literal | sess.example.com",
		}},
		{"python as ts ignored", "app/client.ts", "py/client.py", nil},
		{"appsettings", "appsettings.json", "config/appsettings.json", []string{
			"appsettings.url | var:TOM_API_URL",
			"appsettings.url | api.payments.io",
			"appsettings.url | var:WIN_URL",
			"appsettings.url | search.internal",
			"appsettings.url | top.example.com",
		}},
		{"appsettings env variant", "src/Api/AppSettings.Production.JSON", "config/appsettings.json", []string{
			"appsettings.url | var:TOM_API_URL",
			"appsettings.url | api.payments.io",
			"appsettings.url | var:WIN_URL",
			"appsettings.url | search.internal",
			"appsettings.url | top.example.com",
		}},
		{"appsettings comment retry", "appsettings.json", "config/appsettings.comment.json", []string{"appsettings.url | x.example.com"}},
		{"csharp pinned", "Pinned.cs", "csharp/Pinned.cs", []string{"csharp.httpclient_base | b.example.com"}},
		{"axios pinned", "pinned.ts", "ts/pinned.ts", []string{"axios.create | b.example.com"}},
		{"other json ignored", "config/other.json", "config/appsettings.json", nil},
		{"dotenv", "svc/.env", "config/env.sample", []string{
			"dotenv.url | tom.example.com",
			"dotenv.url | pay.example.com",
			"dotenv.url | var:SVC_HOST_VAR",
		}},
		{"dotenv variant", "svc/.env.production", "config/env.sample", []string{
			"dotenv.url | tom.example.com",
			"dotenv.url | pay.example.com",
			"dotenv.url | var:SVC_HOST_VAR",
		}},
		{"compose", "docker-compose.yml", "config/docker-compose.yml", []string{
			"compose.env | tom.internal",
			"compose.env | pay.example.com",
		}},
		{"compose override name", "deploy/compose.prod.yaml", "config/docker-compose.yml", []string{
			"compose.env | tom.internal",
			"compose.env | pay.example.com",
		}},
		{"k8s", "k8s/api.yaml", "config/k8s.yaml", []string{
			"k8s.env | search.example.com",
			"k8s.env | tom.example.com",
		}},
		{"yaml of another kind", "k8s/svc.yaml", "config/other.yaml", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := httpFacts(t, c.vpath, c.disk)
			var g []string
			for _, f := range got {
				if len(f.Sources) != 1 {
					t.Errorf("%s: sources %v", f.Key, f.Sources)
					continue
				}
				g = append(g, f.Sources[0]+" | "+f.Key)
				if f.Primary.File != c.vpath || f.Primary.Line == 0 || f.Primary.Text == "" || len(f.Derived) != 0 {
					t.Errorf("%s: bad evidence %+v", f.Key, f.Primary)
				}
				if _, ok := f.Attrs["host"]; !ok {
					t.Errorf("%s: attrs %v", f.Key, f.Attrs)
				}
				if strings.HasPrefix(f.Key, "var:") && (f.Attrs["host"] != "" || f.Attrs["var"] != strings.TrimPrefix(f.Key, "var:")) {
					t.Errorf("%s: var attrs %v", f.Key, f.Attrs)
				}
			}
			w := append([]string(nil), c.want...)
			sort.Strings(g)
			sort.Strings(w)
			if strings.Join(g, "\n") != strings.Join(w, "\n") {
				t.Fatalf("facts mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(w, "\n  "))
			}
		})
	}
}

func findHTTP(t *testing.T, fs []truth.Fact, key string) truth.Fact {
	t.Helper()
	for _, f := range fs {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("no fact %s", key)
	return truth.Fact{}
}

func TestHTTPAttrsAndLines(t *testing.T) {
	cs := httpFacts(t, "Wiring.cs", "csharp/Clients.cs")
	f := findHTTP(t, cs, "api.payments.io")
	if f.Attrs["host"] != "api.payments.io" || f.Attrs["path"] != "/v1" || f.Attrs["var"] != "" || f.Primary.Line != 8 {
		t.Errorf("payments: %+v %+v", f.Attrs, f.Primary)
	}
	if f := findHTTP(t, cs, "tom-api.internal"); f.Attrs["path"] != "/" {
		t.Errorf("tom attrs %v", f.Attrs)
	}

	if f := findHTTP(t, httpFacts(t, "Pinned.cs", "csharp/Pinned.cs"), "b.example.com"); f.Primary.Line != 6 || !strings.Contains(f.Primary.Text, "new Uri") {
		t.Errorf("cs pinned: %+v", f.Primary)
	}
	if f := findHTTP(t, httpFacts(t, "pinned.ts", "ts/pinned.ts"), "b.example.com"); f.Primary.Line != 2 {
		t.Errorf("axios pinned: %+v", f.Primary)
	}
	if f := findHTTP(t, httpFacts(t, "appsettings.json", "config/appsettings.comment.json"), "x.example.com"); f.Primary.Line != 3 {
		t.Errorf("comment retry: %+v", f.Primary)
	}

	st := httpFacts(t, "appsettings.json", "config/appsettings.json")
	f = findHTTP(t, st, "var:TOM_API_URL")
	if f.Attrs["var"] != "TOM_API_URL" || f.Attrs["config_key"] != "TomApiUrl" || f.Primary.Line != 5 {
		t.Errorf("tom: %+v %+v", f.Attrs, f.Primary)
	}
	f = findHTTP(t, st, "api.payments.io")
	if f.Attrs["var"] != "" || f.Attrs["config_key"] != "PaymentsBaseAddress" || f.Attrs["path"] != "/v1" {
		t.Errorf("payments cfg: %v", f.Attrs)
	}
	f = findHTTP(t, st, "top.example.com")
	if f.Attrs["path"] != "/path" || f.Primary.Line != 14 {
		t.Errorf("top: %+v %+v", f.Attrs, f.Primary)
	}

	k := httpFacts(t, "k8s.yaml", "config/k8s.yaml")
	if f := findHTTP(t, k, "tom.example.com"); f.Primary.Line != 20 || f.Attrs["config_key"] != "TOM_API_URL" {
		t.Errorf("k8s tom: %+v %v", f.Primary, f.Attrs)
	}
	if f := findHTTP(t, k, "search.example.com"); f.Primary.Line != 6 || f.Attrs["config_key"] != "SEARCH_URL" {
		t.Errorf("k8s search: %+v %v", f.Primary, f.Attrs)
	}
}

func TestHTTPConfigKeyRegex(t *testing.T) {
	for k, want := range map[string]bool{
		"TomApiUrl": true, "PaymentsBaseUrl": true, "ServiceEndpoint": true, "BASE_URL": true,
		"api_url": true, "Url": true, "WebHost": true, "NotAUrl": false, "Ghost": false, "Notes": false,
	} {
		if cfgURLKey.MatchString(k) != want {
			t.Errorf("%s: want %v", k, want)
		}
	}
}

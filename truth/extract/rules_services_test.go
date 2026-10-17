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

// serviceFacts maps virtual paths to files under testdata/services.
func serviceFacts(t *testing.T, files map[string]string) []truth.Fact {
	t.Helper()
	m := fstest.MapFS{}
	for vp, disk := range files {
		data, err := os.ReadFile(filepath.Join("testdata", "services", disk))
		if err != nil {
			t.Fatal(err)
		}
		m[vp] = &fstest.MapFile{Data: data}
	}
	rep, err := Extract(m)
	if err != nil {
		t.Fatal(err)
	}
	var out []truth.Fact
	for _, f := range rep.Facts {
		if f.Class == truth.ClassServices {
			out = append(out, f)
		}
	}
	return out
}

func TestServiceRules(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []wantFact
	}{
		{"compose", map[string]string{"docker-compose.yml": "compose/docker-compose.yml"}, []wantFact{
			{"compose.service", "own:orderapi", false},
			{"compose.service", "own:billingsvc", false},
			{"compose.service", "infra:db", false},
			{"compose.service", "infra:cache", false},
			{"compose.service", "infra:broker", false},
			{"compose.service", "infra:zk", false},
			{"compose.service", "external:proxy", false},
			{"compose.service", "external:web", false},
		}},
		{"k8s workloads only", map[string]string{"deploy/app.yaml": "k8s/workloads.yaml"}, []wantFact{
			{"k8s.workload", "own:ordersapi", false},
			{"k8s.workload", "own:nightlyreport", false},
			{"k8s.workload", "own:ledger", false},
		}},
		{"helm chart", map[string]string{"charts/pay/Chart.yaml": "helm/Chart.yaml"}, []wantFact{
			{"helm.chart", "own:paymentschart", false},
		}},
		{"helm library chart ignored", map[string]string{"charts/lib/Chart.yaml": "helm/Library.yaml"}, nil},
		{"terraform", map[string]string{"infra/main.tf": "tf/main.tf"}, []wantFact{
			{"terraform.compute", "own:thumbnailer", true},
			{"terraform.compute", "own:ordersecs", true},
			{"terraform.compute", "own:orderstask", true},
		}},
		{"cdk ts", map[string]string{"infra/stack.ts": "cdk/stack.ts"}, []wantFact{
			{"cdk.function", "own:imageresizer", false},
			{"cdk.function", "own:auditfn", false},
		}},
		{"cdk without import", map[string]string{"infra/plain.ts": "cdk/plain.ts"}, nil},
		{"cdk python", map[string]string{"infra/stack.py": "cdk/stack.py"}, []wantFact{
			{"cdk.function", "own:pyworker", false},
			{"cdk.function", "own:otherfn", false},
		}},
		{"csproj web sdk with assembly name", map[string]string{"src/Web/Web.csproj": "cs/Web.csproj"}, []wantFact{
			{"csharp.web_sdk", "own:acmegateway", false},
		}},
		{"csproj exe uses file stem", map[string]string{"src/Worker/Worker.csproj": "cs/Worker.csproj"}, []wantFact{
			{"csharp.web_sdk", "own:worker", false},
		}},
		{"cdk long props and nested new", map[string]string{"infra/props.ts": "cdk/props.ts"}, []wantFact{
			{"cdk.function", "own:longnamefn", false},
			{"cdk.function", "own:rtnamefn", false},
		}},
		{"compose merge keys and aliases", map[string]string{"docker-compose.yml": "compose/merge.yml"}, []wantFact{
			{"compose.service", "own:api", false},
			{"compose.service", "own:worker", false},
			{"compose.service", "infra:cache", false},
		}},
		{"csproj MSBuildProjectName", map[string]string{"src/Prop/Prop.csproj": "cs/Prop.csproj"}, []wantFact{
			{"csharp.web_sdk", "own:prop", false},
		}},
		{"csproj IsTestProject", map[string]string{"src/T/IsTest.csproj": "cs/IsTest.csproj"}, nil},
		{"nest two creates one fact", map[string]string{"api/main.ts": "nest/double.ts"}, []wantFact{
			{"nest.bootstrap", "own:api", false},
		}},
		{"helm template", map[string]string{"charts/x/templates/deployment.yaml": "helm/deployment.yaml"}, nil},
		{"library csproj", map[string]string{"src/Lib/Lib.csproj": "cs/Lib.csproj"}, nil},
		{"commented OutputType", map[string]string{"src/C/Commented.csproj": "cs/Commented.csproj"}, nil},
		{"test project exe", map[string]string{"src/T/Tests.csproj": "cs/Tests.csproj"}, nil},
		{"nest with package.json", map[string]string{
			"apps/orders/src/main.ts":  "nest/main.ts",
			"apps/orders/package.json": "nest/package.json",
		}, []wantFact{{"nest.bootstrap", "own:ordersapi", false}}},
		{"nest without package.json", map[string]string{"billing/src/main.ts": "nest/main.ts"}, []wantFact{
			{"nest.bootstrap", "own:billing", false},
		}},
		{"nest at root without package.json", map[string]string{"main.ts": "nest/main.ts"}, nil},
		{"nest monorepo uses apps dir", map[string]string{
			"apps/pay/src/main.ts": "nest/main.ts",
			"package.json":         "nest/package.json",
		}, []wantFact{{"nest.bootstrap", "own:pay", false}}},
		{"ts file without NestFactory", map[string]string{"src/other.ts": "nest/other.ts"}, nil},
		{"dockerfile uvicorn", map[string]string{"svc/api/Dockerfile": "docker/Dockerfile"}, []wantFact{
			{"python.entry", "own:api", false},
		}},
		{"dockerfile shell form gunicorn", map[string]string{"svc/gw/Dockerfile.prod": "docker/Dockerfile.shell"}, []wantFact{
			{"python.entry", "own:gw", false},
		}},
		{"dockerfile node", map[string]string{"svc/web/Dockerfile": "docker/Dockerfile.node"}, nil},
		{"root dockerfile", map[string]string{"Dockerfile": "docker/Dockerfile"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var g, w []string
			got := serviceFacts(t, c.files)
			for _, f := range got {
				if len(f.Sources) != 1 {
					t.Errorf("%s: sources %v", f.Key, f.Sources)
					continue
				}
				g = append(g, f.Sources[0]+" | "+f.Key)
				if f.Primary.File == "" || f.Primary.Line == 0 || f.Primary.Text == "" {
					t.Errorf("%s: bad primary %+v", f.Key, f.Primary)
				}
				if f.Attrs["name"] == "" || f.Attrs["kind"] == "" || !strings.HasPrefix(f.Key, f.Attrs["kind"]+":") {
					t.Errorf("%s: bad attrs %v", f.Key, f.Attrs)
				}
				for _, x := range c.want {
					if x.key == f.Key && (len(f.Derived) > 0) != x.derived {
						t.Errorf("%s: derived=%v want %v", f.Key, f.Derived, x.derived)
					}
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

func TestServiceAttrsAndLines(t *testing.T) {
	byKey := func(files map[string]string) map[string]truth.Fact {
		m := map[string]truth.Fact{}
		for _, f := range serviceFacts(t, files) {
			m[f.Key] = f
		}
		return m
	}
	c := byKey(map[string]string{"docker-compose.yml": "compose/docker-compose.yml"})
	check := func(key, name, kind, tech, image string, line int, m map[string]truth.Fact) {
		t.Helper()
		f, ok := m[key]
		if !ok {
			t.Fatalf("missing %s", key)
		}
		a := f.Attrs
		if a["name"] != name || a["kind"] != kind || a["tech"] != tech || a["image"] != image || f.Primary.Line != line {
			t.Errorf("%s: attrs %v line %d", key, a, f.Primary.Line)
		}
	}
	check("own:billingsvc", "Billing_Svc", "own", "", "registry.local/billing:latest", 6, c)
	check("infra:db", "db", "infra", "postgres", "bitnami/postgresql:16", 10, c)
	check("infra:broker", "broker", "infra", "kafka", "confluentinc/cp-kafka:7.5.0", 14, c)
	check("external:web", "web", "external", "", "nginx:latest", 20, c)

	k := byKey(map[string]string{"a.yaml": "k8s/workloads.yaml"})
	check("own:ordersapi", "orders-api", "own", "", "", 4, k)
	check("own:ledger", "ledger", "own", "", "", 28, k)

	tf := byKey(map[string]string{"main.tf": "tf/main.tf"})
	if f := tf["own:thumbnailer"]; f.Primary.Line != 2 || f.Derived[0].Line != 1 {
		t.Errorf("terraform evidence %+v %+v", f.Primary, f.Derived)
	}
	cs := byKey(map[string]string{"Worker.csproj": "cs/Worker.csproj"})
	if f := cs["own:worker"]; f.Primary.Line != 1 {
		t.Errorf("csproj stem line %d", f.Primary.Line)
	}
	cw := byKey(map[string]string{"Web.csproj": "cs/Web.csproj"})
	if f := cw["own:acmegateway"]; f.Primary.Line != 4 || f.Attrs["name"] != "Acme.Gateway" {
		t.Errorf("csproj assembly %+v %v", f.Primary, f.Attrs)
	}
	n := byKey(map[string]string{"a/main.ts": "nest/main.ts", "a/package.json": "nest/package.json"})
	if f := n["own:ordersapi"]; f.Primary.Line != 5 || f.Attrs["name"] != "orders-api" {
		t.Errorf("nest %+v %v", f.Primary, f.Attrs)
	}
	d := byKey(map[string]string{"x/Dockerfile": "docker/Dockerfile"})
	if f := d["own:x"]; f.Primary.Line != 3 {
		t.Errorf("docker line %+v", f.Primary)
	}
}

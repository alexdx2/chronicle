package truth

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/alexdx2/chronicle-core"

func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

func listDeps(t *testing.T, root string, pkgs ...string) []string {
	t.Helper()
	args := append([]string{"list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}"}, pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	var res []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			res = append(res, l)
		}
	}
	return res
}

// checkDirectImports verifies the direct imports of one truth package against
// the allowlist: std, truth itself, gopkg.in/yaml.v3, github.com/BurntSushi/toml,
// and, for truth/compare only, the snapshot package. The forbidden packages
// (deps, extract, scan, graph, manifest, store, validate) are not named: the
// list is an allowlist on purpose.
func checkDirectImports(pkg string, imports []string) error {
	truthPrefix := modulePath + "/truth"
	for _, imp := range imports {
		switch {
		case imp == truthPrefix || strings.HasPrefix(imp, truthPrefix+"/"):
		case imp == "gopkg.in/yaml.v3", imp == "github.com/BurntSushi/toml":
		case pkg == truthPrefix+"/compare" && imp == modulePath+"/snapshot":
		// reviewcheck is a comparer, not part of the independent reference
		// path: it may import review/ and snapshot/, but never graph/ or
		// scan/ directly (their transitive presence via review/ is allowed
		// in TestIndependence).
		case pkg == truthPrefix+"/reviewcheck" && (imp == modulePath+"/snapshot" || imp == modulePath+"/review"):
		case !strings.Contains(strings.SplitN(imp, "/", 2)[0], "."):
			// std library: first path element has no dot
		default:
			return fmt.Errorf("%s imports %q, which is not on the allowlist", pkg, imp)
		}
	}
	return nil
}

func TestCheckDirectImports(t *testing.T) {
	cmp := modulePath + "/truth/compare"
	cases := []struct {
		name    string
		pkg     string
		imports []string
		wantErr bool
	}{
		{"compare importing graph", cmp, []string{"fmt", modulePath + "/graph"}, true},
		{"compare importing snapshot", cmp, []string{"fmt", modulePath + "/snapshot"}, false},
		{"extract importing snapshot", modulePath + "/truth/extract", []string{modulePath + "/snapshot"}, true},
		{"reviewcheck importing review", modulePath + "/truth/reviewcheck", []string{modulePath + "/review", modulePath + "/snapshot"}, false},
		{"reviewcheck importing graph", modulePath + "/truth/reviewcheck", []string{modulePath + "/graph"}, true},
		{"reviewcheck importing scan", modulePath + "/truth/reviewcheck", []string{modulePath + "/scan"}, true},
		{"compare importing review", cmp, []string{modulePath + "/review"}, true},
		{"extract importing yaml and truth", modulePath + "/truth/extract", []string{"gopkg.in/yaml.v3", modulePath + "/truth"}, false},
	}
	for _, c := range cases {
		if err := checkDirectImports(c.pkg, c.imports); (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

// TestIndependence guarantees the truth reference never depends on Chronicle's
// own extraction pipeline. Direct imports of every truth package are
// allowlisted (see checkDirectImports). Transitive dependencies are allowed
// only for the base set (truth, yaml.v3, toml and its subpackages) and, for
// truth/compare alone, the non-std closure of the snapshot package, and for
// truth/reviewcheck alone, the closure of review and snapshot.
func TestIndependence(t *testing.T) {
	root := moduleRoot(t)
	truthPrefix := modulePath + "/truth"
	base := func(p string) bool {
		return p == truthPrefix || strings.HasPrefix(p, truthPrefix+"/") ||
			p == "gopkg.in/yaml.v3" || p == "github.com/BurntSushi/toml" || strings.HasPrefix(p, "github.com/BurntSushi/toml/")
	}

	snapshotSet := map[string]bool{}
	for _, p := range listDeps(t, root, "./snapshot") {
		snapshotSet[p] = true
	}

	reviewSet := map[string]bool{}
	for _, p := range listDeps(t, root, "./review") {
		reviewSet[p] = true
	}

	listPkgs := exec.Command("go", "list", "./truth/...")
	listPkgs.Dir = root
	out, err := listPkgs.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./truth/...: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		imp := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, pkg)
		imp.Dir = root
		iout, err := imp.CombinedOutput()
		if err != nil {
			t.Fatalf("go list imports %s: %v\n%s", pkg, err, iout)
		}
		if err := checkDirectImports(pkg, strings.Fields(string(iout))); err != nil {
			t.Error(err)
		}
		allowSnapshot := pkg == truthPrefix+"/compare"
		allowReview := pkg == truthPrefix+"/reviewcheck"
		for _, dep := range listDeps(t, root, pkg) {
			if base(dep) || (allowSnapshot && snapshotSet[dep]) || (allowReview && (reviewSet[dep] || snapshotSet[dep])) {
				continue
			}
			t.Errorf("%s depends on %q, which is not on the allowlist", pkg, dep)
		}
	}
}

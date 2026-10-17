package truth

import (
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

// TestIndependence guarantees the truth reference never depends on Chronicle's
// own extraction pipeline. The forbidden packages are deps, extract, scan,
// graph, manifest, store and validate. This is an allowlist on purpose: a new
// import must be added here deliberately, rather than a blocklist that silently
// permits whatever it forgot to name.
//
// Allowed: truth itself, gopkg.in/yaml.v3 and github.com/BurntSushi/toml; and,
// for truth/compare only, the snapshot package with its own non-std closure.
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

	// Each package under ./truth/... is checked on its own, so the snapshot
	// closure is allowed only for truth/compare. A missing truth/compare is fine.
	listPkgs := exec.Command("go", "list", "./truth/...")
	listPkgs.Dir = root
	out, err := listPkgs.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./truth/...: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		allowSnapshot := pkg == truthPrefix+"/compare"
		for _, dep := range listDeps(t, root, pkg) {
			if base(dep) || (allowSnapshot && snapshotSet[dep]) {
				continue
			}
			t.Errorf("%s depends on %q, which is not on the allowlist", pkg, dep)
		}
	}
}

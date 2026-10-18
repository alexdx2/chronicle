package truth_test

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract"
)

// Golden gates for the tom-and-jerry fixture.
//
// Editing rule: fixtures/tom-and-jerry/truth-expected.json is hand-written
// from source lines, never pasted from extractor output. When Gate 1 fails,
// fix the rule or re-read the source line and fix the golden; never loosen
// the gate.

const fixtureDir = "../fixtures/tom-and-jerry"

type goldenEntry struct {
	Key  string `json:"key"`
	File string `json:"file"`
	Line int    `json:"line"`
}

type goldenFile map[truth.Class][]goldenEntry

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	raw, err := os.ReadFile(fixtureDir + "/truth-expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	// "_comment" is a string; decode per class so it is ignored.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	g = goldenFile{}
	for k := range m {
		known := k == "_comment"
		for _, c := range truth.Classes {
			known = known || k == string(c)
		}
		if !known {
			t.Fatalf("truth-expected.json: unknown top-level key %q", k)
		}
	}
	for _, c := range truth.Classes {
		var es []goldenEntry
		if r, ok := m[string(c)]; ok {
			if err := json.Unmarshal(r, &es); err != nil {
				t.Fatalf("class %s: %v", c, err)
			}
		}
		g[c] = es
	}
	return g
}

func keySet(es []goldenEntry) map[string]bool {
	s := map[string]bool{}
	for _, e := range es {
		s[e.Key] = true
	}
	return s
}

func sorted(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Gate 1: the rules against the hand-written golden.
func TestGoldenExtractionMatchesSources(t *testing.T) {
	g := loadGolden(t)
	rep, err := extract.Extract(os.DirFS(fixtureDir))
	if err != nil {
		t.Fatal(err)
	}
	got := map[truth.Class]map[string]bool{}
	for _, c := range truth.Classes {
		got[c] = map[string]bool{}
	}
	for _, f := range rep.Facts {
		got[f.Class][f.Key] = true
	}
	cited := map[string]bool{}
	for _, f := range rep.Facts {
		for _, ev := range append([]truth.Evidence{f.Primary}, f.Derived...) {
			cited[fmt.Sprintf("%s:%d", ev.File, ev.Line)] = true
		}
	}
	for _, c := range truth.Classes {
		for _, e := range g[c] {
			if !cited[fmt.Sprintf("%s:%d", e.File, e.Line)] {
				t.Logf("%s: golden entry %s (%s:%d) is cited by no extracted fact", c, e.Key, e.File, e.Line)
			}
		}
	}
	for _, c := range truth.Classes {
		want := keySet(g[c])
		switch c {
		case truth.ClassEndpoints, truth.ClassHTTP, truth.ClassDeps, truth.ClassServices, truth.ClassPackages:
			if len(want) > 0 && len(got[c]) == 0 {
				t.Errorf("%s: golden has %d entries but nothing was extracted (vacuous pass)", c, len(want))
			}
		}
		var missing, extra []string
		for _, k := range sorted(want) {
			if !got[c][k] {
				missing = append(missing, k)
			}
		}
		for _, k := range sorted(got[c]) {
			if !want[k] {
				extra = append(extra, k)
			}
		}
		switch c {
		case truth.ClassEndpoints, truth.ClassChannels:
			for _, k := range missing {
				t.Errorf("%s: golden key not extracted: %s", c, k)
			}
			for _, k := range extra {
				t.Errorf("%s: extracted key not in golden: %s", c, k)
			}
		default:
			for _, k := range extra {
				t.Errorf("%s: extracted key not in golden: %s", c, k)
			}
		}
		t.Logf("%s: golden recall %d/%d (missing %v)", c, len(want)-len(missing), len(want), missing)
	}
}

// Gate 2: the golden against the independent graph golden, no extraction.
func TestGoldenAgainstExpectedGraph(t *testing.T) {
	g := loadGolden(t)
	raw, err := os.ReadFile(fixtureDir + "/expected-graph-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []struct {
			NodeKey string `json:"node_key"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &graph); err != nil {
		t.Fatal(err)
	}
	const epPrefix = "contract:endpoint:tom-and-jerry:"
	const topicPrefix = "contract:topic:tom-and-jerry:"
	endpoints, topics := map[string]bool{}, map[string]bool{}
	// modelled reports whether the graph golden knows the service at all
	// (a service:service:<domain>:<dir> node). A service the graph golden does not model
	// (scoreboard-api today) cannot be calibrated against it; such entries are
	// logged, not failed. Any service the graph does model stays strict.
	modelled := func(file string) bool {
		dir := strings.SplitN(file, "/", 2)[0]
		suffix := ":" + dir
		for _, n := range graph.Nodes {
			if strings.HasPrefix(n.NodeKey, "service:service:") && strings.HasSuffix(n.NodeKey, suffix) {
				return true
			}
		}
		return false
	}
	for _, n := range graph.Nodes {
		switch {
		case strings.HasPrefix(n.NodeKey, epPrefix):
			rest := strings.TrimPrefix(n.NodeKey, epPrefix)
			method, p, ok := strings.Cut(rest, "__")
			if !ok {
				continue
			}
			// Valid only because fixture paths contain no underscores:
			// the graph writes "/" as "_" in endpoint node keys.
			endpoints[truth.EndpointKey(strings.ToUpper(method), "/"+strings.ReplaceAll(p, "_", "/"))] = true
		case strings.HasPrefix(n.NodeKey, topicPrefix):
			topics[truth.CanonName(strings.TrimPrefix(n.NodeKey, topicPrefix))] = true
		}
	}
	for _, e := range g[truth.ClassEndpoints] {
		if !endpoints[e.Key] {
			if !modelled(e.File) {
				t.Logf("golden endpoint %q: service of %s is not modelled by expected-graph-v2.json, skipped", e.Key, e.File)
				continue
			}
			t.Errorf("golden endpoint %q is not in expected-graph-v2.json", e.Key)
		}
	}
	for _, e := range g[truth.ClassChannels] {
		name := e.Key
		if _, rest, ok := strings.Cut(name, ":"); ok {
			name = rest
		}
		if i := strings.LastIndex(name, "#"); i >= 0 {
			name = name[:i]
		}
		if !topics[truth.CanonName(name)] {
			t.Errorf("golden channel %q is not in expected-graph-v2.json", e.Key)
		}
	}
}

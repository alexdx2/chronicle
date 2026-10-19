package extract

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

// TestFleetRepos compares the extraction of fixture repos with their
// hand-written truth-expected.json. It runs only when CHRONICLE_FLEET_DIR
// (a directory of repos) and CHRONICLE_FLEET_REPOS (comma separated names)
// are set; it is a development aid for new rules, not a gate.
func TestFleetRepos(t *testing.T) {
	dir, names := os.Getenv("CHRONICLE_FLEET_DIR"), os.Getenv("CHRONICLE_FLEET_REPOS")
	if dir == "" || names == "" {
		t.Skip("CHRONICLE_FLEET_DIR / CHRONICLE_FLEET_REPOS not set")
	}
	for _, name := range strings.Split(names, ",") {
		name := name
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(dir, name)
			rep, err := Extract(hideSheets{os.DirFS(root)})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(root, "truth-expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var gold map[string]json.RawMessage
			if err := json.Unmarshal(raw, &gold); err != nil {
				t.Fatal(err)
			}
			got := map[string]map[string]bool{}
			for _, f := range rep.Facts {
				c := string(f.Class)
				if got[c] == nil {
					got[c] = map[string]bool{}
				}
				got[c][f.Key] = true
			}
			bad := false
			for _, c := range truth.Classes {
				var es []struct{ Key string }
				if r, ok := gold[string(c)]; ok {
					_ = json.Unmarshal(r, &es)
				}
				want := map[string]bool{}
				for _, e := range es {
					want[e.Key] = true
				}
				var miss, extra []string
				for k := range want {
					if !got[string(c)][k] {
						miss = append(miss, k)
					}
				}
				for k := range got[string(c)] {
					if !want[k] {
						extra = append(extra, k)
					}
				}
				sort.Strings(miss)
				sort.Strings(extra)
				if len(miss)+len(extra) > 0 {
					bad = true
					t.Errorf("%s: missing %q, extra %q", c, miss, extra)
				}
			}
			if !bad {
				t.Logf("%s matches the golden", name)
			}
		})
	}
}

type hideSheets struct{ fs.FS }

func (h hideSheets) Open(name string) (fs.File, error) {
	if strings.HasPrefix(filepath.Base(name), "truth-expected") {
		return nil, fs.ErrNotExist
	}
	return h.FS.Open(name)
}

func (h hideSheets) ReadDir(name string) ([]fs.DirEntry, error) {
	es, err := fs.ReadDir(h.FS, name)
	out := es[:0:0]
	for _, e := range es {
		if !strings.HasPrefix(e.Name(), "truth-expected") {
			out = append(out, e)
		}
	}
	return out, err
}

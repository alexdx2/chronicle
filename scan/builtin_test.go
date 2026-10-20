package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract"
	"github.com/alexdx2/chronicle-core/truth/scancfg"
)

// The builtin YAML rules and their Go twins must agree on the reference
// fixtures: the YAML may find fewer (a template sees one file and one call
// shape) but never a fact the reference does not.
func TestBuiltinRulesAgreeWithGoRules(t *testing.T) {
	cfg, err := scancfg.Parse(mustRead(t, "builtin/channels.yaml"), scancfg.SourceBuiltin)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := scancfg.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"java/KStreams.java":   "App.java",
		"java/NotStreams.java": "Not.java",
		"py/mqtt_client.py":    "m.py",
		"py/mqtt_off.py":       "off.py",
	}
	minFacts := map[string]int{"java/KStreams.java": 3, "py/mqtt_client.py": 3}
	for fix, vpath := range fixtures {
		src, err := os.ReadFile(filepath.Join("..", "truth", "extract", "testdata", "channels", filepath.FromSlash(fix)))
		if err != nil {
			t.Fatal(err)
		}
		rep, err := extract.Extract(fstest.MapFS{vpath: {Data: src}})
		if err != nil {
			t.Fatal(err)
		}
		ref := map[string]bool{}
		for _, f := range rep.Facts {
			if f.Class == truth.ClassChannels {
				ref[f.Key] = true
			}
		}
		got := map[string]bool{}
		for _, f := range eng.Run(vpath, src, nil) {
			got[truth.ChannelKey(f.Kind, strings.ToLower(f.Name), f.Side)] = true
		}
		for k := range got {
			if !ref[k] {
				t.Errorf("%s: YAML found %s, the reference did not (reference %v)", fix, k, ref)
			}
		}
		if len(got) < minFacts[fix] {
			t.Errorf("%s: YAML found only %v, want at least %d", fix, got, minFacts[fix])
		}
		if len(ref) == 0 && len(got) != 0 {
			t.Errorf("%s: gate failed: %v", fix, got)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

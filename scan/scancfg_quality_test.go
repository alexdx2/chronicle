package scan

import (
	"context"
	"errors"

	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/truth/scancfg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const oneRule = "channels:\n  - id: r\n    patterns: [{call: 'bus.%s({name}', side: produce}]\n"

// The cache must notice a rewritten scan.yaml even when size and mtime are the
// same (coarse file systems, checkouts that restore the mtime).
func TestLoadScanConfig_CacheSeesSameSizeSameMtimeRewrite(t *testing.T) {
	root := writeTree(t, map[string]string{".depbot/scan.yaml": strings.Replace(oneRule, "%s", "aaa", 1)})
	p := filepath.Join(root, ".depbot", "scan.yaml")
	st, _ := os.Stat(p)
	first := LoadScanConfig(root)
	if first.Channels[len(first.Channels)-1].Patterns[0].Call != "bus.aaa({name}" {
		t.Fatalf("first: %+v", first.Channels)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(oneRule, "%s", "bbb", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, st.ModTime(), st.ModTime())
	second := LoadScanConfig(root)
	if got := second.Channels[len(second.Channels)-1].Patterns[0].Call; got != "bus.bbb({name}" {
		t.Fatalf("stale configuration served from the cache: %s", got)
	}
	if first.Hash() == second.Hash() {
		t.Fatal("versions did not change")
	}
}

func TestLoadScanConfig_FleetLayerBetweenBuiltinAndProject(t *testing.T) {
	fleet := t.TempDir()
	if err := os.WriteFile(filepath.Join(fleet, "b.yaml"), []byte(strings.Replace(oneRule, "%s", "fleet", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fleet, "a.yml"), []byte("channels:\n  - id: only-fleet\n    patterns: [{call: 'q.put({name}', side: produce}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(fleet, "broken.yaml"), []byte("channels: [oops"), 0o644)
	_ = os.WriteFile(filepath.Join(fleet, "notes.txt"), []byte("not yaml"), 0o644)
	t.Setenv("CHRONICLE_SCAN_CONFIG_DIR", fleet)

	root := writeTree(t, map[string]string{"x.go": "package x\n"})
	cfg := LoadScanConfig(root)
	byID := map[string]string{}
	for _, c := range cfg.Channels {
		byID[c.ID] = c.Source + "/" + map[bool]string{true: "reviewed", false: "unreviewed"}[c.Reviewed]
	}
	if byID["r"] != "fleet/reviewed" || byID["only-fleet"] != "fleet/reviewed" {
		t.Fatalf("fleet layer: %v", byID)
	}
	if byID["kafka-streams"] == "" {
		t.Fatalf("builtin layer lost: %v", byID)
	}

	// the project layer replaces a fleet rule with the same id and is not reviewed unless it says so
	if err := os.MkdirAll(filepath.Join(root, ".depbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".depbot", "scan.yaml"), []byte(strings.Replace(oneRule, "%s", "project", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = LoadScanConfig(root)
	for _, c := range cfg.Channels {
		if c.ID == "r" && (c.Source != "project" || c.Reviewed || !strings.Contains(c.Patterns[0].Call, "project")) {
			t.Fatalf("project did not override: %+v", c)
		}
	}
	// a changed fleet file is picked up without touching the project file
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(fleet, "a.yml"), []byte("channels:\n  - id: only-fleet\n    patterns: [{call: 'q.push({name}', side: produce}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = LoadScanConfig(root)
	for _, c := range cfg.Channels {
		if c.ID == "only-fleet" && !strings.Contains(c.Patterns[0].Call, "q.push") {
			t.Fatalf("fleet change not seen: %+v", c)
		}
	}
}

func TestScanConfigVersion(t *testing.T) {
	t.Setenv("CHRONICLE_SCAN_CONFIG_DIR", "")
	bare := writeTree(t, map[string]string{"x.go": "package x\n"})
	vb := ScanConfigVersion(bare)
	if !strings.HasPrefix(vb, "+scan-config@") {
		t.Fatalf("builtin rules always count: %q", vb)
	}
	with := writeTree(t, map[string]string{".depbot/scan.yaml": strings.Replace(oneRule, "%s", "a", 1)})
	if vw := ScanConfigVersion(with); vw == vb || !strings.HasPrefix(vw, "+scan-config@") {
		t.Fatalf("project layer must change the version: %q vs %q", vw, vb)
	}
}

func TestScanConfigGet(t *testing.T) {
	t.Setenv("CHRONICLE_SCAN_CONFIG_DIR", "")
	none := ScanConfigGet(writeTree(t, map[string]string{"x.go": "package x\n"}))
	if none.Exists || none.ProjectYAML != "" || none.ProjectErr != "" || len(none.Rules) == 0 || none.Version == "" {
		t.Fatalf("no project file: %+v", none)
	}
	yaml := "provenance: llm:m\n" + strings.Replace(oneRule, "%s", "a", 1) + "documents:\n  - {id: d1, glob: '*.json', emit: [{name: '{value}', side: produce}]}\n"
	got := ScanConfigGet(writeTree(t, map[string]string{".depbot/scan.yaml": yaml}))
	if !got.Exists || got.ProjectYAML != yaml {
		t.Fatalf("project text: %+v", got)
	}
	joined := strings.Join(got.Rules, "\n")
	for _, want := range []string{"r (channel, llm:m, unreviewed)", "d1 (document, llm:m, unreviewed)", "kafka-streams (channel, builtin, reviewed)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rules lack %q:\n%s", want, joined)
		}
	}
	bad := ScanConfigGet(writeTree(t, map[string]string{".depbot/scan.yaml": "channels: [oops"}))
	if bad.ProjectErr == "" || !bad.Exists {
		t.Fatalf("broken project file must be reported: %+v", bad)
	}
}

// The answer sheet of a repository is not input for a rule author or an explain.
func TestScanConfig_AnswerSheetsAreNotEvidence(t *testing.T) {
	root := writeTree(t, map[string]string{
		"truth-expected.json":       `{"channels":[{"Key":"topic:secret#declare"}]}`,
		"truth-expected.fleet.json": `{"x":1}`,
		"svc/app.py":                "import faust\napp.topic('real')\n",
	})
	cand := "channels:\n  - id: faust\n    patterns: [{call: 'app.topic({name}', side: declare}]\n"
	dr := ScanConfigDryRun(root, cand)
	for _, f := range dr.Facts {
		if strings.Contains(f.File, "truth-expected") {
			t.Fatalf("answer sheet read as input: %+v", f)
		}
	}
	if len(dr.Facts) != 1 || dr.Facts[0].Key != "topic:real#declare" {
		t.Fatalf("facts: %+v", dr.Facts)
	}
}

// A dry run never writes into the repository.
func TestScanConfigDryRun_ReadOnly(t *testing.T) {
	root := writeTree(t, map[string]string{"svc/app.py": "import faust\napp.topic('real')\n"})
	before := snapshotTree(t, root)
	_ = ScanConfigDryRun(root, "channels:\n  - id: faust\n    patterns: [{call: 'app.topic({name}', side: declare}]\n")
	_ = ScanConfigDryRun(root, "channels: [oops")
	_ = ScanConfigExplain(root, "svc/app.py")
	_ = ScanConfigGet(root)
	if after := snapshotTree(t, root); after != before {
		t.Fatalf("read-only tools changed the tree:\nbefore: %s\nafter:  %s", before, after)
	}
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			c, _ := os.ReadFile(p)
			b.WriteString(p + ":" + string(c) + "|")
		}
		return nil
	})
	return b.String()
}

// Save writes exactly what it was given and nothing else, creating .depbot.
func TestScanConfigSave_WritesProjectFileOnly(t *testing.T) {
	root := writeTree(t, map[string]string{"svc/app.py": "import faust\napp.topic('a')\n"})
	cand := "channels:\n  - id: faust\n    patterns: [{call: 'app.topic({name}', side: declare}]\n"
	res, err := ScanConfigSave(root, cand, false)
	if err != nil || !res.Saved {
		t.Fatalf("%+v %v", res, err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".depbot", "scan.yaml"))
	if err != nil || string(b) != cand {
		t.Fatalf("file: %q %v", b, err)
	}
	// an invalid candidate is refused and leaves the file alone
	res, _ = ScanConfigSave(root, "channels: [{id: BAD}]", true)
	if res.Saved {
		t.Fatal("invalid candidate saved")
	}
	if b2, _ := os.ReadFile(filepath.Join(root, ".depbot", "scan.yaml")); string(b2) != cand {
		t.Fatal("invalid save changed the file")
	}
}

type errBackend struct{}

func (errBackend) Name() string  { return "err" }
func (errBackend) Model() string { return "err-model" }
func (errBackend) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("backend down")
}

const authorRepo = "import mybus\nbus.announce('orders.v1')\n"

func authorTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{"svc/app.py": authorRepo})
}

func fileExists(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".depbot", "scan.yaml"))
	return err == nil
}

func TestAuthor_BackendErrorSavesNothing(t *testing.T) {
	root := authorTree(t)
	res, err := ScanConfigAuthor(context.Background(), errBackend{}, root, AuthorOptions{})
	if err == nil || res.Saved || fileExists(root) {
		t.Fatalf("saved on backend failure: %+v %v", res, err)
	}
}

func TestAuthor_NeverValidNeverSaved(t *testing.T) {
	root := authorTree(t)
	b := &seqBackend{answers: []string{"channels: [{id: Bad}]", "not yaml: [", "bogus: 1"}}
	res, err := ScanConfigAuthor(context.Background(), b, root, AuthorOptions{MaxRounds: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Saved || res.Refused == "" || len(res.Rounds) != 3 || fileExists(root) {
		t.Fatalf("%+v", res)
	}
}

func TestAuthor_ValidButEmptyIsNotAResult(t *testing.T) {
	root := authorTree(t)
	// valid, matches nothing in the repository
	b := &seqBackend{answers: []string{"version: 1\nchannels:\n  - id: nothing\n    patterns: [{call: 'nope.send({name}', side: produce}]\n"}}
	res, err := ScanConfigAuthor(context.Background(), b, root, AuthorOptions{MaxRounds: 2})
	if err != nil || res.Saved || res.Refused == "" || fileExists(root) {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(b.users[1], "nothing") {
		t.Error("the empty result must be fed back")
	}
}

// A model cannot present its rules as a human's: whatever provenance or
// reviewed mark the answer carries, the saved file is stamped llm:<model> and
// its rules stay unreviewed.
func TestAuthor_ModelCannotPoseAsHuman(t *testing.T) {
	for name, answer := range map[string]string{
		"provenance project": "version: 1\nprovenance: project\nchannels:\n  - id: mybus\n    reviewed: true\n    patterns: [{call: 'bus.announce({name}', side: produce}]\n",
		"provenance other":   "version: 1\nprovenance: llm:other-model\nchannels:\n  - id: mybus\n    reviewed: true\n    patterns: [{call: 'bus.announce({name}', side: produce}]\n",
		"no version line":    "channels:\n  - id: mybus\n    reviewed: true\n    patterns: [{call: 'bus.announce({name}', side: produce}]\n",
		"reviewed only":      "version: 1\nchannels:\n  - id: mybus\n    reviewed: true\n    patterns: [{call: 'bus.announce({name}', side: produce}]\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := authorTree(t)
			res, err := ScanConfigAuthor(context.Background(), &seqBackend{answers: []string{answer}}, root, AuthorOptions{})
			if err != nil || !res.Saved {
				t.Fatalf("%+v %v", res, err)
			}
			b, _ := os.ReadFile(filepath.Join(root, ".depbot", "scan.yaml"))
			if n := strings.Count(string(b), "provenance:"); n != 1 || !strings.Contains(string(b), "provenance: llm:seq-model") {
				t.Fatalf("provenance not stamped once:\n%s", b)
			}
			for _, c := range LoadScanConfig(root).Channels {
				if c.ID == "mybus" && (c.Reviewed || c.Source != "llm:seq-model") {
					t.Fatalf("rule posed as human: %+v", c)
				}
			}
		})
	}
}

func TestAuthor_ContextCancelled(t *testing.T) {
	root := authorTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &ctxBackend{}
	if res, err := ScanConfigAuthor(ctx, b, root, AuthorOptions{}); err == nil || res.Saved || fileExists(root) {
		t.Fatalf("%+v %v", res, err)
	}
}

type ctxBackend struct{}

func (ctxBackend) Name() string  { return "ctx" }
func (ctxBackend) Model() string { return "ctx-model" }
func (ctxBackend) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, ctx.Err()
}

func TestStripFences(t *testing.T) {
	for in, want := range map[string]string{
		"```yaml\na: 1\n```":          "a: 1\n",
		"```\na: 1\n```\n":            "a: 1\n",
		"a: 1":                        "a: 1\n",
		"  \n```yml\na: 1\nb: 2\n```": "a: 1\nb: 2\n",
	} {
		if got := stripFences(in); got != want {
			t.Errorf("stripFences(%q) = %q, want %q", in, got, want)
		}
	}
}

// explain takes a path from a model: it must never read outside the repository.
func TestScanConfigExplain_StaysInsideRepository(t *testing.T) {
	outer := t.TempDir()
	if err := os.WriteFile(filepath.Join(outer, "secret.py"), []byte("import faust\napp.topic('leaked')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(outer, "repo")
	cand := "channels:\n  - id: faust\n    patterns: [{call: 'app.topic({name}', side: declare}]\n"
	for p, b := range map[string]string{"repo/.depbot/scan.yaml": cand, "repo/ok.py": "app.topic('inside')\n"} {
		fp := filepath.Join(outer, p)
		_ = os.MkdirAll(filepath.Dir(fp), 0o755)
		if err := os.WriteFile(fp, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(outer, "secret.py"), filepath.Join(root, "link.py")); err != nil {
		t.Fatal(err)
	}
	if got := ScanConfigExplain(root, "ok.py"); len(got) != 1 {
		t.Fatalf("control: %+v", got)
	}
	for _, f := range []string{"../secret.py", filepath.Join(outer, "secret.py"), "./../secret.py", "a/../../secret.py", "link.py"} {
		if got := ScanConfigExplain(root, f); len(got) != 0 {
			t.Errorf("explain %q read outside the repository: %+v", f, got)
		}
	}
}

func TestScanConfigDryRun_DoesNotFollowSymlinksOutside(t *testing.T) {
	outer := t.TempDir()
	_ = os.WriteFile(filepath.Join(outer, "secret.py"), []byte("import faust\napp.topic('leaked')\n"), 0o644)
	root := writeTree(t, map[string]string{"in.py": "app.topic('inside')\n"})
	if err := os.Symlink(filepath.Join(outer, "secret.py"), filepath.Join(root, "link.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outer, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	dr := ScanConfigDryRun(root, "channels:\n  - id: faust\n    patterns: [{call: 'app.topic({name}', side: declare}]\n")
	for _, f := range dr.Facts {
		if strings.Contains(f.Key, "leaked") {
			t.Fatalf("followed a symlink out of the repository: %+v", f)
		}
	}
	if len(dr.Facts) != 1 {
		t.Fatalf("facts: %+v", dr.Facts)
	}
}

// What the builtin rules must NOT see: look-alikes in other libraries, calls
// without a name, text inside strings and comments, and files outside the gate.
func TestBuiltinRules_NoFalsePositives(t *testing.T) {
	cfg, err := scancfg.Parse(mustRead(t, "builtin/channels.yaml"), scancfg.SourceBuiltin)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := scancfg.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	streamsHead := "import org.apache.kafka.streams.StreamsBuilder;\nclass A { void f(StreamsBuilder b) {\n"
	cases := []struct {
		name, file, src string
		want            []string // keys expected, in addition to nothing else
	}{
		{"java stream() with no name", "A.java", streamsHead + "list.stream().map(x -> x);\n}}", nil},
		{"java collection stream in a non-streams file", "B.java", "class B { void f(){ list.stream(\"x\"); } }", nil},
		{"java call text in a string", "A.java", streamsHead + "log.info(\"b.stream(\\\"orders\\\")\");\n}}", nil},
		{"java call in a line comment", "A.java", streamsHead + "// b.to(\"orders\");\n}}", nil},
		{"java call in a block comment", "A.java", streamsHead + "/* b.to(\"orders\") */\n}}", nil},
		{"java unresolved variable", "A.java", streamsHead + "b.stream(topicName);\n}}", nil},
		{"java real call", "A.java", streamsHead + "b.stream(\"orders\")\n    .to(\"audit\");\n}}", []string{"topic:orders#consume", "topic:audit#produce"}},
		{"java chained over lines", "A.java", streamsHead + "b\n  .stream(\n   \"orders\")\n  .to( \"audit\" );\n}}", []string{"topic:orders#consume", "topic:audit#produce"}},
		{"python redis publish", "r.py", "import redis\nr.publish('chan', 'x')\n", nil},
		{"python mqtt mentioned only in a comment", "m.py", "# uses mqtt later\nclient.publish('a/b', 1)\n", nil},
		{"python mqtt real", "m.py", "import paho.mqtt.client as mqtt\nc.publish('a/b', 1)\nc.subscribe(\"c/d\")\n", []string{"topic:a/b#produce", "topic:c/d#consume"}},
		{"js publish without the mqtt import", "a.js", "const bus = require('events');\nbus.publish('x');\n", nil},
		{"js mqtt in a string only", "a.js", "const s = \"mqtt\";\nbus.publish('x');\n", nil},
		{"js mqtt real", "a.ts", "import mqtt from 'mqtt';\nclient.publish('t/1', 'm');\nclient.subscribe(\"t/2\");\n", []string{"topic:t/1#produce", "topic:t/2#consume"}},
		{"wrong extension", "a.rb", "import mqtt\nclient.publish('t/1')\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]bool{}
			for _, f := range eng.Run(c.file, []byte(c.src), nil) {
				got[f.Key()] = true
			}
			for _, k := range c.want {
				if !got[k] {
					t.Errorf("missing %s (got %v)", k, got)
				}
			}
			if len(got) != len(c.want) {
				t.Errorf("got %v, want exactly %v", got, c.want)
			}
		})
	}
}

// With an answer sheet, rules whose facts are mostly wrong are not saved, and
// the model is told when it found the channel but gave it the wrong role.
func TestAuthor_WrongSidesAreNotSaved(t *testing.T) {
	root := writeTree(t, map[string]string{
		"svc/app.py":          "import mybus\nbus.announce('orders.v1')\nbus.announce('refunds.v1')\n",
		"truth-expected.json": `{"channels":[{"key":"topic:orders.v1#produce"},{"key":"topic:refunds.v1#produce"}]}`,
	})
	wrong := "version: 1\nchannels:\n  - id: mybus\n    patterns: [{call: 'bus.announce({name}', side: consume}]\n"
	b := &seqBackend{answers: []string{wrong}}
	res, err := ScanConfigAuthor(context.Background(), b, root, AuthorOptions{MaxRounds: 2})
	if err != nil || res.Saved || fileExists(root) {
		t.Fatalf("wrong-side candidate saved: %+v %v", res, err)
	}
	if !strings.Contains(b.users[1], "side is wrong for") {
		t.Errorf("no side hint in the feedback:\n%s", b.users[1])
	}
	right := strings.Replace(wrong, "side: consume", "side: produce", 1)
	res, err = ScanConfigAuthor(context.Background(), &seqBackend{answers: []string{wrong, right}}, root, AuthorOptions{MaxRounds: 3})
	if err != nil || !res.Saved || res.Rounds[len(res.Rounds)-1].Hits != 2 {
		t.Fatalf("corrected candidate: %+v %v", res, err)
	}
}

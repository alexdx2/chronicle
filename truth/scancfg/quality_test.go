package scancfg

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A model-written layer must not be able to vouch for itself: the independent
// reference executes only rules a human reviewed.
func TestParse_LLMProvenanceCannotSelfReview(t *testing.T) {
	y := `
provenance: llm:some-model
channels:
  - id: sneaky
    reviewed: true
    patterns:
      - {call: "bus.send({name}", side: produce}
documents:
  - id: sneaky-doc
    glob: "**/a.json"
    reviewed: true
    emit:
      - {name: "{value}", side: consume}
`
	c, err := Parse([]byte(y), SourceProject)
	if err != nil {
		t.Fatal(err)
	}
	if c.Channels[0].Reviewed || c.Documents[0].Reviewed {
		t.Fatalf("llm-written rule marked itself reviewed: %+v %+v", c.Channels[0], c.Documents[0])
	}
	if tr := c.Trusted(); len(tr.Channels) != 0 || len(tr.Documents) != 0 {
		t.Fatalf("reference would execute model rules: %+v", tr)
	}
	// A human-written project file keeps its own mark.
	h, err := Parse([]byte(strings.Replace(y, "provenance: llm:some-model\n", "", 1)), SourceProject)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Channels[0].Reviewed || !h.Documents[0].Reviewed {
		t.Fatalf("human reviewed mark lost: %+v", h.Channels[0])
	}
}

func TestParse_Limits(t *testing.T) {
	big := "version: 1\n# " + strings.Repeat("x", MaxConfigBytes) + "\n"
	if _, err := Parse([]byte(big), SourceProject); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("size limit: %v", err)
	}
	if c, err := Parse(nil, SourceProject); err != nil || !c.Empty() {
		t.Fatalf("empty file is an empty config: %v %v", c, err)
	}
	if _, err := Parse([]byte("version: 2\n"), SourceProject); err == nil {
		t.Fatal("future version must be refused")
	}
	if _, err := Parse([]byte("provenance: hand\n"), SourceProject); err == nil {
		t.Fatal("unknown provenance must be refused")
	}
}

func TestValidate_EveryProblemAtOnce(t *testing.T) {
	cases := map[string]string{
		"duplicate channel id": "channels:\n  - {id: a, patterns: [{call: 'x({name}', side: produce}]}\n  - {id: a, patterns: [{call: 'y({name}', side: produce}]}\n",
		"no patterns":          "channels:\n  - {id: a}\n",
		"bad kind":             "channels:\n  - {id: a, patterns: [{call: 'x({name}', side: produce, kind: weird}]}\n",
		"no name":              "channels:\n  - {id: a, patterns: [{call: 'x(1', side: produce}]}\n",
		"two names":            "channels:\n  - {id: a, patterns: [{call: 'x({name}, {name}', side: produce}]}\n",
		"unknown placeholder":  "channels:\n  - {id: a, patterns: [{call: 'x({nope}', side: produce}]}\n",
		"id outside define":    "channels:\n  - {id: a, patterns: [{call: '{id} = t({name}', side: produce}]}\n",
		"define without id":    "channels:\n  - {id: a, patterns: [{call: 't({name}', side: define}]}\n",
		"define with ref":      "channels:\n  - {id: a, patterns: [{call: '{id} = {ref}({name}', side: define}]}\n",
		"empty template":       "channels:\n  - {id: a, patterns: [{call: '  ', side: produce}]}\n",
		"doc without glob":     "documents:\n  - {id: d, emit: [{name: x, side: produce}]}\n",
		"doc without emit":     "documents:\n  - {id: d, glob: '*.json'}\n",
		"doc empty name":       "documents:\n  - {id: d, glob: '*.json', emit: [{name: '', side: produce}]}\n",
		"doc no side":          "documents:\n  - {id: d, glob: '*.json', emit: [{name: x}]}\n",
		"doc bad side_by_key":  "documents:\n  - {id: d, glob: '*.json', emit: [{name: x, side_by_key: {a: nope}}]}\n",
		"doc duplicate id":     "documents:\n  - {id: d, glob: '*.json', emit: [{name: x, side: produce}]}\n  - {id: d, glob: '*.json', emit: [{name: x, side: produce}]}\n",
		"config file no glob":  "config:\n  files:\n    - {format: json}\n",
		"config bad format":    "config:\n  files:\n    - {glob: '*.x', format: cobol}\n",
		"config key no as":     "config:\n  keys:\n    - {match: 'a.*'}\n",
		"client no addr":       "clients:\n  - {call: 'http.get(url)'}\n",
		"alias no service":     "aliases:\n  - {host: h}\n",
		"uppercase id":         "channels:\n  - {id: Abc, patterns: [{call: 'x({name}', side: produce}]}\n",
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y), SourceProject); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// All problems of one file are reported together.
	_, err := Parse([]byte("channels:\n  - {id: A, patterns: [{call: 'x(', side: nope, kind: weird}]}\nclients:\n  - {call: z}\naliases:\n  - {host: h}\n"), SourceProject)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"id", "side", "kind", "{addr}", "aliases"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestMerge_Properties(t *testing.T) {
	a := mustParse(t, sample, "")
	b := mustParse(t, "channels:\n  - id: extra\n    patterns:\n      - {call: 'x.pub({name}', side: produce}\naliases:\n  - {host: h, service: s}\n", "")
	if Merge(a).Hash() != Merge(a).Hash() {
		t.Fatal("hash is not deterministic")
	}
	// A rule with the same id replaces, never duplicates.
	if got := Merge(a, a); len(got.Channels) != len(a.Channels) || len(got.Documents) != len(a.Documents) {
		t.Fatalf("merge of a layer with itself grew: %d channels", len(got.Channels))
	}
	// Order of unrelated rules follows layer order.
	m := Merge(a, b)
	if m.Channels[len(m.Channels)-1].ID != "extra" {
		t.Fatalf("order: %+v", m.Channels)
	}
	// nil layers are skipped.
	if Merge(nil, a, nil).Hash() != Merge(a).Hash() {
		t.Fatal("nil layer changed the result")
	}
	// A stronger layer replacing a rule changes the coverage version.
	over := mustParse(t, "channels:\n  - id: rabbit\n    patterns:\n      - {call: 'q.put({name}', side: produce}\n", "")
	if Merge(a, over).CoverageVersion() == Merge(a).CoverageVersion() {
		t.Fatal("changed rule did not change the coverage version")
	}
	if (&Config{}).CoverageVersion() != "" || (*Config)(nil).CoverageVersion() != "" {
		t.Fatal("an empty configuration must not alter the coverage version")
	}
	// env_defaults counts as content.
	off := false
	e := &Config{Config: ConfigSection{EnvDefaults: &off}}
	if e.Empty() || e.Config.EnvDefaultsOn() || !(&ConfigSection{}).EnvDefaultsOn() {
		t.Fatal("env_defaults handling")
	}
}

func TestClientEngine(t *testing.T) {
	cfg := mustParse(t, `
clients:
  - call: "axios.create({baseURL: {addr}"
    when: {ext: [.ts, .js]}
  - call: "http.NewClient({addr}"
`, "")
	if ce, err := NewClientEngine(&Config{}); ce != nil || err != nil {
		t.Fatalf("no clients -> nil engine: %v %v", ce, err)
	}
	ce, err := NewClientEngine(cfg)
	if err != nil || ce == nil {
		t.Fatal(err)
	}
	ts := "const api = axios.create({ baseURL: CATALOG_URL });\nconst b = axios.create({baseURL: \"http://x\"})\n"
	got := ce.RunClients("src/a.ts", []byte(ts))
	names := map[string]bool{}
	for _, f := range got {
		names[f.Name] = true
	}
	if !names["CATALOG_URL"] || !names["http://x"] {
		t.Fatalf("clients: %+v", got)
	}
	// the ext gate holds for the first rule only
	if r := ce.RunClients("src/a.py", []byte(ts)); len(r) != 0 {
		t.Fatalf("ext gate: %+v", r)
	}
	if r := ce.RunClients("c/main.go", []byte(`c := http.NewClient(baseURL)`)); len(r) != 1 || r[0].Name != "baseURL" {
		t.Fatalf("go client: %+v", r)
	}
	if r := ce.RunClients("src/a.ts", []byte("// axios.create({ baseURL: X })\n")); len(r) != 0 {
		t.Fatalf("comment matched: %+v", r)
	}
}

func TestDecodeDocument(t *testing.T) {
	j, ok := DecodeDocument("a.json", []byte(`{"a":{"b":"x","c":[1,true,{"d":"y"}]},"n":1.5,"z":null}`))
	if !ok || j["a.b"] != "x" || j["a.c.0"] != "1" || j["a.c.1"] != "true" || j["a.c.2.d"] != "y" || j["n"] != "1.5" {
		t.Fatalf("json: %v ok=%v", j, ok)
	}
	y, ok := DecodeDocument("a.yml", []byte("a:\n  b: x\n  list: [p, q]\nn: 3\nflag: true\n"))
	if !ok || y["a.b"] != "x" || y["a.list.1"] != "q" || y["n"] != "3" || y["flag"] != "true" {
		t.Fatalf("yaml: %v ok=%v", y, ok)
	}
	p, ok := DecodeDocument("app.properties", []byte("# c\n! c\n; c\n\na.b = x\nc: \"quoted\"\nnoeq\nd=e=f\n"))
	if !ok || p["a.b"] != "x" || p["c"] != "quoted" || p["d"] != "e=f" || len(p) != 3 {
		t.Fatalf("properties: %v ok=%v", p, ok)
	}
	for _, bad := range []struct{ f, s string }{{"a.json", "{nope"}, {"a.yaml", "a: [1"}, {"a.bin", "x"}, {"noext", "x"}} {
		if d, ok := DecodeDocument(bad.f, []byte(bad.s)); ok || d != nil {
			t.Errorf("%s decoded: %v", bad.f, d)
		}
	}
}

func TestSameFileConstants(t *testing.T) {
	src := "const A = \"alpha\";\nexport const B: string = 'beta'\nprivate static final String C = \"gamma\";\nD = `delta`\nx = compute()\n  E: \"epsilon\"\nlet F = 'a' + 'b'\n"
	m := SameFileConstants([]byte(src))
	for k, v := range map[string]string{"A": "alpha", "B": "beta", "C": "gamma", "D": "delta", "E": "epsilon"} {
		if m[k] != v {
			t.Errorf("%s = %q, want %q (all %v)", k, m[k], v, m)
		}
	}
	if _, ok := m["x"]; ok {
		t.Error("a call result is not a constant")
	}
}

func TestRunDocuments_Templates(t *testing.T) {
	cfg := mustParse(t, `
documents:
  - id: bindings
    glob: "deploy/*.yaml"
    when_keys: {"kind": "Binding"}
    select: "spec.topics.*"
    emit:
      - {name: "{doc:spec.prefix}.{value}", side: consume}
      - {name: "{key:2}", side: produce, kind: queue}
      - {name: "{each:doc:spec.extra}", side: declare}
      - {name: "{key:9}", side: produce}
`, "")
	src := "kind: Binding\nspec:\n  prefix: acme\n  extra: a, b ,,c\n  topics:\n    t1: orders\n    t2: refunds\n"
	got := RunDocuments(cfg, "deploy/b.yaml", []byte(src))
	have := map[string]bool{}
	for _, f := range got {
		have[f.Key()] = true
	}
	for _, k := range []string{"topic:acme.orders#consume", "topic:acme.refunds#consume", "queue:t1#produce", "queue:t2#produce", "topic:a#declare", "topic:b#declare", "topic:c#declare"} {
		if !have[k] {
			t.Errorf("missing %s in %v", k, have)
		}
	}
	if len(got) != 7 {
		t.Errorf("a template that cannot expand must emit nothing, got %d facts: %v", len(got), have)
	}
	// when_keys that do not hold, glob that does not match, undecodable file
	if r := RunDocuments(cfg, "deploy/b.yaml", []byte("kind: Other\nspec: {topics: {t: x}}\n")); len(r) != 0 {
		t.Errorf("when_keys: %v", r)
	}
	if r := RunDocuments(cfg, "other/b.yaml", []byte(src)); len(r) != 0 {
		t.Errorf("glob: %v", r)
	}
	if r := RunDocuments(cfg, "deploy/b.yaml", []byte("a: [1")); len(r) != 0 {
		t.Errorf("broken yaml: %v", r)
	}
}

// Determinism: the same inputs give the same facts in the same order, however
// many times and from however many goroutines the engine is asked.
func TestEngine_Deterministic(t *testing.T) {
	cfg := mustParse(t, sample, "")
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	src := []byte("import faust\n" + strings.Repeat("a = app.topic(\"orders\")\nb = app.topic(\"pay\")\n", 50))
	first := fmt.Sprint(e.Run("a.py", src, nil))
	done := make(chan string, 8)
	for i := 0; i < 8; i++ {
		go func() { done <- fmt.Sprint(e.Run("a.py", src, nil)) }()
	}
	for i := 0; i < 8; i++ {
		if got := <-done; got != first {
			t.Fatal("engine output differs between runs")
		}
	}
}

// A pathological file must not stall a scan: the engine stays within its budget
// (plus the cost of the lexer) on input built to be slow for backtracking.
func TestEngine_PathologicalInputStaysBounded(t *testing.T) {
	cfg := mustParse(t, "channels:\n  - id: p\n    patterns:\n      - {call: 'a.b({any}, {any}, {name}', side: produce}\n", "")
	e, _ := NewEngine(cfg)
	inputs := map[string]string{
		"open parens":  strings.Repeat("a.b(((((((((( ", 20000),
		"commas":       "a.b(" + strings.Repeat("x,", 100000),
		"long strings": strings.Repeat("a.b(\""+strings.Repeat("y", 500)+"\", ", 2000),
		"nested":       "a.b(" + strings.Repeat("(", 5000) + strings.Repeat(")", 5000),
	}
	for name, in := range inputs {
		start := time.Now()
		_ = e.Run("x.go", []byte(in), nil)
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: %v", name, d)
		}
	}
}

func TestGlobMatch_Table(t *testing.T) {
	for _, c := range []struct {
		glob, name string
		want       bool
	}{
		{"**/*.py", "a/b/c.py", true},
		{"**/*.py", "c.py", true},
		{"*.py", "a/c.py", false},
		{"src/**", "src/a/b.go", true},
		{"src/*/x.go", "src/a/x.go", true},
		{"src/*/x.go", "src/a/b/x.go", false},
		{"a?.go", "ab.go", true},
		{"a?.go", "a/.go", false},
		{"**/asyncapi.yaml", "asyncapi.yaml", true},
		{"build.gradle*", "build.gradle.kts", true},
	} {
		if got := GlobMatch(c.glob, c.name); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v, want %v", c.glob, c.name, got, c.want)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(sample))
	f.Add([]byte("provenance: llm:x\nchannels:\n  - {id: a, patterns: [{call: '{id} = t({name}', side: define}]}\n"))
	f.Add([]byte("documents:\n  - {id: d, glob: '*.json', emit: [{name: '{each:doc:a.b}', side: produce}]}\n"))
	f.Add([]byte("clients:\n  - {call: 'f({addr}'}\nconfig:\n  keys: [{match: 'a.*', as: channel}]\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := Parse(data, SourceProject)
		if err != nil {
			return
		}
		// What Parse accepts must compile, hash, merge and run without panics.
		if err := c.Validate(); err != nil {
			t.Fatalf("Parse accepted what Validate refuses: %v", err)
		}
		e, err := NewEngine(c)
		if err != nil {
			t.Fatalf("validated config does not compile: %v", err)
		}
		if _, err := NewClientEngine(c); err != nil {
			t.Fatalf("validated clients do not compile: %v", err)
		}
		_ = c.Hash()
		_ = Merge(c, c).Trusted().CoverageVersion()
		src := []byte("import faust\nx = app.topic(\"a\")\nbus.send(\"b\", 1)\n{\"a\":{\"b\":\"c\"}}")
		_ = e.Run("a.py", src, func(_, expr string) (string, string, bool) { return expr, "t", true })
		_ = e.Defs("a.py", src, nil)
		_ = RunDocuments(c, "a.json", src)
		_ = RunDocuments(c, "a.yaml", src)
	})
}

func FuzzCompileTemplate(f *testing.F) {
	for _, s := range []string{"app.topic({name}", "@app.agent({ref})", "{id} = t({name}", "x({any}, {name})", "{Type} {name}", "{", "{}", "}{", "(((", "{name}{name}", "a\x00b({name}"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tpl string) {
		c, err := compileTemplate(tpl)
		if err != nil {
			return
		}
		_ = c.re.FindAllStringSubmatchIndex("app.topic(\"x\") @app.agent(y) z = t(\"q\")", -1)
	})
}

func FuzzEngineRun(f *testing.F) {
	f.Add([]byte("import faust\norders = app.topic(\"orders\")\n@app.agent(orders)\nasync def f(s): pass\n"), "a.py")
	f.Add([]byte("\"\"\"app.topic('doc')\"\"\"\n# app.topic('c')\nx = 'it''s'\n"), "b.py")
	f.Add([]byte("a.b(\"unterminated\n`tpl ${x} app.topic(`\n"), "c.ts")
	cfg, err := Parse([]byte(`
channels:
  - id: faust
    when: {imports: [faust]}
    patterns:
      - {call: '{id} = app.topic({name}', side: define}
      - {call: '@app.agent({ref})', side: consume}
      - {call: 'app.topic({name}', side: declare}
      - {call: 'bus.send({any}, {name}', side: produce}
`), SourceProject)
	if err != nil {
		f.Fatal(err)
	}
	e, err := NewEngine(cfg)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, src []byte, file string) {
		res := func(_, expr string) (string, string, bool) { return expr, "x", true }
		defs := e.Defs(file, src, res)
		facts := e.RunWith(file, src, res, defs)
		for _, fc := range facts {
			if fc.Name == "" || fc.Line < 1 {
				t.Fatalf("bad fact %+v", fc)
			}
		}
	})
}

func FuzzDecodeDocument(f *testing.F) {
	f.Add("a.json", []byte(`{"a":[1,{"b":null}]}`))
	f.Add("a.yaml", []byte("a: &x [1]\nb: *x\n"))
	f.Add("a.properties", []byte("a=b\n=c\n:d\n"))
	f.Fuzz(func(t *testing.T, name string, src []byte) {
		doc, ok := DecodeDocument(name, src)
		if !ok && doc != nil {
			t.Fatal("failed decode returned a document")
		}
	})
}

// Whitespace is free around punctuation and required between two words.
func TestTemplate_Whitespace(t *testing.T) {
	cases := []struct {
		tpl, code string
		want      bool
	}{
		{"axios.create({baseURL: {name}", `axios.create({ baseURL : "http://x" })`, true},
		{"axios.create({ baseURL: {name}", `axios.create({baseURL:"http://x"})`, true},
		{"new HttpClient({name}", `new   HttpClient ( "u" )`, true},
		{"new HttpClient({name}", `newHttpClient("u")`, false},
		{"bus.send({name}", "bus.send(\n    \"orders\"\n)", true},
		{"bus.send({name}", "bus . send(\"orders\")", false}, // a dotted name is one token
		{"x = f({name}", `x=f("a")`, true},
		{"x = f({name}", `xx = f("a")`, false},
		{"@app.agent({name})", "@app.agent(\n\"payments\"\n)", true},
		{"a->b({name}", `a -> b ("n")`, true},
	}
	for _, c := range cases {
		cfg := &Config{Channels: []ChannelRule{{ID: "t", Patterns: []Pattern{{Call: c.tpl, Side: "produce"}}}}}
		e, err := NewEngine(cfg)
		if err != nil {
			t.Fatalf("%q: %v", c.tpl, err)
		}
		got := len(e.Run("a.ts", []byte(c.code), nil)) > 0
		if got != c.want {
			t.Errorf("template %q on %q: matched=%v, want %v", c.tpl, c.code, got, c.want)
		}
	}
}

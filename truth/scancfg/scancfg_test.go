package scancfg

import (
	"strings"
	"testing"
)

const sample = `
version: 1
channels:
  - id: faust-topics
    when: {ext: [.py], imports: [faust]}
    patterns:
      - {call: "app.topic({name}", side: declare, kind: topic, transport: kafka}
      - {call: "@app.agent({name})", side: consume, transport: kafka}
    ignore_names: ["test-*"]
  - id: rabbit
    patterns:
      - {call: "channel.queue_declare(queue={name}", side: declare, kind: queue, transport: amqp}
      - {call: "channel.basic_publish(exchange={any}, routing_key={name}", side: produce, kind: queue, transport: amqp}
documents:
  - id: asyncapi
    glob: "**/asyncapi.yaml"
    select: "channels.*"
    emit:
      - {name: "{key:1}", side_by_key: {publish: produce, subscribe: consume}}
`

func mustParse(t *testing.T, y string, src string) *Config {
	t.Helper()
	c, err := Parse([]byte(y), SourceProject)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseValidate(t *testing.T) {
	mustParse(t, sample, SourceProject)
	_, err := Parse([]byte("channels:\n  - id: Bad ID\n    patterns:\n      - {call: 'x(', side: nope}\n"), SourceProject)
	if err == nil || !strings.Contains(err.Error(), "side") || !strings.Contains(err.Error(), "id") {
		t.Fatalf("want multi-problem error, got %v", err)
	}
	if _, err := Parse([]byte("bogus: 1\n"), SourceProject); err == nil {
		t.Fatal("unknown field must fail")
	}
}

func TestEngine(t *testing.T) {
	cfg := mustParse(t, sample, "")
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	py := `import faust
app = faust.App("x")
orders = app.topic("orders.v1")
bad = app.topic("test-x")
@app.agent(
    "payments"
)
async def f(s): pass
dyn = app.topic(TOPIC)
`
	got := e.Run("svc/app.py", []byte(py), func(file, expr string) (string, string, bool) {
		if expr == "TOPIC" {
			return "dyn.topic", "config:TOPIC", true
		}
		return "", "", false
	})
	keys := map[string]int{}
	for _, f := range got {
		keys[f.Key()] = f.Line
	}
	for k, line := range map[string]int{"topic:orders.v1#declare": 3, "topic:payments#consume": 5, "topic:dyn.topic#declare": 9} {
		if keys[k] != line {
			t.Errorf("%s: line %d, want %d (all %v)", k, keys[k], line, keys)
		}
	}
	if _, ok := keys["topic:test-x#declare"]; ok {
		t.Error("ignored name leaked")
	}
	// imports gate
	if r := e.Run("svc/other.py", []byte(`x = app.topic("nope")`), nil); len(r) != 0 {
		t.Errorf("imports gate failed: %v", r)
	}
	// comments are invisible
	if r := e.Run("svc/c.py", []byte("import faust\n# app.topic(\"commented\")\n"), nil); len(r) != 0 {
		t.Errorf("comment matched: %v", r)
	}
	// {any} argument selection
	r := e.Run("w.go", []byte(`channel.basic_publish(exchange="", routing_key="jobs", body=b)`), nil)
	if len(r) != 1 || r[0].Key() != "queue:jobs#produce" {
		t.Errorf("arg selection: %v", r)
	}
}

func TestDocuments(t *testing.T) {
	cfg := mustParse(t, sample, "")
	y := "channels:\n  orders:\n    publish: {}\n  refunds:\n    subscribe: {}\n"
	_ = y
	// selected values are scalars; use a shape with scalar leaves
	src := "channels:\n  orders.publish: x\n  refunds.subscribe: y\n"
	cfg.Documents[0].Emit[0].Name = "{key:1}"
	cfg.Documents[0].Select = "channels.*"
	// keys flatten as "channels.orders.publish" splits into segments; use dotted key names
	_ = src
	nested := "channels:\n  orders:\n    publish: a\n  refunds:\n    subscribe: b\n"
	cfg.Documents[0].Select = "channels.*.*"
	cfg.Documents[0].Emit[0].Name = "{key:1}"
	got := RunDocuments(cfg, "api/asyncapi.yaml", []byte(nested))
	m := map[string]bool{}
	for _, f := range got {
		m[f.Key()] = true
	}
	if !m["topic:orders#produce"] || !m["topic:refunds#consume"] {
		t.Fatalf("documents: %v", got)
	}
}

func TestMergeTrustedHash(t *testing.T) {
	a, _ := Parse([]byte(sample), SourceBuiltin)
	b := mustParse(t, "channels:\n  - id: rabbit\n    patterns:\n      - {call: 'x.pub({name}', side: produce}\n", "")
	m := Merge(a, b)
	if len(m.Channels) != 2 || m.Channels[1].Source != SourceProject {
		t.Fatalf("override: %+v", m.Channels)
	}
	tr := m.Trusted()
	if len(tr.Channels) != 1 || tr.Channels[0].ID != "faust-topics" {
		t.Fatalf("trusted: %+v", tr.Channels)
	}
	if m.Hash() == a.Hash() || m.CoverageVersion() == "" {
		t.Fatal("hash must differ")
	}
}

func TestScope(t *testing.T) {
	s := Scope{Include: []string{"src/**"}, Exclude: []string{"**/vendor/**", "**/*_test.go"}}
	for f, want := range map[string]bool{"src/a/b.go": true, "src/vendor/x.go": false, "src/a_test.go": false, "docs/a.md": false} {
		if s.Allowed(f) != want {
			t.Errorf("%s", f)
		}
	}
}

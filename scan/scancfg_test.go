package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/llm"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, b := range files {
		fp := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestScanConfigRuleChannels(t *testing.T) {
	root := writeTree(t, map[string]string{
		".depbot/scan.yaml": `version: 1
config:
  files: [{glob: "etc/topics.ini", format: properties}]
  keys:
    - {match: "topic_*", as: channel}
channels:
  - id: faust
    when: {ext: [.py], imports: [faust]}
    patterns:
      - {call: "app.topic({name}", side: declare, transport: kafka}
      - {call: "@app.agent({name})", side: consume, transport: kafka}
documents:
  - id: asyncapi
    glob: "**/asyncapi.yaml"
    select: "channels.*.*"
    emit:
      - {name: "{key:1}", side_by_key: {publish: produce, subscribe: consume}, transport: kafka}
noise:
  - {name: "tmp-*"}
aliases:
  - {host: billing.internal, service: billing-api}
routes:
  - {hint: "routes are mounted under /api/v1"}
`,
		"etc/topics.ini":    "topic_orders = orders.v2\n",
		"svc/app.py":        "import faust\nORDERS = \"payments.v1\"\napp = faust.App('x')\nt = app.topic(ORDERS)\nu = app.topic(topic_orders)\nv = app.topic('tmp-x')\n@app.agent('refunds')\nasync def f(s): pass\n",
		"api/asyncapi.yaml": "channels:\n  shipped:\n    publish: a\n  returned:\n    subscribe: b\n",
		"svc/unrelated.py":  "app.topic('nope')\n",
	})
	files := []string{".depbot/scan.yaml", "etc/topics.ini", "svc/app.py", "api/asyncapi.yaml", "svc/unrelated.py"}
	tab, _ := ReadConfigHosts(root, files)
	got := map[string]string{}
	for _, rc := range tab.RuleChannels {
		got[rc.Side+":"+rc.Name] = rc.File
	}
	for _, want := range []string{"declare:payments.v1", "declare:orders.v2", "consume:refunds", "produce:shipped", "consume:returned"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	for _, bad := range []string{"declare:tmp-x", "declare:nope"} {
		if _, ok := got[bad]; ok {
			t.Errorf("unexpected %s", bad)
		}
	}
	if len(tab.Aliases) != 1 || len(tab.Hints) != 1 {
		t.Errorf("aliases %v hints %v", tab.Aliases, tab.Hints)
	}
	if v := ScanConfigVersion(root); v == "" {
		t.Error("coverage version empty")
	}
}

func TestScanConfigBrokenIsIgnored(t *testing.T) {
	root := writeTree(t, map[string]string{".depbot/scan.yaml": "channels: [oops"})
	if _, err := LoadScanConfigErr(root); err == nil {
		t.Fatal("want error")
	}
	if cfg := LoadScanConfig(root); cfg == nil {
		t.Fatal("scan must continue with the other layers")
	}
}

func TestScanConfigTools(t *testing.T) {
	root := writeTree(t, map[string]string{
		"svc/app.py":          "import faust\napp.topic('orders.v1')\napp.topic('refunds.v1')\n",
		"truth-expected.json": `{"channels":[{"Key":"topic:orders.v1#declare"},{"Key":"topic:refunds.v1#declare"},{"Key":"topic:gone.v1#declare"}]}`,
	})
	cand := "channels:\n  - id: faust\n    patterns: [{call: 'app.topic({name}', side: declare}]\n"
	dr := ScanConfigDryRun(root, cand)
	if !dr.Valid || len(dr.Added) != 2 {
		t.Fatalf("dry run: %+v", dr)
	}
	if dr.Golden == nil || len(dr.Golden.Missing) != 1 {
		t.Fatalf("golden: %+v", dr.Golden)
	}
	if bad := ScanConfigDryRun(root, "channels: [{id: X}]"); bad.Valid || bad.Errors == "" {
		t.Fatal("invalid must be reported")
	}
	res, err := ScanConfigSave(root, cand, false)
	if err != nil || !res.Saved {
		t.Fatalf("save: %+v %v", res, err)
	}
	// dropping the rule now loses facts: refused until accepted
	res, _ = ScanConfigSave(root, "version: 1\n", false)
	if res.Saved || len(res.Removed) != 2 {
		t.Fatalf("loss guard: %+v", res)
	}
	if res, _ = ScanConfigSave(root, "version: 1\n", true); !res.Saved {
		t.Fatalf("accepted: %+v", res)
	}
	if ex := ScanConfigExplain(writeTree(t, map[string]string{"a.py": "app.topic('x')\n", ".depbot/scan.yaml": cand}), "a.py"); len(ex) != 1 {
		t.Fatalf("explain: %+v", ex)
	}
}

type seqBackend struct {
	answers []string
	n       int
	users   []string
}

func (s *seqBackend) Name() string  { return "seq" }
func (s *seqBackend) Model() string { return "seq-model" }
func (s *seqBackend) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	s.users = append(s.users, req.User)
	a := s.answers[min(s.n, len(s.answers)-1)]
	s.n++
	return llm.Response{Text: a}, nil
}

func TestScanConfigAuthor(t *testing.T) {
	root := writeTree(t, map[string]string{
		"svc/app.py":          "import mybus\nbus.announce('orders.v1')\nbus.announce('refunds.v1')\n",
		"truth-expected.json": `{"channels":[{"Key":"topic:orders.v1#produce"},{"Key":"topic:refunds.v1#produce"}]}`,
	})
	b := &seqBackend{answers: []string{
		"channels: [{id: Bad}]",
		"```yaml\nversion: 1\nchannels:\n  - id: mybus\n    when: {imports: [mybus]}\n    patterns: [{call: 'bus.announce({name}', side: produce}]\n```",
	}}
	res, err := ScanConfigAuthor(context.Background(), b, root, AuthorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Saved || len(res.Rounds) != 2 || res.Rounds[0].Valid || !res.Rounds[1].Valid || res.Rounds[1].Missing != 0 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(b.users[1], "invalid") {
		t.Error("the error must be fed back")
	}
	if !strings.Contains(b.users[0], "bus.announce") {
		t.Error("evidence snippets must reach the model")
	}
	cfg := LoadScanConfig(root)
	var src string
	for _, c := range cfg.Channels {
		if c.ID == "mybus" {
			src = c.Source
			if c.Reviewed {
				t.Error("a model-written rule must be unreviewed")
			}
		}
	}
	if src != "llm:seq-model" {
		t.Errorf("provenance %q", src)
	}
}

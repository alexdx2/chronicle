package scan

import (
	"os"
	"path/filepath"
	"testing"
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

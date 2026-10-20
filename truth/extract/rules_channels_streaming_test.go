package extract

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexdx2/chronicle-core/truth"
)

func channelKeys(t *testing.T, fsys fstest.MapFS) map[string]truth.Fact {
	t.Helper()
	rep, err := Extract(fsys)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]truth.Fact{}
	for _, f := range rep.Facts {
		if f.Class == truth.ClassChannels {
			out[f.Key] = f
		}
	}
	return out
}

func wantKeys(t *testing.T, got map[string]truth.Fact, rule string, want ...string) {
	t.Helper()
	var g []string
	for k, f := range got {
		g = append(g, k)
		if len(f.Sources) != 1 || f.Sources[0] != rule {
			t.Errorf("%s: sources %v, want %s", k, f.Sources, rule)
		}
		if f.Primary.Line == 0 || f.Primary.Text == "" {
			t.Errorf("%s: bad primary %+v", k, f.Primary)
		}
	}
	sort.Strings(g)
	sort.Strings(want)
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Fatalf("keys mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(want, "\n  "))
	}
}

func readTD(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "channels", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestKafkaStreamsChannels(t *testing.T) {
	got := channelKeys(t, fstest.MapFS{"src/Topo.java": {Data: readTD(t, "java/KStreams.java")}})
	wantKeys(t, got, "kafka.streams",
		"topic:ks-in#consume", "topic:ks-table#consume", "topic:ks-global#consume",
		"topic:ks-out#produce", "topic:ks-through#produce")
	if l := got["topic:ks-through#produce"].Primary.Line; l != 18 {
		t.Errorf("multi-line call primary line %d, want 18", l)
	}
	wantKeys(t, channelKeys(t, fstest.MapFS{"src/Other.java": {Data: readTD(t, "java/NotStreams.java")}}), "kafka.streams")
	wantKeys(t, channelKeys(t, fstest.MapFS{"src/test/java/Topo.java": {Data: readTD(t, "java/KStreams.java")}}), "kafka.streams")
}

func TestKsqlChannels(t *testing.T) {
	got := channelKeys(t, fstest.MapFS{"sql/a.sql": {Data: readTD(t, "sql/ksql.sql")}})
	wantKeys(t, got, "ksql.channel",
		"topic:k-raw#consume", "topic:k-users#consume", "topic:k-enriched#produce")
	if l := got["topic:k-users#consume"].Primary.Line; l != 4 {
		t.Errorf("primary line %d, want 4", l)
	}
}

func TestFaustChannels(t *testing.T) {
	got := channelKeys(t, fstest.MapFS{"app/main.py": {Data: readTD(t, "py/faust_app.py")}})
	wantKeys(t, got, "faust.channel",
		"topic:f-in#consume", "topic:f-out#produce",
		"topic:f-both#consume", "topic:f-both#produce", "topic:f-both-2#consume", "topic:f-both-2#produce",
		"topic:f-inline#consume")
	wantKeys(t, channelKeys(t, fstest.MapFS{"app/nf.py": {Data: readTD(t, "py/NoFaust.py")}}), "faust.channel")
	wantKeys(t, channelKeys(t, fstest.MapFS{"app/test_main.py": {Data: readTD(t, "py/faust_app.py")}}), "faust.channel")
}

func TestFaustChannelsAcrossModules(t *testing.T) {
	fsys := fstest.MapFS{
		"fraud/__init__.py":    {Data: []byte("")},
		"fraud/app.py":         {Data: readTD(t, "py/faust_decl.py")},
		"fraud/agents.py":      {Data: readTD(t, "py/faust_agents.py")},
		"fraud/other.py":       {Data: []byte("import faust\nfrom fraud.settings import shared\n\n@app.agent(shared)\nasync def x(s): pass\n")},
		"tests/test_agents.py": {Data: []byte("from fraud.app import shared\n@app.agent(shared)\n")},
	}
	got := channelKeys(t, fsys)
	wantKeys(t, got, "faust.channel", "topic:f-shared#consume", "topic:f-plain#produce")
	if f := got["topic:f-shared#consume"]; f.Primary.File != "fraud/app.py" || f.Primary.Line != 4 {
		t.Errorf("evidence %+v, want the declaration", f.Primary)
	}
}

func TestKafkaConnectChannels(t *testing.T) {
	wantKeys(t, channelKeys(t, fstest.MapFS{"c/pg.json": {Data: readTD(t, "json/pg-source.json")}}), "kafka.connect",
		"topic:cdc.public.users#produce", "topic:cdc.sales.orders#produce")
	wantKeys(t, channelKeys(t, fstest.MapFS{"c/legacy.json": {Data: readTD(t, "json/legacy-source.json")}}), "kafka.connect",
		"topic:legacy.shop.items#produce")
	got := channelKeys(t, fstest.MapFS{"c/sink.json": {Data: readTD(t, "json/sink.json")}})
	wantKeys(t, got, "kafka.connect", "topic:c-a#consume", "topic:c-b#consume")
	if l := got["topic:c-a#consume"].Primary.Line; l != 3 {
		t.Errorf("primary line %d, want 3", l)
	}
	wantKeys(t, channelKeys(t, fstest.MapFS{"package.json": {Data: readTD(t, "json/other.json")}}), "kafka.connect")
}

func TestSpringCloudStreamChannels(t *testing.T) {
	got := channelKeys(t, fstest.MapFS{"src/main/resources/application.yml": {Data: readTD(t, "yaml/application.yml")}})
	wantKeys(t, got, "spring.cloud.stream",
		"topic:s-payments#consume", "topic:s-refunds#consume", "topic:s-receipts#produce")
	if l := got["topic:s-receipts#produce"].Primary.Line; l != 9 {
		t.Errorf("primary line %d, want 9", l)
	}
	wantKeys(t, channelKeys(t, fstest.MapFS{"src/main/resources/application-dev.properties": {Data: readTD(t, "yaml/application.properties")}}), "spring.cloud.stream",
		"topic:sp-in#consume", "topic:sp-out#produce")
	wantKeys(t, channelKeys(t, fstest.MapFS{"application.yml": {Data: readTD(t, "yaml/flat.yml")}}), "spring.cloud.stream",
		"topic:flat-in#consume")
	wantKeys(t, channelKeys(t, fstest.MapFS{"src/test/resources/application.yml": {Data: readTD(t, "yaml/application.yml")}}), "spring.cloud.stream")
}

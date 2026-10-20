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

func cloudFacts(t *testing.T, vpath, disk string) []truth.Fact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "channels", "cloud", disk))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Extract(fstest.MapFS{vpath: {Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	var out []truth.Fact
	for _, f := range rep.Facts {
		if f.Class == truth.ClassChannels {
			out = append(out, f)
		}
	}
	return out
}

func TestChannelCloudRules(t *testing.T) {
	cases := []struct {
		name, vpath, disk string
		want              []string
	}{
		{"servicebus dotnet", "src/Bus.cs", "ServiceBus.cs", []string{
			"servicebus | topic:topic-a#consume", "servicebus | subscription:sub-a#consume",
			"servicebus | queue:queue-b#consume", "servicebus | queue:queue-c#consume",
			"servicebus | topic:topic-d#consume", "servicebus | subscription:sub-d#consume",
			"servicebus | queue:queue-e#consume",
			"servicebus | topic:topic-f#consume", "servicebus | subscription:sub-f#consume",
			"servicebus | queue:queue-g#produce", "servicebus | topic:topic-h#produce",
		}},
		{"servicebus dotnet test file", "tests/BusTests.cs", "ServiceBus.cs", nil},
		{"servicebus lookalike without the sdk", "src/Other.cs", "servicebus_other.cs", nil},
		{"servicebus js", "src/bus.ts", "servicebus.ts", []string{
			"servicebus | queue:js-queue#consume",
			"servicebus | topic:js-topic#consume", "servicebus | subscription:js-sub#consume",
			"servicebus | queue:js-out#produce",
		}},
		{"servicebus python", "app/bus.py", "servicebus.py", []string{
			"servicebus | queue:py-q-in#consume", "servicebus | queue:py-q-pos#consume",
			"servicebus | topic:py-topic#consume", "servicebus | subscription:py-sub#consume",
			"servicebus | topic:py-topic2#consume", "servicebus | subscription:py-sub2#consume",
			"servicebus | topic:py-topic-out#produce", "servicebus | queue:py-q-out#produce",
		}},
		{"pubsub python", "app/ps.py", "pubsub.py", []string{
			"pubsub | topic:ps-topic#produce", "pubsub | topic:ps-full#produce", "pubsub | topic:ps-inline#produce",
			"pubsub | topic:ps-fstring#produce",
			"pubsub | subscription:ps-sub#consume", "pubsub | subscription:ps-sub-full#consume",
			"pubsub | subscription:ps-pull#consume",
		}},
		{"pubsub python test file", "app/test_ps.py", "pubsub.py", nil},
		{"pubsub node", "src/ps.ts", "pubsub.ts", []string{
			"pubsub | topic:ps-js-out#produce", "pubsub | subscription:ps-js-sub#consume",
			"pubsub | topic:ps-js-inline#produce", "pubsub | subscription:ps-js-full#consume",
		}},
		{"pubsub node without the sdk", "src/other.ts", "pubsub_nogate.ts", nil},
		{"cdk event bus ts", "lib/bus.ts", "bus.ts", []string{
			"cdk.eventbus | bus:bus-a#declare", "cdk.eventbus | bus:bus-b#declare",
		}},
		{"cdk event bus python", "stacks/bus.py", "bus.py", []string{"cdk.eventbus | bus:py-bus#declare"}},
		{"event bus without cdk", "src/bus.ts", "bus_nocdk.ts", nil},
		{"terraform cloud", "infra/cloud.tf", "cloud.tf", []string{
			"terraform.cloud | topic:tf-orders#declare", "terraform.cloud | topic:tf-signals#declare",
			"terraform.cloud | subscription:tf-sub#declare",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var g []string
			for _, f := range cloudFacts(t, c.vpath, c.disk) {
				g = append(g, f.Sources[0]+" | "+f.Key)
				if f.Primary.File != c.vpath || f.Primary.Line == 0 || f.Primary.Text == "" {
					t.Errorf("%s: bad primary %+v", f.Key, f.Primary)
				}
			}
			w := append([]string(nil), c.want...)
			sort.Strings(g)
			sort.Strings(w)
			if strings.Join(g, "\n") != strings.Join(w, "\n") {
				t.Fatalf("facts mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(w, "\n  "))
			}
		})
	}
}

func TestChannelCloudEvidenceLines(t *testing.T) {
	for _, f := range cloudFacts(t, "app/ps.py", "pubsub.py") {
		switch f.Key {
		case "topic:ps-topic#produce": // defined on line 6, published on line 16
			if f.Primary.Line != 6 || len(f.Derived) != 1 || f.Derived[0].Line != 16 {
				t.Errorf("%s: %+v %+v", f.Key, f.Primary, f.Derived)
			}
		case "topic:ps-inline#produce":
			if f.Primary.Line != 18 || len(f.Derived) != 0 {
				t.Errorf("%s: %+v %+v", f.Key, f.Primary, f.Derived)
			}
		}
	}
	for _, f := range cloudFacts(t, "infra/cloud.tf", "cloud.tf") {
		if f.Key == "topic:tf-orders#declare" && (f.Primary.Line != 2 || len(f.Derived) != 1 || f.Derived[0].Line != 1) {
			t.Errorf("terraform evidence %+v %+v", f.Primary, f.Derived)
		}
	}
	for _, f := range cloudFacts(t, "src/Bus.cs", "ServiceBus.cs") {
		if f.Key == "topic:topic-d#consume" && f.Primary.Line != 10 {
			t.Errorf("multi-line receiver line %d, want 10", f.Primary.Line)
		}
	}
}

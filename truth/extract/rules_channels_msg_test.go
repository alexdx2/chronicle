package extract

import (
	"sort"
	"strings"
	"testing"
)

func TestChannelMessagingRules(t *testing.T) {
	cases := []struct {
		name  string
		vpath string
		disk  string
		want  []string // "rule | key"
	}{
		{"nats go", "internal/bus/bus.go", "go/nats.go", []string{
			"nats.go | subject:orders.created#produce",
			"nats.go | subject:orders.durable#produce",
			"nats.go | subject:orders.async#produce",
			"nats.go | subject:orders.ctx#produce",
			"nats.go | subject:orders.rpc#produce",
			"nats.go | subject:orders.msg#produce",
			"nats.go | subject:orders.in#consume",
			"nats.go | subject:orders.work#consume",
			"nats.go | subject:orders.js#consume",
			"nats.go | subject:orders.pull#consume",
			"nats.go | stream:orders#declare",
			"nats.go | stream:newapi#declare",
		}},
		{"nats go without import", "internal/bus/bus.go", "go/nats_off.go", nil},
		{"nats go test file", "internal/bus/bus_test.go", "go/nats.go", nil},
		{"nats js", "src/bus.ts", "ts/nats.ts", []string{
			"nats.js | subject:js.out#produce",
			"nats.js | subject:js.durable#produce",
			"nats.js | subject:js.in#consume",
			"nats.js | subject:js.tmpl#consume",
			"nats.js | stream:jsstream#declare",
		}},
		{"nats js without import", "src/bus.ts", "ts/nats_off.ts", nil},
		{"nats js spec file", "src/bus.spec.ts", "ts/nats.ts", nil},
		{"nats py", "app/bus.py", "py/nats_client.py", []string{
			"nats.py | subject:py.out#produce",
			"nats.py | subject:py.durable#produce",
			"nats.py | subject:py.rpc#produce",
			"nats.py | subject:py.in#consume",
			"nats.py | subject:py.js#consume",
			"nats.py | stream:pystream#declare",
		}},
		{"nats py test file", "tests/test_bus.py", "py/nats_client.py", nil},
		{"redis js", "src/r.ts", "ts/redis.ts", []string{
			"redis.js | stream:stream-a#produce",
			"redis.js | stream:stream-b#produce",
			"redis.js | stream:stream-c#consume",
			"redis.js | stream:stream-d#consume",
			"redis.js | stream:stream-e#consume",
			"redis.js | stream:stream-f#consume",
			"redis.js | stream:stream-g#consume",
			"redis.js | topic:chan-a#produce",
			"redis.js | topic:chan-b#consume",
			"redis.js | topic:chan-c#consume",
			"redis.js | topic:events:*#consume",
			"redis.js | topic:chan-d#consume",
		}},
		{"redis js without import", "src/r.ts", "ts/redis_off.ts", nil},
		{"redis py", "app/r.py", "py/redis_client.py", []string{
			"redis.py | stream:py-stream-a#produce",
			"redis.py | stream:py-stream-b#consume",
			"redis.py | stream:py-stream-c#consume",
			"redis.py | stream:py-stream-d#consume",
			"redis.py | stream:py-stream-e#consume",
			"redis.py | topic:py-chan-a#produce",
			"redis.py | topic:py-chan-b#consume",
			"redis.py | topic:py-chan-c#consume",
			"redis.py | topic:py-pat-*#consume",
		}},
		{"redis dotnet", "src/Bus.cs", "csharp/RedisStreams.cs", []string{
			"redis.dotnet | stream:cs-stream-a#produce",
			"redis.dotnet | stream:cs-stream-b#produce",
			"redis.dotnet | stream:cs-stream-c#consume",
			"redis.dotnet | stream:cs-stream-d#consume",
			"redis.dotnet | topic:cs-chan-a#produce",
			"redis.dotnet | topic:cs-chan-b#produce",
			"redis.dotnet | topic:cs-chan-c#consume",
			"redis.dotnet | topic:cs-chan-d#consume",
		}},
		{"mqtt py", "iot/main.py", "py/mqtt_client.py", []string{
			"mqtt.py | topic:devices/+/telemetry#consume",
			"mqtt.py | topic:fleet/##consume",
			"mqtt.py | topic:multi/a#consume",
			"mqtt.py | topic:multi/b#consume",
			"mqtt.py | topic:kw/topic#consume",
			"mqtt.py | topic:devices/gw1/commands#produce",
			"mqtt.py | topic:kw/out#produce",
			"mqtt.py | topic:single/out#produce",
		}},
		{"mqtt py without import", "iot/bus.py", "py/mqtt_off.py", nil},
		{"mqtt js", "src/m.ts", "ts/mqtt.ts", []string{
			"mqtt.js | topic:sensors/+/temp#consume",
			"mqtt.js | topic:list/a#consume",
			"mqtt.js | topic:list/b#consume",
			"mqtt.js | topic:obj/a#consume",
			"mqtt.js | topic:obj/b#consume",
			"mqtt.js | topic:sensors/cmd#produce",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, f := range channelFacts(t, c.vpath, c.disk) {
				got = append(got, f.Sources[0]+" | "+f.Key)
				if f.Primary.Line == 0 || f.Primary.Text == "" {
					t.Errorf("%s: no evidence", f.Key)
				}
			}
			sort.Strings(got)
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

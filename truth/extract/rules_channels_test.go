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

func channelFacts(t *testing.T, vpath, disk string) []truth.Fact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "channels", disk))
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

func TestChannelRules(t *testing.T) {
	cases := []struct {
		name  string
		vpath string
		disk  string
		want  []wantFact
	}{
		{"kafka dotnet", "src/Bus.cs", "csharp/Kafka.cs", []wantFact{
			{"kafka.dotnet", "topic:orders-created#consume", false},
			{"kafka.dotnet", "topic:payments#consume", false},
			{"kafka.dotnet", "topic:refunds#consume", false},
			{"kafka.dotnet", "topic:order-shipped#produce", false},
			{"kafka.dotnet", "topic:audit-log#produce", false},
			{"kafka.dotnet", "topic:inventory#consume", false},
			{"kafka.dotnet", "topic:kept-cs#consume", false},
			{"kafka.dotnet", "topic:committed#consume", false},
			{"kafka.dotnet", "topic:tp-out#produce", false},
			{"kafka.dotnet", "topic:bare-tp#declare", false},
		}},
		{"kafka kotlin", "src/main/kotlin/Bus.kt", "kotlin/Kafka.kt", []wantFact{
			{"kotlin.kafka", "topic:payment-captured#produce", false},
			{"kotlin.kafka", "topic:typed-topic#produce", false},
			{"kotlin.kafka", "topic:multi-line#produce", false},
			{"kotlin.kafka", "topic:orders-in#consume", false},
			{"kotlin.kafka", "topic:refunds-in#consume", false},
			{"kotlin.kafka", "topic:single-set#consume", false},
			{"kotlin.kafka", "topic:java-style#consume", false},
		}},
		{"kotlin without kafka ignored", "src/Rx.kt", "kotlin/NoKafka.kt", nil},
		{"kotlin test file ignored", "src/main/kotlin/BusTest.kt", "kotlin/BusTest.kt", nil},
		{"dotnet test file yields nothing", "tests/BusTests.cs", "csharp/Kafka.cs", nil},
		{"dotnet redis subscribe ignored", "src/Chat.cs", "csharp/Redis.cs", nil},
		{"dotnet DetailType field ignored", "src/Dto.cs", "csharp/Dto.cs", nil},
		{"kafkajs primary line", "src/lines.ts", "ts/kafka_lines.ts", []wantFact{
			{"kafkajs", "topic:real-one#consume", false},
		}},
		{"python receiver gate", "app/gate.py", "py/kafka_gate.py", []wantFact{
			{"kafka.py", "topic:assigned-out#produce", false},
		}},
		{"sqs and eventbridge dotnet", "src/Q.cs", "csharp/Sqs.cs", []wantFact{
			{"sqs.dotnet", "queue:orders-queue#produce", false},
			{"sqs.dotnet", "queue:inbox#consume", false},
			{"sqs.dotnet", "queue:billing#declare", false},
			{"sqs.dotnet", "queue:dead-letters#declare", false},
			{"eventbridge.dotnet", "bus:orderplaced#produce", false},
		}},
		{"kafkajs nest sqs.js", "src/bus.ts", "ts/kafka.ts", []wantFact{
			{"kafkajs", "topic:orders-created#consume", false},
			{"kafkajs", "topic:a-topic#consume", false},
			{"kafkajs", "topic:b-topic#consume", false},
			{"kafkajs", "topic:kept-ts#consume", false},
			{"kafkajs", "topic:payments-done#produce", false},
			{"nest.pattern", "topic:user.created#consume", false},
			{"nest.pattern", "topic:user.get#consume", false},
			{"sqs.js", "queue:js-out#produce", false},
			{"sqs.js", "queue:js-in#consume", false},
		}},
		{"spec file yields nothing", "src/bus.spec.ts", "ts/kafka.ts", nil},
		{"kafka python and boto3", "app/consumer.py", "py/consumer.py", []wantFact{
			{"kafka.py", "topic:py-orders#consume", false},
			{"kafka.py", "topic:py-refunds#consume", false},
			{"kafka.py", "topic:sub-a#consume", false},
			{"kafka.py", "topic:sub-b#consume", false},
			{"kafka.py", "topic:sub-c#consume", false},
			{"kafka.py", "topic:kept-py#consume", false},
			{"kafka.py", "topic:py-out#produce", false},
			{"boto3.sqs", "queue:py-queue#declare", false},
			{"boto3.sqs", "queue:py-send#produce", false},
			{"boto3.sqs", "queue:py-recv#consume", false},
		}},
		{"python test file yields nothing", "app/test_consumer.py", "py/consumer.py", nil},
		{"python without kafka", "app/sock.py", "py/socket_client.py", nil},
		{"terraform", "infra/main.tf", "infra/main.tf", []wantFact{
			{"terraform.channel", "queue:orders-queue-tf#declare", true},
			{"terraform.channel", "topic:alerts-topic#declare", true},
			{"terraform.channel", "topic:tf-kafka#declare", true},
			{"terraform.channel", "topic:tf-confluent#declare", true},
		}},
		{"cdk ts", "infra/stack.ts", "infra/stack.ts", []wantFact{
			{"cdk.channel", "queue:cdk-orders#declare", false},
			{"cdk.channel", "topic:cdk-topic#declare", false},
		}},
		{"cdk without import", "infra/q.ts", "infra/plain_queue.ts", nil},
		{"cdk python", "infra/stack.py", "infra/cdk_stack.py", []wantFact{
			{"cdk.channel", "queue:cdk-py-queue#declare", false},
			{"cdk.channel", "topic:cdk-py-topic#declare", false},
		}},
		{"compose kafka topics", "docker-compose.yml", "infra/docker-compose.yml", []wantFact{
			{"compose.kafka_topics", "topic:comp-a#declare", false},
			{"compose.kafka_topics", "topic:comp-b#declare", false},
			{"compose.kafka_topics", "topic:comp-c#declare", false},
			{"compose.kafka_topics", "topic:sp-a#declare", false},
			{"compose.kafka_topics", "topic:sp-b#declare", false},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := channelFacts(t, c.vpath, c.disk)
			var g, w []string
			for _, f := range got {
				if len(f.Sources) != 1 {
					t.Errorf("%s: sources %v", f.Key, f.Sources)
					continue
				}
				g = append(g, f.Sources[0]+" | "+f.Key)
				if f.Primary.File != c.vpath || f.Primary.Line == 0 || f.Primary.Text == "" {
					t.Errorf("%s: bad primary %+v", f.Key, f.Primary)
				}
				if f.Attrs["name"] == "" || f.Attrs["kind"] == "" || f.Attrs["side"] == "" {
					t.Errorf("%s: bad attrs %v", f.Key, f.Attrs)
				}
				for _, x := range c.want {
					if x.key == f.Key && (len(f.Derived) > 0) != x.derived {
						t.Errorf("%s: derived=%v want %v", f.Key, f.Derived, x.derived)
					}
				}
			}
			for _, x := range c.want {
				w = append(w, x.src+" | "+x.key)
			}
			sort.Strings(g)
			sort.Strings(w)
			if strings.Join(g, "\n") != strings.Join(w, "\n") {
				t.Fatalf("facts mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(w, "\n  "))
			}
		})
	}
}

func TestChannelAttrsAndLines(t *testing.T) {
	for _, f := range channelFacts(t, "src/Bus.cs", "csharp/Kafka.cs") {
		if f.Key == "topic:order-shipped#produce" {
			if f.Attrs["name"] != "Order Shipped" || f.Attrs["kind"] != "topic" || f.Attrs["side"] != "produce" || f.Primary.Line != 11 {
				t.Errorf("attrs/line %v %+v", f.Attrs, f.Primary)
			}
		}
	}
	for _, f := range channelFacts(t, "infra/main.tf", "infra/main.tf") {
		if f.Key == "queue:orders-queue-tf#declare" && (f.Primary.Line != 2 || f.Derived[0].Line != 1) {
			t.Errorf("terraform evidence %+v %+v", f.Primary, f.Derived)
		}
	}
}

func TestChannelKafkaJSLine(t *testing.T) {
	for _, f := range channelFacts(t, "src/lines.ts", "ts/kafka_lines.ts") {
		if f.Primary.Line != 2 {
			t.Errorf("primary line %d, want 2", f.Primary.Line)
		}
	}
}

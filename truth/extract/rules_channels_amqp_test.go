package extract

import (
	"sort"
	"strings"
	"testing"
)

func TestAmqpChannelRules(t *testing.T) {
	cases := []struct {
		name, vpath, disk string
		want              []string
	}{
		{"amqplib", "src/mq.ts", "amqp/amqplib.ts", []string{
			"amqplib | exchange:orders.events#declare",
			"amqplib | queue:billing.orders#declare",
			"amqplib | queue:billing.orders#consume",
			"amqplib | exchange:orders.events#consume",
			"amqplib | queue:plain.queue#consume",
			"amqplib | exchange:audit.events#produce",
			"amqplib | queue:direct.queue#produce",
			"amqplib | queue:tasks#produce",
		}},
		{"amqplib in test file", "src/mq.test.ts", "amqp/amqplib.ts", nil},
		{"rabbitmq dotnet", "Bus.cs", "amqp/RabbitMq.cs", []string{
			"rabbitmq.dotnet | exchange:ledger.events#declare",
			"rabbitmq.dotnet | queue:ledger.in#declare",
			"rabbitmq.dotnet | queue:ledger.in#consume",
			"rabbitmq.dotnet | exchange:ledger.events#consume",
			"rabbitmq.dotnet | queue:lonely#consume",
			"rabbitmq.dotnet | exchange:ledger.events#produce",
			"rabbitmq.dotnet | queue:work.queue#produce",
			"rabbitmq.dotnet | exchange:alerts#produce",
		}},
		{"rabbitmq dotnet tests dir", "Tests/Bus.cs", "amqp/RabbitMq.cs", nil},
		{"pika", "app/mq.py", "amqp/pika_app.py", []string{
			"pika | exchange:shop.events#declare",
			"pika | queue:shop.mail#declare",
			"pika | queue:shop.mail#consume",
			"pika | exchange:shop.events#consume",
			"pika | queue:bare.queue#consume",
			"pika | exchange:shop.out#produce",
			"pika | queue:py.direct#produce",
		}},
		{"pika test file", "app/test_mq.py", "amqp/pika_app.py", nil},
		{"spring amqp", "src/main/java/shop/Mail.java", "amqp/Amqp.java", []string{
			"spring.amqp | queue:mail.in#consume",
			"spring.amqp | queue:multi.a#consume",
			"spring.amqp | queue:multi.b#consume",
			"spring.amqp | queue:bound.q#declare",
			"spring.amqp | queue:bound.q#consume",
			"spring.amqp | exchange:bound.x#declare",
			"spring.amqp | exchange:bound.x#consume",
			"spring.amqp | exchange:out.exchange#produce",
			"spring.amqp | queue:declared.q#declare",
			"spring.amqp | queue:built.q#declare",
			"spring.amqp | exchange:declared.x#declare",
		}},
		{"terraform rabbitmq", "infra/rabbit.tf", "amqp/rabbit.tf", []string{
			"terraform.rabbitmq | exchange:tf.events#declare",
			"terraform.rabbitmq | queue:tf.queue#declare",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, f := range channelFacts(t, c.vpath, c.disk) {
				if len(f.Sources) != 1 || f.Primary.Line == 0 || f.Primary.Text == "" || f.Primary.File != c.vpath {
					t.Errorf("%s: bad evidence/sources %+v %v", f.Key, f.Primary, f.Sources)
				}
				got = append(got, f.Sources[0]+" | "+f.Key)
			}
			sort.Strings(got)
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

// The exchange consume fact sits on the binding line and cites the consuming
// call as derived evidence.
func TestAmqpBoundExchangeEvidence(t *testing.T) {
	for _, f := range channelFacts(t, "Bus.cs", "amqp/RabbitMq.cs") {
		if f.Key == "exchange:ledger.events#consume" {
			if f.Primary.Line != 9 || len(f.Derived) != 1 || f.Derived[0].Line != 10 {
				t.Errorf("evidence %+v %+v", f.Primary, f.Derived)
			}
			return
		}
	}
	t.Fatal("no exchange consume fact")
}

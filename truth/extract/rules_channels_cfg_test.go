package extract

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexdx2/chronicle-core/truth"
)

// cfgFacts extracts a small in-memory tree and returns the channel facts as
// "source | key | primary file:line | derived file:line" lines.
func cfgFacts(t *testing.T, files map[string]string) []string {
	t.Helper()
	m := fstest.MapFS{}
	for p, c := range files {
		m[p] = &fstest.MapFile{Data: []byte(c)}
	}
	rep, err := Extract(m)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range rep.Facts {
		if f.Class != truth.ClassChannels {
			continue
		}
		s := f.Sources[0] + " | " + f.Key + " | " + f.Primary.File + ":" + itoa(f.Primary.Line)
		for _, d := range f.Derived {
			s += " | " + d.File + ":" + itoa(d.Line)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestCfgChannels(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"dotnet indexer, local, GetValue, section, options", map[string]string{
			"App/appsettings.json": "{\n // comment\n \"Kafka\": {\"Topics\": {\n  \"In\": \"in-topic\",\n  \"Out\": \"out-topic\",\n  \"Audit\": \"audit-topic\"\n }},\n \"Bus\": {\"Retry\": \"retry-topic\"}\n}\n",
			"App/Bus.cs": `using Confluent.Kafka;
class Bus {
  void Run(IConfiguration config, IOptions<KafkaOptions> opts) {
    var t = config["Kafka:Topics:In"];
    consumer.Subscribe(t);
    producer.ProduceAsync(config.GetValue<string>("Kafka:Topics:Out"), msg);
    producer.Produce(config.GetSection("Kafka:Topics")["Audit"], msg);
    consumer.Subscribe(opts.Value.Retry);
    consumer.Subscribe(config["Kafka:Topics:Missing"]);
    // consumer.Subscribe(config["Kafka:Topics:In"]);
    _log.Info("call consumer.Subscribe(config[\"Kafka:Topics:Out\"])");
  }
}
`,
		}, []string{
			"kafka.dotnet.config | topic:audit-topic#produce | App/Bus.cs:7 | App/appsettings.json:6",
			"kafka.dotnet.config | topic:in-topic#consume | App/Bus.cs:5 | App/appsettings.json:4",
			"kafka.dotnet.config | topic:out-topic#produce | App/Bus.cs:6 | App/appsettings.json:5",
			"kafka.dotnet.config | topic:retry-topic#consume | App/Bus.cs:8 | App/appsettings.json:8",
		}},
		{"dotnet without kafka and test file", map[string]string{
			"appsettings.json": `{"A": {"T": "x"}}`,
			"Chat.cs":          `class C { void M(IConfiguration c) { redis.Subscribe(c["A:T"]); } }`,
			"BusTests.cs":      `using Confluent.Kafka; class C { void M(IConfiguration c) { k.Subscribe(c["A:T"]); } }`,
		}, nil},
		{"ts env default through config module", map[string]string{
			"src/config.ts": "export const config = {\n  topic: process.env.T || 'orders',\n  other: process.env.O,\n};\nexport const AUDIT = process.env['AUDIT_TOPIC'] ?? \"audit\";\n",
			"src/k.ts": `import { Kafka } from 'kafkajs';
import { config, AUDIT as audit } from './config';
const local = process.env.LOCAL_TOPIC ?? 'local';
export async function f() {
  await producer.send({ topic: config.topic, messages: [] });
  await producer.send({ topic: audit, messages: [] });
  await producer.send({ topic: config.other, messages: [] });
  await consumer.subscribe({ topics: [local, config.topic] });
  // await producer.send({ topic: process.env.X || 'commented', messages: [] });
  log.info("producer.send({ topic: process.env.X || 'logged' })");
}
`,
		}, []string{
			"kafkajs.config | topic:audit#produce | src/k.ts:6 | src/config.ts:5",
			"kafkajs.config | topic:local#consume | src/k.ts:8 | src/k.ts:3",
			"kafkajs.config | topic:orders#consume | src/k.ts:8 | src/config.ts:2",
			"kafkajs.config | topic:orders#produce | src/k.ts:5 | src/config.ts:2",
		}},
		{"ts destructured env and shorthand", map[string]string{
			"src/k.ts": "import { Kafka } from 'kafkajs';\nconst { ORDERS_TOPIC = 'orders-d' } = process.env;\nconst topic = ORDERS_TOPIC;\nproducer.send({ topic, messages: [] });\n",
		}, []string{"kafkajs.config | topic:orders-d#produce | src/k.ts:4 | src/k.ts:2"}},
		{"ts spec file", map[string]string{
			"src/k.spec.ts": "const t = process.env.T || 'x';\nproducer.send({ topic: t });\n",
		}, nil},
		{"ts amqp and nats from env", map[string]string{
			"src/a.ts": "import amqp from 'amqplib';\nconst Q = process.env.JOBS_QUEUE || 'jobs';\nawait ch.assertQueue(Q);\nawait ch.consume(Q, cb);\n",
			"src/n.ts": "import { connect } from 'nats';\nconst S = process.env.SUBJ || 'evt.created';\nnc.publish(S, data);\nnc.subscribe(S);\n",
		}, []string{
			"amqp.js.config | queue:jobs#consume | src/a.ts:4 | src/a.ts:2",
			"amqp.js.config | queue:jobs#declare | src/a.ts:3 | src/a.ts:2",
			"nats.js.config | subject:evt.created#consume | src/n.ts:4 | src/n.ts:2",
			"nats.js.config | subject:evt.created#produce | src/n.ts:3 | src/n.ts:2",
		}},
		{"python getenv", map[string]string{
			"app/settings.py": "import os\nTOPIC = os.getenv('ORDERS_TOPIC', 'orders')\nOTHER = os.environ.get('X')\nREQ = os.environ['Y']\n",
			"app/prod.py": `from kafka import KafkaProducer, KafkaConsumer
from .settings import TOPIC, OTHER, REQ
import os
producer = KafkaProducer()
producer.send(TOPIC, b'x')
producer.send(OTHER, b'x')
producer.send(REQ, b'x')
producer.send(topic=os.environ.get('AUD', 'audit'), value=b'y')
consumer = KafkaConsumer(os.getenv('IN_TOPIC', 'in'))
# producer.send(os.getenv('C', 'commented'))
`,
		}, []string{
			"kafka.py.config | topic:audit#produce | app/prod.py:8",
			"kafka.py.config | topic:in#consume | app/prod.py:9",
			"kafka.py.config | topic:orders#produce | app/prod.py:5 | app/settings.py:2",
		}},
		{"python pika", map[string]string{
			"w.py": "import pika, os\nch.queue_declare(queue=os.getenv('Q', 'work'))\nch.basic_publish(exchange=os.getenv('EX', 'ex1'), routing_key='r', body=b'')\nch.basic_publish(exchange='', routing_key='r', body=b'')\n",
		}, []string{
			"pika.config | exchange:ex1#produce | w.py:3",
			"pika.config | queue:work#declare | w.py:2",
		}},
		{"spring value and listener placeholders", map[string]string{
			"svc/src/main/resources/application.yml":        "app:\n  topics:\n    orders: orders-y\n    out-topic: out-y\n    env: ${KAFKA_ENV_TOPIC:env-default}\n",
			"svc/src/main/resources/application.properties": "app.topics.props=props-topic\n",
			"svc/src/main/java/x/Bus.java": `package x;
class Bus {
  @Value("${app.topics.out-topic}")
  private String outTopic;
  @Value("${app.topics.missing:fallback}")
  private String fb;
  @Value("${app.topics.unknown}")
  private String unknown;
  private final KafkaTemplate<String, String> kafkaTemplate;
  @KafkaListener(topics = "${app.topics.orders}")
  void in(String s) {}
  @KafkaListener(topics = {"${app.topics.props}", "${app.topics.env}"})
  void in2(String s) {}
  void out() {
    kafkaTemplate.send(outTopic, "x");
    kafkaTemplate.send(fb, "x");
    kafkaTemplate.send(unknown, "x");
    // kafkaTemplate.send(outTopic, "commented");
  }
}
`,
		}, []string{
			"spring.kafka.config | topic:env-default#consume | svc/src/main/java/x/Bus.java:12 | svc/src/main/resources/application.yml:5",
			"spring.kafka.config | topic:fallback#produce | svc/src/main/java/x/Bus.java:16",
			"spring.kafka.config | topic:orders-y#consume | svc/src/main/java/x/Bus.java:10 | svc/src/main/resources/application.yml:3",
			"spring.kafka.config | topic:out-y#produce | svc/src/main/java/x/Bus.java:15 | svc/src/main/resources/application.yml:4",
			"spring.kafka.config | topic:props-topic#consume | svc/src/main/java/x/Bus.java:12 | svc/src/main/resources/application.properties:1",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cfgFacts(t, c.files)
			// Only the config rules are under test here.
			var g []string
			for _, s := range got {
				if strings.Contains(strings.SplitN(s, " | ", 2)[0], "config") {
					g = append(g, s)
				}
			}
			if strings.Join(g, "\n") != strings.Join(c.want, "\n") {
				t.Fatalf("got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(c.want, "\n  "))
			}
		})
	}
}

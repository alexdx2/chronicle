package scan

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
)

// Channels that infrastructure and connector configuration declare, whatever
// the code does with them: terraform resources, AWS CDK constructs, Kafka
// Connect connectors. They enter the table as channel entries under reserved
// key prefixes ("declare:", "connect.source:", "connect.sink:") that the
// resolver turns into declared channels and produce/consume facts of the file.

var (
	tfChannelResource = regexp.MustCompile(`resource\s+"(kafka_topic|confluent_kafka_topic|google_pubsub_topic|google_pubsub_subscription|rabbitmq_queue|rabbitmq_exchange|aws_sqs_queue|aws_sns_topic|azurerm_servicebus_topic|azurerm_servicebus_queue)"\s+"([^"]+)"\s*\{`)
	tfNameAttr        = regexp.MustCompile(`(?m)^\s*(?:name|topic_name)\s*=\s*"([^"$]+)"`)
	cdkChannel        = regexp.MustCompile(`new\s+(?:\w+\.)?(Topic|Queue|EventBus|Stream)\s*\(\s*[\w.]+\s*,\s*['"][^'"]*['"]\s*,\s*\{`)
	cdkNameProp       = regexp.MustCompile(`(?:topicName|queueName|eventBusName|streamName)\s*:\s*['"]([^'"$]+)['"]`)
	cdkPyChannel      = regexp.MustCompile(`\b(?:sns\.Topic|sqs\.Queue|events\.EventBus|kinesis\.Stream)\s*\(`)
	cdkPyNameKw       = regexp.MustCompile(`(?:topic_name|queue_name|event_bus_name|stream_name)\s*=\s*['"]([^'"$]+)['"]`)
)

// matchingBrace returns the text up to the brace that closes the one just before s.
func matchingBrace(s string, max int) string {
	if len(s) > max {
		s = s[:max]
	}
	depth := 1
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return ""
}

// parseTerraformChannels reads declared channels of a .tf file.
func parseTerraformChannels(t *graph.ConfigHosts, file string, body []byte) {
	src := string(body)
	for _, m := range tfChannelResource.FindAllStringSubmatchIndex(src, -1) {
		blk := matchingBrace(src[m[1]:], 4000)
		if nm := tfNameAttr.FindStringSubmatch(blk); nm != nil {
			t.AddChannel("declare:"+src[m[2]:m[3]]+"."+src[m[4]:m[5]], nm[1], file)
		}
	}
}

// parseCDKChannels reads Topic/Queue/EventBus/Stream constructs with an
// explicit physical name (TypeScript/JavaScript options object, Python keywords).
func parseCDKChannels(t *graph.ConfigHosts, file string, body []byte) {
	if !bytes.Contains(body, []byte("Name")) && !bytes.Contains(body, []byte("_name")) {
		return
	}
	src := string(body)
	for _, m := range cdkChannel.FindAllStringSubmatchIndex(src, -1) {
		blk := matchingBrace(src[m[1]:], 1500)
		if nm := cdkNameProp.FindStringSubmatch(blk); nm != nil {
			t.AddChannel("declare:cdk."+strings.ToLower(src[m[2]:m[3]]), nm[1], file)
		}
	}
	for _, m := range cdkPyChannel.FindAllStringIndex(src, -1) {
		end := m[1] + 600
		if end > len(src) {
			end = len(src)
		}
		if nm := cdkPyNameKw.FindStringSubmatch(src[m[1]:end]); nm != nil {
			t.AddChannel("declare:cdk.py", nm[1], file)
		}
	}
}

// isConnectorJSON: a Kafka Connect connector configuration.
func isConnectorJSON(p string, body []byte) bool {
	return strings.EqualFold(path.Ext(p), ".json") && bytes.Contains(body, []byte(`"connector.class"`))
}

// parseConnectorChannels reads a Debezium source (topic.prefix + each
// table.include.list entry) or a sink connector (topics).
func parseConnectorChannels(t *graph.ConfigHosts, file string, body []byte) {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return
	}
	cfg := doc
	if inner, ok := doc["config"].(map[string]any); ok {
		cfg = inner
	}
	str := func(k string) string {
		if v, ok := cfg[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	class := strings.ToLower(str("connector.class"))
	switch {
	case strings.Contains(class, "debezium") || strings.HasSuffix(class, "sourceconnector"):
		prefix := str("topic.prefix")
		if prefix == "" {
			prefix = str("database.server.name")
		}
		if prefix == "" {
			return
		}
		for _, list := range []string{"table.include.list", "collection.include.list"} {
			for _, entry := range strings.Split(str(list), ",") {
				entry = strings.TrimSpace(entry)
				if entry == "" || strings.ContainsAny(entry, `*\()[]?+$`) {
					continue
				}
				t.AddChannel("connect.source:"+entry, prefix+"."+entry, file)
			}
		}
	case strings.Contains(class, "sink"):
		for _, name := range strings.Split(str("topics"), ",") {
			name = strings.TrimSpace(name)
			if name != "" && !strings.ContainsAny(name, `*\()[]?+$`) {
				t.AddChannel("connect.sink:"+name, name, file)
			}
		}
	}
}

package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// MQTT topics (kind "topic"). subscribe consumes, publish produces; the name
// is the literal as written, wildcards (+, #) included. Python (paho-mqtt,
// aiomqtt, asyncio-mqtt, gmqtt) and JavaScript/TypeScript (mqtt.js,
// async-mqtt) are covered; a file must import the client.

var (
	mqttPyImport = chPyImport(`paho|aiomqtt|asyncio_mqtt|gmqtt`)
	mqttJSImport = chJSImport(`mqtt|async-mqtt|mqtt/[\w./-]+`)

	mqttTuple  = regexp.MustCompile(`\(\s*(['"])([^'"]+)['"]\s*,`)
	mqttObjKey = regexp.MustCompile(`[{,]\s*(['"])([^'"]+)['"]\s*:`)
)

func init() {
	Rules = append(Rules,
		funcRule{"mqtt.py", func(p string) bool { return hasExt(p, ".py") }, applyMqttPy},
		funcRule{"mqtt.js", func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }, applyMqttJS},
	)
}

// mqttListItems reads the topic names of `["a", "b"]`, `[("a", 0), ("b", 1)]`
// or `{ 'a': {qos: 0} }` given as the first argument.
func mqttListItems(a string) []string {
	a = strings.TrimSpace(a)
	switch {
	case strings.HasPrefix(a, "{"):
		var out []string
		for _, m := range mqttObjKey.FindAllStringSubmatch(a, -1) {
			out = append(out, m[2])
		}
		return out
	case strings.HasPrefix(a, "["):
		inner := strings.TrimSuffix(strings.TrimPrefix(a, "["), "]")
		if strings.Contains(inner, "(") {
			var out []string
			for _, m := range mqttTuple.FindAllStringSubmatch(inner, -1) {
				out = append(out, m[2])
			}
			return out
		}
		return literalItems(inner)
	}
	return nil
}

func mqttTopics(kw string) func(c chCall) []string {
	return func(c chCall) []string {
		if len(c.args) == 0 {
			return nil
		}
		if l := mqttListItems(c.args[0]); l != nil {
			return l
		}
		return chFirst(kw)(c)
	}
}

func applyMqttPy(f *FileCtx) []truth.Fact {
	sub := chSpec{names: `subscribe`, kind: "topic", side: "consume", pick: mqttTopics("topic")}
	pub := chSpec{names: `publish`, kind: "topic", side: "produce", pick: chFirst("topic")}
	// paho.mqtt.publish.single("t", ...) and paho.mqtt.subscribe.simple("t")
	one := chSpec{names: `single`, kind: "topic", side: "produce", pick: func(c chCall) []string {
		if strings.HasSuffix(c.recv, "publish") {
			return chFirst("topic")(c)
		}
		return nil
	}}
	simple := chSpec{names: `simple`, kind: "topic", side: "consume", pick: func(c chCall) []string {
		if strings.HasSuffix(c.recv, "subscribe") {
			return mqttTopics("topics")(c)
		}
		return nil
	}}
	return chRule("mqtt.py", chImports(mqttPyImport), sub, pub, one, simple)(f)
}

func applyMqttJS(f *FileCtx) []truth.Fact {
	return chRule("mqtt.js", chImports(mqttJSImport),
		chSpec{names: `subscribe|subscribeAsync`, kind: "topic", side: "consume", pick: mqttTopics("")},
		chSpec{names: `publish|publishAsync`, kind: "topic", side: "produce", pick: chFirst("")},
	)(f)
}

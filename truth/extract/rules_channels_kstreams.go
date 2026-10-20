package extract

import (
	"regexp"

	"github.com/alexdx2/chronicle-core/truth"
)

// Kafka Streams (Java): builder.stream/table/globalTable("x") consume,
// stream.to("x") and .through("x") produce. The Consumed.with / Produced.with
// variants take the name as first argument too. Only string literals count;
// a constant, a pattern or a TopicNameExtractor yields nothing.

func init() {
	Rules = append(Rules,
		funcRule{"kafka.streams", func(p string) bool { return hasExt(p, ".java") }, applyKafkaStreams},
	)
}

var (
	kstreamsSource = regexp.MustCompile(`\.\s*(?:stream|table|globalTable)\(\s*"([^"]+)"\s*[,)]`)
	kstreamsSink   = regexp.MustCompile(`\.\s*(?:to|through)\(\s*"([^"]+)"\s*[,)]`)
)

// isKafkaStreamsFile gates the rule: the file must use the Kafka Streams API,
// otherwise `.to("x")` and `.stream("x")` are calls of unrelated libraries.
func isKafkaStreamsFile(f *FileCtx) bool {
	return codeContains(f, "org.apache.kafka.streams", "streamsbuilder")
}

var kafkaStreamsRule = specRule("kafka.streams", isKafkaStreamsFile,
	chanSpec{re: kstreamsSource, kind: "topic", side: "consume", win: 2},
	chanSpec{re: kstreamsSink, kind: "topic", side: "produce", win: 2},
)

func applyKafkaStreams(f *FileCtx) []truth.Fact { return kafkaStreamsRule(f) }

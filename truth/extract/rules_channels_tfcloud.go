package extract

import (
	"regexp"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Terraform resources that declare a channel by `name`: the Mongey/kafka
// provider's kafka_topic and the Google provider's google_pubsub_topic and
// google_pubsub_subscription. (aws_sqs_queue, aws_sns_topic and
// confluent_kafka_topic are in rules_channels_infra.go.)

func init() {
	Rules = append(Rules, funcRule{"terraform.cloud", func(p string) bool { return hasExt(p, ".tf") }, applyTerraformCloud})
}

var tfCloudResource = regexp.MustCompile(`^\s*resource\s+"(kafka_topic|google_pubsub_topic|google_pubsub_subscription)"\s+"[^"]+"\s*\{`)

func applyTerraformCloud(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	tfResourceAttrs(f, tfCloudResource, func(string) string { return "name" },
		func(res, l lex.Line, typ, val string) {
			kind := "topic"
			if typ == "google_pubsub_subscription" {
				kind = "subscription"
			}
			if fc, ok := channelFact("terraform.cloud", f, l, kind, val, "declare", evidence(f, res)); ok {
				out = append(out, fc)
			}
		})
	return out
}

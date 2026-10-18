package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Infrastructure-declared channels: Terraform, CDK and compose.

func init() {
	Rules = append(Rules,
		funcRule{"terraform.channel", func(p string) bool { return hasExt(p, ".tf") }, applyTerraformChannel},
		funcRule{"cdk.channel", func(p string) bool { return hasExt(p, ".ts", ".js", ".py") }, applyCDK},
		funcRule{"compose.kafka_topics", isComposePath, applyComposeTopics},
	)
}

var tfResource = regexp.MustCompile(`^\s*resource\s+"(aws_sqs_queue|aws_sns_topic|kafka_topic|confluent_kafka_topic)"\s+"[^"]+"\s*\{`)

func applyTerraformChannel(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	kindOf := func(typ string) (kind, attr string) {
		switch typ {
		case "aws_sns_topic":
			return "topic", "name"
		case "kafka_topic", "confluent_kafka_topic":
			return "topic", "topic_name"
		}
		return "queue", "name"
	}
	var out []truth.Fact
	tfResourceAttrs(f, tfResource, func(typ string) string { _, a := kindOf(typ); return a },
		func(res, l lex.Line, typ, val string) {
			kind, _ := kindOf(typ)
			if fc, ok := channelFact("terraform.channel", f, l, kind, val, "declare", evidence(f, res)); ok {
				out = append(out, fc)
			}
		})
	return out
}

var (
	cdkTS = regexp.MustCompile(`new\s+(?:sqs\.|sns\.)?(Queue|Topic)\(\s*\w+\s*,\s*['"][^'"]*['"]\s*,\s*\{[^}]*?\b(?:queueName|topicName):\s*['"]([^'"]+)['"]\s*[,}]`)
	cdkPy = regexp.MustCompile(`\b(?:sqs|sns)\.(Queue|Topic)\(\s*\w+\s*,\s*['"][^'"]*['"][^)]*?\b(?:queue_name|topic_name)\s*=\s*['"]([^'"]+)['"]\s*[,)]`)
)

func applyCDK(f *FileCtx) []truth.Fact {
	if f.IsTest || !codeContains(f, "aws-cdk-lib", "aws_cdk", "@aws-cdk/") {
		return nil
	}
	re := cdkTS
	if hasExt(f.Path, ".py") {
		re = cdkPy
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		text := codeWindow(f.Lines, i, 7)
		loc := re.FindStringSubmatchIndex(text)
		if loc == nil || loc[0] >= len(l.Code) {
			continue
		}
		kind := "queue"
		if text[loc[2]:loc[3]] == "Topic" {
			kind = "topic"
		}
		if fc, ok := channelFact("cdk.channel", f, l, kind, text[loc[4]:loc[5]], "declare"); ok {
			out = append(out, fc)
		}
	}
	return out
}

var composeTopics = regexp.MustCompile(`\bKAFKA_CREATE_TOPICS\s*[=:]\s*["']?([^"'\r\n]+)`)

func applyComposeTopics(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for _, l := range f.Lines {
		m := composeTopics.FindStringSubmatch(l.Code)
		if m == nil {
			continue
		}
		for _, spec := range strings.Split(m[1], ",") {
			name, _, _ := strings.Cut(strings.TrimSpace(spec), ":")
			if fc, ok := channelFact("compose.kafka_topics", f, l, "topic", name, "declare"); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

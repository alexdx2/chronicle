package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// Infrastructure-declared channels: Terraform, CDK and compose.

func init() {
	Rules = append(Rules,
		funcRule{"terraform.channel", func(p string) bool { return hasExt(p, ".tf") }, applyTerraformChannel},
		funcRule{"cdk.channel", func(p string) bool { return hasExt(p, ".ts", ".js", ".py") }, applyCDK},
		funcRule{"compose.kafka_topics", isComposePath, applyComposeTopics},
	)
}

var (
	tfResource = regexp.MustCompile(`^\s*resource\s+"(aws_sqs_queue|aws_sns_topic|kafka_topic|confluent_kafka_topic)"\s+"[^"]+"\s*\{`)
	tfAttr     = regexp.MustCompile(`^\s*(name|topic_name)\s*=\s*"([^"]*)"`)
)

func applyTerraformChannel(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for i := 0; i < len(f.Lines); i++ {
		m := tfResource.FindStringSubmatch(f.Lines[i].Code)
		if m == nil {
			continue
		}
		kind, attr := "queue", "name"
		switch m[1] {
		case "aws_sns_topic":
			kind = "topic"
		case "kafka_topic", "confluent_kafka_topic":
			kind, attr = "topic", "topic_name"
		}
		res := f.Lines[i]
		depth := strings.Count(res.Code, "{") - strings.Count(res.Code, "}")
		j := i + 1
		for ; j < len(f.Lines) && depth > 0; j++ {
			l := f.Lines[j]
			if a := tfAttr.FindStringSubmatch(l.Code); a != nil && depth == 1 && a[1] == attr {
				if fc, ok := channelFact("terraform.channel", f, l, kind, a[2], "declare", evidence(f, res)); ok {
					out = append(out, fc)
				}
			}
			depth += strings.Count(l.Code, "{") - strings.Count(l.Code, "}")
		}
		i = j - 1
	}
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

var composeTopics = regexp.MustCompile(`\bKAFKA_CREATE_TOPICS\s*[=:]\s*["']?([^"'\s]+)`)

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
			name, _, _ := strings.Cut(spec, ":")
			if fc, ok := channelFact("compose.kafka_topics", f, l, "topic", name, "declare"); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}

package extract

import (
	"regexp"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

func init() {
	Rules = append(Rules,
		funcRule{"terraform.rabbitmq", func(p string) bool { return hasExt(p, ".tf") }, applyTerraformRabbit},
	)
}

var tfRabbitResource = regexp.MustCompile(`^\s*resource\s+"(rabbitmq_exchange|rabbitmq_queue)"\s+"[^"]+"\s*\{`)

// applyTerraformRabbit: rabbitmq_exchange / rabbitmq_queue resources (provider
// cyrilgdn/rabbitmq) declare the channel named by their name attribute.
func applyTerraformRabbit(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	tfResourceAttrs(f, tfRabbitResource, func(string) string { return "name" },
		func(res, l lex.Line, typ, val string) {
			kind := "queue"
			if typ == "rabbitmq_exchange" {
				kind = "exchange"
			}
			if fc, ok := channelFact("terraform.rabbitmq", f, l, kind, val, "declare", evidence(f, res)); ok {
				out = append(out, fc)
			}
		})
	return out
}

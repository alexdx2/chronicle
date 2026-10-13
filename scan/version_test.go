package scan

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/prompts"
)

func TestExtractorVersion_DependsOnlyOnPackRefs(t *testing.T) {
	a := ExtractorVersion([]prompts.PackRef{{ID: "kafka", Version: "aaa"}, {ID: "dotnet", Version: "bbb"}})
	b := ExtractorVersion([]prompts.PackRef{{ID: "dotnet", Version: "bbb"}, {ID: "kafka", Version: "aaa"}})
	c := ExtractorVersion([]prompts.PackRef{{ID: "dotnet", Version: "bbb"}, {ID: "kafka", Version: "ccc"}})
	if a != b || a == c || !strings.HasPrefix(a, "fact-schema@1+packs:") || len(a) != len("fact-schema@1+packs:")+12 {
		t.Fatalf("%s %s %s", a, b, c)
	}
}

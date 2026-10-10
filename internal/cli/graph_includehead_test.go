package cli

import (
	"strings"
	"testing"
)

func TestResolveOptsFor_ValidatesIncludeHead(t *testing.T) {
	sha40 := strings.Repeat("a1", 20)
	cases := []struct {
		name    string
		in      string
		wantErr bool
		heads   int
	}{
		{"empty", "", false, 0},
		{"sha40", sha40, false, 1},
		{"short", "abc123", true, 0},
		{"nonhex40", strings.Repeat("z", 40), true, 0},
		{"upper40", strings.ToUpper(sha40), true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opt, err := resolveOptsFor(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "--include-head must be a full commit sha") {
				t.Fatalf("message: %v", err)
			}
			if len(opt.IncludeHeads) != c.heads {
				t.Fatalf("heads=%v", opt.IncludeHeads)
			}
		})
	}
}

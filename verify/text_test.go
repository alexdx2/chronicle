package verify

import (
	"encoding/json"
	"testing"
)

func verifyText(t *testing.T, content, substring string) *Result {
	t.Helper()
	raw, _ := json.Marshal(TextAssertion{Substring: substring})
	r, err := (&TextVerifier{}).Verify([]byte(content), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTextContains_TokenBounded(t *testing.T) {
	cases := []struct {
		name, content, sub, want string
	}{
		{"exact quoted", "const T = 'battle-results';\n", "battle-results", "valid"},
		{"longer topic must not match", "const T = 'battle-results-v2';\n", "battle-results", "missing"},
		{"longer prefix must not match", "const T = 'old-battle-results';\n", "battle-results", "missing"},
		{"underscore suffix", "x = battle_results_x\n", "battle_results", "missing"},
		{"dotted identifier ok", "bus.publish('battle-result.event')\n", "battle-result.event", "valid"},
		{"route piecewise still matches", "@Controller('arena')\n@Post('attack')\n", "/arena/attack", "valid"},
		{"route literal", "app.post('/arena/attack', h)\n", "/arena/attack", "valid"},
		{"two lines, only one bounded", "a = 'battle-results-v2'\nb = 'battle-results'\n", "battle-results", "valid"},
	}
	for _, c := range cases {
		if got := verifyText(t, c.content, c.sub).Status; got != c.want {
			t.Errorf("%s: status = %q, want %q", c.name, got, c.want)
		}
	}
}

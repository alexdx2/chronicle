package llm

import "testing"

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"plain object", `{"a":1}`, `{"a":1}`, false},
		{"fenced", "```json\n{\"a\": [1,2]}\n```", `{"a": [1,2]}`, false},
		{"fence without lang", "```\n{\"a\":1}\n```", `{"a":1}`, false},
		{"prose around", "Here you go:\n{\"a\":{\"b\":\"}\"}}\nHope that helps!", `{"a":{"b":"}"}}`, false},
		{"prose and fence", "Sure.\n```json\n[1,2,3]\n```\nDone.", `[1,2,3]`, false},
		{"top-level array", `[{"a":1},{"a":2}]`, `[{"a":1},{"a":2}]`, false},
		{"bracket in prose first", "see [note] then {\"a\":1}", "", true},
		{"valid value in prose before payload", "see [1] then {\"a\":1}", "", true},
		{"truncated nested must not yield fragment", `{"a":[{"b":1},{"c":`, "", true},
		{"two values", `{"a":1} {"b":2}`, "", true},
		{"garbage", "no json here", "", true},
		{"truncated", `{"a": [1,`, "", true},
		{"empty", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ExtractJSON(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

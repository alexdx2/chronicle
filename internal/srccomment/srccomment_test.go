package srccomment

import (
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	cs := "var a = \"// not a comment\"; // real\nvar url = \"http://x/y\";\n/* block\nMapGet(\"/b\") */ live();\n"
	m, ok := Mask("Program.cs", []byte(cs))
	if !ok {
		t.Fatal("cs is known")
	}
	at := func(sub string) bool { return m[strings.Index(cs, sub)] }
	for sub, want := range map[string]bool{
		"not a comment": false, "real": true, "http://x/y": false, "block": true, "/b": true, "live": false,
	} {
		if at(sub) != want {
			t.Errorf("%q in comment = %v, want %v", sub, at(sub), want)
		}
	}
	if _, ok := Mask("data.json", []byte("{}")); ok {
		t.Error("json has no known comment syntax")
	}
	py := "x = 1  # tail\ny = '#no'\n"
	pm, _ := Mask("a.py", []byte(py))
	if !pm[strings.Index(py, "tail")] || pm[strings.Index(py, "no'")] {
		t.Error("python # handling wrong")
	}
	if got := string(Strip("a.cs", []byte("a // b\nc"))); got != "a     \nc" {
		t.Errorf("Strip = %q", got)
	}
}

package viewmodel

import "testing"

// TestTextEscape_MatchesMermaidText: the public wrapper delegates to the
// exact same private helper the diagram renderer uses — not a parallel
// reimplementation that could quietly drift from it.
func TestTextEscape_MatchesMermaidText(t *testing.T) {
	for _, s := range []string{
		`plain text`,
		`<img src=x onerror=alert(1)>`,
		"quote\" pipe| angle<>\r\nnewline",
		``,
	} {
		if got, want := TextEscape(s), mermaidText(s); got != want {
			t.Errorf("TextEscape(%q) = %q, want mermaidText's own %q", s, got, want)
		}
	}
}

// TestTextEscape_NoLiteralAngleBrackets: the one security property this
// escaping is actually relied on for (see task-13-report.md) — a hostile
// string never leaves a literal `<` or `>` behind.
func TestTextEscape_NoLiteralAngleBrackets(t *testing.T) {
	got := TextEscape(`<img src=x onerror=alert(1)>`)
	for _, r := range got {
		if r == '<' || r == '>' {
			t.Fatalf("TextEscape(...) = %q, still contains a literal angle bracket", got)
		}
	}
}

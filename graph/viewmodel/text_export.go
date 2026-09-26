package viewmodel

// TextEscape is the public door onto this package's own untrusted-text
// escaping convention (private mermaidText, render.go): no `"`, `<`, `>`,
// `|`, or newline survives, replaced with mermaid's own pseudo-entity
// syntax (`#quot;`, `#lt;`, `#gt;`, `#124;`) rather than standard HTML
// entities. That convention was built for a mermaid diagram label, but it is
// exactly as safe for plain prose/table text that might carry untrusted
// graph data (a node name, a note): the two characters that matter for
// injection, `<` and `>`, are gone either way. A caller outside this package
// (Task 13's Markdown renderer, package review) that needs to escape
// graph-derived display text for a non-mermaid surface calls this instead of
// inventing a second, differently-behaved escaping convention — see
// task-13-report.md for why one convention throughout was the deliberate
// choice over a prettier HTML-entity escape for the non-diagram case.
func TextEscape(s string) string {
	return mermaidText(s)
}

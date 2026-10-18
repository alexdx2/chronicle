// Package lex blanks comments out of source text so extraction rules never
// see them. String literals are kept intact: rules read route paths from them.
// Known over-blanking by design: a JS regex literal containing /* opens a block
// comment the lexer cannot tell apart, so the rest of the file is blanked.
// Known under-blanking: a JS regex literal containing a quote opens a string
// the lexer cannot tell apart, so a trailing same-line // comment can stay in Code.
package lex

import (
	"bytes"
	"path/filepath"
	"strings"
)

// Family selects the comment and string syntax of a file.
type Family int

const (
	CLike  Family = iota // //, /* */, "…", '…', `…`, C# @"…" and """…"""
	Python               // #, '…', "…"; '''…''' and """…""" are blanked like comments (docstrings)
	Hash                 // yaml, toml, hcl, dotenv, Dockerfile: # comments, single-line quotes
	None                 // json, xml: no comments
)

// Line is one source line (split on '\n'; a trailing '\r' stays).
// Code equals Raw with comment bytes replaced by spaces (same length).
type Line struct {
	Num  int
	Raw  string
	Code string
}

// Result is the lexed file. Unterminated is set when a block comment or a
// multi-line string never closes; the rest of the file is then blanked.
type Result struct {
	Lines        []Line
	Unterminated bool
}

// FamilyFor picks the family from the base name of path.
func FamilyFor(path string) Family {
	base := strings.ToLower(filepath.Base(path))
	if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") ||
		base == ".env" || strings.HasPrefix(base, ".env.") {
		return Hash
	}
	switch filepath.Ext(base) {
	case ".cs", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return CLike
	case ".py":
		return Python
	case ".yml", ".yaml", ".toml", ".tf":
		return Hash
	}
	return None
}

// Scan lexes src and returns per-line raw and comment-free text.
func Scan(src []byte, f Family) Result {
	code := append([]byte(nil), src...)
	n := len(src)
	unterminated := false
	blank := func(a, b int) {
		for k := a; k < b; k++ {
			if code[k] != '\n' && code[k] != '\r' {
				code[k] = ' '
			}
		}
	}
	// closeAt finds the end (exclusive) of a multi-line construct starting the
	// search at from; esc honours backslash escapes. -1 if it never closes.
	closeAt := func(from int, delim string, esc bool) int {
		for j := from; j < n; j++ {
			if esc && src[j] == '\\' {
				j++
				continue
			}
			if bytes.HasPrefix(src[j:], []byte(delim)) {
				return j + len(delim)
			}
		}
		return -1
	}
	// multi handles a construct that may span lines; blankIt marks comments.
	multi := func(i, bodyFrom int, delim string, esc, blankIt bool) int {
		end := closeAt(bodyFrom, delim, esc)
		if end < 0 {
			unterminated = true
			blank(i, n)
			return n
		}
		if blankIt {
			blank(i, end)
		}
		return end
	}
	has := func(i int, s string) bool { return bytes.HasPrefix(src[i:], []byte(s)) }

	for i := 0; f != None && i < n; {
		c := src[i]
		switch {
		case (f == CLike && has(i, "//")) || ((f == Python || f == Hash) && c == '#'):
			j := bytes.IndexByte(src[i:], '\n')
			if j < 0 {
				j = n - i
			}
			blank(i, i+j)
			i += j
		case f == CLike && has(i, "/*"):
			i = multi(i, i+2, "*/", false, true)
		case f == CLike && has(i, `@"`):
			// C# verbatim: "" is an escaped quote, no backslash escapes.
			j := i + 2
			for j < n && !(src[j] == '"' && !(j+1 < n && src[j+1] == '"')) {
				if src[j] == '"' {
					j++
				}
				j++
			}
			if j >= n {
				unterminated = true
				blank(i, n)
				i = n
			} else {
				i = j + 1
			}
		case f == CLike && has(i, `"""`):
			i = multi(i, i+3, `"""`, false, false)
		case f == Python && (has(i, `"""`) || has(i, `'''`)):
			i = multi(i, i+3, string(src[i:i+3]), true, true)
		case f == CLike && c == '`':
			i = multi(i, i+1, "`", true, false)
		case c == '"' || c == '\'':
			// Single-line string; an unclosed quote is not a string at all.
			esc := !(f == Hash && c == '\'')
			j := i + 1
			for j < n && src[j] != c && src[j] != '\n' {
				if esc && src[j] == '\\' && j+1 < n && src[j+1] != '\n' {
					j++
				}
				j++
			}
			if j < n && src[j] == c {
				i = j + 1
			} else {
				// Unclosed: not a string (e.g. an apostrophe in JSX text);
				// resume right after the quote so later openers are seen.
				i++
			}
		default:
			i++
		}
	}

	rawLines := bytes.Split(src, []byte{'\n'})
	codeLines := bytes.Split(code, []byte{'\n'})
	if n > 0 && src[n-1] == '\n' {
		rawLines, codeLines = rawLines[:len(rawLines)-1], codeLines[:len(codeLines)-1]
	}
	if n == 0 {
		rawLines, codeLines = nil, nil
	}
	res := Result{Unterminated: unterminated, Lines: make([]Line, len(rawLines))}
	for k := range rawLines {
		res.Lines[k] = Line{Num: k + 1, Raw: string(rawLines[k]), Code: string(codeLines[k])}
	}
	return res
}

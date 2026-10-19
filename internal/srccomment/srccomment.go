// Package srccomment says which bytes of a source file are inside a comment.
// It is a small lexer (strings, line comments, block comments), not a parser:
// good enough to tell a commented-out route from a live one.
package srccomment

import (
	"path/filepath"
	"strings"
)

type syntax struct {
	line  []string // line-comment openers
	block bool     // /* ... */
}

func syntaxFor(path string) (syntax, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".cs", ".java", ".kt", ".kts",
		".go", ".rs", ".swift", ".c", ".h", ".cc", ".cpp", ".hpp", ".scala", ".dart", ".proto", ".groovy":
		return syntax{line: []string{"//"}, block: true}, true
	case ".php":
		return syntax{line: []string{"//", "#"}, block: true}, true
	case ".py", ".rb", ".sh", ".bash", ".zsh", ".yaml", ".yml":
		return syntax{line: []string{"#"}}, true
	}
	return syntax{}, false
}

// Mask returns, per byte of content, whether it lies inside a comment. ok is
// false for a file type whose comment syntax is not known (nothing is masked).
func Mask(path string, content []byte) (mask []bool, ok bool) {
	syn, known := syntaxFor(path)
	if !known {
		return nil, false
	}
	mask = make([]bool, len(content))
	hasPrefix := func(i int, p string) bool {
		return i+len(p) <= len(content) && string(content[i:i+len(p)]) == p
	}
	for i := 0; i < len(content); {
		c := content[i]
		// string literals
		if c == '"' || c == '\'' || c == '`' {
			if c == '\'' && strings.EqualFold(filepath.Ext(path), ".rs") {
				i++
				continue
			}
			triple := hasPrefix(i, `"""`) || hasPrefix(i, `'''`)
			verbatim := c == '"' && i > 0 && content[i-1] == '@' && strings.EqualFold(filepath.Ext(path), ".cs")
			if triple {
				q := string(content[i : i+3])
				j := i + 3
				for j < len(content) && !hasPrefix(j, q) {
					j++
				}
				i = j + 3
				continue
			}
			j := i + 1
			for j < len(content) {
				if content[j] == '\\' && !verbatim {
					j += 2
					continue
				}
				if content[j] == c {
					if verbatim && j+1 < len(content) && content[j+1] == c {
						j += 2
						continue
					}
					break
				}
				if content[j] == '\n' && c != '`' && !verbatim {
					break
				}
				j++
			}
			i = j + 1
			continue
		}
		matched := false
		for _, op := range syn.line {
			if hasPrefix(i, op) {
				j := i
				for j < len(content) && content[j] != '\n' {
					mask[j] = true
					j++
				}
				i = j
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if syn.block && hasPrefix(i, "/*") {
			j := i
			for j < len(content) && !(content[j] == '*' && j+1 < len(content) && content[j+1] == '/' && j > i+1) {
				mask[j] = true
				j++
			}
			for k := j; k < len(content) && k < j+2; k++ {
				mask[k] = true
			}
			i = j + 2
			continue
		}
		i++
	}
	return mask, true
}

// Strip returns content with every comment byte replaced by a space (newlines
// kept), so a regexp over the result sees live code only.
func Strip(path string, content []byte) []byte {
	mask, ok := Mask(path, content)
	if !ok {
		return content
	}
	out := make([]byte, len(content))
	for i, c := range content {
		if mask[i] && c != '\n' {
			out[i] = ' '
		} else {
			out[i] = c
		}
	}
	return out
}

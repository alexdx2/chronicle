package extract

import "strings"

// litMask marks the bytes of s that lie inside quoted literals (quotes
// included). With rust set a single quote opens a literal only when it forms a
// char literal ('x', '\n'); lifetimes such as 'a do not.
func litMask(s string, rust bool) []bool {
	m := make([]bool, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '"' && c != '\'' {
			continue
		}
		if c == '\'' && rust {
			end := -1
			if i+2 < len(s) && s[i+1] != '\\' && s[i+2] == '\'' {
				end = i + 2
			} else if i+3 < len(s) && s[i+1] == '\\' {
				if j := strings.IndexByte(s[i+3:], '\''); j >= 0 && j < 8 {
					end = i + 3 + j
				}
			}
			if end < 0 {
				continue
			}
			for k := i; k <= end; k++ {
				m[k] = true
			}
			i = end
			continue
		}
		j := i + 1
		for j < len(s) && s[j] != c {
			if s[j] == '\\' && j+1 < len(s) {
				j++
			}
			j++
		}
		if j >= len(s) { // unclosed: not a literal
			continue
		}
		for k := i; k <= j; k++ {
			m[k] = true
		}
		i = j
	}
	return m
}

// maskedCode returns s with every literal byte (quotes included) replaced by
// a space, so structure (parentheses, braces, keywords) can be scanned
// without string contents getting in the way. Offsets stay the same.
func maskedCode(s string, rust bool) string {
	m := litMask(s, rust)
	b := []byte(s)
	for i := range b {
		if m[i] && b[i] != '\n' {
			b[i] = ' '
		}
	}
	return string(b)
}

// isTestLike extends FileCtx.IsTest with the Ruby and PHP test file names.
func isTestLike(f *FileCtx) bool {
	if f.IsTest {
		return true
	}
	b := baseName(f.Path)
	return strings.HasSuffix(b, "_spec.rb") || strings.HasSuffix(b, "_test.rb") ||
		strings.HasSuffix(b, "Test.php") || strings.HasSuffix(b, "Tests.php") ||
		strings.HasSuffix(b, "_test.rs")
}

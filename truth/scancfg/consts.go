package scancfg

import "regexp"

var constAssignRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:public\s+|private\s+|static\s+|final\s+|readonly\s+|const\s+|let\s+|var\s+|val\s+|String\s+|string\s+)*([A-Za-z_]\w*)\s*(?::\s*[\w\[\]., ]+)?\s*[:=]\s*(?:"([^"\n]*)"|'([^'\n]*)'|` + "`([^`\\n]*)`" + `)`)

// SameFileConstants maps IDENT -> literal for simple assignments in one file.
func SameFileConstants(body []byte) map[string]string {
	m := map[string]string{}
	for _, g := range constAssignRe.FindAllStringSubmatch(string(body), -1) {
		for _, v := range g[2:] {
			if v != "" {
				m[g[1]] = v
				break
			}
		}
	}
	return m
}

package deps

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

type npm struct{}

var npmSections = []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"}

func (npm) matches(p string) bool { return base(p) == "package.json" }

func (npm) parse(_, rel string, body []byte) (Report, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return Report{}, err
	}
	var r Report
	text := string(body)
	if raw, ok := doc["name"]; ok {
		var name string
		if json.Unmarshal(raw, &name) == nil && name != "" {
			r.Publishes = append(r.Publishes, Package{
				Name: Normalize("npm", name), Ecosystem: "npm", File: rel,
				Line: lineOf(body, strings.Index(text, `"name"`)), Text: `"name"`,
			})
		}
	}
	for _, section := range npmSections {
		raw, ok := doc[section]
		if !ok {
			continue
		}
		var entries map[string]string
		if json.Unmarshal(raw, &entries) != nil {
			continue
		}
		start := strings.Index(text, `"`+section+`"`)
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			version := entries[name]
			d := Dependency{
				Name: Normalize("npm", name), Version: version, Section: section,
				Ecosystem: "npm", File: rel,
				Internal: strings.HasPrefix(version, "workspace:"),
			}
			if start >= 0 {
				if i := strings.Index(text[start:], `"`+name+`"`); i >= 0 {
					off := start + i
					d.Line = lineOf(body, off)
					d.Text = npmEntryRe.FindString(text[off:])
				}
			}
			r.Depends = append(r.Depends, d)
		}
	}
	return r, nil
}

// npmEntryRe matches a `"name": "version"` pair at the start of its input.
var npmEntryRe = regexp.MustCompile(`^"(?:[^"\\]|\\.)*"\s*:\s*"(?:[^"\\]|\\.)*"`)

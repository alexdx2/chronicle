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
				App: npmIsApp(doc),
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

// npmAppDeps are the dependencies that make a package an application.
var npmAppDeps = map[string]bool{
	"@nestjs/core": true, "@nestjs/common": true, "@nestjs/platform-express": true,
	"@nestjs/platform-fastify": true, "express": true, "fastify": true, "koa": true,
	"next": true, "nuxt": true, "@angular/core": true, "astro": true,
	"@remix-run/node": true, "hono": true, "@hapi/hapi": true, "@apollo/server": true,
}

// npmIsApp reports whether a package.json describes something that runs: a
// start or serve script, a server or web-framework dependency, a vite project
// with a dev script, or a private package that exposes no library entry point
// (none of main/exports/types). A bin alone does not make an application: a
// CLI published to a registry is a package.
//
// Known gap: Go modules, JVM projects and setup.cfg get no service node from
// the deps pass (their App is never set); the model declares those services.
func npmIsApp(doc map[string]json.RawMessage) bool {
	var scripts map[string]json.RawMessage
	if raw, ok := doc["scripts"]; ok && json.Unmarshal(raw, &scripts) == nil {
		for _, k := range []string{"start", "serve"} {
			if _, ok := scripts[k]; ok {
				return true
			}
		}
	}
	names := func(section string) map[string]json.RawMessage {
		var entries map[string]json.RawMessage
		if raw, ok := doc[section]; ok && json.Unmarshal(raw, &entries) == nil {
			return entries
		}
		return nil
	}
	for name := range names("dependencies") {
		if npmAppDeps[name] {
			return true
		}
	}
	if _, dev := scripts["dev"]; dev {
		if _, ok := names("devDependencies")["vite"]; ok {
			return true
		}
		if _, ok := names("dependencies")["vite"]; ok {
			return true
		}
	}
	var private bool
	if raw, ok := doc["private"]; ok && json.Unmarshal(raw, &private) == nil && private {
		// A private workspace root is not a runnable package.
		if _, ws := doc["workspaces"]; ws {
			return false
		}
		for _, k := range []string{"main", "exports", "types"} {
			if _, ok := doc[k]; ok {
				return false
			}
		}
		return true
	}
	return false
}

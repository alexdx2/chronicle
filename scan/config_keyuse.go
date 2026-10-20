package scan

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/internal/srccomment"
)

// A client built from a configuration value in any language:
//
//	BillingClient(config.property("billing.url").getString())    // Kotlin
//	CatalogClient(os.getenv("CATALOG_API_URL"))                  // Python
//	CatalogClient::new(std::env::var("CATALOG_API_URL")?)        // Rust
//
// Once the table holds every key and variable name, a source line that names
// one of them as a string literal inside the arguments of a client-shaped call
// is a use of that address, wherever the language keeps its config reads.

var clientCalleeRe = regexp.MustCompile(`\b((?:[A-Za-z_][\w]*\.)*[A-Z][A-Za-z0-9_]*(?:Client|Api|API|Gateway|Connector|Proxy|Sdk|SDK)|Faraday|HTTParty|Net::HTTP)(?:::new|\.new|\.create|\.New)?\s*\(`)

// useKeyLiteral says a table key is specific enough to be recognised as a
// string literal: a dotted or colon path, or an UPPER_SNAKE variable name.
func useKeyLiteral(key string) bool {
	if len(key) < 5 {
		return false
	}
	if strings.ContainsAny(key, ".:") {
		return true
	}
	return strings.ToUpper(key) == key && strings.Contains(key, "_")
}

// parseKeyLiteralUses adds a ConfigUse per client-shaped call whose arguments
// hold a table key as a string literal. The key's own file is never a use.
func parseKeyLiteralUses(t *graph.ConfigHosts, root string, files []string) {
	keys := map[string]string{} // key -> file that defines it
	for _, e := range t.Entries {
		if useKeyLiteral(e.Key) {
			keys[e.Key] = e.File
		}
	}
	if len(keys) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, p := range files {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || len(body) > maxConfigBytes {
			continue
		}
		src := string(srccomment.Strip(p, body))
		// cheap exit: the file must mention at least one key
		mentions := false
		for k := range keys {
			if strings.Contains(src, k) {
				mentions = true
				break
			}
		}
		if !mentions {
			continue
		}
		lines := strings.Split(src, "\n")
		for i, l := range lines {
			cm := clientCalleeRe.FindStringSubmatchIndex(l)
			if cm == nil {
				continue
			}
			window := l[cm[1]:]
			if i+1 < len(lines) && !strings.Contains(window, ")") {
				window += " " + lines[i+1]
			}
			for k, def := range keys {
				if def == p {
					continue
				}
				for _, q := range []string{`"`, `'`} {
					lit := q + k + q
					if !strings.Contains(window, lit) {
						continue
					}
					id := p + "\x00" + k
					if seen[id] {
						continue
					}
					seen[id] = true
					t.AddUse(graph.ConfigUse{File: p, Line: i + 1, Text: lit, Callee: l[cm[2]:cm[3]], Symbol: `Configuration["` + k + `"]`})
				}
			}
		}
	}
}

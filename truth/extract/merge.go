package extract

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// priorities is keyed by the rule-name prefix up to and including the first
// "." (or the whole name when it has no dot).
var priorities = map[string]int{
	// code-level
	"csharp.": 3, "nest.": 3, "express.": 3, "fastapi.": 3, "flask.": 3, "django.": 3,
	"axios.": 3, "fetch.": 3, "requests.": 3, "kafka.": 3, "kafkajs": 3, "kafkajs.": 3, "sqs.": 3,
	"eventbridge.": 3, "boto3.": 3, "python.": 3,
	// contract
	"openapi.": 2, "lock.": 2, "manifest.": 2,
	// infra
	"compose.": 1, "k8s.": 1, "helm.": 1, "terraform.": 1, "cdk.": 1,
	// config
	"appsettings.": 0, "dotenv.": 0,
}

// Priority ranks a rule source: code 3 > contract 2 > infra 1 > config 0.
// Unknown prefixes rank 0.
func Priority(source string) int {
	if v, ok := priorities[source]; ok {
		return v
	}
	key := source
	if i := strings.Index(source, "."); i >= 0 {
		key = source[:i+1]
	}
	return priorities[key]
}

func factPriority(f truth.Fact) int {
	p := 0
	for _, s := range f.Sources {
		if v := Priority(s); v > p {
			p = v
		}
	}
	return p
}

// identityAttrs are the attrs that say what a fact is; a disagreement on them
// is worth a warning. Per-evidence attrs (path, version, importer, section,
// config_key) legitimately differ between sources and never warn.
var identityAttrs = map[string]bool{
	"method": true, "host": true, "var": true, "kind": true, "side": true, "ecosystem": true, "tech": true,
}

var classOrder = func() map[truth.Class]int {
	m := map[truth.Class]int{}
	for i, c := range truth.Classes {
		m[c] = i
	}
	return m
}()

// Merge folds facts with identical Class+Key into one: Sources and Derived are
// unioned, Primary and attrs come from the highest-priority fact (first seen
// wins ties). Output is sorted by class order, then key.
func Merge(facts []truth.Fact) ([]truth.Fact, []string) {
	groups := map[string][]truth.Fact{}
	var order []string
	for _, f := range facts {
		k := string(f.Class) + "\x00" + f.Key
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], f)
	}
	var out []truth.Fact
	var warns []string
	warned := map[string]bool{}
	for _, k := range order {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool { return factPriority(g[i]) > factPriority(g[j]) })
		best := g[0]
		m := truth.Fact{Class: best.Class, Key: best.Key, Primary: best.Primary}
		if len(best.Attrs) > 0 {
			m.Attrs = map[string]string{}
			for a, v := range best.Attrs {
				m.Attrs[a] = v
			}
		}
		srcSet := map[string]bool{}
		type ek struct {
			f string
			l int
		}
		seen := map[ek]bool{{m.Primary.File, m.Primary.Line}: true}
		addEv := func(e truth.Evidence) {
			if !seen[ek{e.File, e.Line}] {
				seen[ek{e.File, e.Line}] = true
				m.Derived = append(m.Derived, e)
			}
		}
		for i, f := range g {
			for _, s := range f.Sources {
				srcSet[s] = true
			}
			if i > 0 && f.Primary.File != "" {
				addEv(f.Primary)
			}
			for _, e := range f.Derived {
				addEv(e)
			}
			if i > 0 {
				names := make([]string, 0, len(f.Attrs))
				for a := range f.Attrs {
					names = append(names, a)
				}
				sort.Strings(names)
				for _, a := range names {
					v := f.Attrs[a]
					if kept, ok := m.Attrs[a]; !ok {
						if m.Attrs == nil {
							m.Attrs = map[string]string{}
						}
						m.Attrs[a] = v
					} else if kept != v && identityAttrs[a] {
						w := fmt.Sprintf("conflict %s %s %s: %s vs %s (kept %s)", m.Class, m.Key, a, kept, v, kept)
						if !warned[w] {
							warned[w] = true
							warns = append(warns, w)
						}
					}
				}
			}
		}
		for s := range srcSet {
			m.Sources = append(m.Sources, s)
		}
		sort.Strings(m.Sources)
		sort.SliceStable(m.Derived, func(i, j int) bool {
			if m.Derived[i].File != m.Derived[j].File {
				return m.Derived[i].File < m.Derived[j].File
			}
			return m.Derived[i].Line < m.Derived[j].Line
		})
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return classOrder[out[i].Class] < classOrder[out[j].Class]
		}
		return out[i].Key < out[j].Key
	})
	return out, warns
}

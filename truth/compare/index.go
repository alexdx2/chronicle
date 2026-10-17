// Package compare judges a truth Report against a Chronicle graph snapshot.
package compare

import (
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth"
)

type epNode struct {
	key, name, method, path string
}

type chNode struct {
	key, name, canon string
	produce, consume bool
}

type pkgNode struct {
	key, name, eco string
	incoming       bool // has an incoming DEPENDS_ON edge
	own            bool // target of a PUBLISHES edge
}

type svcNode struct {
	key, name, typ string
	names          map[string]bool // CanonName of name and every alias
}

type httpNode struct {
	key, name string
	external  bool            // external_system node
	hosts     map[string]bool // CanonHost forms
	names     map[string]bool // CanonName forms
}

// index holds the active part of a snapshot, organised per fact class.
type index struct {
	endpoints []epNode
	channels  []chNode
	pkgs      []pkgNode
	pkgByDep  map[string][]int
	pkgByName map[string][]int
	services  []svcNode
	http      []httpNode
	coverage  map[string]bool
}

func isActiveNode(n snapshot.Node) bool { return n.Status == "" || n.Status == "active" }

// splitKey returns the parts of "layer:type:domain:rest".
func splitKey(key string) (layer, typ, domain, rest string, ok bool) {
	p := strings.SplitN(key, ":", 4)
	if len(p) < 4 {
		return "", "", "", "", false
	}
	return p[0], p[1], p[2], p[3], true
}

func buildIndex(snap *snapshot.Snapshot) *index {
	ix := &index{pkgByDep: map[string][]int{}, pkgByName: map[string][]int{}, coverage: map[string]bool{}}
	if snap == nil {
		return ix
	}
	for _, c := range snap.Coverage {
		ix.coverage[strings.TrimPrefix(c.Path, "./")] = true
	}
	nodes := map[string]snapshot.Node{}
	for _, n := range snap.Nodes {
		if isActiveNode(n) {
			nodes[n.ID] = n
		}
	}
	// Edge facts. Edge sources are not domain-checked: snapshots are single-repo.
	pub, con := map[string]bool{}, map[string]bool{}
	dependsIn, publishesIn := map[string]bool{}, map[string]bool{}
	callTargets := map[string]bool{}
	for _, e := range snap.Edges {
		if !e.Active {
			continue
		}
		switch e.Type {
		case "PUBLISHES_TOPIC":
			pub[e.To] = true
		case "CONSUMES_TOPIC":
			con[e.From], con[e.To] = true, true
		case "DEPENDS_ON":
			dependsIn[e.To] = true
		case "PUBLISHES":
			publishesIn[e.To] = true
		case "CALLS_SERVICE", "CALLS_ENDPOINT":
			callTargets[e.To] = true
		}
	}
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := nodes[id]
		_, typ, _, rest, ok := splitKey(id)
		if !ok {
			continue
		}
		if n.Type != "" {
			typ = n.Type
		}
		switch typ {
		case "endpoint":
			i := strings.Index(rest, ":")
			if i <= 0 {
				continue
			}
			ix.endpoints = append(ix.endpoints, epNode{key: id, name: n.Name, method: strings.ToUpper(rest[:i]), path: truth.CanonPath(rest[i+1:])})
		case "topic", "queue":
			name := n.Name
			if name == "" {
				name = rest
			}
			ix.channels = append(ix.channels, chNode{key: id, name: name, canon: truth.CanonName(name), produce: pub[id], consume: con[id]})
		case "package":
			eco, name := "", rest
			if i := strings.Index(rest, ":"); i >= 0 {
				eco, name = rest[:i], rest[i+1:]
			}
			idx := len(ix.pkgs)
			ix.pkgs = append(ix.pkgs, pkgNode{key: id, name: name, eco: eco, incoming: dependsIn[id], own: publishesIn[id]})
			ix.pkgByDep[truth.CanonDep(eco, name)] = append(ix.pkgByDep[truth.CanonDep(eco, name)], idx)
			ix.pkgByName[truth.CanonName(name)] = append(ix.pkgByName[truth.CanonName(name)], idx)
		}
		switch typ {
		case "service", "external_system", "infrastructure":
			names := map[string]bool{}
			add := func(s string) {
				if c := truth.CanonName(s); c != "" {
					names[c] = true
				}
			}
			add(n.Name)
			for _, a := range n.Aliases {
				add(a.Alias)
			}
			ix.services = append(ix.services, svcNode{key: id, name: n.Name, typ: typ, names: names})
		}
		// CALLS_ENDPOINT targets are endpoint nodes judged under endpoints, so
		// the http index holds service/external_system nodes only.
		if typ == "external_system" || (typ == "service" && callTargets[id]) {
			h := httpNode{key: id, name: n.Name, external: typ == "external_system", hosts: map[string]bool{}, names: map[string]bool{}}
			addName := func(s string) {
				if c := truth.CanonName(s); c != "" {
					h.names[c] = true
				}
				if c := truth.CanonHost(s); c != "" {
					h.hosts[c] = true
				}
			}
			addName(n.Name)
			for _, a := range n.Aliases {
				addName(a.Alias)
			}
			ix.http = append(ix.http, h)
		}
	}
	return ix
}

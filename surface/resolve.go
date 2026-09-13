package surface

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// Resolver turns the names a surface extract speaks (a Prisma "Model.field",
// a GraphQL mutation name, a Next.js route) into node keys of the graph a scan
// already built. Every resolution is verified against the store: a name that
// resolves to nothing is an unresolved name, never an invented node.
//
// A Resolver is scoped to one import. Plan primes its endpoint index once at
// the start; reusing a Resolver across imports would serve a stale index.
type Resolver struct {
	Store  *store.Store
	Domain string

	// endpointsByName maps the flattened name of every endpoint node in the
	// domain to the keys that carry it. Built once per Plan — a miss used to
	// re-list every endpoint in the domain, once per unresolved mutation.
	endpointsByName map[string][]string
}

// FieldKey maps "Shop.quietFromHour" to "data:field:<domain>:shop/quiet-from-hour"
// — both halves through validate.NormalizeName, which is exactly how the Prisma
// extractor spells the key. The node must exist.
func (r *Resolver) FieldKey(ref string) (string, error) {
	model, field, ok := splitRef(ref)
	if !ok {
		return "", fmt.Errorf("field ref %q is not Model.field", ref)
	}
	key := fmt.Sprintf("data:field:%s:%s/%s", r.Domain,
		validate.NormalizeName(model), validate.NormalizeName(field))
	if _, err := r.Store.GetNodeByKey(key); err != nil {
		return "", fmt.Errorf("field %q: no node %s in the graph — scan the data layer first", ref, key)
	}
	return key, nil
}

// MutationKey maps a GraphQL mutation name to its endpoint node. The canonical
// spelling is "contract:endpoint:<domain>:mutation:/<lowercased name>"; when
// that key is absent the graph may still hold the same operation under another
// route shape, so we fall back to matching endpoint NAMES with case and
// punctuation dropped. Exactly one hit resolves; several is an error that
// lists them rather than guessing.
func (r *Resolver) MutationKey(name string) (string, error) {
	key, _, err := r.resolveMutation(name)
	return key, err
}

// resolveMutation is MutationKey plus the near misses, so an unresolved name
// can carry its candidates instead of only its failure.
func (r *Resolver) resolveMutation(name string) (key string, candidates []string, err error) {
	if name == "" {
		return "", nil, fmt.Errorf("mutation name is empty")
	}
	canonical := fmt.Sprintf("contract:endpoint:%s:mutation:/%s", r.Domain, strings.ToLower(name))
	if _, e := r.Store.GetNodeByKey(canonical); e == nil {
		return canonical, nil, nil
	}

	index, e := r.EndpointIndex()
	if e != nil {
		return "", nil, fmt.Errorf("mutation %q: %w", name, e)
	}
	hits := append([]string(nil), index[flatten(name)]...)
	sort.Strings(hits)
	switch len(hits) {
	case 1:
		return hits[0], nil, nil
	case 0:
		return "", nil, fmt.Errorf("mutation %q: no endpoint node (tried %s and a name match)", name, canonical)
	default:
		return "", hits, fmt.Errorf("mutation %q: ambiguous — %d endpoints match: %s", name, len(hits), strings.Join(hits, ", "))
	}
}

// EndpointIndex returns (building once) the flattened-name index of every
// active endpoint node in the domain.
func (r *Resolver) EndpointIndex() (map[string][]string, error) {
	if r.endpointsByName != nil {
		return r.endpointsByName, nil
	}
	nodes, err := r.Store.ListNodes(store.NodeFilter{NodeType: "endpoint", Domain: r.Domain, Status: "active"})
	if err != nil {
		return nil, err
	}
	index := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		if flat := flatten(n.Name); flat != "" {
			index[flat] = append(index[flat], n.NodeKey)
		}
	}
	r.endpointsByName = index
	return index, nil
}

// primeEndpointIndex rebuilds the index so one Plan sees one snapshot of the
// graph, however many names it has to resolve.
func (r *Resolver) primeEndpointIndex() {
	r.endpointsByName = nil
	_, _ = r.EndpointIndex()
}

// RouteKey maps a page route to the GET endpoint that serves it.
func (r *Resolver) RouteKey(route string) (string, error) {
	if route == "" {
		return "", fmt.Errorf("route is empty")
	}
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	key := strings.ToLower(fmt.Sprintf("contract:endpoint:%s:get:%s", r.Domain, route))
	if _, err := r.Store.GetNodeByKey(key); err != nil {
		return "", fmt.Errorf("route %q: no node %s in the graph", route, key)
	}
	return key, nil
}

// splitRef splits "Model.field" on its LAST dot, so a namespaced model still
// yields a field.
func splitRef(ref string) (model, field string, ok bool) {
	i := strings.LastIndex(ref, ".")
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

// flatten lowercases and drops every non-alphanumeric rune, so
// "saveVoiceConfig", "save_voice_config" and "mutation:/save-voice-config"
// only differ where the letters do.
func flatten(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

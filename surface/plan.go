package surface

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/validate"
)

// ErrUnresolved is the refusal a caller can answer with AllowUnresolved: the
// extract named something the graph cannot confirm, so nothing was planned.
var ErrUnresolved = errors.New("unresolved names")

// ExtractorID is the extractor every surface-extract evidence row is stamped
// with. It names who made the claim: the product's own surface generator.
const ExtractorID = "okeep-surface"

// Options configures a plan.
type Options struct {
	Domain          string
	AllowUnresolved bool
}

// Unresolved is one name the extract used that the graph could not confirm:
// a mutation with no endpoint, a route with no page, a field with no column.
// It is never silently dropped — either it fails the plan, or it is reported.
type Unresolved struct {
	Kind       string   // "mutation" | "route" | "field" | "edge_endpoint" | "edge_kind"
	Name       string   // the name as the extract spelled it
	Owner      string   // the ui node key that used it
	Candidates []string // near misses, when the resolver found several
	Err        error
}

// MarshalJSON keeps Err readable in a tool result (an error marshals to {}).
func (u Unresolved) MarshalJSON() ([]byte, error) {
	msg := ""
	if u.Err != nil {
		msg = u.Err.Error()
	}
	return json.Marshal(struct {
		Kind       string   `json:"kind"`
		Name       string   `json:"name"`
		Owner      string   `json:"owner,omitempty"`
		Candidates []string `json:"candidates,omitempty"`
		Error      string   `json:"error,omitempty"`
	}{u.Kind, u.Name, u.Owner, u.Candidates, msg})
}

func (u Unresolved) String() string {
	s := fmt.Sprintf("%s %q", u.Kind, u.Name)
	if u.Owner != "" {
		s += " (used by " + u.Owner + ")"
	}
	if u.Err != nil {
		s += ": " + u.Err.Error()
	}
	return s
}

// NodeKey builds the ui node key for one address. Product nodes are keyed by
// the product name, everything else by its dotted address. The key goes
// through validate.NormalizeNodeKey so it is the same fixed point the store
// writes — the closed-world diff compares these against stored keys.
func NodeKey(domain, kind, address string) string {
	key := fmt.Sprintf("ui:%s:%s:%s", kind, domain, address)
	if norm, err := validate.NormalizeNodeKey(key); err == nil {
		return norm
	}
	return key
}

// ka identifies a ui node the way the extract does: its kind and its address
// together. Load has already refused two ui nodes sharing an address, so this
// is a total key, not a best effort.
type ka struct{ Kind, Address string }

// modelFieldRef matches the decision keys that name a data field —
// "Shop.quietFromHour", "ShopWorkingHour.dayOfWeek". Everything else in a
// decisions file ("models", "columns", "covered", a dotted ui address) is a
// human's own bookkeeping and must not be looked up as a field.
var modelFieldRef = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*\.[a-zA-Z0-9_]+$`)

// Plan turns a loaded extract into an ImportPayload for graph.ImportAll,
// plus the ui node keys the file claims (the closed-world set) and everything
// it named that the graph could not confirm.
//
// It writes nothing. Field entries in the file are references, not nodes: a
// "field" kind resolves to the data:field node a scan already made, and an
// unknown one is an unresolved name like any other.
func Plan(f *File, r *Resolver, o Options) (graph.ImportPayload, []string, []Unresolved, error) {
	var payload graph.ImportPayload
	var uiKeys []string
	var unresolved []Unresolved

	domain := o.Domain
	if domain == "" {
		domain = r.Domain
	}
	version := strconv.Itoa(f.SchemaVersion)

	note := func(u Unresolved) { unresolved = append(unresolved, u) }

	// One snapshot of the graph's endpoint names for this whole plan.
	r.primeEndpointIndex()

	// --- ui nodes ------------------------------------------------------
	byKA := map[ka]Node{}        // (kind, address) -> node
	keyOf := map[ka]string{}     // (kind, address) -> ui node key
	atAddress := map[string]ka{} // address -> the one ui node there (parenting)
	byID := map[string]Node{}    // the file's own ids (edge endpoints)
	productAddress := f.Product
	// The product node carries no address of its own — the product name is
	// its address, and the root every top-level screen hangs off.
	addrOf := func(n Node) string {
		if n.Kind == "product" {
			return productAddress
		}
		return n.Address
	}
	idOf := func(n Node) ka { return ka{n.Kind, addrOf(n)} }

	for _, n := range f.Nodes {
		byID[n.ID] = n
		if !n.IsUI() {
			continue
		}
		id := idOf(n)
		byKA[id] = n
		keyOf[id] = NodeKey(domain, n.Kind, id.Address)
		atAddress[id.Address] = id
	}

	// The product node names the product rather than a place in the source and
	// real extracts ship it with no evidence, so its own evidence is the
	// extract file itself.
	fileOf := func(n Node) string {
		if file := n.FirstFile(); file != "" {
			return file
		}
		if n.Kind == "product" {
			return f.Path
		}
		return ""
	}

	for _, n := range f.Nodes {
		if !n.IsUI() {
			continue
		}
		id := idOf(n)
		key := keyOf[id]
		uiKeys = append(uiKeys, key)

		md, _ := json.Marshal(map[string]string{
			"product": f.Product,
			"address": id.Address,
			"kind":    n.Kind,
		})
		payload.Nodes = append(payload.Nodes, graph.ImportNode{
			NodeKey:       key,
			Layer:         "ui",
			NodeType:      n.Kind,
			DomainKey:     domain,
			Name:          n.Label,
			QualifiedName: id.Address,
			FilePath:      fileOf(n),
			Metadata:      graph.FlexString(md),
		})
		payload.Evidence = append(payload.Evidence, graph.ImportEvidence{
			TargetKind:       "node",
			NodeKey:          key,
			SourceKind:       "surface_extract",
			FilePath:         fileOf(n),
			LineStart:        graph.FlexInt(n.FirstLine()),
			ExtractorID:      ExtractorID,
			ExtractorVersion: version,
			CommitSHA:        f.Commit,
		})
	}

	// addEdge records one edge plus its evidence, anchored at the source
	// node's own file:line.
	addEdge := func(from Node, fromKey, toKey, edgeType, derivation string) {
		edgeKey := validate.BuildEdgeKey(fromKey, toKey, edgeType)
		payload.Edges = append(payload.Edges, graph.ImportEdge{
			EdgeKey:        edgeKey,
			FromNodeKey:    fromKey,
			ToNodeKey:      toKey,
			EdgeType:       edgeType,
			DerivationKind: derivation,
			FromLayer:      "ui",
			ToLayer:        layerOfKey(toKey),
		})
		payload.Evidence = append(payload.Evidence, graph.ImportEvidence{
			TargetKind:       "edge",
			EdgeKey:          edgeKey,
			SourceKind:       "surface_extract",
			FilePath:         fileOf(from),
			LineStart:        graph.FlexInt(from.FirstLine()),
			ExtractorID:      ExtractorID,
			ExtractorVersion: version,
			CommitSHA:        f.Commit,
		})
	}

	// --- CONTAINS: the address IS the tree -----------------------------
	for _, n := range f.Nodes {
		if !n.IsUI() || n.Kind == "product" {
			continue
		}
		parent, ok := parentOf(n.Address, atAddress, productAddress)
		if !ok {
			continue
		}
		addEdge(byKA[parent], keyOf[parent], keyOf[idOf(n)], "CONTAINS", "hard")
	}

	// --- SERVED_BY: a screen's route is an endpoint --------------------
	for _, n := range f.Nodes {
		if n.Kind != "screen" || n.Route == "" {
			continue
		}
		key, err := r.RouteKey(n.Route)
		if err != nil {
			note(Unresolved{Kind: "route", Name: n.Route, Owner: keyOf[idOf(n)], Err: err})
			continue
		}
		addEdge(n, keyOf[idOf(n)], key, "SERVED_BY", "linked")
	}

	// --- WRITES_VIA: a control writes through named operations ---------
	for _, n := range f.Nodes {
		if !n.IsUI() {
			continue
		}
		for _, via := range n.Via {
			key, candidates, err := r.resolveMutation(via)
			if err != nil {
				note(Unresolved{Kind: "mutation", Name: via, Owner: keyOf[idOf(n)], Candidates: candidates, Err: err})
				continue
			}
			addEdge(n, keyOf[idOf(n)], key, "WRITES_VIA", "linked")
		}
	}

	// --- WRITES_FIELD: the "edits" edges -------------------------------
	for _, e := range f.Edges {
		from, okFrom := byID[e.From]
		to, okTo := byID[e.To]
		if !okFrom || !okTo {
			note(Unresolved{Kind: "edge_endpoint", Name: e.From + " -> " + e.To,
				Err: fmt.Errorf("edge references a node id absent from the file")})
			continue
		}
		if e.Kind != "edits" {
			note(Unresolved{Kind: "edge_kind", Name: e.Kind, Owner: keyOf[idOf(from)],
				Err: fmt.Errorf("unknown edge kind %q (known: edits)", e.Kind)})
			continue
		}
		fieldKey, err := r.FieldKey(to.Ref)
		if err != nil {
			note(Unresolved{Kind: "field", Name: to.Ref, Owner: keyOf[idOf(from)], Err: err})
			continue
		}
		addEdge(from, keyOf[idOf(from)], fieldKey, "WRITES_FIELD", "linked")
	}

	// --- decisions: declared evidence on the thing they govern ---------
	// A decision key is either a ui address this file knows or a Model.field
	// the graph knows. Anything else (an extract's "homes" group keys plain
	// words like "models" or "columns") is not a decision about a node and is
	// ignored — a human document must not be able to fail an import.
	for _, group := range sortedKeys(f.Decisions) {
		for _, dkey := range sortedKeys(f.Decisions[group]) {
			d := f.Decisions[group][dkey]
			target := ""
			if id, ok := atAddress[dkey]; ok {
				target = keyOf[id]
			} else if modelFieldRef.MatchString(dkey) {
				if k, err := r.FieldKey(dkey); err == nil {
					target = k
				}
			}
			if target == "" {
				continue
			}
			bare := d
			bare.Evidence = nil
			assertion, err := json.Marshal(bare)
			if err != nil {
				continue
			}
			ev := graph.ImportEvidence{
				TargetKind:       "node",
				NodeKey:          target,
				SourceKind:       "declared",
				ExtractorID:      ExtractorID,
				ExtractorVersion: version,
				CommitSHA:        f.Commit,
				Assertion:        string(assertion),
				AssertionKind:    "decision",
				AssertionVersion: version,
				Metadata:         fmt.Sprintf(`{"group":%q}`, group),
			}
			if d.Evidence != nil {
				ev.FilePath = d.Evidence.File
				ev.LineStart = graph.FlexInt(d.Evidence.Line)
			}
			payload.Evidence = append(payload.Evidence, ev)
		}
	}

	if len(unresolved) > 0 && !o.AllowUnresolved {
		return payload, uiKeys, unresolved, fmt.Errorf(
			"surface: %w — %d: %s (pass allow_unresolved to import what does resolve)",
			ErrUnresolved, len(unresolved), joinUnresolved(unresolved))
	}
	return payload, uiKeys, unresolved, nil
}

// parentOf finds the containing ui node for a dotted address: the longest
// known prefix, falling back to the product. "panel.ustawienia" under a file
// with no "panel" node hangs off the product; "a.b.c" hangs off "a.b" when
// that exists, else "a", else the product.
func parentOf(addr string, atAddress map[string]ka, productAddress string) (ka, bool) {
	rest := addr
	for {
		i := strings.LastIndex(rest, ".")
		if i <= 0 {
			break
		}
		rest = rest[:i]
		if id, ok := atAddress[rest]; ok && rest != addr {
			return id, true
		}
	}
	if id, ok := atAddress[productAddress]; ok && productAddress != addr {
		return id, true
	}
	return ka{}, false
}

// layerOfKey reads the layer off a node key ("data:field:mini:x" → "data").
func layerOfKey(key string) string {
	if i := strings.Index(key, ":"); i > 0 {
		return key[:i]
	}
	return ""
}

func joinUnresolved(us []Unresolved) string {
	parts := make([]string, 0, len(us))
	for _, u := range us {
		parts = append(parts, u.String())
	}
	return strings.Join(parts, "; ")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

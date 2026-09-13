// Package surface imports a product's own surface extract — the screens,
// panels and controls a user actually meets — into Chronicle's ui layer.
//
// The extract is produced by the product (okeep's `surface.json`), not by a
// Chronicle scan: only the product knows which React tree is a settings panel
// and which mutation a toggle writes through. Chronicle's job here is to take
// that claim, resolve its names against the graph a scan already built
// (data:field nodes, contract:endpoint nodes), and record the result as
// evidence-backed ui nodes anchored to the commit the extract was made at.
//
// The import is closed-world per product: a ui node that this product's file
// no longer mentions is tombstoned. It never touches the code layer.
package surface

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SupportedSchemaVersions lists the surface.json schema versions Load accepts.
var SupportedSchemaVersions = []int{1}

// File is one product's surface extract.
type File struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Product       string                         `json:"product"`
	Repo          string                         `json:"repo"`
	Commit        string                         `json:"commit"`
	Nodes         []Node                         `json:"nodes"`
	Edges         []Edge                         `json:"edges"`
	Decisions     map[string]map[string]Decision `json:"decisions"` // group -> address/Model.field -> decision
	ContentHash   string                         `json:"-"`         // sha256 of the raw bytes, hex
	Path          string                         `json:"-"`         // where it was read from
}

// Node is one entry of the extract's node list. The four ui kinds
// (product, screen, panel, control) become ui-layer nodes; "field" entries are
// REFERENCES to data:field nodes a scan already made, never new nodes.
type Node struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Address  string   `json:"address"`
	Label    string   `json:"label"`
	Route    string   `json:"route,omitempty"`
	Ref      string   `json:"ref,omitempty"` // field: Model.field
	Via      []string `json:"via,omitempty"`
	Evidence []Loc    `json:"evidence"`
}

// Loc is a file:line pointer into the product's own source.
type Loc struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// Edge is one relation the extract claims. Today the only kind is "edits"
// (a control edits a data field).
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// Decision is a human ruling recorded beside the thing it governs.
type Decision struct {
	Verdict     string `json:"verdict"`
	Placement   string `json:"placement"`
	Owner       string `json:"owner"`
	DerivedFrom string `json:"derivedFrom,omitempty"`
	Escape      string `json:"escape,omitempty"`
	Note        string `json:"note,omitempty"`
	Evidence    *Loc   `json:"evidence,omitempty"`
}

// uiKinds are the node kinds that become ui-layer nodes.
var uiKinds = map[string]bool{"product": true, "screen": true, "panel": true, "control": true}

// IsUI reports whether this node becomes a ui-layer node.
func (n Node) IsUI() bool { return uiKinds[n.Kind] }

// FirstFile returns the first evidence file, or "" when the node carries none.
func (n Node) FirstFile() string {
	if len(n.Evidence) == 0 {
		return ""
	}
	return n.Evidence[0].File
}

// FirstLine returns the first evidence line, or 0.
func (n Node) FirstLine() int {
	if len(n.Evidence) == 0 {
		return 0
	}
	return n.Evidence[0].Line
}

// UINodes returns only the nodes that become ui-layer nodes, in file order.
func (f *File) UINodes() []Node {
	var out []Node
	for _, n := range f.Nodes {
		if n.IsUI() {
			out = append(out, n)
		}
	}
	return out
}

// Header is a short description of the extract, for error messages.
func (f *File) Header() string {
	return fmt.Sprintf("schemaVersion=%d product=%s repo=%s commit=%s", f.SchemaVersion, f.Product, f.Repo, f.Commit)
}

// rawFile mirrors File but keeps decisions unparsed: real extracts carry
// groups whose values are not decisions at all ("homes" maps a plain word to
// a map of model → placement). Those must be skipped, never fail the import.
type rawFile struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Product       string                     `json:"product"`
	Repo          string                     `json:"repo"`
	Commit        string                     `json:"commit"`
	Nodes         []Node                     `json:"nodes"`
	Edges         []Edge                     `json:"edges"`
	Decisions     map[string]json.RawMessage `json:"decisions"`
}

// Load reads a surface extract, checks its schema version, and records the
// sha256 of the exact bytes it read — the content hash that makes a re-import
// of the same commit either a no-op or a refusal.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("surface: reading %s: %w", path, err)
	}

	var raw rawFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("surface: parsing %s: %w", path, err)
	}
	if !supported(raw.SchemaVersion) {
		return nil, fmt.Errorf("surface: unsupported schemaVersion %d (supported: %s)",
			raw.SchemaVersion, joinInts(SupportedSchemaVersions))
	}
	if raw.Product == "" {
		return nil, fmt.Errorf("surface: %s has no product", path)
	}
	if raw.Commit == "" {
		return nil, fmt.Errorf("surface: %s has no commit — knowledge must be named by the commit it describes", path)
	}
	if len(raw.Nodes) == 0 {
		return nil, fmt.Errorf("surface: %s has no nodes", path)
	}

	sum := sha256.Sum256(data)
	f := &File{
		SchemaVersion: raw.SchemaVersion,
		Product:       raw.Product,
		Repo:          raw.Repo,
		Commit:        raw.Commit,
		Nodes:         raw.Nodes,
		Edges:         raw.Edges,
		Decisions:     parseDecisions(raw.Decisions),
		ContentHash:   hex.EncodeToString(sum[:]),
		Path:          path,
	}

	for i, n := range f.Nodes {
		switch {
		case n.Kind == "field":
			if n.Ref == "" {
				return nil, fmt.Errorf("surface: node[%d] %q is a field with no ref", i, n.ID)
			}
		case n.IsUI():
			if n.Kind != "product" && n.Address == "" {
				return nil, fmt.Errorf("surface: node[%d] %q (%s) has no address", i, n.ID, n.Kind)
			}
		}
	}
	return f, nil
}

// parseDecisions keeps every group whose entries look like decisions and
// silently drops the rest — an extract's decision file is a human document and
// grows groups Chronicle has no opinion about.
func parseDecisions(raw map[string]json.RawMessage) map[string]map[string]Decision {
	out := map[string]map[string]Decision{}
	for group, body := range raw {
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(body, &entries); err != nil {
			continue // not an object of entries (an array, a scalar) — not decisions
		}
		parsed := map[string]Decision{}
		for key, entry := range entries {
			var d Decision
			if err := json.Unmarshal(entry, &d); err != nil {
				continue
			}
			parsed[key] = d
		}
		if len(parsed) > 0 {
			out[group] = parsed
		}
	}
	return out
}

func supported(v int) bool {
	for _, s := range SupportedSchemaVersions {
		if s == v {
			return true
		}
	}
	return false
}

func joinInts(vs []int) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ", ")
}

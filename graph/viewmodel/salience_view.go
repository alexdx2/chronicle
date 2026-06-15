package viewmodel

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// saliencePolicyFor resolves the salience policy for a store: a project-local
// chronicle.types.yaml in the store dir wins if present, otherwise the built-in
// defaults. Returns a non-nil policy; on any load error it falls back to an
// empty policy so diagram building never fails on salience alone.
func saliencePolicyFor(st *store.Store) *registry.SaliencePolicy {
	typesPath := filepath.Join(st.Dir(), "chronicle.types.yaml")
	var reg *registry.Registry
	var err error
	if _, statErr := os.Stat(typesPath); statErr == nil {
		reg, err = registry.LoadFile(typesPath)
	} else {
		reg, err = registry.LoadDefaults()
	}
	if err != nil || reg == nil {
		return &registry.SaliencePolicy{}
	}
	return reg.SaliencePolicy()
}

// nodeRole extracts the semantic role (if any) stored in a node's metadata.
// Role population is a downstream (scan) concern; until then this returns "".
func nodeRole(n *store.NodeRow) string {
	if n.Metadata == "" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(n.Metadata), &m) != nil {
		return ""
	}
	if r, ok := m["role"].(string); ok {
		return r
	}
	return ""
}

package viewmodel

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/graph/salience"
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

// resolveRolesByNode batch-loads role_classification evidence (one query) and
// resolves the winning role per node via salience.ResolveRole. This is the
// evidence-backed, multi-claim path; the result is the node's effective role.
// Returns nil when there are no role claims (callers fall back to metadata).
func resolveRolesByNode(st *store.Store) map[int64]string {
	evs, err := st.ListEvidenceBySourceKind("role_classification")
	if err != nil || len(evs) == 0 {
		return nil
	}
	claims := make(map[int64][]salience.RoleClaim)
	for i := range evs {
		e := &evs[i]
		if e.NodeID == 0 || e.EvidenceStatus == "stale" || e.EvidencePolarity == "negative" {
			continue
		}
		role, reason := parseRoleClaim(e.Assertion, e.Metadata)
		if role == "" {
			continue
		}
		claims[e.NodeID] = append(claims[e.NodeID], salience.RoleClaim{Role: role, Confidence: e.Confidence, Reason: reason})
	}
	if len(claims) == 0 {
		return nil
	}
	out := make(map[int64]string, len(claims))
	for nid, cs := range claims {
		if w, ok := salience.ResolveRole(cs); ok {
			out[nid] = w.Role
		}
	}
	return out
}

// parseRoleClaim extracts {role, reason} from an evidence assertion (preferred)
// or metadata JSON blob.
func parseRoleClaim(assertion, metadata string) (string, string) {
	for _, blob := range []string{assertion, metadata} {
		if blob == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(blob), &m) != nil {
			continue
		}
		role, _ := m["role"].(string)
		if role == "" {
			continue
		}
		reason, _ := m["role_reason"].(string)
		if reason == "" {
			reason, _ = m["reason"].(string)
		}
		return role, reason
	}
	return "", ""
}

// effectiveRole returns the node's role, preferring the evidence-resolved
// winning role (roleByNode) over the role cached in node metadata.
func effectiveRole(n *store.NodeRow, roleByNode map[int64]string) string {
	if roleByNode != nil {
		if r := roleByNode[n.NodeID]; r != "" {
			return r
		}
	}
	return nodeRole(n)
}

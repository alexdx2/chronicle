package viewmodel

import (
	"encoding/json"
	"fmt"
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

// nodeRoleClaim extracts the semantic role (if any) stored in a node's
// metadata, with the extractor confidence recorded alongside it. Confidence 0
// means "not recorded" (manually set role) — the engine treats that as trusted.
func nodeRoleClaim(n *store.NodeRow) (string, float64) {
	if n.Metadata == "" {
		return "", 0
	}
	var m map[string]any
	if json.Unmarshal([]byte(n.Metadata), &m) != nil {
		return "", 0
	}
	role, ok := m["role"].(string)
	if !ok {
		return "", 0
	}
	conf, _ := m["role_confidence"].(float64)
	return role, conf
}

// resolveRolesByNode batch-loads role_classification evidence (one query) and
// resolves the winning claim per node via salience.ResolveRole. This is the
// evidence-backed, multi-claim path; the result is the node's effective role
// plus its claim confidence (feeds the demote gate).
// Returns nil when there are no role claims (callers fall back to metadata).
func resolveRolesByNode(st *store.Store) map[int64]salience.RoleClaim {
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
	out := make(map[int64]salience.RoleClaim, len(claims))
	for nid, cs := range claims {
		if w, ok := salience.ResolveRole(cs); ok {
			out[nid] = w
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

// boundaryCrossings returns the IDs of nodes with an active edge to a code
// node owned by a DIFFERENT service — the deterministic topology signal for
// bounded salience promotion (spec §4.1). Degree is measured in the supplied
// (view-scoped) edge set, so a globally popular node does not surface in
// every view (spec open question #2: degree in the view subgraph).
func boundaryCrossings(edges []store.EdgeRow, owned map[int64]*store.NodeRow) map[int64]bool {
	out := make(map[int64]bool)
	for i := range edges {
		e := &edges[i]
		f, t := owned[e.FromNodeID], owned[e.ToNodeID]
		if f == nil || t == nil || f.NodeID == t.NodeID {
			continue
		}
		out[e.FromNodeID] = true
		out[e.ToNodeID] = true
	}
	return out
}

// salienceOverride converts a validated ViewSpec override into the engine's
// Override. Callers must have validated the values (validateSalienceOverrides).
func salienceOverride(ov SalienceOverrideSpec) *salience.Override {
	var out salience.Override
	if t, ok := salience.ParseTier(ov.Tier); ok {
		out.Tier = &t
	}
	if m, ok := salience.ParseRenderMode(ov.RenderMode); ok {
		out.RenderMode = &m
	}
	if out.Tier == nil && out.RenderMode == nil {
		return nil
	}
	return &out
}

// validateSalienceOverrides enforces the closed vocabularies on user overrides.
func validateSalienceOverrides(specs map[string]SalienceOverrideSpec) error {
	for key, ov := range specs {
		if ov.Tier != "" {
			if _, ok := salience.ParseTier(ov.Tier); !ok {
				return fmt.Errorf("salience_overrides[%s]: invalid tier %q (want primary|secondary|detail)", key, ov.Tier)
			}
		}
		if ov.RenderMode != "" {
			if _, ok := salience.ParseRenderMode(ov.RenderMode); !ok {
				return fmt.Errorf("salience_overrides[%s]: invalid render_mode %q", key, ov.RenderMode)
			}
		}
	}
	return nil
}

// effectiveRoleClaim returns the node's role and claim confidence, preferring
// the evidence-resolved winning claim (roleByNode) over metadata.
func effectiveRoleClaim(n *store.NodeRow, roleByNode map[int64]salience.RoleClaim) (string, float64) {
	if roleByNode != nil {
		if w, ok := roleByNode[n.NodeID]; ok && w.Role != "" {
			return w.Role, w.Confidence
		}
	}
	return nodeRoleClaim(n)
}

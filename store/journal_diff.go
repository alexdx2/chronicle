package store

import (
	"fmt"
	"sort"
	"strings"
)

// GraphDiff lists semantic divergences between two graph databases.
type GraphDiff struct {
	OnlyInA []string `json:"only_in_a"`
	OnlyInB []string `json:"only_in_b"`
	Changed []string `json:"changed"`
}

func (d *GraphDiff) Clean() bool {
	return len(d.OnlyInA) == 0 && len(d.OnlyInB) == 0 && len(d.Changed) == 0
}

func (d *GraphDiff) String() string {
	var b strings.Builder
	for _, k := range d.OnlyInA {
		fmt.Fprintf(&b, "only in live:   %s\n", k)
	}
	for _, k := range d.OnlyInB {
		fmt.Fprintf(&b, "only in replay: %s\n", k)
	}
	for _, k := range d.Changed {
		fmt.Fprintf(&b, "differs:        %s\n", k)
	}
	return b.String()
}

// semanticRows returns key → semantic-fields fingerprint for current rows.
func semanticRows(s *Store, query string) (map[string]string, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	cols, _ := rows.Columns()
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%v", vals[0])
		var parts []string
		for _, v := range vals[1:] {
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		out[key] = strings.Join(parts, "|")
	}
	return out, rows.Err()
}

const diffNodesQ = `
	SELECT node_key, layer, node_type, domain_key, name,
	       COALESCE(qualified_name,''), COALESCE(file_path,''), COALESCE(lang,''), status
	FROM graph_nodes
	WHERE (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
	ORDER BY node_key`

const diffEdgesQ = `
	SELECT edge_key, edge_type, derivation_kind, active,
	       COALESCE(from_node_key,''), COALESCE(to_node_key,'')
	FROM graph_edges
	WHERE (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
	ORDER BY edge_key`

// Evidence diff: current versions only (closed/superseded rows share the uid
// with their replacement — including them collapses nondeterministically).
// GROUP BY uid with COUNT(*) + MIN/MAX of the semantic columns makes the
// fingerprint deterministic even if one db holds duplicate current rows for a
// uid: map insertion order can no longer pick a different winner per side.
const diffEvidenceQ = `
	SELECT COALESCE(evidence_uid, CAST(evidence_id AS TEXT)) AS uid,
	       MIN(target_kind), MIN(source_kind), MIN(COALESCE(file_path,'')), MIN(COALESCE(line_start,0)),
	       MIN(extractor_id), MIN(evidence_polarity), MIN(evidence_status), MAX(evidence_status), COUNT(*)
	FROM graph_evidence
	WHERE (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
	GROUP BY uid
	ORDER BY 1`

// DiffGraphStores compares live (a) vs replayed (b) on semantic columns.
// Derived values (confidence/freshness/trust_score), auto-increment ids,
// and revision-id columns are excluded — replay does not rebuild revisions,
// and trust is recomputed, not replayed.
func DiffGraphStores(a, b *Store) (*GraphDiff, error) {
	diff := &GraphDiff{}
	for label, q := range map[string]string{
		"node": diffNodesQ, "edge": diffEdgesQ, "evidence": diffEvidenceQ,
	} {
		ra, err := semanticRows(a, q)
		if err != nil {
			return nil, err
		}
		rb, err := semanticRows(b, q)
		if err != nil {
			return nil, err
		}
		for k, va := range ra {
			vb, ok := rb[k]
			switch {
			case !ok:
				diff.OnlyInA = append(diff.OnlyInA, label+":"+k)
			case va != vb:
				diff.Changed = append(diff.Changed, label+":"+k+" ("+va+" vs "+vb+")")
			}
		}
		for k := range rb {
			if _, ok := ra[k]; !ok {
				diff.OnlyInB = append(diff.OnlyInB, label+":"+k)
			}
		}
	}
	sort.Strings(diff.OnlyInA)
	sort.Strings(diff.OnlyInB)
	sort.Strings(diff.Changed)
	return diff, nil
}

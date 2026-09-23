package viewmodel

import "testing"

// Every drawn element carries its provenance: a diagram without evidence is
// a drawing, not architecture memory. Nodes carry confidence/trust/status
// and where the best evidence lives; edges carry the weakest confidence of
// their components and how many evidence rows back them.
func TestViewNodesCarryEvidence(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c3", "tom-and-jerry", "arena-api")
	if err != nil {
		t.Fatalf("BuildPreset(c3): %v", err)
	}
	withEvidence, withRef := 0, 0
	for _, n := range view.Nodes {
		if n.Boundary {
			continue
		}
		if n.Confidence <= 0 || n.Trust <= 0 {
			t.Errorf("%s: confidence=%v trust=%v — must come from the graph row", n.Name, n.Confidence, n.Trust)
		}
		if n.EvidenceCount > 0 {
			withEvidence++
			if n.EvidenceRef == "" {
				t.Errorf("%s has %d evidence rows but no ref", n.Name, n.EvidenceCount)
			} else {
				withRef++
			}
		}
	}
	if withEvidence == 0 {
		t.Errorf("no C3 component carries evidence counts; nodes=%+v", view.Nodes)
	}
}

func TestViewEdgesCarryEvidence(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c2", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c2): %v", err)
	}
	backed := 0
	for _, e := range view.Edges {
		if e.Kind == "uses_infra" {
			continue
		}
		if e.Confidence <= 0 || e.Trust <= 0 {
			t.Errorf("%s→%s %s: confidence=%v trust=%v", e.From, e.To, e.Kind, e.Confidence, e.Trust)
		}
		if e.Derivation == "" {
			t.Errorf("%s→%s %s: derivation missing", e.From, e.To, e.Kind)
		}
		if e.EvidenceCount > 0 {
			backed++
		}
		if e.Weight > 1 && e.EvidenceCount < e.Weight && e.EvidenceCount != 0 {
			// a collapsed arrow sums its components' evidence
			t.Errorf("%s→%s %s: weight=%d evidence=%d (should sum components)", e.From, e.To, e.Kind, e.Weight, e.EvidenceCount)
		}
	}
	if backed == 0 {
		t.Errorf("no C2 arrow carries evidence; edges=%+v", view.Edges)
	}
}

// A stale node says so on the card: status rides along (active is omitted).
func TestViewNodeStatusRidesAlong(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c1", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c1): %v", err)
	}
	for _, n := range view.Nodes {
		if n.Status == "active" {
			t.Errorf("%s: status active should be omitted (zero value)", n.Name)
		}
	}
}

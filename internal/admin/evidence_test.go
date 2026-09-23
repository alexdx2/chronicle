package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// An arrow must be inspectable on its own: POST /api/evidence with the
// component edge keys of a (collapsed) arrow returns each edge's provenance
// rows, so the dashboard can show "why does this arrow exist" without
// going through a node.
func TestEvidence_ByEdgeKeys(t *testing.T) {
	srv, _ := setupC4Graph(t)
	st := srv.getStore()
	edges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil || len(edges) == 0 {
		t.Fatalf("ListEdges: %v (%d)", err, len(edges))
	}
	var inj *store.EdgeRow
	for i := range edges {
		if edges[i].EdgeType == "INJECTS" {
			inj = &edges[i]
			break
		}
	}
	if inj == nil {
		t.Fatal("fixture has no INJECTS edge")
	}
	if _, err := st.AddEvidence(store.EvidenceRow{TargetKind: "edge", EdgeID: inj.EdgeID, SourceKind: "file",
		FilePath: "svc-a/src/arena/arena.controller.ts", LineStart: 7, ExtractorID: "ts", ExtractorVersion: "1",
		Confidence: 0.95, EvidenceStatus: "valid", EvidencePolarity: "positive", AssertionKind: "constructor_injection", Metadata: "{}"}); err != nil {
		t.Fatal(err)
	}

	body := `{"edge_keys":["` + inj.EdgeKey + `","nope->nope:X"]}`
	req := httptest.NewRequest("POST", "/api/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleEvidence(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Edges []struct {
			Edge     store.EdgeRow      `json:"edge"`
			Evidence []store.EvidenceRow `json:"evidence"`
		} `json:"edges"`
		Missing []string `json:"missing"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Edges) != 1 || resp.Edges[0].Edge.EdgeKey != inj.EdgeKey {
		t.Fatalf("edges=%+v want the INJECTS edge", resp.Edges)
	}
	if len(resp.Edges[0].Evidence) != 1 || resp.Edges[0].Evidence[0].LineStart != 7 {
		t.Errorf("evidence=%+v want the one row at line 7", resp.Edges[0].Evidence)
	}
	if len(resp.Missing) != 1 || resp.Missing[0] != "nope->nope:X" {
		t.Errorf("missing=%v want the unknown key reported", resp.Missing)
	}
}

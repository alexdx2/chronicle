package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// The config-file row of a config-derived call only confirms the value. When
// the caller line goes away the edge is retired although the config file is
// unchanged and its row is still valid.
func TestConfigDerivedCall_RetiredWhenCallerLineGoesAway(t *testing.T) {
	cfg := fleetConfig()
	cfg.AddUse(ConfigUse{File: "portal-bff/src/routes/notify.ts", Line: 4, Text: "process.env.NOTIFY_SVC_URL", Callee: "NotifyClient", Symbol: "process.env.NOTIFY_SVC_URL"})
	_, s, _ := resolveFiles(t, cfg, srcFacts{"portal-bff/src/routes/notify.ts", []Fact{}})
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	if len(edges) != 1 {
		t.Fatalf("edges = %d", len(edges))
	}
	evs, _ := s.ListEvidenceByEdge(edges[0].EdgeID)
	cfgRows := 0
	for _, e := range evs {
		if e.ASTRule == store.ConfigValueRule {
			cfgRows++
			if e.FilePath != "portal-bff/.env" {
				t.Errorf("config row on %q", e.FilePath)
			}
		}
	}
	if len(evs) != 2 || cfgRows != 1 {
		t.Fatalf("evidence rows = %d (config %d), want 2 (1)", len(evs), cfgRows)
	}
	if _, _, _, err := s.MarkEvidenceStaleByFiles([]string{"portal-bff/src/routes/notify.ts"}); err != nil {
		t.Fatal(err)
	}
	revID, _ := s.CreateRevision("demo", "s2", "", "manual", "incremental", "{}")
	if _, _, err := s.RetireUnbackedByFiles("demo", []string{"portal-bff/src/routes/notify.ts"}, revID); err != nil {
		t.Fatal(err)
	}
	if got := activeEdgesOf(t, s, "CALLS_SERVICE"); len(got) != 0 {
		t.Errorf("edge survived on the config row alone: %v", got)
	}
}

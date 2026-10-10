package headless

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/review"
)

func narrateFixture() *review.ReviewResult {
	return &review.ReviewResult{
		Head:    review.Provenance{Commit: "abc123"},
		Verdict: review.Verdict{SystemsAffected: 1, IntegrationsAdded: 1, Learned: 3},
		Findings: []review.ReviewFinding{{
			Severity: review.SeverityChanged, Kind: review.IntegrationAdded,
			System: review.SystemRef{Kind: "service", Key: "svc:billing", Name: "billing", Repo: "okeep"},
			Reason: review.ReasonIntegrationAdded, Counterpart: "svc:ledger",
			Evidence: []review.EvidenceRef{{File: "src/pay.ts"}},
			Downstream: []review.ReviewFinding{{
				Severity: review.SeverityAffected, Kind: review.IntegrationAdded,
				System: review.SystemRef{Name: "ledger"}, Reason: "provider_gains_caller",
			}},
		}},
		ChangedFiles: []gitdiff.ChangedFile{{Path: "src/pay.ts", Status: "M"}},
	}
}

func TestBuildNarratePrompt_IncludesFindingsAndLearned(t *testing.T) {
	req := BuildNarratePrompt(narrateFixture(), nil, 90)
	if !strings.Contains(req.System, "At most 90 words") {
		t.Fatalf("system = %q", req.System)
	}
	if !strings.HasPrefix(req.User, "Review: abc123\n") {
		t.Fatalf("first line = %q", req.User)
	}
	for _, want := range []string{"billing (okeep)", "integration_added", "ledger", "src/pay.ts",
		"3 facts were learned from unchanged neighbour files; they are not part of this change"} {
		if !strings.Contains(req.User, want) {
			t.Errorf("user text lacks %q:\n%s", want, req.User)
		}
	}
}

func TestBuildNarratePrompt_Truncates(t *testing.T) {
	r := narrateFixture()
	r.Findings = nil
	for i := 0; i < 500; i++ {
		r.Findings = append(r.Findings, review.ReviewFinding{
			Severity: review.SeverityChanged, Kind: review.IntegrationAdded,
			System: review.SystemRef{Name: fmt.Sprintf("system-%d", i), Repo: "r"},
			Reason: review.ReasonIntegrationAdded, File: strings.Repeat("long/path/", 12) + "f.ts",
		})
	}
	req := BuildNarratePrompt(r, nil, 0)
	if len(req.User) > 24000 {
		t.Fatalf("len = %d", len(req.User))
	}
	if !strings.Contains(req.User, "and") || !strings.Contains(req.User, "more") {
		t.Fatalf("no truncation marker:\n%s", req.User[len(req.User)-300:])
	}
	if !strings.HasPrefix(req.User, "Review: abc123\n") {
		t.Fatal("head lost")
	}
}

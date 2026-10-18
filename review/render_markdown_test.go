package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// --- shared fixture: the mockup's own main example --------------------------
//
// Mirrors 2026-09-25-review-report-mockup.html's "!42 · arena: critical hits
// in attack results" thread, and the brief's own fixture ingredients list
// ("producer changed + new topic + removed call + 1 unknown file, 3 affected
// systems"): a producer changed on an existing topic (battle-results, 3
// affected consumers across 2 repos), a brand new topic with no subscribers
// yet (battle.critical-hit — Analyze's own "no consumers" degrade rule), a
// removed plain dependency whose callee loses its only caller (jerry-api,
// informational), one unknown file, and one touched-but-inert consumer edge
// (arena.controller injects arena.service — "Code changed, graph
// unchanged"). Hand-built, not derived from a real Diff/ClassifyDelta/
// Analyze pipeline — the same synthetic-fixture convention finding_test.go
// and diagram_test.go's battleResultsReviewResult already use, deliberately
// kept independent of the tracked fixtures/tom-and-jerry database so this
// golden test cannot break just because that database is rescanned.
func mockupFixtureResult() *ReviewResult {
	arenaAPI := SystemRef{Kind: kindService, Key: "service:service:tom-and-jerry:arena-api", Name: "arena-api", Repo: "tom-and-jerry", Domain: "tom-and-jerry"}

	integrationAdded := ReviewFinding{
		Severity:    SeverityChanged,
		Kind:        IntegrationAdded,
		System:      arenaAPI,
		Reason:      ReasonNewTopicNoConsumers,
		Counterpart: "battle.critical-hit",
		Facts: []Fact{{
			ID: "battle-result.producer->battle.critical-hit:PUBLISHES_TOPIC", Kind: KindEdge,
			Edge: &snapshot.Edge{
				Type: "PUBLISHES_TOPIC", From: "battle-result.producer", To: "battle.critical-hit",
				Evidence: []snapshot.Evidence{{
					File: "arena-api/src/arena/battle-result.producer.ts", Blob: "3f9e2c1ab12cd34", Range: "58-64", Confidence: 0.90,
				}},
			},
		}},
	}

	producerChanged := ReviewFinding{
		Severity:    SeverityChanged,
		Kind:        ProducerChanged,
		System:      arenaAPI,
		Reason:      string(ProducerChanged),
		Counterpart: "battle-results",
		Facts: []Fact{{
			ID: "battle-result.producer->battle-results:PUBLISHES_TOPIC", Kind: KindEdge,
			Edge: &snapshot.Edge{
				Type: "PUBLISHES_TOPIC", From: "battle-result.producer", To: "battle-results",
				Evidence: []snapshot.Evidence{{
					File: "arena-api/src/arena/battle-result.producer.ts", Blob: "3f9e2c1ab12cd34", Range: "40-52", Confidence: 0.90,
				}},
			},
		}},
		Downstream: []ReviewFinding{
			{Severity: SeverityAffected, Kind: ProducerChanged,
				System: SystemRef{Kind: kindService, Key: "service:service:tom-and-jerry:scoreboard-api", Name: "scoreboard-api", Repo: "tom-and-jerry", Domain: "tom-and-jerry"},
				Path:   []PathStep{{Node: "battle-results", Edge: "CONSUMES_TOPIC"}}},
			{Severity: SeverityAffected, Kind: ProducerChanged,
				System: SystemRef{Kind: kindService, Key: "service:service:tom-and-jerry:spectators-api", Name: "spectators-api", Repo: "tom-and-jerry", Domain: "tom-and-jerry"},
				Path:   []PathStep{{Node: "battle-results", Edge: "CONSUMES_TOPIC"}}},
			{Severity: SeverityAffected, Kind: ProducerChanged,
				System: SystemRef{Kind: kindService, Key: "service:service:analytics:analytics-api", Name: "analytics-api", Repo: "analytics", Domain: "analytics"},
				Path:   []PathStep{{Node: "battle-results", Edge: "CONSUMES_TOPIC"}}},
		},
	}

	integrationRemoved := ReviewFinding{
		Severity:    SeverityChanged,
		Kind:        IntegrationRemoved,
		System:      arenaAPI,
		Reason:      string(IntegrationRemoved),
		Counterpart: "jerry-api",
		Facts: []Fact{{
			ID: "jerry.client->jerry-api:CALLS_SERVICE", Kind: KindEdge,
			Edge: &snapshot.Edge{
				Type: "CALLS_SERVICE", From: "jerry.client", To: "jerry-api",
				Evidence: []snapshot.Evidence{{
					File: "arena-api/src/arena/jerry.client.ts", Blob: "88ab017cdef12", Range: "31-36", Confidence: 0.85,
				}},
			},
		}},
		Downstream: []ReviewFinding{
			{Severity: SeverityInformational, Kind: IntegrationRemoved, Reason: ReasonProviderLostConsumer,
				System: SystemRef{Kind: kindService, Key: "service:service:tom-and-jerry:jerry-api", Name: "jerry-api", Repo: "tom-and-jerry", Domain: "tom-and-jerry"}},
		},
	}

	consumerChanged := ReviewFinding{
		Severity:    SeverityChanged,
		Kind:        ConsumerChanged,
		System:      arenaAPI,
		Reason:      string(ConsumerChanged),
		Counterpart: "arena.service",
		Facts: []Fact{{
			ID: "arena.controller->arena.service:INJECTS", Kind: KindEdge,
			Edge: &snapshot.Edge{
				Type: "INJECTS", From: "arena.controller", To: "arena.service",
				Evidence: []snapshot.Evidence{{
					File: "arena-api/src/arena/arena.controller.ts", Range: "31-44",
				}},
			},
		}},
	}

	unknownFinding := ReviewFinding{
		Severity: SeverityUnknown,
		File:     "arena-api/src/arena/battle.gateway.ts",
		Reason:   ReasonHeadStaleFile,
	}

	findings := []ReviewFinding{integrationAdded, producerChanged, integrationRemoved, consumerChanged, unknownFinding}

	delta := deltaFromFindings(findings)

	r := &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base: Provenance{
			Ref: "origin/main", Commit: "d1eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Origin: OriginSnapshot,
			Graph: &snapshot.GraphVersion{Commit: "c1eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Seq: 16},
		},
		Head: Provenance{
			Ref: "HEAD", Commit: "3f9e2c1ab12cd34eeeeeeeeeeeeeeeeeeeeeeeee", Origin: OriginSnapshot,
			Graph: &snapshot.GraphVersion{Commit: "3f9e2c1ab12cd34eeeeeeeeeeeeeeeeeeeeeeeee", Seq: 17},
		},
		Findings: findings,
		Delta:    delta,
		// The real `git diff --name-status` result for this scenario (task-18
		// changed-file-count fix): 3 files with real evidence above, plus the
		// one Unknown file (battle.gateway.ts) — matching the golden fixture's
		// own "3/4 changed files covered" line.
		ChangedFiles: []gitdiff.ChangedFile{
			{Path: "arena-api/src/arena/battle-result.producer.ts"},
			{Path: "arena-api/src/arena/jerry.client.ts"},
			{Path: "arena-api/src/arena/arena.controller.ts"},
			{Path: "arena-api/src/arena/battle.gateway.ts"},
		},
	}
	r.Verdict = verdictFrom(findings)
	return r
}

// deltaFromFindings builds the GraphDelta a fixture's Findings would have
// come from — every Fact each finding (and its Downstream, which never
// carries its own Facts, per ReviewFinding's own doc comment) references,
// routed the same way Diff itself would route it, so changedFileCounts'
// "covered" computation exercises the real function against real evidence
// rather than an empty Delta. Facts here happen to be Touched/Added-shaped
// only (this fixture never exercises Changed/Moved), which is enough for
// this test's own purposes — see task-13-report.md's point 4 discussion of
// coveredFiles.
func deltaFromFindings(findings []ReviewFinding) *GraphDelta {
	delta := &GraphDelta{}
	for _, f := range findings {
		for _, fact := range f.Facts {
			switch f.Kind {
			case IntegrationAdded:
				delta.Added = append(delta.Added, fact)
			case IntegrationRemoved:
				delta.Removed = append(delta.Removed, fact)
			default:
				delta.Touched = append(delta.Touched, Touched{ID: fact.ID, Kind: fact.Kind, Fact: fact})
			}
		}
	}
	return delta
}

// fixtureDiagrams hand-writes plausible, deterministic mermaid text for
// MarkdownOptions.Compact/Full — RenderMarkdown only ever embeds this text
// verbatim in a fence (see MarkdownOptions' own doc comment for why it
// cannot build the diagram itself), so a real viewmodel.BuildView pass adds
// setup cost without adding coverage of anything this package's own code
// does.
func fixtureDiagrams() (compact, full *DiagramResult) {
	compact = &DiagramResult{Mermaid: "graph LR\n  arena_api[\"arena-api\"]\n  scoreboard_api[\"scoreboard-api\"]\n  arena_api -->|publishes| scoreboard_api\n"}
	full = &DiagramResult{Mermaid: "graph LR\n  arena_api[\"arena-api\"]\n  scoreboard_api[\"scoreboard-api\"]\n  spectators_api[\"spectators-api\"]\n  analytics_api[\"analytics-api\"]\n  jerry_api[\"jerry-api\"]\n  arena_api -->|publishes| scoreboard_api\n  arena_api -->|publishes| spectators_api\n  arena_api -->|publishes| analytics_api\n"}
	return compact, full
}

func mockupFixtureOptions() MarkdownOptions {
	compact, full := fixtureDiagrams()
	return MarkdownOptions{
		Command:  "chronicle review main...feat/critical-hits",
		JSONLink: "https://chronicle.example/review/42.json",
		OpenLink: "https://chronicle.example/review/42",
		Compact:  compact,
		Full:     full,
	}
}

// --- golden helpers ----------------------------------------------------------

func goldenPath(name string) string {
	return filepath.Join("testdata", name)
}

// compareGolden fails with a diff-friendly message when got doesn't match
// the golden file byte-for-byte. Set CHRONICLE_UPDATE_GOLDEN=1 to write got
// as the new golden — this task's own convenience for the one-time authoring
// pass (the brief's own words: golden files are "updated by hand" when the
// template intentionally changes), never required for a normal test run.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := goldenPath(name)
	if os.Getenv("CHRONICLE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v", path, err)
	}
	if got != string(want) {
		t.Errorf("RenderMarkdown output != golden %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// --- Step 1 tests -------------------------------------------------------------

// TestRenderMarkdown_Fixture: the normal-report path against the mockup's
// own main scenario — byte-exact against testdata/review_markdown_fixture.golden.md.
func TestRenderMarkdown_Fixture(t *testing.T) {
	r := mockupFixtureResult()
	if r.Verdict.NoSystemChange {
		t.Fatal("fixture Verdict.NoSystemChange = true, want false (fixture has real changed relationships)")
	}
	if len(r.ChangedRelationships()) == 0 {
		t.Fatal("fixture ChangedRelationships() empty, want 3 (integration added/removed, producer changed)")
	}

	got, err := RenderMarkdown(r, mockupFixtureOptions())
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	compareGolden(t, "review_markdown_fixture.golden.md", got)
}

// TestRenderMarkdown_NoSystemChange: the mockup's "!43 · refactor battle
// guard" state — a touched INJECTS edge with no downstream, and nothing
// else. Golden: testdata/review_markdown_nosystemchange.golden.md.
func TestRenderMarkdown_NoSystemChange(t *testing.T) {
	consumerChanged := ReviewFinding{
		Severity: SeverityChanged, Kind: ConsumerChanged,
		System:      SystemRef{Kind: kindService, Key: "service:service:d:battle-api", Name: "battle-api"},
		Reason:      string(ConsumerChanged),
		Counterpart: "battle.guard",
		Facts: []Fact{{
			ID: "arena.controller->battle.guard:INJECTS", Kind: KindEdge,
			Edge: &snapshot.Edge{Type: "INJECTS", From: "arena.controller", To: "battle.guard",
				Evidence: []snapshot.Evidence{{File: "arena.controller.ts", Range: "10-20"}}},
		}},
	}
	producerChanged := ReviewFinding{
		Severity: SeverityChanged, Kind: ProducerChanged,
		System:      SystemRef{Kind: kindService, Key: "service:service:d:battle-api", Name: "battle-api"},
		Reason:      string(ProducerChanged),
		Counterpart: "battle-events",
		Facts: []Fact{{
			ID: "battle.guard->battle-events:PUBLISHES_TOPIC", Kind: KindEdge,
			Edge: &snapshot.Edge{Type: "PUBLISHES_TOPIC", From: "battle.guard", To: "battle-events",
				Evidence: []snapshot.Evidence{{File: "battle.guard.ts", Range: "5-9"}}},
		}},
	}
	findings := []ReviewFinding{consumerChanged, producerChanged}

	r := &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base:          Provenance{Ref: "origin/main", Commit: "aaa", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "aaa", Seq: 5}},
		Head:          Provenance{Ref: "HEAD", Commit: "bbb", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "bbb", Seq: 6}},
		Findings:      findings,
		Delta:         deltaFromFindings(findings),
		ChangedFiles:  []gitdiff.ChangedFile{{Path: "arena.controller.ts"}, {Path: "battle.guard.ts"}},
	}
	r.Verdict = verdictFrom(findings)
	if !r.Verdict.NoSystemChange {
		t.Fatalf("fixture Verdict.NoSystemChange = false, want true; Verdict=%+v", r.Verdict)
	}

	got, err := RenderMarkdown(r, MarkdownOptions{})
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	compareGolden(t, "review_markdown_nosystemchange.golden.md", got)
}

// TestRenderMarkdown_MixedUnknownAndTouched: fix round 1's regression test
// (task-13-report.md), pinning the independent reviewer's own repro shape
// exactly. Two real Touched findings — the SAME inert
// ConsumerChanged/ProducerChanged pair TestRenderMarkdown_NoSystemChange
// uses, empty Downstream — plus one Unknown finding for a THIRD, unrelated
// file. ChangedRelationships() is empty for this input (the two Touched
// findings are excluded by its own Producer/ConsumerChanged-with-no-
// Downstream filter, not because nothing real happened), and
// Verdict.UnknownFiles is 1 (>0) — the OLD 4th-state trigger
// (len(ChangedRelationships())==0 && UnknownFiles>0) fired on exactly this
// input and silently dropped the two real findings, showing only "1 of 3
// changed files changed after their last scan." with nothing else. The
// fixed trigger (covered==0 && uncovered>0, via changedFileCounts) must NOT
// fire here — covered is 2 (both Touched findings carry real evidence, in
// two distinct files) — so this must fall through to the NORMAL report,
// with the two real findings actually visible in "Code changed, graph
// unchanged".
func TestRenderMarkdown_MixedUnknownAndTouched(t *testing.T) {
	consumerChanged := ReviewFinding{
		Severity: SeverityChanged, Kind: ConsumerChanged,
		System:      SystemRef{Kind: kindService, Key: "service:service:d:battle-api", Name: "battle-api"},
		Reason:      string(ConsumerChanged),
		Counterpart: "battle.guard",
		Facts: []Fact{{
			ID: "arena.controller->battle.guard:INJECTS", Kind: KindEdge,
			Edge: &snapshot.Edge{Type: "INJECTS", From: "arena.controller", To: "battle.guard",
				Evidence: []snapshot.Evidence{{File: "arena.controller.ts", Range: "10-20"}}},
		}},
	}
	producerChanged := ReviewFinding{
		Severity: SeverityChanged, Kind: ProducerChanged,
		System:      SystemRef{Kind: kindService, Key: "service:service:d:battle-api", Name: "battle-api"},
		Reason:      string(ProducerChanged),
		Counterpart: "battle-events",
		Facts: []Fact{{
			ID: "battle.guard->battle-events:PUBLISHES_TOPIC", Kind: KindEdge,
			Edge: &snapshot.Edge{Type: "PUBLISHES_TOPIC", From: "battle.guard", To: "battle-events",
				Evidence: []snapshot.Evidence{{File: "battle.guard.ts", Range: "5-9"}}},
		}},
	}
	unknownFinding := ReviewFinding{
		Severity: SeverityUnknown,
		File:     "leaderboard/score.service.cs",
		Reason:   ReasonHeadStaleFile,
	}
	findings := []ReviewFinding{consumerChanged, producerChanged, unknownFinding}

	r := &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base:          Provenance{Ref: "origin/main", Commit: "aaa", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "aaa", Seq: 5}},
		Head:          Provenance{Ref: "HEAD", Commit: "bbb", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "bbb", Seq: 6}},
		Findings:      findings,
		Delta:         deltaFromFindings(findings),
		ChangedFiles: []gitdiff.ChangedFile{
			{Path: "arena.controller.ts"}, {Path: "battle.guard.ts"}, {Path: "leaderboard/score.service.cs"},
		},
	}
	r.Verdict = verdictFrom(findings)

	// Pin the precondition that makes this the reviewer's exact repro: the
	// OLD trigger's two clauses both hold...
	if len(r.ChangedRelationships()) != 0 {
		t.Fatalf("fixture ChangedRelationships() = %+v, want empty (both Touched findings excluded by their own empty-Downstream filter)", r.ChangedRelationships())
	}
	if r.Verdict.UnknownFiles == 0 {
		t.Fatal("fixture Verdict.UnknownFiles = 0, want > 0")
	}
	// ...but the NEW trigger's own clause does not: real evidence exists in
	// 2 distinct files (arena.controller.ts, battle.guard.ts), so covered>0.
	covered, uncovered, total := changedFileCounts(r)
	if covered == 0 {
		t.Fatalf("fixture changedFileCounts covered = 0, want > 0 (this is the case the fix targets)")
	}
	if uncovered != 1 || total != 3 {
		t.Fatalf("fixture changedFileCounts = (covered=%d, uncovered=%d, total=%d), want (covered>0, uncovered=1, total=3)", covered, uncovered, total)
	}

	got, err := RenderMarkdown(r, MarkdownOptions{})
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}

	if strings.Contains(got, "Impact unknown.") {
		t.Errorf("RenderMarkdown took the impact-unknown branch (dropping the 2 real findings), want the normal report:\n%s", got)
	}
	if !strings.Contains(got, "### Chronicle review") {
		t.Errorf("RenderMarkdown output missing the normal report's own heading:\n%s", got)
	}
	if !strings.Contains(got, "Code changed, graph unchanged (2)") {
		t.Errorf("RenderMarkdown output missing \"Code changed, graph unchanged (2)\" — the 2 real findings must still be shown:\n%s", got)
	}
	if !strings.Contains(got, "`arena.controller` → `battle.guard`") {
		t.Errorf("RenderMarkdown output missing the first real finding's own line:\n%s", got)
	}
	if !strings.Contains(got, "`battle.guard` → `battle-events`") {
		t.Errorf("RenderMarkdown output missing the second real finding's own line:\n%s", got)
	}
	if !strings.Contains(got, "#### Unknown impact (1 file)") {
		t.Errorf("RenderMarkdown output missing the one real Unknown file's own section:\n%s", got)
	}
}

// TestRenderMarkdown_MissingGraph: the mockup's "!45 · add Chronicle graph"
// state — base predates the graph entirely, head has it (the real shape of
// that scenario: the PR that adds the graph file necessarily has
// head.Origin == "snapshot" — see RenderMarkdown's own doc comment).
// Golden: testdata/review_markdown_missinggraph.golden.md.
func TestRenderMarkdown_MissingGraph(t *testing.T) {
	r := &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base:          Provenance{Ref: "main", Commit: "aaa", Origin: OriginMissing},
		Head:          Provenance{Ref: "HEAD", Commit: "bbb", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "bbb", Seq: 1}},
		Verdict:       Verdict{NoSystemChange: false},
		Warnings:      []string{baseMissingGraphMessage},
	}
	got, err := RenderMarkdown(r, MarkdownOptions{})
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	compareGolden(t, "review_markdown_missinggraph.golden.md", got)
}

// TestRenderMarkdown_ImpactUnknown: this task's own 4th state (see
// RenderMarkdown's doc comment and task-13-report.md) — the mockup's
// "!44 · new leaderboard filters" thread: every changed file is Unknown, so
// there is no relationship verdict to compute at all. Golden:
// testdata/review_markdown_impactunknown.golden.md.
func TestRenderMarkdown_ImpactUnknown(t *testing.T) {
	findings := []ReviewFinding{
		{Severity: SeverityUnknown, File: "leaderboard/score.service.cs", Reason: ReasonHeadStaleFile},
		{Severity: SeverityUnknown, File: "leaderboard/score.controller.cs", Reason: ReasonHeadStaleFile},
	}
	r := &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base:          Provenance{Ref: "origin/main", Commit: "aaa", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "aaa", Seq: 8}},
		Head:          Provenance{Ref: "HEAD", Commit: "bbb", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "bbb", Seq: 8}},
		Findings:      findings,
		Delta:         &GraphDelta{},
		ChangedFiles: []gitdiff.ChangedFile{
			{Path: "leaderboard/score.service.cs"}, {Path: "leaderboard/score.controller.cs"},
		},
	}
	r.Verdict = verdictFrom(findings)
	if r.Verdict.NoSystemChange {
		t.Fatal("fixture Verdict.NoSystemChange = true, want false (UnknownFiles > 0)")
	}
	if len(r.ChangedRelationships()) != 0 {
		t.Fatalf("fixture ChangedRelationships() = %+v, want empty", r.ChangedRelationships())
	}

	got, err := RenderMarkdown(r, MarkdownOptions{})
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	compareGolden(t, "review_markdown_impactunknown.golden.md", got)
}

// TestRenderMarkdown_EscapesHostileText: a finding whose node name is a
// hostile HTML string never leaves a literal "<img" in the output — the one
// security property this renderer's escaping (viewmodel.TextEscape) is
// actually relied on for (see templates.go's own top comment and
// graph/viewmodel/text_export.go).
func TestRenderMarkdown_EscapesHostileText(t *testing.T) {
	hostile := `<img src=x onerror=alert(1)>`
	finding := ReviewFinding{
		Severity: SeverityChanged, Kind: ProducerChanged,
		System:      SystemRef{Kind: kindService, Key: "svc:a", Name: hostile},
		Reason:      string(ProducerChanged),
		Counterpart: hostile,
		Facts: []Fact{{
			ID: "a->b:PUBLISHES_TOPIC", Kind: KindEdge,
			Edge: &snapshot.Edge{Type: "PUBLISHES_TOPIC", From: hostile, To: hostile,
				Evidence: []snapshot.Evidence{{File: hostile, Range: "1-2"}}},
		}},
		Downstream: []ReviewFinding{
			{Severity: SeverityAffected, Kind: ProducerChanged,
				System: SystemRef{Kind: kindService, Key: "svc:b", Name: hostile},
				Path:   []PathStep{{Node: hostile, Edge: "CONSUMES_TOPIC"}}},
		},
	}
	findings := []ReviewFinding{finding}
	r := &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base:          Provenance{Ref: "main", Commit: "aaa", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "aaa", Seq: 1}},
		Head:          Provenance{Ref: "HEAD", Commit: "bbb", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "bbb", Seq: 1}},
		Findings:      findings,
		Delta:         deltaFromFindings(findings),
	}
	r.Verdict = verdictFrom(findings)

	got, err := RenderMarkdown(r, MarkdownOptions{})
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	if strings.Contains(got, "<img") {
		t.Errorf("RenderMarkdown output contains an untransformed <img tag:\n%s", got)
	}
}

// TestRenderMarkdown_RowLimit: 30 affected systems in "Who may be affected",
// MaxRows:5 — exactly 5 data rows plus a "+25 more" line.
func TestRenderMarkdown_RowLimit(t *testing.T) {
	const n = 30
	downstream := make([]ReviewFinding, 0, n)
	for i := 0; i < n; i++ {
		key := "svc:" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		downstream = append(downstream, ReviewFinding{
			Severity: SeverityAffected, Kind: ProducerChanged,
			System: SystemRef{Kind: kindService, Key: key, Name: key},
			Path:   []PathStep{{Node: "topic", Edge: "CONSUMES_TOPIC"}},
		})
	}
	finding := ReviewFinding{
		Severity: SeverityChanged, Kind: ProducerChanged,
		System:      SystemRef{Kind: kindService, Key: "svc:origin", Name: "origin-api"},
		Reason:      string(ProducerChanged),
		Counterpart: "topic",
		Facts: []Fact{{
			ID: "origin->topic:PUBLISHES_TOPIC", Kind: KindEdge,
			Edge: &snapshot.Edge{Type: "PUBLISHES_TOPIC", From: "origin", To: "topic",
				Evidence: []snapshot.Evidence{{File: "origin.ts", Range: "1-2"}}},
		}},
		Downstream: downstream,
	}
	findings := []ReviewFinding{finding}
	r := &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base:          Provenance{Ref: "main", Commit: "aaa", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "aaa", Seq: 1}},
		Head:          Provenance{Ref: "HEAD", Commit: "bbb", Origin: OriginSnapshot, Graph: &snapshot.GraphVersion{Commit: "bbb", Seq: 1}},
		Findings:      findings,
		Delta:         deltaFromFindings(findings),
	}
	r.Verdict = verdictFrom(findings)
	if got := r.Verdict.SystemsAffected; got != n {
		t.Fatalf("fixture Verdict.SystemsAffected = %d, want %d", got, n)
	}

	got, err := RenderMarkdown(r, MarkdownOptions{MaxRows: 5})
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}

	dataRows := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "| **") {
			dataRows++
		}
	}
	if dataRows != 5 {
		t.Errorf("Who-may-be-affected data rows = %d, want 5\n%s", dataRows, got)
	}
	if !strings.Contains(got, "+25 more") {
		t.Errorf("output does not contain %q:\n%s", "+25 more", got)
	}
}

// Two extractors observing the same lines of one fact are one evidence row
// (Source names both); a different range stays its own row.
func TestEvidenceSection_DedupesAcrossExtractors(t *testing.T) {
	edge := &snapshot.Edge{
		Type: "CONSUMES_TOPIC", From: "consumer", To: "battle-results-v2",
		Evidence: []snapshot.Evidence{
			{File: "svc/c.ts", Blob: "aaaaaaa1", Range: "20-20", Extractor: "chronicle-ast", Confidence: 0.5},
			{File: "svc/c.ts", Blob: "aaaaaaa1", Range: "20-20", Extractor: "chronicle-structural", Confidence: 0.6},
			{File: "svc/c.ts", Blob: "aaaaaaa1", Range: "31-33", Extractor: "chronicle-ast", Confidence: 0.5},
		},
	}
	f := ReviewFinding{
		Severity: SeverityChanged, Kind: IntegrationAdded, System: SystemRef{Name: "svc"},
		Facts: []Fact{{ID: "consumer->battle-results-v2:CONSUMES_TOPIC", Kind: KindEdge, Edge: edge}},
	}
	got := flattenEvidence(f.Facts)
	if len(got) != 2 || got[0].Extractor != "chronicle-ast+chronicle-structural" || got[0].Confidence != 0.6 || got[1].Range != "31-33" {
		t.Fatalf("flattenEvidence = %+v, want 2 refs, the first folded (both extractors, 0.6)", got)
	}

	sec := evidenceSection(&ReviewResult{Findings: []ReviewFinding{f}}, 20)
	if !strings.Contains(sec, "Evidence (2 facts)") {
		t.Errorf("summary count not the deduped row count:\n%s", sec)
	}
	if strings.Count(sec, "svc/c.ts:20-20") != 1 || !strings.Contains(sec, "ast+structural") {
		t.Errorf("duplicate 20-20 rows or extractor list missing:\n%s", sec)
	}
	if strings.Count(sec, "svc/c.ts:31-33") != 1 {
		t.Errorf("distinct range lost:\n%s", sec)
	}
}

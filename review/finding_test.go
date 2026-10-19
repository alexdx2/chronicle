package review

import (
	"errors"
	"reflect"
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
)

// fakeImpactQuerier implements ImpactQuerier with canned, per-key responses
// — a literal SystemImpact/SystemRef, no traversal — so finding_test.go can
// exercise Analyze's branching in isolation from the real graph engine
// (impact_test.go already covers CoreImpactQuerier itself against the real
// fixture).
type fakeImpactQuerier struct {
	impacts  map[string][]SystemImpact
	subs     map[string][]SystemImpact
	collapse map[string]SystemRef
	one      *SystemRef // when set, Collapse returns it for every key
	err      error      // non-nil: every method fails with this error
}

var _ ImpactQuerier = fakeImpactQuerier{}

func (f fakeImpactQuerier) QueryImpact(seed string, depth int) ([]SystemImpact, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.impacts[seed], nil
}

func (f fakeImpactQuerier) TopicSubscribers(topic string) ([]SystemImpact, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.subs[topic], nil
}

func (f fakeImpactQuerier) Collapse(nodeKey string) (SystemRef, error) {
	if f.err != nil {
		return SystemRef{}, f.err
	}
	if ref, ok := f.collapse[nodeKey]; ok {
		return ref, nil
	}
	if f.one != nil {
		return *f.one, nil
	}
	return SystemRef{Kind: kindDomain, Key: nodeKey, Name: nodeKey}, nil
}

// --- shared fixtures ------------------------------------------------------

var (
	sysCaller = SystemRef{Kind: kindService, Key: "sys:caller", Name: "caller"}
	sysA      = SystemRef{Kind: kindService, Key: "sys:a", Name: "a"}
	sysB      = SystemRef{Kind: kindService, Key: "sys:b", Name: "b"}
	sysTopic1 = SystemRef{Kind: kindService, Key: "sys:topic-consumer-1", Name: "tc1"}
	sysTopic2 = SystemRef{Kind: kindService, Key: "sys:topic-consumer-2", Name: "tc2"}
)

func findingByKey(t *testing.T, findings []ReviewFinding, key string) *ReviewFinding {
	t.Helper()
	for i := range findings {
		if findings[i].System.Key == key {
			return &findings[i]
		}
	}
	return nil
}

func onlySeverity(findings []ReviewFinding, sev Severity) []ReviewFinding {
	var out []ReviewFinding
	for _, f := range findings {
		if f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}

// --- Row 1: IntegrationAdded/Removed, plain sync edge ---------------------

// TestAnalyze_IntegrationAdded_Sync: an Added CALLS_SERVICE edge — plain
// ClassBehaviorSync, not PUBLISHES_TOPIC — gets QueryImpact(SourceID) as
// Affected plus an Informational entry for the counterpart, reason
// provider_gains_caller. Also exercises the top-level Changed finding's own
// fields (System from Collapse(SourceID), Counterpart, Facts, Evidence).
func TestAnalyze_IntegrationAdded_Sync(t *testing.T) {
	edge := snapshot.Edge{
		ID: "caller->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "caller", To: "b",
		Evidence: []snapshot.Evidence{{File: "f.ts", Blob: "blob1", Range: "1-2", Extractor: "ext", Confidence: 0.9}},
	}
	c := Change{Kind: IntegrationAdded, SourceID: "caller", Counterpart: "b", Seed: "head", Facts: []Fact{edgeFact(edge.ID, edge)}}

	headQ := fakeImpactQuerier{
		impacts:  map[string][]SystemImpact{"caller": {{System: sysA, Path: []PathStep{{Node: "a", Edge: "CALLS_SERVICE"}}}}},
		collapse: map[string]SystemRef{"caller": sysCaller, "b": sysB},
	}
	baseQ := fakeImpactQuerier{err: errors.New("must not be called for a head-seeded change")}

	findings := Analyze([]Change{c}, baseQ, headQ, 4)
	if len(findings) != 1 {
		t.Fatalf("Analyze = %+v, want exactly 1 finding", findings)
	}
	f := findings[0]

	if f.Severity != SeverityChanged || f.Kind != IntegrationAdded {
		t.Errorf("finding = %+v, want Severity=Changed Kind=IntegrationAdded", f)
	}
	if f.System != sysCaller {
		t.Errorf("System = %+v, want Collapse(SourceID) = %+v", f.System, sysCaller)
	}
	if f.Reason != ReasonIntegrationAdded {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonIntegrationAdded)
	}
	if f.Counterpart != "b" {
		t.Errorf("Counterpart = %q, want %q", f.Counterpart, "b")
	}
	if !reflect.DeepEqual(f.Facts, c.Facts) {
		t.Errorf("Facts = %+v, want Change.Facts %+v", f.Facts, c.Facts)
	}
	wantEvidence := []EvidenceRef{{File: "f.ts", Blob: "blob1", Range: "1-2", Extractor: "ext", Confidence: 0.9}}
	if !reflect.DeepEqual(f.Evidence, wantEvidence) {
		t.Errorf("Evidence = %+v, want %+v", f.Evidence, wantEvidence)
	}

	affected := onlySeverity(f.Downstream, SeverityAffected)
	if len(affected) != 1 || affected[0].System != sysA {
		t.Errorf("Affected downstream = %+v, want exactly [sysA]", affected)
	}
	info := onlySeverity(f.Downstream, SeverityInformational)
	if len(info) != 1 || info[0].System != sysB || info[0].Reason != ReasonProviderGainsCaller {
		t.Errorf("Informational downstream = %+v, want [{System:sysB Reason:provider_gains_caller}]", info)
	}
	for _, d := range f.Downstream {
		if d.Kind != IntegrationAdded {
			t.Errorf("Downstream finding Kind = %q, want it to inherit the parent's IntegrationAdded", d.Kind)
		}
		if d.Facts != nil || d.Evidence != nil {
			t.Errorf("Downstream finding Facts/Evidence = %+v/%+v, want both nil", d.Facts, d.Evidence)
		}
	}
}

// TestAnalyze_DropsSelfAffected pins Bug 3's second fix (task-18-report.md,
// dropSelfAffected): a Downstream Affected entry whose collapsed System
// equals the finding's own System must be removed — the design spec's own
// rule (review-engine-design.md §5: a change is never both changed and
// affected by the same underlying event). This is exactly the shape a flow
// node re-attributed to its triggering endpoint's owner (impact.go's Bug 3
// fix) produces when that owner IS the system that changed: QueryImpact's
// SystemImpact results can legitimately include the source's own collapsed
// identity (via some other node that also collapses to it) alongside a
// real, distinct affected system — this must drop the former, keep the
// latter.
func TestAnalyze_DropsSelfAffected(t *testing.T) {
	edge := snapshot.Edge{ID: "caller->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "caller", To: "b"}
	c := Change{Kind: IntegrationAdded, SourceID: "caller", Counterpart: "b", Seed: "head", Facts: []Fact{edgeFact(edge.ID, edge)}}

	headQ := fakeImpactQuerier{
		impacts: map[string][]SystemImpact{"caller": {
			{System: sysCaller, Path: []PathStep{{Node: "self-loop", Edge: "REQUIRES"}}}, // collapses back to the source's own system
			{System: sysA, Path: []PathStep{{Node: "a", Edge: "CALLS_SERVICE"}}},
		}},
		collapse: map[string]SystemRef{"caller": sysCaller, "b": sysB},
	}
	baseQ := fakeImpactQuerier{err: errors.New("must not be called for a head-seeded change")}

	findings := Analyze([]Change{c}, baseQ, headQ, 4)
	if len(findings) != 1 {
		t.Fatalf("Analyze = %+v, want exactly 1 finding", findings)
	}
	f := findings[0]
	if f.System != sysCaller {
		t.Fatalf("finding.System = %+v, want sysCaller (precondition for this test)", f.System)
	}

	affected := onlySeverity(f.Downstream, SeverityAffected)
	if len(affected) != 1 || affected[0].System != sysA {
		t.Errorf("Affected downstream = %+v, want exactly [sysA] (sysCaller dropped as self-affected)", affected)
	}
	// The informational counterpart entry (sysB, unrelated to self) must
	// survive untouched — dropSelfAffected only ever filters Affected.
	info := onlySeverity(f.Downstream, SeverityInformational)
	if len(info) != 1 || info[0].System != sysB {
		t.Errorf("Informational downstream = %+v, want exactly [sysB]", info)
	}
}

// TestAnalyze_DropSelfAffected_KeepsSelfInformational: an Informational entry
// whose System happens to equal the finding's own System (a self-call losing
// itself as a caller — a degenerate but real shape) must NOT be dropped:
// dropSelfAffected's own doc comment explains why only Affected is filtered.
func TestAnalyze_DropSelfAffected_KeepsSelfInformational(t *testing.T) {
	edge := snapshot.Edge{ID: "caller->caller:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "caller", To: "caller"}
	c := Change{Kind: IntegrationRemoved, SourceID: "caller", Counterpart: "caller", Seed: "base", Facts: []Fact{edgeFact(edge.ID, edge)}}

	baseQ := fakeImpactQuerier{
		collapse: map[string]SystemRef{"caller": sysCaller},
	}
	headQ := fakeImpactQuerier{err: errors.New("must not be called for a base-seeded change")}

	findings := Analyze([]Change{c}, baseQ, headQ, 4)
	if len(findings) != 1 {
		t.Fatalf("Analyze = %+v, want exactly 1 finding", findings)
	}
	info := onlySeverity(findings[0].Downstream, SeverityInformational)
	if len(info) != 1 || info[0].System != sysCaller || info[0].Reason != ReasonProviderLostConsumer {
		t.Errorf("Informational downstream = %+v, want exactly [{System:sysCaller Reason:provider_lost_consumer}] (a self-referencing Informational entry must not be dropped)", info)
	}
}

// TestAnalyze_IntegrationRemoved_InformationalCounterpart (named explicitly
// by the brief): symmetric case for removal — reason provider_lost_consumer
// on the informational entry, integration_removed on the top finding, and q
// must be baseQ (Change.Seed=="base").
func TestAnalyze_IntegrationRemoved_InformationalCounterpart(t *testing.T) {
	edge := snapshot.Edge{ID: "caller->b:INJECTS", Type: "INJECTS", From: "caller", To: "b"}
	c := Change{Kind: IntegrationRemoved, SourceID: "caller", Counterpart: "b", Seed: "base", Facts: []Fact{edgeFact(edge.ID, edge)}}

	baseQ := fakeImpactQuerier{
		impacts:  map[string][]SystemImpact{"caller": {{System: sysA}}},
		collapse: map[string]SystemRef{"caller": sysCaller, "b": sysB},
	}
	headQ := fakeImpactQuerier{err: errors.New("must not be called for a base-seeded change")}

	findings := Analyze([]Change{c}, baseQ, headQ, 4)
	f := findings[0]
	if f.Reason != ReasonIntegrationRemoved {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonIntegrationRemoved)
	}
	info := onlySeverity(f.Downstream, SeverityInformational)
	if len(info) != 1 || info[0].System != sysB || info[0].Reason != ReasonProviderLostConsumer {
		t.Errorf("Informational downstream = %+v, want [{System:sysB Reason:provider_lost_consumer}]", info)
	}
	affected := onlySeverity(f.Downstream, SeverityAffected)
	if len(affected) != 1 || affected[0].System != sysA {
		t.Errorf("Affected downstream = %+v, want exactly [sysA]", affected)
	}
}

// TestAnalyze_ConsumerChanged_FromTopic: a touched CONSUMES_TOPIC edge
// becomes ConsumerChanged (classify.go's own rule) — Analyze treats it
// exactly like ConsumerChanged from a plain sync edge (QueryImpact(SourceID)
// only, no TopicSubscribers, no Informational): confirms CONSUMES_TOPIC
// groups with "plain sync" for downstream purposes, not with PUBLISHES_TOPIC.
func TestAnalyze_IntegrationAdded_ConsumesTopic_IsPlainSyncShaped(t *testing.T) {
	edge := snapshot.Edge{ID: "caller->topic:CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: "caller", To: "topic"}
	c := Change{Kind: IntegrationAdded, SourceID: "caller", Counterpart: "topic", Seed: "head", Facts: []Fact{edgeFact(edge.ID, edge)}}

	headQ := fakeImpactQuerier{
		impacts:  map[string][]SystemImpact{"caller": {{System: sysA}}},
		collapse: map[string]SystemRef{"caller": sysCaller, "topic": sysB},
		// subs is intentionally left nil/unset: if Analyze mistakenly routed
		// a CONSUMES_TOPIC change through the PUBLISHES_TOPIC branch, it
		// would call TopicSubscribers("topic") instead of Collapse("topic"),
		// getting an EMPTY Affected list where this test expects the
		// Informational entry below instead.
	}

	findings := Analyze([]Change{c}, fakeImpactQuerier{err: errors.New("base unused")}, headQ, 4)
	f := findings[0]
	info := onlySeverity(f.Downstream, SeverityInformational)
	if len(info) != 1 || info[0].System != sysB || info[0].Reason != ReasonProviderGainsCaller {
		t.Errorf("Informational downstream = %+v, want [{System:sysB Reason:provider_gains_caller}] (CONSUMES_TOPIC must use the plain-sync branch)", info)
	}
}

// --- Row 2: IntegrationAdded/Removed on PUBLISHES_TOPIC --------------------

// TestAnalyze_IntegrationAdded_PublishesTopic_WithConsumers: Downstream =
// QueryImpact(SourceID) ++ TopicSubscribers(Counterpart), no Informational,
// default Reason kept (consumers exist).
func TestAnalyze_IntegrationAdded_PublishesTopic_WithConsumers(t *testing.T) {
	edge := snapshot.Edge{ID: "producer->topic:PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: "producer", To: "topic"}
	c := Change{Kind: IntegrationAdded, SourceID: "producer", Counterpart: "topic", Seed: "head", Facts: []Fact{edgeFact(edge.ID, edge)}}

	headQ := fakeImpactQuerier{
		impacts: map[string][]SystemImpact{"producer": {{System: sysA}}},
		subs:    map[string][]SystemImpact{"topic": {{System: sysTopic1}, {System: sysTopic2}}},
	}

	findings := Analyze([]Change{c}, fakeImpactQuerier{err: errors.New("base unused")}, headQ, 4)
	f := findings[0]
	if f.Reason != ReasonIntegrationAdded {
		t.Errorf("Reason = %q, want the default (consumers exist, no override)", f.Reason)
	}
	if len(onlySeverity(f.Downstream, SeverityInformational)) != 0 {
		t.Errorf("Downstream = %+v, want no Informational entries (a topic isn't a system)", f.Downstream)
	}
	affected := onlySeverity(f.Downstream, SeverityAffected)
	if len(affected) != 3 {
		t.Fatalf("Affected downstream = %+v, want 3 (1 from QueryImpact + 2 from TopicSubscribers)", affected)
	}
	if findingByKey(t, affected, sysA.Key) == nil || findingByKey(t, affected, sysTopic1.Key) == nil || findingByKey(t, affected, sysTopic2.Key) == nil {
		t.Errorf("Affected downstream = %+v, want sysA + sysTopic1 + sysTopic2", affected)
	}
}

// TestAnalyze_NewTopicNoConsumers_Reason (named explicitly by the brief):
// Added + TopicSubscribers empty discards Downstream IN FULL — including
// the QueryImpact(SourceID) half, which this test deliberately makes
// non-empty to prove it really gets thrown away, not just the topic half —
// and overrides the top-level Reason.
func TestAnalyze_NewTopicNoConsumers_Reason(t *testing.T) {
	edge := snapshot.Edge{ID: "producer->topic:PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: "producer", To: "topic"}
	c := Change{Kind: IntegrationAdded, SourceID: "producer", Counterpart: "topic", Seed: "head", Facts: []Fact{edgeFact(edge.ID, edge)}}

	headQ := fakeImpactQuerier{
		impacts: map[string][]SystemImpact{"producer": {{System: sysA}}}, // non-empty on purpose
		subs:    map[string][]SystemImpact{},                             // topic: no consumers
	}

	findings := Analyze([]Change{c}, fakeImpactQuerier{err: errors.New("base unused")}, headQ, 4)
	f := findings[0]
	if f.Reason != ReasonNewTopicNoConsumers {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonNewTopicNoConsumers)
	}
	if len(f.Downstream) != 0 {
		t.Errorf("Downstream = %+v, want [] IN FULL (the QueryImpact(SourceID) half must be discarded too)", f.Downstream)
	}
}

// TestAnalyze_IntegrationRemoved_PublishesTopic_NoOverrideForRemoved proves
// the new_topic_no_consumers override is Added-only: a Removed
// PUBLISHES_TOPIC change with zero remaining subscribers keeps its default
// Reason and keeps the QueryImpact(SourceID) half of Downstream.
func TestAnalyze_IntegrationRemoved_PublishesTopic_NoOverrideForRemoved(t *testing.T) {
	edge := snapshot.Edge{ID: "producer->topic:PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: "producer", To: "topic"}
	c := Change{Kind: IntegrationRemoved, SourceID: "producer", Counterpart: "topic", Seed: "base", Facts: []Fact{edgeFact(edge.ID, edge)}}

	baseQ := fakeImpactQuerier{
		impacts: map[string][]SystemImpact{"producer": {{System: sysA}}},
		subs:    map[string][]SystemImpact{},
	}

	findings := Analyze([]Change{c}, baseQ, fakeImpactQuerier{err: errors.New("head unused")}, 4)
	f := findings[0]
	if f.Reason != ReasonIntegrationRemoved {
		t.Errorf("Reason = %q, want the default %q (override is Added-only)", f.Reason, ReasonIntegrationRemoved)
	}
	affected := onlySeverity(f.Downstream, SeverityAffected)
	if len(affected) != 1 || affected[0].System != sysA {
		t.Errorf("Downstream = %+v, want the QueryImpact(SourceID) half kept: [sysA]", affected)
	}
}

// --- ProducerChanged / ConsumerChanged ------------------------------------

// TestAnalyze_ProducerChanged: Downstream = TopicSubscribers(Counterpart)
// only.
func TestAnalyze_ProducerChanged(t *testing.T) {
	c := Change{Kind: ProducerChanged, SourceID: "producer", Counterpart: "topic", Seed: "head"}
	headQ := fakeImpactQuerier{subs: map[string][]SystemImpact{"topic": {{System: sysTopic1}}}}

	findings := Analyze([]Change{c}, fakeImpactQuerier{err: errors.New("base unused")}, headQ, 4)
	f := findings[0]
	if f.Reason != ReasonProducerChanged {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonProducerChanged)
	}
	if len(f.Downstream) != 1 || f.Downstream[0].System != sysTopic1 || f.Downstream[0].Severity != SeverityAffected {
		t.Errorf("Downstream = %+v, want exactly [Affected sysTopic1]", f.Downstream)
	}
}

// TestAnalyze_ConsumerChanged: Downstream = QueryImpact(SourceID) only.
func TestAnalyze_ConsumerChanged(t *testing.T) {
	c := Change{Kind: ConsumerChanged, SourceID: "caller", Counterpart: "b", Seed: "head"}
	headQ := fakeImpactQuerier{impacts: map[string][]SystemImpact{"caller": {{System: sysA}}}}

	findings := Analyze([]Change{c}, fakeImpactQuerier{err: errors.New("base unused")}, headQ, 4)
	f := findings[0]
	if f.Reason != ReasonConsumerChanged {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonConsumerChanged)
	}
	if len(f.Downstream) != 1 || f.Downstream[0].System != sysA || f.Downstream[0].Severity != SeverityAffected {
		t.Errorf("Downstream = %+v, want exactly [Affected sysA]", f.Downstream)
	}
}

// --- Contract* -------------------------------------------------------------

// TestAnalyze_ContractAdded_Standalone: Downstream = nil, default Reason.
func TestAnalyze_ContractAdded_Standalone(t *testing.T) {
	node := snapshot.Node{ID: "contract:endpoint:d:new", Layer: "contract", Type: "endpoint"}
	c := Change{Kind: ContractAdded, SourceID: node.ID, Counterpart: "", Seed: "head", Facts: []Fact{nodeFact(node.ID, node)}}

	headQ := fakeImpactQuerier{err: errors.New("QueryImpact/TopicSubscribers must not be called for ContractAdded")}
	findings := Analyze([]Change{c}, fakeImpactQuerier{}, headQ, 4)
	f := findings[0]
	if f.Reason != ReasonContractAdded {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonContractAdded)
	}
	if f.Downstream != nil {
		t.Errorf("Downstream = %+v, want nil", f.Downstream)
	}
}

// TestAnalyze_ContractAdded_Merged covers the genuine ambiguity flagged in
// task-11-report.md: classify.go's merge rule can ALSO produce a
// ContractAdded Change (an EXPOSES_ENDPOINT edge + its brand-new endpoint
// node merged into one Change), recognizable here by a non-empty
// Counterpart (the merge rule always sets it; the standalone rule above
// never does) and 2 Facts instead of 1. This task's reading: Downstream=nil
// applies to EVERY ContractAdded uniformly regardless of merge status — the
// rejected alternative (treating the merged case like a sync
// IntegrationAdded, walking QueryImpact(SourceID)) is documented in
// task-11-report.md. This test pins the chosen reading so a future change
// that special-cases the merged form has to consciously touch this test.
func TestAnalyze_ContractAdded_Merged(t *testing.T) {
	edge := snapshot.Edge{ID: "svcA->ep:EXPOSES_ENDPOINT", Type: "EXPOSES_ENDPOINT", From: "svcA", To: "ep"}
	node := snapshot.Node{ID: "ep", Layer: "contract", Type: "endpoint"}
	c := Change{
		Kind: ContractAdded, SourceID: "svcA", Counterpart: "ep", Seed: "head",
		Facts: []Fact{edgeFact(edge.ID, edge), nodeFact(node.ID, node)},
	}

	headQ := fakeImpactQuerier{err: errors.New("QueryImpact/TopicSubscribers must not be called for ContractAdded")}
	findings := Analyze([]Change{c}, fakeImpactQuerier{}, headQ, 4)
	f := findings[0]
	if f.Downstream != nil {
		t.Errorf("Downstream = %+v, want nil (merged ContractAdded treated the same as standalone)", f.Downstream)
	}
	if f.Reason != ReasonContractAdded {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonContractAdded)
	}
	if f.Counterpart != "ep" {
		t.Errorf("Counterpart = %q, want %q (merge rule sets it, unlike standalone)", f.Counterpart, "ep")
	}
}

// TestAnalyze_ContractRemoved: Downstream = QueryImpact(SourceID) on BASE.
func TestAnalyze_ContractRemoved(t *testing.T) {
	node := snapshot.Node{ID: "contract:endpoint:d:old", Layer: "contract", Type: "endpoint"}
	c := Change{Kind: ContractRemoved, SourceID: node.ID, Counterpart: "", Seed: "base", Facts: []Fact{nodeFact(node.ID, node)}}

	baseQ := fakeImpactQuerier{impacts: map[string][]SystemImpact{node.ID: {{System: sysA}}}}
	headQ := fakeImpactQuerier{err: errors.New("ContractRemoved must query BASE, not head")}

	findings := Analyze([]Change{c}, baseQ, headQ, 4)
	f := findings[0]
	if f.Reason != ReasonContractRemoved {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonContractRemoved)
	}
	if len(f.Downstream) != 1 || f.Downstream[0].System != sysA {
		t.Errorf("Downstream = %+v, want exactly [Affected sysA] from baseQ", f.Downstream)
	}
}

// TestAnalyze_ContractChanged: Downstream = QueryImpact(SourceID) on HEAD.
func TestAnalyze_ContractChanged(t *testing.T) {
	node := snapshot.Node{ID: "data:model:d:user", Layer: "data", Type: "model"}
	c := Change{Kind: ContractChanged, SourceID: node.ID, Counterpart: "", Seed: "head", Facts: []Fact{nodeFact(node.ID, node)}}

	headQ := fakeImpactQuerier{impacts: map[string][]SystemImpact{node.ID: {{System: sysA}}}}
	baseQ := fakeImpactQuerier{err: errors.New("ContractChanged must query HEAD, not base")}

	findings := Analyze([]Change{c}, baseQ, headQ, 4)
	f := findings[0]
	if f.Reason != ReasonContractChanged {
		t.Errorf("Reason = %q, want %q", f.Reason, ReasonContractChanged)
	}
	if len(f.Downstream) != 1 || f.Downstream[0].System != sysA {
		t.Errorf("Downstream = %+v, want exactly [Affected sysA] from headQ", f.Downstream)
	}
}

// TestAnalyze_ContractChanged_EdgeFact_SeedsFromEdgeFrom (fix-round-1,
// task-11-report.md): classify.go's own doc comment says a Changed CONTRACT
// EDGE (e.g. HAS_FIELD) also produces ContractChanged, with SourceID = that
// EDGE's own composite key ("svcA->field:HAS_FIELD"), not a node key.
// Analyze must not seed QueryImpact with that unresolvable key: it must
// notice Facts[0] is edge-shaped and seed from the edge's own From node
// (svcA) instead — a real, resolvable node key — recovering real Downstream
// signal instead of silently coming back empty. The fake querier is keyed
// ONLY by "svcA" (the edge's From), not by the edge's own composite key: if
// Analyze regressed to seeding with Change.SourceID directly, this lookup
// would miss and Downstream would come back empty again, catching the
// regression.
func TestAnalyze_ContractChanged_EdgeFact_SeedsFromEdgeFrom(t *testing.T) {
	edge := snapshot.Edge{ID: "svcA->field:HAS_FIELD", Type: "HAS_FIELD", From: "svcA", To: "field"}
	c := Change{Kind: ContractChanged, SourceID: edge.ID, Counterpart: "", Seed: "head", Facts: []Fact{edgeFact(edge.ID, edge)}}

	headQ := fakeImpactQuerier{
		impacts: map[string][]SystemImpact{
			"svcA": {{System: sysA}}, // keyed by the edge's own From, the corrected seed
		},
	}

	findings := Analyze([]Change{c}, fakeImpactQuerier{err: errors.New("base unused")}, headQ, 4)
	f := findings[0]
	if len(f.Downstream) != 1 || f.Downstream[0].System != sysA {
		t.Fatalf("Downstream = %+v, want exactly [Affected sysA] (seeded from the edge's own From, not its composite SourceID key)", f.Downstream)
	}
}

// TestAnalyze_ContractChanged_EdgeSourceID_DoesNotCrash (still a valid
// defensive-fallback proof after fix-round-1, but no longer exercising the
// "SourceID is an unresolvable edge key" problem itself — that's fixed above
// via contractChangedSeed, which this test's Downstream seed (svcA, the
// edge's own From) now correctly receives too): this pins that an
// ImpactQuerier error for ANY reason still degrades gracefully rather than
// panicking or fabricating a finding. System is a separate story — it is
// deliberately still built from the raw Change.SourceID (the edge's own
// composite key, per Analyze's own doc comment: this fix only changes the
// Downstream BFS seed, not what the finding reports itself to be), so it
// still can't resolve and stays at its zero value, exactly as before.
func TestAnalyze_ContractChanged_EdgeSourceID_DoesNotCrash(t *testing.T) {
	edge := snapshot.Edge{ID: "svcA->field:HAS_FIELD", Type: "HAS_FIELD", From: "svcA", To: "field"}
	c := Change{Kind: ContractChanged, SourceID: edge.ID, Counterpart: "", Seed: "head", Facts: []Fact{edgeFact(edge.ID, edge)}}

	headQ := fakeImpactQuerier{err: errors.New("simulated failure — any ImpactQuerier error must degrade gracefully")}

	findings := Analyze([]Change{c}, fakeImpactQuerier{}, headQ, 4)
	f := findings[0]
	if f.System != (SystemRef{}) {
		t.Errorf("System = %+v, want the zero value: System is still built from the raw edge-shaped SourceID, unaffected by the Downstream seed fix", f.System)
	}
	if f.Downstream != nil {
		t.Errorf("Downstream = %+v, want nil on a QueryImpact error", f.Downstream)
	}
}

// TestAnalyze_MultipleChanges_Independent: Analyze processes each Change
// independently and preserves order/count — a basic sanity check beyond the
// single-Change tests above.
func TestAnalyze_MultipleChanges_Independent(t *testing.T) {
	edge1 := snapshot.Edge{ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b"}
	edge2 := snapshot.Edge{ID: "c->d:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "c", To: "d"}
	changes := []Change{
		{Kind: IntegrationAdded, SourceID: "a", Counterpart: "b", Seed: "head", Facts: []Fact{edgeFact(edge1.ID, edge1)}},
		{Kind: IntegrationRemoved, SourceID: "c", Counterpart: "d", Seed: "base", Facts: []Fact{edgeFact(edge2.ID, edge2)}},
	}
	findings := Analyze(changes, fakeImpactQuerier{}, fakeImpactQuerier{}, 4)
	if len(findings) != 2 {
		t.Fatalf("Analyze = %+v, want exactly 2 findings", findings)
	}
	if findings[0].Kind != IntegrationAdded || findings[1].Kind != IntegrationRemoved {
		t.Errorf("findings kinds = [%s, %s], want [IntegrationAdded, IntegrationRemoved]", findings[0].Kind, findings[1].Kind)
	}
}

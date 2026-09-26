package review

import "github.com/alexdx2/chronicle-core/snapshot"

// Severity is how sure/serious a ReviewFinding is.
type Severity string

const (
	SeverityChanged       Severity = "changed"
	SeverityAffected      Severity = "affected"
	SeverityInformational Severity = "informational"
	SeverityUnknown       Severity = "unknown"
)

// EvidenceRef is one flattened piece of provenance for a Changed finding:
// which file/blob/range an extractor asserted the changed fact from.
type EvidenceRef struct {
	File, Blob, Range, Extractor string
	Confidence                   float64
}

// Reason is ReviewFinding's own closed dictionary. Every value Analyze or
// Review can attach to a finding is named here, even the two
// (ReasonHeadMissingGraph/ReasonBaseMissingGraph) that no code path in this
// task actually attaches to a ReviewFinding: Review's missing-graph branches
// report their warning as a single ReviewResult.Warnings string with
// Findings left empty (see Review's own doc comment), so those two reason
// codes exist for closed-dictionary completeness, not because something
// here constructs a finding that carries them. If a later task finds a real
// ReviewFinding call site for either, this is where it plugs in.
const (
	ReasonIntegrationAdded     = "integration_added"
	ReasonIntegrationRemoved   = "integration_removed"
	ReasonProviderLostConsumer = "provider_lost_consumer"
	ReasonProviderGainsCaller  = "provider_gains_caller"
	ReasonProducerChanged      = "producer_changed"
	ReasonConsumerChanged      = "consumer_changed"
	ReasonContractAdded        = "contract_added"
	ReasonContractRemoved      = "contract_removed"
	ReasonContractChanged      = "contract_changed"
	ReasonNewTopicNoConsumers  = "new_topic_no_consumers"
	ReasonHeadStaleFile        = "head_stale_file"
	ReasonBaseStaleFile        = "base_stale_file"
	ReasonHeadMissingGraph     = "head_missing_graph"
	ReasonBaseMissingGraph     = "base_missing_graph"
)

// ReviewFinding is one node of the tree Analyze (and Review's own
// unknown-file pass) produces: a top-level Severity Changed finding per
// Change, carrying zero or more Downstream Affected/Informational findings;
// or a standalone Severity Unknown finding for a changed file the graph
// cannot vouch for.
type ReviewFinding struct {
	Severity Severity
	// Kind is the ChangeKind that produced this finding — the top-level
	// Change's own Kind for a Changed finding, and (Analyze's own choice,
	// not spelled out field-by-field in the brief's table but implied by
	// this field's zero value being reserved for Unknown alone, and needed
	// for WhoMayBeAffected to keep any "why" after it flattens Downstream
	// out of its parent tree) the SAME parent Kind inherited onto every
	// Downstream Affected/Informational finding too. "" only for Unknown.
	Kind ChangeKind
	// System is empty only for Unknown (File is used instead).
	System SystemRef
	// File is set only for Severity Unknown.
	File string
	// Reason is a value from the dictionary above.
	Reason string
	// Path is set for Affected findings collapsed from a SystemImpact; empty
	// for Changed (the change's own origin, not a path to it) and for
	// Informational (a direct counterpart, collapsed with no traversal).
	Path []PathStep
	// Counterpart is the node_key of the relationship's other side (a topic
	// or endpoint), "" if there isn't one — set only on the top-level
	// Changed finding (= the originating Change.Counterpart); Task 12 needs
	// it to pin the intermediate contract node on the compact diagram.
	Counterpart string
	// Facts is the originating Change.Facts — only on Severity Changed;
	// nil for Downstream findings, which have no fact of their own, only a
	// path to one.
	Facts []Fact
	// Evidence is Facts flattened to EvidenceRef, one per underlying
	// snapshot.Evidence entry — only on Severity Changed. Affected/
	// Informational findings carry nil: a SystemImpact has no evidence of
	// its own, only a path, which is deliberately treated as sufficient
	// provenance for a downstream finding (see task-11-report.md).
	Evidence []EvidenceRef
	// Downstream holds this Changed finding's own consequences (Affected/
	// Informational) — only on Severity Changed; always nil on a Downstream
	// finding itself (this tree is exactly two levels deep).
	Downstream []ReviewFinding
}

// Analyze turns each Change into one Severity Changed ReviewFinding (with
// its own Downstream), per the branch table below. q is whichever of
// {baseQ, headQ} matches Change.Seed ("base" -> baseQ, "head" -> headQ) —
// picked once per Change and reused for every query and collapse that
// Change's branch makes, including the ones the table calls out by graph
// name (ContractRemoved's "BASE", ContractChanged's "HEAD"): those are not
// a special override, they are simply restating what Change.Seed already
// pins for that Kind by construction (ContractRemoved's Seed is always
// "base", ContractChanged's is always "head" — see classify.go's own doc
// comment), so picking q by Seed uniformly already gives every branch the
// graph side the brief names.
//
//   - IntegrationAdded/IntegrationRemoved, underlying edge NOT PUBLISHES_TOPIC
//     (a plain ClassBehaviorSync edge — CALLS_SERVICE/CALLS_ENDPOINT/INJECTS
//     — or CONSUMES_TOPIC, which behaves like a plain sync dependency for
//     this purpose: gaining/losing a consumer of some topic doesn't change
//     what the topic's OTHER consumers see, unlike gaining/losing a
//     producer — see task-11-report.md):
//     Downstream = q.QueryImpact(SourceID, depth) as Affected
//     ++ [{Severity:Informational, System: q.Collapse(Counterpart),
//     Reason: ReasonProviderLostConsumer (Removed) |
//     ReasonProviderGainsCaller (Added)}]
//   - IntegrationAdded/IntegrationRemoved on PUBLISHES_TOPIC:
//     Downstream = q.QueryImpact(SourceID, depth) as Affected
//     ++ q.TopicSubscribers(Counterpart) as Affected
//     (no Informational — a topic isn't a system); if Kind is Added AND
//     TopicSubscribers comes back empty, Downstream becomes [] IN FULL
//     (the QueryImpact half is discarded too, not just the topic half) and
//     the CHANGED finding's own Reason becomes ReasonNewTopicNoConsumers.
//   - ProducerChanged: Downstream = q.TopicSubscribers(Counterpart) as Affected.
//   - ConsumerChanged: Downstream = q.QueryImpact(SourceID, depth) as Affected.
//   - ContractAdded (standalone or merged with its EXPOSES_ENDPOINT edge —
//     see task-11-report.md for why this task treats both the same):
//     Downstream = nil.
//   - ContractRemoved: Downstream = q.QueryImpact(SourceID, depth) as
//     Affected (q is baseQ here, per Change.Seed).
//   - ContractChanged: Downstream = q.QueryImpact(seed, depth) as Affected
//     (q is headQ here, per Change.Seed), where seed is normally SourceID —
//     EXCEPT when the underlying fact is a changed CONTRACT EDGE (e.g.
//     HAS_FIELD), classify.go's own doc comment for the last row of its
//     table): there, SourceID is that edge's own composite key ("a->b:
//     HAS_FIELD"), which QueryImpact/Collapse can never resolve via
//     GetNodeByKey (a real gap this task's own SourceID convention has for
//     exactly one Kind — see task-11-report.md's fix-round-1 section). In
//     that case seed is the edge's own From node instead (contractChangedSeed)
//     — a real, resolvable node key, the model/service the changed contract
//     edge originates from. This ONLY changes which key seeds the Downstream
//     BFS: the finding's own SourceID/System/Reason still reflect the real
//     fact unchanged (every other ChangeKind's SourceID is already
//     guaranteed to be a real node key by classify.go's more specific rules,
//     so no other branch needs this treatment).
func Analyze(changes []Change, baseQ, headQ ImpactQuerier, depth int) []ReviewFinding {
	findings := make([]ReviewFinding, 0, len(changes))
	for _, c := range changes {
		findings = append(findings, analyzeOne(c, baseQ, headQ, depth))
	}
	return findings
}

func analyzeOne(c Change, baseQ, headQ ImpactQuerier, depth int) ReviewFinding {
	q := headQ
	if c.Seed == seedBase {
		q = baseQ
	}

	finding := ReviewFinding{
		Severity:    SeverityChanged,
		Kind:        c.Kind,
		System:      safeCollapse(q, c.SourceID),
		Reason:      string(c.Kind),
		Counterpart: c.Counterpart,
		Facts:       c.Facts,
		Evidence:    flattenEvidence(c.Facts),
	}

	switch c.Kind {
	case IntegrationAdded, IntegrationRemoved:
		analyzeIntegration(&finding, c, q, depth)
	case ProducerChanged:
		finding.Downstream = affectedFromTopic(q, c.Counterpart)
	case ConsumerChanged:
		finding.Downstream = affectedFrom(q, c.SourceID, depth)
	case ContractAdded:
		// Downstream = nil: see the judgment call in task-11-report.md —
		// an addition (standalone or merged) doesn't threaten anything
		// that already existed, so there is no downstream to walk.
	case ContractRemoved:
		finding.Downstream = affectedFrom(q, c.SourceID, depth)
	case ContractChanged:
		finding.Downstream = affectedFrom(q, contractChangedSeed(c), depth)
	}

	for i := range finding.Downstream {
		finding.Downstream[i].Kind = c.Kind
	}
	return finding
}

// contractChangedSeed picks the QueryImpact seed for a ContractChanged
// Change (fix-round-1, task-11-report.md). classify.go's changedChanges rule
// produces ContractChanged from EITHER a changed contract NODE (SourceID is
// already that node's own key — a real, resolvable node_key) OR a changed
// contract EDGE such as HAS_FIELD (SourceID is that EDGE's own composite key,
// e.g. "a->b:HAS_FIELD", which QueryImpact/Collapse can never resolve via
// GetNodeByKey). For the edge case, seed from the edge's own From node
// instead — the model/service the changed contract edge originates from, a
// real node key — rather than the unresolvable edge key. This deliberately
// only changes the DOWNSTREAM BFS seed: Change.SourceID itself, and
// everything analyzeOne derives from it before this point (the finding's own
// System, Reason, Counterpart, Facts, Evidence), are untouched — they still
// reflect the real fact, edge-shaped or not.
func contractChangedSeed(c Change) string {
	if len(c.Facts) > 0 && c.Facts[0].Kind == KindEdge && c.Facts[0].Edge != nil {
		return c.Facts[0].Edge.From
	}
	return c.SourceID
}

// analyzeIntegration handles the two IntegrationAdded/IntegrationRemoved
// rows, branching on the underlying edge type — ChangeKind alone doesn't
// carry the PUBLISHES_TOPIC/plain-sync distinction these two rows need (see
// Analyze's own doc comment); Facts[0].Edge is guaranteed non-nil here by
// classify.go's own construction (the merge and standalone paths both put
// exactly one edge fact into a Change of either Kind).
func analyzeIntegration(finding *ReviewFinding, c Change, q ImpactQuerier, depth int) {
	edgeType := ""
	if len(c.Facts) > 0 && c.Facts[0].Edge != nil {
		edgeType = c.Facts[0].Edge.Type
	}

	if edgeType == "PUBLISHES_TOPIC" {
		fromSeed := affectedFrom(q, c.SourceID, depth)
		subs := affectedFromTopic(q, c.Counterpart)
		if c.Kind == IntegrationAdded && len(subs) == 0 {
			finding.Downstream = nil
			finding.Reason = ReasonNewTopicNoConsumers
			return
		}
		finding.Downstream = append(fromSeed, subs...)
		return
	}

	reason := ReasonProviderGainsCaller
	if c.Kind == IntegrationRemoved {
		reason = ReasonProviderLostConsumer
	}
	info := ReviewFinding{Severity: SeverityInformational, System: safeCollapse(q, c.Counterpart), Reason: reason}
	finding.Downstream = append(affectedFrom(q, c.SourceID, depth), info)
}

// affectedFrom wraps q.QueryImpact into Affected ReviewFindings, swallowing
// a query error to an empty Downstream contribution rather than crashing:
// Analyze has no error return, so a broken seed (e.g. ContractChanged's
// SourceID can be a changed HAS_FIELD EDGE's own key, not a node key — see
// task-11-report.md) degrades to "nothing found" instead of panicking.
func affectedFrom(q ImpactQuerier, seed string, depth int) []ReviewFinding {
	if seed == "" {
		return nil
	}
	impacts, err := q.QueryImpact(seed, depth)
	if err != nil {
		return nil
	}
	return affectedFindingsFrom(impacts)
}

func affectedFromTopic(q ImpactQuerier, topic string) []ReviewFinding {
	if topic == "" {
		return nil
	}
	impacts, err := q.TopicSubscribers(topic)
	if err != nil {
		return nil
	}
	return affectedFindingsFrom(impacts)
}

func affectedFindingsFrom(impacts []SystemImpact) []ReviewFinding {
	if len(impacts) == 0 {
		return nil
	}
	out := make([]ReviewFinding, 0, len(impacts))
	for _, imp := range impacts {
		out = append(out, ReviewFinding{Severity: SeverityAffected, System: imp.System, Path: imp.Path})
	}
	return out
}

func safeCollapse(q ImpactQuerier, key string) SystemRef {
	if key == "" {
		return SystemRef{}
	}
	ref, err := q.Collapse(key)
	if err != nil {
		return SystemRef{}
	}
	return ref
}

// flattenEvidence turns Change.Facts into EvidenceRef, one per underlying
// snapshot.Evidence entry on each fact's Node or Edge (whichever Kind says).
func flattenEvidence(facts []Fact) []EvidenceRef {
	var out []EvidenceRef
	for _, f := range facts {
		var evs []snapshot.Evidence
		switch {
		case f.Kind == KindNode && f.Node != nil:
			evs = f.Node.Evidence
		case f.Kind == KindEdge && f.Edge != nil:
			evs = f.Edge.Evidence
		}
		for _, e := range evs {
			out = append(out, EvidenceRef{
				File: e.File, Blob: e.Blob, Range: e.Range, Extractor: e.Extractor,
				Confidence: e.Confidence,
			})
		}
	}
	return out
}

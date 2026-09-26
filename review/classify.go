package review

// ChangeKind names one direction-aware kind of review finding ClassifyDelta
// can emit. Each value pairs a semantic action (a new/removed
// integration, a change to an existing one, a contract's own lifecycle)
// with the direction that matters for a human reading a review report and
// for Task 11's impact BFS (which side of the diff to seed the walk from).
type ChangeKind string

const (
	IntegrationAdded   ChangeKind = "integration_added"
	IntegrationRemoved ChangeKind = "integration_removed"
	ProducerChanged    ChangeKind = "producer_changed"
	ConsumerChanged    ChangeKind = "consumer_changed"
	ContractAdded      ChangeKind = "contract_added"
	ContractRemoved    ChangeKind = "contract_removed"
	ContractChanged    ChangeKind = "contract_changed"
)

// seedBase and seedHead are the two values Change.Seed ever takes: which
// side of the diff SourceID is present on, so Task 11's impact BFS knows
// which graph to seed the walk from.
const (
	seedBase = "base"
	seedHead = "head"
)

// Change is one direction-aware, human- (or impact-analysis-) actionable
// finding derived from a GraphDelta by ClassifyDelta.
type Change struct {
	Kind ChangeKind
	// SourceID is the node_key whose code expresses the change: the caller/
	// publisher/subscriber for an integration or producer/consumer change,
	// or the contract node itself for a contract change.
	SourceID string
	// Counterpart is the other side (a topic or service node for an
	// integration/producer/consumer change), or "" when there isn't one (a
	// standalone contract change).
	Counterpart string
	// Seed is "base" or "head": which graph SourceID exists on, for Task
	// 11's impact BFS to seed the walk from.
	Seed string
	// Facts is the fact (or, only under the merge rule below, the two
	// facts) this Change was derived from.
	Facts []Fact
}

// ClassifyDelta turns a GraphDelta into the direction-aware Changes a human
// or a later impact-analysis pass (Task 11) can act on. Structural facts
// never reach a Change — Diff already keeps them out of
// Added/Removed/Changed/Touched (they only ever move the Structural*
// counters), and this function independently double-checks Classify(fact)
// itself before acting on anything, so it is correct even fed a
// hand-constructed GraphDelta that didn't come from Diff.
//
// Classification table (see task-10-brief.md for the original):
//
//   - Added/Removed behavioral edge A->B (ClassBehaviorSync or
//     ClassBehaviorAsync): Kind = IntegrationAdded/IntegrationRemoved,
//     SourceID=A, Counterpart=B, Seed = "head" for Added, "base" for Removed.
//   - Touched edge, ClassBehaviorAsync with type PUBLISHES_TOPIC:
//     Kind = ProducerChanged, SourceID=A (publisher), Counterpart=B (topic),
//     Seed = "head".
//   - Touched edge, ClassBehaviorSync (CALLS_SERVICE, CALLS_ENDPOINT,
//     INJECTS) or ClassBehaviorAsync with type CONSUMES_TOPIC:
//     Kind = ConsumerChanged, SourceID=A (caller/subscriber), Counterpart=B,
//     Seed = "head".
//   - Added/Removed/Changed node, ClassContract (without a paired edge — see
//     the merge rule): Kind = ContractAdded/ContractRemoved/ContractChanged,
//     SourceID=the node, Counterpart="", Seed matches the node's side
//     ("head" for Added, "base" for Removed, "head" for Changed — a Changed
//     fact exists on both sides, and "head" is the same convention Touched
//     uses above).
//   - Changed node or edge of any non-structural class not already covered
//     by Kind of addition/removal above: if it is ClassContract ->
//     ContractChanged, SourceID = the fact's own id, Counterpart=""; this is
//     the same rule as the Changed-node case above, just also reached by a
//     Changed CONTRACT EDGE (e.g. HAS_FIELD). Otherwise: no Change — it is
//     already accounted for in GraphDelta.Changed for JSON rendering, but it
//     is not a review finding.
//   - Moved is never mentioned by the table and never produces a Change: a
//     fact whose evidence attachment moved but whose meaning didn't is not a
//     semantic event.
//
// Merge rule (the one that keeps the verdict honest — see task-10-report.md
// for the two related judgment calls this function makes beyond the literal
// table): if an edge A->B of type EXPOSES_ENDPOINT (ClassContract) or
// PUBLISHES_TOPIC (ClassBehaviorAsync) is added AT THE SAME TIME as node B
// itself (B's id appears as its own fact in the same Added list), that is
// ONE Change, not two:
//
//	PUBLISHES_TOPIC + new topic    -> IntegrationAdded, SourceID=A, Counterpart=B, Facts=[edge,node]
//	EXPOSES_ENDPOINT + new endpoint -> ContractAdded,    SourceID=A, Counterpart=B, Facts=[edge,node]
//
// Symmetrically for Removed (IntegrationRemoved/ContractRemoved). Neither
// fact contributes to any other Change once merged this way — in
// particular, node B does NOT also get its own standalone ContractAdded/
// Removed. A node with no matching edge in the same delta (a contract
// declared but not yet wired up) falls through to its own standalone
// ContractAdded/ContractRemoved exactly as the node row above describes. An
// edge of one of these two types with no matching node in the same delta
// (the target already existed) falls through to the ordinary rules above:
// for PUBLISHES_TOPIC (ClassBehaviorAsync) that's still a standalone
// IntegrationAdded/Removed; for EXPOSES_ENDPOINT (ClassContract, not a
// behavioral edge) the table has no standalone Added/Removed row for a
// CONTRACT EDGE, so it produces no Change — a documented judgment call, see
// task-10-report.md.
func ClassifyDelta(d *GraphDelta) []Change {
	if d == nil {
		return nil
	}

	var changes []Change

	addedConsumed := make(map[int]bool, len(d.Added))
	removedConsumed := make(map[int]bool, len(d.Removed))

	// Merge pass first, so the ordinary per-fact rules below can see which
	// facts are already spoken for.
	changes = append(changes, mergeNewNodeWithEdge(d.Added, addedConsumed, true)...)
	changes = append(changes, mergeNewNodeWithEdge(d.Removed, removedConsumed, false)...)

	changes = append(changes, standaloneAddedOrRemoved(d.Added, addedConsumed, true)...)
	changes = append(changes, standaloneAddedOrRemoved(d.Removed, removedConsumed, false)...)

	changes = append(changes, touchedChanges(d.Touched)...)
	changes = append(changes, changedChanges(d.Changed)...)

	// d.Moved is intentionally never visited: the table never mentions it.

	return changes
}

// mergeNewNodeWithEdge scans facts (either d.Added or d.Removed, per
// addedSide) for a PUBLISHES_TOPIC or EXPOSES_ENDPOINT edge whose target
// node (Edge.To) is ALSO present, unconsumed, in the same list. Each match
// marks both the edge's and the node's index consumed (via the shared
// consumed map, mutated in place so the caller's later standalone pass sees
// it) and emits exactly one merged Change carrying both facts.
func mergeNewNodeWithEdge(facts []Fact, consumed map[int]bool, addedSide bool) []Change {
	seed := seedHead
	if !addedSide {
		seed = seedBase
	}

	var changes []Change
	for i, f := range facts {
		if consumed[i] || f.Kind != KindEdge || f.Edge == nil {
			continue
		}
		edgeType := f.Edge.Type
		if edgeType != "PUBLISHES_TOPIC" && edgeType != "EXPOSES_ENDPOINT" {
			continue
		}

		j, node, ok := findUnconsumedNode(facts, consumed, f.Edge.To)
		if !ok {
			continue
		}
		consumed[i] = true
		consumed[j] = true

		kind := IntegrationAdded
		switch {
		case addedSide && edgeType == "EXPOSES_ENDPOINT":
			kind = ContractAdded
		case !addedSide && edgeType == "PUBLISHES_TOPIC":
			kind = IntegrationRemoved
		case !addedSide && edgeType == "EXPOSES_ENDPOINT":
			kind = ContractRemoved
		}

		changes = append(changes, Change{
			Kind:        kind,
			SourceID:    f.Edge.From,
			Counterpart: f.Edge.To,
			Seed:        seed,
			Facts:       []Fact{f, node},
		})
	}
	return changes
}

// findUnconsumedNode looks in facts for an unconsumed KindNode fact whose ID
// equals id — the correlation mechanism the merge rule uses, an exact
// string comparison since node/edge keys are globally unique canonical
// identifiers, not a heuristic.
func findUnconsumedNode(facts []Fact, consumed map[int]bool, id string) (int, Fact, bool) {
	for j, g := range facts {
		if consumed[j] {
			continue
		}
		if g.Kind == KindNode && g.ID == id {
			return j, g, true
		}
	}
	return 0, Fact{}, false
}

// standaloneAddedOrRemoved applies the ordinary (non-merge) Added/Removed
// rules to every fact in facts that the merge pass didn't already consume:
// a behavioral edge (ClassBehaviorSync or ClassBehaviorAsync) becomes
// IntegrationAdded/Removed; a ClassContract node becomes
// ContractAdded/Removed; everything else (a non-contract node, or a
// ClassContract/ClassData/ClassFlow edge with no row of its own here)
// produces no Change. Classify(f) is checked and ClassStructural is
// skipped defensively, even though Diff itself never places a structural
// fact in Added/Removed.
func standaloneAddedOrRemoved(facts []Fact, consumed map[int]bool, addedSide bool) []Change {
	seed := seedHead
	if !addedSide {
		seed = seedBase
	}

	var changes []Change
	for i, f := range facts {
		if consumed[i] {
			continue
		}
		class := Classify(f)
		if class == ClassStructural {
			continue
		}

		switch {
		case f.Kind == KindEdge && f.Edge != nil && (class == ClassBehaviorSync || class == ClassBehaviorAsync):
			kind := IntegrationAdded
			if !addedSide {
				kind = IntegrationRemoved
			}
			changes = append(changes, Change{
				Kind: kind, SourceID: f.Edge.From, Counterpart: f.Edge.To,
				Seed: seed, Facts: []Fact{f},
			})

		case f.Kind == KindNode && class == ClassContract:
			kind := ContractAdded
			if !addedSide {
				kind = ContractRemoved
			}
			changes = append(changes, Change{
				Kind: kind, SourceID: f.ID, Counterpart: "",
				Seed: seed, Facts: []Fact{f},
			})

			// Anything else (a non-contract node, or a ClassContract/
			// ClassData/ClassFlow edge outside the merge rule — e.g. a
			// standalone Added EXPOSES_ENDPOINT edge whose target node
			// already existed) has no row in the table: no Change.
		}
	}
	return changes
}

// touchedChanges applies rows 2/3 of the table: only an edge counts (there
// is no Touched-node row), and only two shapes produce a Change —
// PUBLISHES_TOPIC becomes ProducerChanged, everything else behavioral
// (ClassBehaviorSync, or ClassBehaviorAsync's other member CONSUMES_TOPIC)
// becomes ConsumerChanged. A Touched ClassContract/ClassData/ClassFlow edge
// produces no Change.
func touchedChanges(touched []Touched) []Change {
	var changes []Change
	for _, t := range touched {
		f := t.Fact
		if f.Kind != KindEdge || f.Edge == nil {
			continue
		}
		class := Classify(f)
		if class == ClassStructural {
			continue
		}

		switch {
		case class == ClassBehaviorAsync && f.Edge.Type == "PUBLISHES_TOPIC":
			changes = append(changes, Change{
				Kind: ProducerChanged, SourceID: f.Edge.From, Counterpart: f.Edge.To,
				Seed: seedHead, Facts: []Fact{f},
			})

		case class == ClassBehaviorSync || (class == ClassBehaviorAsync && f.Edge.Type == "CONSUMES_TOPIC"):
			changes = append(changes, Change{
				Kind: ConsumerChanged, SourceID: f.Edge.From, Counterpart: f.Edge.To,
				Seed: seedHead, Facts: []Fact{f},
			})
		}
	}
	return changes
}

// changedChanges applies the table's last row to every entry of
// GraphDelta.Changed: a non-structural fact (node or edge) classified
// ClassContract becomes ContractChanged, SourceID = the fact's own id,
// Counterpart="". Anything else produces no Change — it is already
// accounted for in GraphDelta.Changed for JSON rendering, but it is not a
// review finding. Classify(c.After) is used for both the structural check
// and the class check; Classify(c.Before) would give the same answer here
// (the fields Classify depends on — Layer/Type/Domain for a node, Type for
// an edge — are baked into the shared ID Changed matches on, so they are
// identical on both sides of a Changed pair; see diffMatchedNodes/
// diffMatchedEdges in diff.go, which rely on the same invariant).
func changedChanges(changed []Changed) []Change {
	var changes []Change
	for _, c := range changed {
		class := Classify(c.After)
		if class == ClassStructural || class != ClassContract {
			continue
		}
		changes = append(changes, Change{
			Kind: ContractChanged, SourceID: c.ID, Counterpart: "",
			Seed: seedHead, Facts: []Fact{c.After},
		})
	}
	return changes
}

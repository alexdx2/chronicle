package review

import (
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
)

// changeByKind finds the (unique-expected) Change of a given Kind in a
// ClassifyDelta result, so tests can assert on it without depending on
// output order.
func changeByKind(t *testing.T, changes []Change, kind ChangeKind) *Change {
	t.Helper()
	var found *Change
	for i := range changes {
		if changes[i].Kind == kind {
			if found != nil {
				t.Fatalf("changes = %+v, want at most one %s", changes, kind)
			}
			found = &changes[i]
		}
	}
	return found
}

// --- Row 1: Added/Removed behavioral edge -> IntegrationAdded/Removed ------

// TestClassify_IntegrationAddedRemoved_Sync: an Added CALLS_SERVICE edge and
// a Removed INJECTS edge — both ClassBehaviorSync — become a standalone
// IntegrationAdded/IntegrationRemoved each, SourceID/Counterpart from the
// edge's own From/To, Seed "head" for Added and "base" for Removed.
func TestClassify_IntegrationAddedRemoved_Sync(t *testing.T) {
	added := snapshot.Edge{ID: "svcA->svcB:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "svcA", To: "svcB"}
	removed := snapshot.Edge{ID: "svcC->svcD:INJECTS", Type: "INJECTS", From: "svcC", To: "svcD"}

	delta := &GraphDelta{
		Added:   []Fact{edgeFact(added.ID, added)},
		Removed: []Fact{edgeFact(removed.ID, removed)},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 2 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 2 changes", changes)
	}

	ia := changeByKind(t, changes, IntegrationAdded)
	if ia == nil || ia.SourceID != "svcA" || ia.Counterpart != "svcB" || ia.Seed != "head" {
		t.Errorf("IntegrationAdded = %+v, want SourceID=svcA Counterpart=svcB Seed=head", ia)
	}
	if ia != nil && (len(ia.Facts) != 1 || ia.Facts[0].ID != added.ID) {
		t.Errorf("IntegrationAdded.Facts = %+v, want exactly [%s]", ia.Facts, added.ID)
	}

	ir := changeByKind(t, changes, IntegrationRemoved)
	if ir == nil || ir.SourceID != "svcC" || ir.Counterpart != "svcD" || ir.Seed != "base" {
		t.Errorf("IntegrationRemoved = %+v, want SourceID=svcC Counterpart=svcD Seed=base", ir)
	}
	if ir != nil && (len(ir.Facts) != 1 || ir.Facts[0].ID != removed.ID) {
		t.Errorf("IntegrationRemoved.Facts = %+v, want exactly [%s]", ir.Facts, removed.ID)
	}
}

// TestClassify_IntegrationAddedRemoved_Async: same shape as the sync test,
// but with PUBLISHES_TOPIC/CONSUMES_TOPIC (ClassBehaviorAsync) edges whose
// target topic is NOT itself present in the same delta — so this exercises
// the ordinary standalone rule, not the new-topic merge rule (that's a
// separate test).
func TestClassify_IntegrationAddedRemoved_Async(t *testing.T) {
	added := snapshot.Edge{ID: "svcA->topic.orders:PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: "svcA", To: "topic.orders"}
	removed := snapshot.Edge{ID: "svcB->topic.invoices:CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: "svcB", To: "topic.invoices"}

	delta := &GraphDelta{
		Added:   []Fact{edgeFact(added.ID, added)},
		Removed: []Fact{edgeFact(removed.ID, removed)},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 2 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 2 changes", changes)
	}

	ia := changeByKind(t, changes, IntegrationAdded)
	if ia == nil || ia.SourceID != "svcA" || ia.Counterpart != "topic.orders" || ia.Seed != "head" {
		t.Errorf("IntegrationAdded = %+v, want SourceID=svcA Counterpart=topic.orders Seed=head", ia)
	}

	ir := changeByKind(t, changes, IntegrationRemoved)
	if ir == nil || ir.SourceID != "svcB" || ir.Counterpart != "topic.invoices" || ir.Seed != "base" {
		t.Errorf("IntegrationRemoved = %+v, want SourceID=svcB Counterpart=topic.invoices Seed=base", ir)
	}
}

// --- Rows 2/3: Touched behavioral edge -> Producer/ConsumerChanged ---------

// TestClassify_ProducerChanged: a Touched PUBLISHES_TOPIC edge (code around
// an unchanged publish relationship moved) becomes ProducerChanged,
// SourceID=publisher, Counterpart=topic, Seed="head".
func TestClassify_ProducerChanged(t *testing.T) {
	edge := snapshot.Edge{ID: "svcA->topic.orders:PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: "svcA", To: "topic.orders"}
	delta := &GraphDelta{
		Touched: []Touched{{ID: edge.ID, Kind: KindEdge, Fact: edgeFact(edge.ID, edge)}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change", changes)
	}
	c := changes[0]
	if c.Kind != ProducerChanged || c.SourceID != "svcA" || c.Counterpart != "topic.orders" || c.Seed != "head" {
		t.Errorf("Change = %+v, want ProducerChanged SourceID=svcA Counterpart=topic.orders Seed=head", c)
	}
	if len(c.Facts) != 1 || c.Facts[0].ID != edge.ID {
		t.Errorf("Facts = %+v, want exactly [%s]", c.Facts, edge.ID)
	}
}

// TestClassify_ConsumerChanged_Sync: a Touched CALLS_SERVICE edge
// (ClassBehaviorSync) becomes ConsumerChanged, SourceID=caller,
// Counterpart=callee, Seed="head".
func TestClassify_ConsumerChanged_Sync(t *testing.T) {
	edge := snapshot.Edge{ID: "svcA->svcB:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "svcA", To: "svcB"}
	delta := &GraphDelta{
		Touched: []Touched{{ID: edge.ID, Kind: KindEdge, Fact: edgeFact(edge.ID, edge)}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change", changes)
	}
	c := changes[0]
	if c.Kind != ConsumerChanged || c.SourceID != "svcA" || c.Counterpart != "svcB" || c.Seed != "head" {
		t.Errorf("Change = %+v, want ConsumerChanged SourceID=svcA Counterpart=svcB Seed=head", c)
	}
}

// TestClassify_ConsumerChanged_ConsumeTopic: a Touched CONSUMES_TOPIC edge
// (ClassBehaviorAsync, but the CONSUMES side) becomes ConsumerChanged too —
// only PUBLISHES_TOPIC gets ProducerChanged, CONSUMES_TOPIC groups with the
// sync callers under ConsumerChanged.
func TestClassify_ConsumerChanged_ConsumeTopic(t *testing.T) {
	edge := snapshot.Edge{ID: "svcB->topic.orders:CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: "svcB", To: "topic.orders"}
	delta := &GraphDelta{
		Touched: []Touched{{ID: edge.ID, Kind: KindEdge, Fact: edgeFact(edge.ID, edge)}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change", changes)
	}
	c := changes[0]
	if c.Kind != ConsumerChanged || c.SourceID != "svcB" || c.Counterpart != "topic.orders" || c.Seed != "head" {
		t.Errorf("Change = %+v, want ConsumerChanged SourceID=svcB Counterpart=topic.orders Seed=head", c)
	}
}

// --- Row 4/5: contract node Added/Removed/Changed --------------------------

// TestClassify_ContractAddedRemovedChanged_Standalone: a lone Added contract
// node, a lone Removed contract node, and a Changed data:model node (which
// is ClassContract per fact.go's contractDataNodeTypes) each become their
// own Contract{Added,Removed,Changed}, SourceID=the node, Counterpart="".
// None of these have a paired edge in the same delta, so the merge rule
// never applies here.
func TestClassify_ContractAddedRemovedChanged_Standalone(t *testing.T) {
	addedNode := snapshot.Node{ID: "contract:endpoint:d:new", Layer: "contract", Type: "endpoint"}
	removedNode := snapshot.Node{ID: "contract:endpoint:d:old", Layer: "contract", Type: "endpoint"}
	before := snapshot.Node{ID: "data:model:d:user", Layer: "data", Type: "model", Attrs: map[string]string{"v": "1"}}
	after := snapshot.Node{ID: "data:model:d:user", Layer: "data", Type: "model", Attrs: map[string]string{"v": "2"}}

	delta := &GraphDelta{
		Added:   []Fact{nodeFact(addedNode.ID, addedNode)},
		Removed: []Fact{nodeFact(removedNode.ID, removedNode)},
		Changed: []Changed{{
			ID: after.ID, Kind: KindNode, Fields: []string{"Attrs"},
			Before: nodeFact(before.ID, before), After: nodeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 3 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 3 changes", changes)
	}

	ca := changeByKind(t, changes, ContractAdded)
	if ca == nil || ca.SourceID != addedNode.ID || ca.Counterpart != "" || ca.Seed != "head" {
		t.Errorf("ContractAdded = %+v, want SourceID=%s Counterpart=\"\" Seed=head", ca, addedNode.ID)
	}

	cr := changeByKind(t, changes, ContractRemoved)
	if cr == nil || cr.SourceID != removedNode.ID || cr.Counterpart != "" || cr.Seed != "base" {
		t.Errorf("ContractRemoved = %+v, want SourceID=%s Counterpart=\"\" Seed=base", cr, removedNode.ID)
	}

	cc := changeByKind(t, changes, ContractChanged)
	if cc == nil || cc.SourceID != after.ID || cc.Counterpart != "" {
		t.Errorf("ContractChanged = %+v, want SourceID=%s Counterpart=\"\"", cc, after.ID)
	}
}

// --- Changed, non-contract: no finding --------------------------------

// TestClassify_ChangedNonContractNotAFinding: a Changed data:enum node
// (ClassData, not ClassContract) must not produce a Change — it's already
// accounted for in GraphDelta.Changed for JSON rendering, but it is not a
// review finding.
func TestClassify_ChangedNonContractNotAFinding(t *testing.T) {
	before := snapshot.Node{ID: "data:enum:d:status", Layer: "data", Type: "enum", Attrs: map[string]string{"v": "1"}}
	after := snapshot.Node{ID: "data:enum:d:status", Layer: "data", Type: "enum", Attrs: map[string]string{"v": "2"}}

	delta := &GraphDelta{
		Changed: []Changed{{
			ID: after.ID, Kind: KindNode, Fields: []string{"Attrs"},
			Before: nodeFact(before.ID, before), After: nodeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 0 {
		t.Errorf("ClassifyDelta = %+v, want empty (ClassData Changed fact is not a finding)", changes)
	}
}

// --- Merge rule: new topic / new endpoint arriving with its edge ----------

// TestClassify_MergeNewPublishWithNewTopic: a delta with an Added
// PUBLISHES_TOPIC edge A->T AND an Added node T (the topic itself is new in
// this same delta) must produce exactly ONE Change (IntegrationAdded),
// carrying both facts — not a separate ContractAdded for the topic node as
// well. The topic node is deliberately modeled as layer="contract" (which
// classifyNode alone would call ClassContract) so this test actually
// exercises the merge/consumption bookkeeping: a broken implementation that
// forgets to mark the node consumed would emit a second, spurious
// ContractAdded here.
func TestClassify_MergeNewPublishWithNewTopic(t *testing.T) {
	edge := snapshot.Edge{ID: "svcA->topic.new:PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: "svcA", To: "topic.new"}
	node := snapshot.Node{ID: "topic.new", Layer: "contract", Type: "topic"}

	delta := &GraphDelta{
		Added: []Fact{edgeFact(edge.ID, edge), nodeFact(node.ID, node)},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change (merge rule), not 2", changes)
	}
	c := changes[0]
	if c.Kind != IntegrationAdded || c.SourceID != "svcA" || c.Counterpart != "topic.new" || c.Seed != "head" {
		t.Errorf("Change = %+v, want IntegrationAdded SourceID=svcA Counterpart=topic.new Seed=head", c)
	}
	if len(c.Facts) != 2 {
		t.Fatalf("Facts = %+v, want exactly 2 (edge + node)", c.Facts)
	}
	gotIDs := map[string]bool{c.Facts[0].ID: true, c.Facts[1].ID: true}
	if !gotIDs[edge.ID] || !gotIDs[node.ID] {
		t.Errorf("Facts = %+v, want [%s, %s] in some order", c.Facts, edge.ID, node.ID)
	}
}

// TestClassify_MergeRemovedEndpointWithRemovedExpose: symmetric case for
// removal — a Removed EXPOSES_ENDPOINT edge A->E AND a Removed node E (the
// endpoint no longer exists at all) must produce exactly ONE Change
// (ContractRemoved) carrying both facts, not a second standalone
// ContractRemoved for the node.
func TestClassify_MergeRemovedEndpointWithRemovedExpose(t *testing.T) {
	edge := snapshot.Edge{ID: "svcA->contract:endpoint:d:old:EXPOSES_ENDPOINT", Type: "EXPOSES_ENDPOINT", From: "svcA", To: "contract:endpoint:d:old"}
	node := snapshot.Node{ID: "contract:endpoint:d:old", Layer: "contract", Type: "endpoint"}

	delta := &GraphDelta{
		Removed: []Fact{edgeFact(edge.ID, edge), nodeFact(node.ID, node)},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change (merge rule), not 2", changes)
	}
	c := changes[0]
	if c.Kind != ContractRemoved || c.SourceID != "svcA" || c.Counterpart != node.ID || c.Seed != "base" {
		t.Errorf("Change = %+v, want ContractRemoved SourceID=svcA Counterpart=%s Seed=base", c, node.ID)
	}
	if len(c.Facts) != 2 {
		t.Fatalf("Facts = %+v, want exactly 2 (edge + node)", c.Facts)
	}
	gotIDs := map[string]bool{c.Facts[0].ID: true, c.Facts[1].ID: true}
	if !gotIDs[edge.ID] || !gotIDs[node.ID] {
		t.Errorf("Facts = %+v, want [%s, %s] in some order", c.Facts, edge.ID, node.ID)
	}
}

// --- Structural facts are never classified, even if hand-fed -------------

// TestClassify_StructuralNeverClassified: ClassifyDelta must defend itself
// against a ClassStructural fact reaching Added directly (Diff itself would
// never put one there — see route() in diff.go — but this delta is
// hand-constructed to bypass Diff entirely, the same way the brief's other
// tests do). A CONTAINS edge placed straight into .Added must not produce
// any Change.
func TestClassify_StructuralNeverClassified(t *testing.T) {
	edge := snapshot.Edge{ID: "a->b:CONTAINS", Type: "CONTAINS", From: "a", To: "b"}
	delta := &GraphDelta{
		Added: []Fact{edgeFact(edge.ID, edge)},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 0 {
		t.Errorf("ClassifyDelta = %+v, want empty (CONTAINS is ClassStructural, must be skipped even in Added)", changes)
	}
}

// --- Judgment calls documented in task-10-report.md -----------------------

// TestClassify_AddedContractEdgeWithoutNewNode_NotAFinding: an Added
// EXPOSES_ENDPOINT edge whose target node is NOT itself part of this delta
// (the endpoint already existed; a second service just started exposing it
// too) does not match the merge rule (no paired Added node) and does not
// match row 1 either (EXPOSES_ENDPOINT is ClassContract, not
// ClassBehaviorSync/Async — row 1 is scoped to behavioral edges only). Per
// the table, this produces no Change. See task-10-report.md for the
// reasoning (a documented judgment call, not directly specified by the
// brief's table).
func TestClassify_AddedContractEdgeWithoutNewNode_NotAFinding(t *testing.T) {
	edge := snapshot.Edge{ID: "svcB->contract:endpoint:d:old:EXPOSES_ENDPOINT", Type: "EXPOSES_ENDPOINT", From: "svcB", To: "contract:endpoint:d:old"}
	delta := &GraphDelta{
		Added: []Fact{edgeFact(edge.ID, edge)},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 0 {
		t.Errorf("ClassifyDelta = %+v, want empty (standalone Added ClassContract edge has no row in the table)", changes)
	}
}

// TestClassify_ChangedContractEdge_IsAFinding: a Changed HAS_FIELD edge
// (ClassContract, an edge rather than a node) must still produce
// ContractChanged via the same unified Changed-handling as a Changed
// contract node — row 5 of the brief's table ("Changed узел или ребро...")
// explicitly covers edges too, not just row 4's node case.
func TestClassify_ChangedContractEdge_IsAFinding(t *testing.T) {
	before := snapshot.Edge{ID: "data:model:d:user->data:field:d:user.email:HAS_FIELD", Type: "HAS_FIELD", From: "data:model:d:user", To: "data:field:d:user.email"}
	after := before
	after.Active = false

	delta := &GraphDelta{
		Changed: []Changed{{
			ID: after.ID, Kind: KindEdge, Fields: []string{"Active"},
			Before: edgeFact(before.ID, before), After: edgeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change", changes)
	}
	c := changes[0]
	if c.Kind != ContractChanged || c.SourceID != after.ID {
		t.Errorf("Change = %+v, want ContractChanged SourceID=%s", c, after.ID)
	}
}

// --- Bug 2 (task-18-report.md): lifecycle transition inside Changed --------
//
// The real scan pipeline never hard-deletes a row it no longer finds — a
// full rescan soft-deletes it (store/edges.go's MarkStaleEdges: active=0 on
// the edge, its evidence staled; the node-status equivalent marks Status
// "stale"). Diff correctly buckets that as Changed (Active/Status are
// semantic fields, present on both sides), but before this fix
// changedChanges either mapped it to the wrong Kind (a removed contract fact
// became ContractChanged, hiding that anything disappeared) or to no Change
// at all (every other class: the generic handling only ever recognized
// ClassContract). These tests pin the fix: a lifecycle-shaped Changed fact
// must get the SAME Kind a genuinely-absent/newly-present fact of the same
// class would.

// TestClassify_LifecycleRemoved_BehaviorEdge is the exact shape of the
// reported bug: a CALLS_ENDPOINT edge (ClassBehaviorSync) whose Active flips
// true->false. Before this fix, changedChanges produced NO Change at all for
// this — classifyDelta's generic Changed handling only recognized
// ClassContract — so a removed integration silently vanished from the
// report.
func TestClassify_LifecycleRemoved_BehaviorEdge(t *testing.T) {
	before := snapshot.Edge{ID: "jerry.client->traps:CALLS_ENDPOINT", Type: "CALLS_ENDPOINT", From: "jerry.client", To: "traps", Active: true}
	after := before
	after.Active = false

	delta := &GraphDelta{
		Changed: []Changed{{
			ID: after.ID, Kind: KindEdge, Fields: []string{"Active"},
			Before: edgeFact(before.ID, before), After: edgeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change (a stale-marked integration must be reported)", changes)
	}
	c := changes[0]
	if c.Kind != IntegrationRemoved {
		t.Errorf("Change.Kind = %s, want IntegrationRemoved", c.Kind)
	}
	if c.SourceID != "jerry.client" || c.Counterpart != "traps" {
		t.Errorf("Change SourceID/Counterpart = %s/%s, want jerry.client/traps", c.SourceID, c.Counterpart)
	}
	if c.Seed != seedBase {
		t.Errorf("Change.Seed = %s, want base (who depended on it while it was still active)", c.Seed)
	}
	if len(c.Facts) != 1 || c.Facts[0].Edge == nil || !c.Facts[0].Edge.Active {
		t.Errorf("Change.Facts = %+v, want exactly the BASE (still-active) fact", c.Facts)
	}
}

// TestClassify_LifecycleAdded_BehaviorEdge is the symmetric restore: Active
// false->true is treated as IntegrationAdded, seeded from head.
func TestClassify_LifecycleAdded_BehaviorEdge(t *testing.T) {
	before := snapshot.Edge{ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b", Active: false}
	after := before
	after.Active = true

	delta := &GraphDelta{
		Changed: []Changed{{
			ID: after.ID, Kind: KindEdge, Fields: []string{"Active"},
			Before: edgeFact(before.ID, before), After: edgeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change", changes)
	}
	c := changes[0]
	if c.Kind != IntegrationAdded {
		t.Errorf("Change.Kind = %s, want IntegrationAdded", c.Kind)
	}
	if c.Seed != seedHead {
		t.Errorf("Change.Seed = %s, want head", c.Seed)
	}
	if len(c.Facts) != 1 || c.Facts[0].Edge == nil || !c.Facts[0].Edge.Active {
		t.Errorf("Change.Facts = %+v, want exactly the HEAD (now-active) fact", c.Facts)
	}
}

// TestClassify_LifecycleRemoved_ContractEdge: a Changed EXPOSES_ENDPOINT edge
// (ClassContract) going Active true->false must be ContractRemoved, not the
// generic ContractChanged the table's last row would otherwise give every
// Changed contract fact.
func TestClassify_LifecycleRemoved_ContractEdge(t *testing.T) {
	before := snapshot.Edge{ID: "ctrl->ep:EXPOSES_ENDPOINT", Type: "EXPOSES_ENDPOINT", From: "ctrl", To: "ep", Active: true}
	after := before
	after.Active = false

	delta := &GraphDelta{
		Changed: []Changed{{
			ID: after.ID, Kind: KindEdge, Fields: []string{"Active"},
			Before: edgeFact(before.ID, before), After: edgeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 || changes[0].Kind != ContractRemoved {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 ContractRemoved change", changes)
	}
}

// TestClassify_LifecycleRemoved_ContractNode: a Changed contract-layer node
// whose Status flips "active"->"stale" must be ContractRemoved, SourceID the
// node's own id, SEEDED FROM BASE — the same convention a genuinely-absent
// contract node's standalone Removed row already uses.
func TestClassify_LifecycleRemoved_ContractNode(t *testing.T) {
	before := snapshot.Node{ID: "contract:topic:d:battle-results", Layer: "contract", Type: "topic", Status: "active"}
	after := before
	after.Status = "stale"

	delta := &GraphDelta{
		Changed: []Changed{{
			ID: after.ID, Kind: KindNode, Fields: []string{"Status"},
			Before: nodeFact(before.ID, before), After: nodeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 change", changes)
	}
	c := changes[0]
	if c.Kind != ContractRemoved || c.SourceID != after.ID {
		t.Errorf("Change = %+v, want ContractRemoved SourceID=%s", c, after.ID)
	}
	if c.Seed != seedBase {
		t.Errorf("Change.Seed = %s, want base", c.Seed)
	}
}

// TestClassify_LifecycleAdded_ContractNode: the node-side symmetric restore,
// "stale" (or "deleted") -> "active" becomes ContractAdded, seeded from head.
func TestClassify_LifecycleAdded_ContractNode(t *testing.T) {
	for _, beforeStatus := range []string{"stale", "deleted"} {
		t.Run(beforeStatus, func(t *testing.T) {
			before := snapshot.Node{ID: "contract:topic:d:battle-results", Layer: "contract", Type: "topic", Status: beforeStatus}
			after := before
			after.Status = "active"

			delta := &GraphDelta{
				Changed: []Changed{{
					ID: after.ID, Kind: KindNode, Fields: []string{"Status"},
					Before: nodeFact(before.ID, before), After: nodeFact(after.ID, after),
				}},
			}

			changes := ClassifyDelta(delta)
			if len(changes) != 1 || changes[0].Kind != ContractAdded || changes[0].Seed != seedHead {
				t.Fatalf("ClassifyDelta = %+v, want exactly 1 ContractAdded change seeded from head", changes)
			}
		})
	}
}

// TestClassify_LifecycleIgnoresOtherStatuses: a node Status transition that
// is neither "active" on one side nor "stale"/"deleted" on the other (here,
// active -> "unknown") is not a lifecycle transition this function
// recognizes — it must fall through to the generic ContractChanged handling,
// not be silently dropped or misreported as a removal.
func TestClassify_LifecycleIgnoresOtherStatuses(t *testing.T) {
	before := snapshot.Node{ID: "contract:topic:d:t", Layer: "contract", Type: "topic", Status: "active"}
	after := before
	after.Status = "unknown"

	delta := &GraphDelta{
		Changed: []Changed{{
			ID: after.ID, Kind: KindNode, Fields: []string{"Status"},
			Before: nodeFact(before.ID, before), After: nodeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 1 || changes[0].Kind != ContractChanged {
		t.Fatalf("ClassifyDelta = %+v, want exactly 1 ContractChanged change (not a recognized lifecycle transition)", changes)
	}
}

// TestClassify_LifecycleDataAndFlowClassesUnaffected: ClassData/ClassFlow
// facts have no Added/Removed row in the table at all — a lifecycle flip on
// one of those must still produce no Change, exactly as before this fix.
func TestClassify_LifecycleDataAndFlowClassesUnaffected(t *testing.T) {
	before := snapshot.Edge{ID: "flow->prov:REQUIRES", Type: "REQUIRES", From: "flow", To: "prov", Active: true}
	after := before
	after.Active = false

	delta := &GraphDelta{
		Changed: []Changed{{
			ID: after.ID, Kind: KindEdge, Fields: []string{"Active"},
			Before: edgeFact(before.ID, before), After: edgeFact(after.ID, after),
		}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 0 {
		t.Errorf("ClassifyDelta = %+v, want empty (ClassFlow has no Added/Removed row in the table)", changes)
	}
}

// TestClassify_MovedNeverClassified: the table never mentions Moved at all
// (a fact whose evidence attachment changed but whose meaning didn't) — it
// must never produce a Change regardless of the fact's class.
func TestClassify_MovedNeverClassified(t *testing.T) {
	before := snapshot.Node{ID: "contract:endpoint:d:foo", Layer: "contract", Type: "endpoint"}
	after := before

	delta := &GraphDelta{
		Moved: []Moved{{ID: after.ID, Kind: KindNode, Before: nodeFact(before.ID, before), After: nodeFact(after.ID, after)}},
	}

	changes := ClassifyDelta(delta)
	if len(changes) != 0 {
		t.Errorf("ClassifyDelta = %+v, want empty (Moved is never classified)", changes)
	}
}

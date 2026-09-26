package review

import "github.com/alexdx2/chronicle-core/snapshot"

// FactKind distinguishes a Fact that wraps a graph node from one that wraps
// a graph edge.
type FactKind string

const (
	KindNode FactKind = "node"
	KindEdge FactKind = "edge"
)

// Fact is one graph fact — a node or an edge — identified by its snapshot id
// (node_key for a node, edge_key for an edge). Exactly one of Node/Edge is
// set, matching Kind.
type Fact struct {
	ID   string
	Kind FactKind
	Node *snapshot.Node
	Edge *snapshot.Edge
}

// FactClass groups a Fact into one of six semantic classes that change
// classification, impact analysis and report rendering (later tasks in this
// plan) reason about: does this fact describe synchronous behavior, async
// behavior, data access, a business flow, a service's contract with its
// consumers, or none of the above (structural — everything else).
type FactClass string

const (
	ClassBehaviorSync  FactClass = "behavior_sync"  // CALLS_SERVICE, CALLS_ENDPOINT, INJECTS
	ClassBehaviorAsync FactClass = "behavior_async" // PUBLISHES_TOPIC, CONSUMES_TOPIC
	ClassData          FactClass = "data"           // USES_MODEL, REFERENCES_MODEL, READS_FIELD, WRITES_FIELD
	ClassFlow          FactClass = "flow"           // TRIGGERS_FLOW, REQUIRES
	ClassContract      FactClass = "contract"       // layer=="contract" nodes; layer=="data" && type in {model,field}; EXPOSES_ENDPOINT, HAS_FIELD edges
	ClassStructural    FactClass = "structural"     // everything else, including registry.structural_edge_types
)

// Edge-type -> class tables. Deliberately independent of
// graph/viewmodel.edgeClasses: that table partitions edge types for diagram
// rendering (e.g. it groups CONTAINS with EXPOSES_ENDPOINT as "hierarchy",
// and its "data" group omits READS_FIELD/WRITES_FIELD entirely) — a
// different, differently-purposed partition of the same vocabulary. Reusing
// it here would misclassify facts against this package's own spec, so this
// table is built directly from the FactClass const block's own comments
// above instead (see task-9-report.md for the full reasoning).
var behaviorSyncEdgeTypes = map[string]bool{
	"CALLS_SERVICE":  true,
	"CALLS_ENDPOINT": true,
	"INJECTS":        true,
}

var behaviorAsyncEdgeTypes = map[string]bool{
	"PUBLISHES_TOPIC": true,
	"CONSUMES_TOPIC":  true,
}

var dataEdgeTypes = map[string]bool{
	"USES_MODEL":       true,
	"REFERENCES_MODEL": true,
	"READS_FIELD":      true,
	"WRITES_FIELD":     true,
}

var flowEdgeTypes = map[string]bool{
	"TRIGGERS_FLOW": true,
	"REQUIRES":      true,
}

var contractEdgeTypes = map[string]bool{
	"EXPOSES_ENDPOINT": true,
	"HAS_FIELD":        true,
}

// contractDataNodeTypes are the layer=="data" node types that count as
// contract facts (a data model's own shape is part of a service's contract
// with its consumers). Every other layer=="data" node type (enum, entity,
// relation, database, table, ...) stays ClassData — data:enum specifically
// is pinned by TestClassify_AllKinds per the brief.
var contractDataNodeTypes = map[string]bool{
	"model": true,
	"field": true,
}

// Classify assigns f to one of the six FactClass values, purely from its own
// Node/Edge fields — no registry.Registry parameter and no registry lookup.
//
// This is safe, not just convenient: registry.TraversalPolicy.IsStructural
// checks a data-driven, per-registry-instance set
// (registry.defaults.yaml's traversal_policy.structural_edge_types), but
// every edge type that set can actually contain either matches one of the
// five specific classes above already (HAS_FIELD -> ClassContract) or falls
// through to the ClassStructural default below with the same result a
// registry lookup would have given — so consulting the registry would add a
// dependency for no behavioral difference. See task-9-report.md for the one
// entry (HAS_FIELD) where this package's own partition intentionally
// disagrees with the registry's grouping, and why that's correct for this
// package's purpose.
func Classify(f Fact) FactClass {
	switch {
	case f.Edge != nil:
		return classifyEdgeType(f.Edge.Type)
	case f.Node != nil:
		return classifyNode(f.Node)
	default:
		return ClassStructural
	}
}

func classifyEdgeType(edgeType string) FactClass {
	switch {
	case behaviorSyncEdgeTypes[edgeType]:
		return ClassBehaviorSync
	case behaviorAsyncEdgeTypes[edgeType]:
		return ClassBehaviorAsync
	case dataEdgeTypes[edgeType]:
		return ClassData
	case flowEdgeTypes[edgeType]:
		return ClassFlow
	case contractEdgeTypes[edgeType]:
		return ClassContract
	default:
		return ClassStructural
	}
}

func classifyNode(n *snapshot.Node) FactClass {
	switch n.Layer {
	case "contract":
		return ClassContract
	case "data":
		if contractDataNodeTypes[n.Type] {
			return ClassContract
		}
		return ClassData
	default:
		return ClassStructural
	}
}

package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

func basePayload() ImportPayload {
	return ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "code:controller:test-domain:nodea",
				Layer:     "code",
				NodeType:  "controller",
				DomainKey: "test-domain",
				Name:      "NodeA",
			},
			{
				NodeKey:   "code:provider:test-domain:nodeb",
				Layer:     "code",
				NodeType:  "provider",
				DomainKey: "test-domain",
				Name:      "NodeB",
			},
		},
		Edges: []ImportEdge{
			{
				FromNodeKey:    "code:controller:test-domain:nodea",
				ToNodeKey:      "code:provider:test-domain:nodeb",
				EdgeType:       "INJECTS",
				DerivationKind: "hard",
				FromLayer:      "code",
				ToLayer:        "code",
			},
		},
		Evidence: []ImportEvidence{
			{
				TargetKind:       "node",
				NodeKey:          "code:controller:test-domain:nodea",
				SourceKind:       "file",
				ExtractorID:      "test-extractor",
				ExtractorVersion: "1.0.0",
			},
		},
	}
}

func TestImportAll(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	result, err := g.ImportAll(basePayload(), revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated != 2 {
		t.Errorf("NodesCreated = %d, want 2", result.NodesCreated)
	}
	if result.EdgesCreated != 1 {
		t.Errorf("EdgesCreated = %d, want 1", result.EdgesCreated)
	}
	if result.EvidenceCreated != 1 {
		t.Errorf("EvidenceCreated = %d, want 1", result.EvidenceCreated)
	}

	// Verify nodes persisted.
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestImportAllPartialAccept(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "code:controller:test-domain:valid",
				Layer:     "code",
				NodeType:  "controller",
				DomainKey: "test-domain",
				Name:      "Valid",
			},
			{
				NodeKey:   "code:badtype:test-domain:invalid",
				Layer:     "code",
				NodeType:  "badtype", // invalid
				DomainKey: "test-domain",
				Name:      "Invalid",
			},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll should not return error for partial accept: %v", err)
	}

	// Valid node written, invalid node rejected
	if result.NodesCreated != 1 {
		t.Errorf("NodesCreated = %d, want 1", result.NodesCreated)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("Rejected = %d, want 1", len(result.Rejected))
	}
	if result.Rejected[0].Kind != "node" {
		t.Errorf("Rejected[0].Kind = %s, want node", result.Rejected[0].Kind)
	}

	// Valid node should be persisted
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("expected 1 node persisted, got %d", len(nodes))
	}
}

func TestImportAllEdgeRejectedWithSuggestion(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "flow:use_case:test-domain:order",
				Layer:     "flow",
				NodeType:  "use_case",
				DomainKey: "test-domain",
				Name:      "PlaceOrder",
			},
			{
				NodeKey:   "flow:use_case:test-domain:payment",
				Layer:     "flow",
				NodeType:  "use_case",
				DomainKey: "test-domain",
				Name:      "ProcessPayment",
			},
		},
		Edges: []ImportEdge{
			{
				FromNodeKey: "flow:use_case:test-domain:order",
				ToNodeKey:   "flow:use_case:test-domain:payment",
				EdgeType:    "TRIGGERS_FLOW", // wrong: flow→flow should be TRANSITIONS_TO
			},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Nodes accepted, edge rejected
	if result.NodesCreated != 2 {
		t.Errorf("NodesCreated = %d, want 2", result.NodesCreated)
	}
	if result.EdgesCreated != 0 {
		t.Errorf("EdgesCreated = %d, want 0", result.EdgesCreated)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("Rejected = %d, want 1", len(result.Rejected))
	}

	rej := result.Rejected[0]
	if rej.Kind != "edge" {
		t.Errorf("Rejected[0].Kind = %s, want edge", rej.Kind)
	}
	if rej.Suggestion == nil {
		t.Fatal("expected suggestion for rejected edge")
	}
	if rej.Suggestion.To != "TRANSITIONS_TO" {
		t.Errorf("expected suggestion TRANSITIONS_TO, got %s", rej.Suggestion.To)
	}
}

func TestImportAllDryRun_Valid(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	result, err := g.ImportAllDryRun(basePayload(), revID)
	if err != nil {
		t.Fatalf("ImportAllDryRun: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid=true, got errors: %v", result.Errors)
	}
	if result.NodesValidated != 2 {
		t.Errorf("NodesValidated = %d, want 2", result.NodesValidated)
	}
	if result.EdgesValidated != 1 {
		t.Errorf("EdgesValidated = %d, want 1", result.EdgesValidated)
	}
	if result.EvidenceValidated != 1 {
		t.Errorf("EvidenceValidated = %d, want 1", result.EvidenceValidated)
	}

	// Nothing should be persisted
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("dry_run should not persist nodes, got %d", len(nodes))
	}
}

func TestImportAllDryRun_Invalid(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "flow:use_case:test-domain:placeorder",
				Layer:     "flow",
				NodeType:  "use_case",
				DomainKey: "test-domain",
				Name:      "PlaceOrder",
			},
		},
		Edges: []ImportEdge{
			{
				FromNodeKey:    "flow:use_case:test-domain:placeorder",
				ToNodeKey:      "code:provider:test-domain:orderservice",
				EdgeType:       "CALLS_SERVICE",
				DerivationKind: "hard",
				FromLayer:      "flow",
				ToLayer:        "code",
			},
		},
	}

	result, err := g.ImportAllDryRun(payload, revID)
	if err != nil {
		t.Fatalf("ImportAllDryRun: %v", err)
	}
	if result.Valid {
		t.Error("expected valid=false for invalid edge")
	}
	if len(result.Errors) == 0 {
		t.Error("expected errors")
	}
	if len(result.SuggestedFixes) == 0 {
		t.Error("expected suggested fixes")
	}

	// Check the fix suggests INVOKES or REQUIRES
	fix := result.SuggestedFixes[0]
	if fix.From != "CALLS_SERVICE" {
		t.Errorf("expected fix.From=CALLS_SERVICE, got %s", fix.From)
	}
	if fix.To != "INVOKES" && fix.To != "REQUIRES" && fix.To != "DEPENDS_ON" {
		t.Errorf("expected fix.To to be a valid flow edge type, got %s", fix.To)
	}

	// Nothing persisted
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("dry_run should not persist nodes, got %d", len(nodes))
	}
}

func TestImportAllDomainAlias(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	// Claude sends "domain" instead of "domain_key" — both should work
	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:  "data:model:test-domain:user",
				Layer:    "data",
				NodeType: "model",
				Domain:   "test-domain", // alias, not domain_key
				Name:     "User",
			},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll with domain alias: %v", err)
	}
	if result.NodesCreated != 1 {
		t.Errorf("NodesCreated = %d, want 1", result.NodesCreated)
	}
}

func TestImportAllEdgeDependencySource(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	payload := basePayload()
	payload.Edges[0].DependencySource = "manifest_peer"

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.EdgesCreated != 1 {
		t.Fatalf("EdgesCreated = %d, want 1", result.EdgesCreated)
	}

	got, err := g.store.GetEdgeByKey("code:controller:test-domain:nodea->code:provider:test-domain:nodeb:INJECTS")
	if err != nil {
		t.Fatalf("GetEdgeByKey: %v", err)
	}
	if got.DependencySource != "manifest_peer" {
		t.Errorf("DependencySource = %q, want manifest_peer", got.DependencySource)
	}
}

func TestImportAllIdempotent(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	payload := basePayload()

	// First import.
	r1, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll first: %v", err)
	}

	// Second import (same data).
	r2, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll second: %v", err)
	}

	// Counts returned should be the same (upsert is idempotent).
	if r1.NodesCreated != r2.NodesCreated {
		t.Errorf("NodesCreated differs: %d vs %d", r1.NodesCreated, r2.NodesCreated)
	}

	// Actual rows in DB should not be duplicated.
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes after double import, got %d", len(nodes))
	}

	edges, err := g.store.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge after double import, got %d", len(edges))
	}
}

func TestImportAutoEvidenceForFilelessNodes(t *testing.T) {
	g := setupGraphDefaults(t)
	revID, err := g.store.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	// One node, no file_path, no explicit evidence.
	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "code:provider:dom:phantomservice",
				Layer:     "code",
				NodeType:  "provider",
				DomainKey: "dom",
				Name:      "PhantomService",
			},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated != 1 {
		t.Fatalf("NodesCreated = %d, want 1", result.NodesCreated)
	}

	if n := countZeroEvidenceNodes(t, g.store); n != 0 {
		t.Fatalf("import left %d nodes without evidence", n)
	}
}

// TestImportAll_EvidenceAssertions verifies that import-payload evidence
// carrying an assertion (module identity claimed by an extractor, e.g. an
// import specifier) is plumbed through ImportAll into the stored evidence
// row, and that ListEvidenceByAssertionKind can retrieve it by assertion_kind
// alone (pro's package index has no domain to filter on).
func TestImportAll_EvidenceAssertions(t *testing.T) {
	g, _ := newTestGraph(t) // package's existing helper
	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:provider:d:consumer", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "consumer"},
			{NodeKey: "code:provider:d:auth-client", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "auth-client"},
		},
		Edges: []ImportEdge{{FromNodeKey: "code:provider:d:consumer", ToNodeKey: "code:provider:d:auth-client", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}},
		Evidence: []ImportEvidence{{
			TargetKind: "edge",
			EdgeKey:    "code:provider:d:consumer->code:provider:d:auth-client:INJECTS",
			SourceKind: "file", FilePath: "src/consumer.ts", ExtractorID: "t", ExtractorVersion: "1",
			Confidence:    0.9,
			Assertion:     `{"module":"@okeep/auth-client"}`,
			AssertionKind: "import_specifier",
		}},
	}
	revID, _ := g.Store().CreateRevision("d", "", "t", "manual", "full", "{}")
	if _, err := g.ImportAll(payload, revID); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	rows, err := g.Store().ListEvidenceByAssertionKind("import_specifier")
	if err != nil || len(rows) != 1 {
		t.Fatalf("want 1 import_specifier row, got %d err %v", len(rows), err)
	}
	if rows[0].Assertion != `{"module":"@okeep/auth-client"}` || rows[0].EdgeID == 0 {
		t.Fatalf("assertion/edge not persisted: %+v", rows[0])
	}
}

// Task 6 field defect #3: ImportAll reported evidence_created > 0 even when
// EVERY node in the payload was rejected — the explicit-evidence loop
// resolves node_key/edge_key against the store, not against this payload's
// own (rejected) rows, so it silently attached to nothing or, worse, to a
// pre-existing node sharing that key (see the un-stale regression below).
// The rule: evidence whose target key appears in result.Rejected is SKIPPED
// entirely — not created, not counted.
func TestImportAllEvidenceHonesty_AllNodesRejectedEvidenceNotCounted(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:test-domain:a", Layer: "bogus-layer", NodeType: "controller", DomainKey: "test-domain", Name: "A"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "node", NodeKey: "code:controller:test-domain:a", SourceKind: "file", ExtractorID: "t", ExtractorVersion: "1"},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated != 0 {
		t.Errorf("NodesCreated = %d, want 0", result.NodesCreated)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("Rejected = %d, want 1", len(result.Rejected))
	}
	if result.EvidenceCreated != 0 {
		t.Errorf("EvidenceCreated = %d, want 0 — the only evidence in the payload targets a node this same payload rejected", result.EvidenceCreated)
	}
}

// Control for the same rule: a mixed payload where ONE node is rejected and
// ONE is valid must still count the valid node's evidence — the honesty fix
// must not become a blanket evidence freeze on any payload that has a
// rejection in it.
func TestImportAllEvidenceHonesty_MixedPayloadOnlyValidNodeEvidenceCounts(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:test-domain:valid", Layer: "code", NodeType: "controller", DomainKey: "test-domain", Name: "Valid"},
			{NodeKey: "code:controller:test-domain:invalid", Layer: "bogus-layer", NodeType: "controller", DomainKey: "test-domain", Name: "Invalid"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "node", NodeKey: "code:controller:test-domain:valid", SourceKind: "file", ExtractorID: "t", ExtractorVersion: "1"},
			{TargetKind: "node", NodeKey: "code:controller:test-domain:invalid", SourceKind: "file", ExtractorID: "t", ExtractorVersion: "1"},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated != 1 {
		t.Errorf("NodesCreated = %d, want 1", result.NodesCreated)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("Rejected = %d, want 1", len(result.Rejected))
	}
	if result.EvidenceCreated != 1 {
		t.Errorf("EvidenceCreated = %d, want 1 (only the valid node's evidence)", result.EvidenceCreated)
	}
}

// Evidence targeting a key that is NOT part of this payload (a pre-existing
// node from an earlier import) keeps today's attach-by-key behavior — the
// honesty fix only withholds evidence from keys THIS payload itself rejected.
func TestImportAllEvidenceHonesty_PreexistingNodeNotInPayloadStillAttaches(t *testing.T) {
	g := setupGraph(t)
	revID1 := makeRevision(t, g)
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:test-domain:existing", Layer: "code", NodeType: "controller",
		DomainKey: "test-domain", Name: "Existing",
	}, revID1); err != nil {
		t.Fatalf("seed UpsertNode: %v", err)
	}
	revID2, err := g.store.CreateRevision("test-domain", "abc123", "def456", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev2: %v", err)
	}

	payload := ImportPayload{
		Evidence: []ImportEvidence{
			{TargetKind: "node", NodeKey: "code:controller:test-domain:existing", SourceKind: "file", ExtractorID: "t", ExtractorVersion: "1"},
		},
	}

	result, err := g.ImportAll(payload, revID2)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.EvidenceCreated != 1 {
		t.Errorf("EvidenceCreated = %d, want 1 — evidence for a node outside this payload keeps today's attach-by-key behavior", result.EvidenceCreated)
	}
}

// Regression for the exact field report: a rejected node upsert (conflicting
// node_type for an existing key) must not let explicit evidence for that
// same key attach to whatever node already lives under it — AddNodeEvidence
// resolves by key against the STORE, and RecalculateNodeTrust derives status
// from the evidence it finds, so an evidence attach can flip a stale node
// back to active even though this payload's write for that key was rejected.
// A session used exactly this side effect to un-stale nodes it never
// actually re-validated.
func TestImportAllEvidenceHonesty_RejectedNodeDoesNotUnstaleViaEvidence(t *testing.T) {
	g := setupGraph(t)
	revID1 := makeRevision(t, g)

	const key = "code:controller:test-domain:legacy"
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: key, Layer: "code", NodeType: "controller",
		DomainKey: "test-domain", Name: "Legacy",
	}, revID1); err != nil {
		t.Fatalf("seed UpsertNode: %v", err)
	}

	// Node not re-seen at rev2 — goes stale.
	revID2, err := g.store.CreateRevision("test-domain", "abc123", "def456", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev2: %v", err)
	}
	if _, err := g.store.MarkStaleNodes("test-domain", revID2); err != nil {
		t.Fatalf("MarkStaleNodes: %v", err)
	}
	before, err := g.store.GetNodeByKey(key)
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if before.Status != "stale" {
		t.Fatalf("fixture invalid: node status = %q before the honesty-under-test import, want stale", before.Status)
	}

	// rev3 payload redeclares the same key under a conflicting node_type
	// (immutable-fields mismatch -> UpsertNode rejects it) but also carries
	// explicit evidence "for" that key.
	revID3, err := g.store.CreateRevision("test-domain", "def456", "ghi789", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev3: %v", err)
	}
	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: key, Layer: "code", NodeType: "provider", DomainKey: "test-domain", Name: "Legacy"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "node", NodeKey: key, SourceKind: "file", ExtractorID: "t", ExtractorVersion: "1"},
		},
	}

	result, err := g.ImportAll(payload, revID3)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("Rejected = %d, want 1 (immutable node_type conflict)", len(result.Rejected))
	}
	if result.EvidenceCreated != 0 {
		t.Errorf("EvidenceCreated = %d, want 0", result.EvidenceCreated)
	}

	after, err := g.store.GetNodeByKey(key)
	if err != nil {
		t.Fatalf("GetNodeByKey after: %v", err)
	}
	if after.Status != "stale" {
		t.Errorf("node un-staled via evidence attached to a rejected node upsert: status = %q, want stale", after.Status)
	}
}

// Edge-side analogue of TestImportAllEvidenceHonesty_RejectedNodeDoesNot
// UnstaleViaEvidence. The rejectedEdgeKeys guard (graph/import.go,
// explicit-evidence loop and both auto-evidence loops) exists because
// AddEdgeEvidence resolves by key against the STORE, not the payload: if a
// STALE edge with the same key survived from an earlier revision, an
// evidence "attach" for a key THIS payload itself rejected would land on
// that stale edge, and RecalculateEdgeTrust -> UpdateEdgeTrust sets
// active=1 for any non-removed/non-contradicted status — un-staling an edge
// nobody re-validated this scan. Non-vacuous by construction: an explicit
// edge_key collision with a pre-existing stale edge (a bare "rejected, no
// collision" version of this test would pass even without the guard, since
// AddEdgeEvidence errors on a key nothing was ever stored under).
func TestImportAllEvidenceHonesty_RejectedEdgeEvidenceNotCounted(t *testing.T) {
	g := setupGraphDefaults(t)
	revID1 := makeRevision(t, g)

	const (
		fromKey  = "flow:use_case:test-domain:order"
		toKey    = "flow:use_case:test-domain:payment"
		edgeType = "TRANSITIONS_TO" // valid flow->flow edge type
	)
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: fromKey, Layer: "flow", NodeType: "use_case", DomainKey: "test-domain", Name: "PlaceOrder",
	}, revID1); err != nil {
		t.Fatalf("seed UpsertNode from: %v", err)
	}
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: toKey, Layer: "flow", NodeType: "use_case", DomainKey: "test-domain", Name: "ProcessPayment",
	}, revID1); err != nil {
		t.Fatalf("seed UpsertNode to: %v", err)
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: fromKey, ToNodeKey: toKey, EdgeType: edgeType, DerivationKind: "hard",
		FromLayer: "flow", ToLayer: "flow",
	}, revID1); err != nil {
		t.Fatalf("seed UpsertEdge: %v", err)
	}
	existingEdgeKey := validate.BuildEdgeKey(fromKey, toKey, edgeType)

	// Edge not re-seen at rev2 -> goes stale (active=0).
	revID2, err := g.store.CreateRevision("test-domain", "abc123", "def456", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev2: %v", err)
	}
	if _, err := g.store.MarkStaleEdges("test-domain", revID2); err != nil {
		t.Fatalf("MarkStaleEdges: %v", err)
	}
	before, err := g.store.GetEdgeByKey(existingEdgeKey)
	if err != nil {
		t.Fatalf("GetEdgeByKey: %v", err)
	}
	if before.Active {
		t.Fatalf("fixture invalid: edge active = true before the honesty-under-test import, want stale (inactive)")
	}

	// rev3 payload redeclares the SAME edge_key EXPLICITLY (a real payload
	// does this to reference a known edge) but with an edge_type the
	// registry has never heard of — UpsertEdge rejects it, yet the payload
	// also carries evidence "for" that exact key.
	revID3, err := g.store.CreateRevision("test-domain", "def456", "ghi789", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev3: %v", err)
	}
	payload := ImportPayload{
		Edges: []ImportEdge{
			{EdgeKey: existingEdgeKey, FromNodeKey: fromKey, ToNodeKey: toKey, EdgeType: "NOT_A_REAL_EDGE_TYPE"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "edge", EdgeKey: existingEdgeKey, SourceKind: "file", ExtractorID: "t", ExtractorVersion: "1"},
		},
	}

	result, err := g.ImportAll(payload, revID3)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.EdgesCreated != 0 {
		t.Errorf("EdgesCreated = %d, want 0", result.EdgesCreated)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Kind != "edge" {
		t.Fatalf("Rejected = %+v, want exactly 1 edge rejection", result.Rejected)
	}
	if result.EvidenceCreated != 0 {
		t.Errorf("EvidenceCreated = %d, want 0 — the only evidence in the payload targets an edge key this same payload rejected", result.EvidenceCreated)
	}

	after, err := g.store.GetEdgeByKey(existingEdgeKey)
	if err != nil {
		t.Fatalf("GetEdgeByKey after: %v", err)
	}
	if after.Active {
		t.Errorf("edge un-staled (active=true) via evidence attached to a rejected edge upsert, want still inactive (stale)")
	}
}

// Control for the same rule: evidence targeting an edge key that is NOT part
// of this payload (a pre-existing edge from an earlier import) keeps today's
// attach-by-key behavior — the honesty guard only withholds evidence from
// keys THIS payload itself rejected.
func TestImportAllEvidenceHonesty_PreexistingEdgeNotInPayloadStillAttaches(t *testing.T) {
	g := setupGraph(t)
	revID1 := makeRevision(t, g)

	const fromKey = "code:controller:test-domain:nodea"
	const toKey = "code:provider:test-domain:nodeb"
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: fromKey, Layer: "code", NodeType: "controller", DomainKey: "test-domain", Name: "NodeA",
	}, revID1); err != nil {
		t.Fatalf("seed UpsertNode from: %v", err)
	}
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: toKey, Layer: "code", NodeType: "provider", DomainKey: "test-domain", Name: "NodeB",
	}, revID1); err != nil {
		t.Fatalf("seed UpsertNode to: %v", err)
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: fromKey, ToNodeKey: toKey, EdgeType: "INJECTS", DerivationKind: "hard",
		FromLayer: "code", ToLayer: "code",
	}, revID1); err != nil {
		t.Fatalf("seed UpsertEdge: %v", err)
	}
	existingEdgeKey := validate.BuildEdgeKey(fromKey, toKey, "INJECTS")

	revID2, err := g.store.CreateRevision("test-domain", "abc123", "def456", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev2: %v", err)
	}

	payload := ImportPayload{
		Evidence: []ImportEvidence{
			{TargetKind: "edge", EdgeKey: existingEdgeKey, SourceKind: "file", ExtractorID: "t", ExtractorVersion: "1"},
		},
	}

	result, err := g.ImportAll(payload, revID2)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.EvidenceCreated != 1 {
		t.Errorf("EvidenceCreated = %d, want 1 — evidence for a pre-existing edge outside this payload keeps today's attach-by-key behavior", result.EvidenceCreated)
	}
}

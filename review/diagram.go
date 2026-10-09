package review

import (
	"fmt"
	"math"
	"sort"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

// defaultMaxNodes is DiagramOptions.MaxNodes' zero-value default (the
// brief's own "0 → 12").
const defaultMaxNodes = 12

// DiagramOptions configures CompactDiagram's own truncation. MaxNodes bounds
// how many distinct SERVICE/DOMAIN/EXTERNAL_SYSTEM systems (SystemRef.Kind
// service|domain|external_system) the diagram pins onto the view; 0 means
// the default, 12. It never bounds the un-collapsed contract (topic/
// endpoint) nodes CompactDiagram pins for context — see pinnedKeys' own doc
// comment for why those are a separate, never-truncated set.
type DiagramOptions struct{ MaxNodes int }

// DiagramResult is one rendered impact diagram: the ViewSpec CompactDiagram/
// FullDiagram built (exposed for a caller that wants to inspect or reuse
// it), viewmodel.RenderMermaid's own output for that Spec's View, and how
// many blast-radius systems the diagram could not show.
//
// Truncated started, in the brief, as a single-cause counter ("how many
// systems didn't fit because of MaxNodes"). Building this against the real
// viewmodel package surfaced a second, structurally distinct cause: a
// pinned key that resolves to a node in a domain OTHER than Scope.Domain is
// dropped by viewmodel.BuildView itself (ViewSpec is single-domain-scoped —
// see ScopeSpec's own doc comment — and Layout.Preset "custom" has no
// c1/c2/c3 boundary-neighbour fallback to draw it anyway), landing in the
// View's own Missing list instead of rendering. Rather than let that second
// cause fail silently (a real, reachable outcome of a multi-hop
// WhoMayBeAffected() blast radius — see task-12-report.md for an empirical
// reproduction), both CompactDiagram and FullDiagram fold both causes into
// this one field: a non-zero Truncated always means "some blast-radius
// system isn't on this diagram", for either reason, regardless of which of
// the two functions produced it. See each function's own doc comment for
// which of the two causes is actually reachable for it.
type DiagramResult struct {
	Spec      viewmodel.ViewSpec
	Mermaid   string
	Truncated int
	// MarksDropped is the number of marked systems left unmarked by
	// FullDiagramUnmarked; 0 otherwise.
	MarksDropped int
}

// severityMarkKind maps a ReviewFinding's Severity to the viewmodel Mark
// kind CompactDiagram/FullDiagram annotate its pin with — the brief's own
// closed table (Changed -> "changed", Affected -> "breaks", Informational
// -> "note"), all three already members of viewmodel.MarkKinds (marks.go)
// with no new registration needed. Severity Unknown and the zero value never
// reach here in practice (systemPins only ever visits Changed/Affected
// findings — see its own doc comment on why Informational, despite having a
// row in this table, is not currently reachable through pinnedKeys'
// construction); the branch is kept anyway for the table's own completeness
// and so a future WhoMayBeAffected() that also surfaces Informational needs
// no change here.
func severityMarkKind(sev Severity) string {
	switch sev {
	case SeverityChanged:
		return "changed"
	case SeverityAffected:
		return "breaks"
	case SeverityInformational:
		return "note"
	default:
		return ""
	}
}

// severityPriority ranks a systemPin's severity tier for CompactDiagram's
// MaxNodes truncation, per the brief's own stated order: changed(0) >
// affected(1) > informational(2), lower keeps first. Deliberately NOT
// viewmodel.MarkRank's rendering-strength order (breaks strongest ... note
// weakest) — a different, unrelated ranking for a different purpose (which
// distinct NODE survives a length cutoff, vs. which MARK wins when several
// compete for one already-drawn element); see task-12-report.md.
func severityPriority(sev Severity) int {
	switch sev {
	case SeverityChanged:
		return 0
	case SeverityAffected:
		return 1
	case SeverityInformational:
		return 2
	default:
		return 3
	}
}

// systemPin is one candidate for CompactDiagram/FullDiagram's Scope.Nodes: a
// SystemRef of Kind service|domain|external_system, the severity it came
// from (its Mark kind and its MaxNodes truncation tier), and — for a
// severity whose Path can vary in length (Affected) — that Path's length,
// the truncation tie-breaker within its own tier.
type systemPin struct {
	ref      SystemRef
	severity Severity
	pathLen  int
}

// systemPins collects pinnedKeys' first and third bullets — every distinct
// SystemRef-kind pin candidate for Scope.Nodes:
//
//   - SystemRef.Key of every r.ChangedRelationships() finding (tier
//     "changed" — the change's own origin system);
//   - SystemRef.Key of every r.WhoMayBeAffected() finding (tier "affected"
//     today — see below).
//
// Deduplicated by SystemRef.Key, keeping the FIRST tier that claims a key:
// since the "changed" loop runs first, a system that is both a change's own
// origin AND someone else's downstream keeps its "changed" tier/Mark, which
// is also exactly the precedence CompactDiagram's own truncation priority
// wants (changed outranks affected) — one dedup pass gives both for free.
// Findings with an empty SystemRef.Key (a Collapse that failed) are skipped
// rather than pinning an empty string.
//
// A note on "Informational", named in the brief's own parenthetical for the
// third bullet ("r.WhoMayBeAffected() (Affected/Informational)") and given
// its own Mark kind in the construction table: r.WhoMayBeAffected() (see
// result.go's own doc comment, and result_test.go/finding_test.go, which
// pin this precisely) flattens ONLY Severity Affected Downstream findings —
// Informational ones are a Downstream-only severity (attached, e.g., by
// finding.go's analyzeIntegration for a plain-sync integration change's
// counterpart) that neither of pinnedKeys' two named method calls ever
// surfaces. This function is built strictly from the two calls the brief
// names, so it never produces a tier "informational" pin today; the
// severity-keyed helpers above (severityMarkKind/severityPriority) still
// handle it, for the table's own completeness and forward-compatibility,
// but nothing currently exercises that branch. See task-12-report.md for
// the fuller reasoning and the alternative (reaching into every finding's
// own Downstream for stray Informational entries beyond what the brief's
// two named methods surface) this rejected.
func systemPins(r *ReviewResult) []systemPin {
	seen := make(map[string]bool)
	var out []systemPin
	for _, f := range r.ChangedRelationships() {
		if f.System.Key == "" || seen[f.System.Key] {
			continue
		}
		seen[f.System.Key] = true
		out = append(out, systemPin{ref: f.System, severity: SeverityChanged})
	}
	for _, f := range r.WhoMayBeAffected() {
		if f.System.Key == "" || seen[f.System.Key] {
			continue
		}
		seen[f.System.Key] = true
		out = append(out, systemPin{ref: f.System, severity: f.Severity, pathLen: len(f.Path)})
	}
	return out
}

// isTopicCounterpart reports whether f's own Counterpart is a topic node —
// pinnedKeys' second bullet's "на топике (не эндпоинте вызова)" condition —
// decided from the same edge fact classify.go/finding.go already used to
// build and branch on this finding: for IntegrationAdded/IntegrationRemoved/
// ProducerChanged/ConsumerChanged, Facts[0] is ALWAYS the touched/added/
// removed edge itself (classify.go's own construction — mergeNewNodeWithEdge,
// standaloneAddedOrRemoved and touchedChanges all put the edge fact first),
// so Facts[0].Edge.Type is exactly the signal to reuse, per Kind:
//
//   - IntegrationAdded/IntegrationRemoved: PUBLISHES_TOPIC only.
//     finding.go's analyzeIntegration explicitly treats a CONSUMES_TOPIC
//     edge like a plain sync dependency for these two Kinds (its own doc
//     comment: "which behaves like a plain sync dependency for this
//     purpose"), so CONSUMES_TOPIC does NOT count as "on a topic" here —
//     matching Analyze's own branch, not just the brief's prose.
//   - ProducerChanged: PUBLISHES_TOPIC — always true by classify.go's own
//     construction (touchedChanges), checked anyway for one uniform rule
//     instead of special-casing a Kind known to always satisfy it.
//   - ConsumerChanged: CONSUMES_TOPIC counts; CALLS_SERVICE/CALLS_ENDPOINT/
//     INJECTS (classify.go's ClassBehaviorSync trio — the brief's own "call
//     endpoint") do not.
//
// Any other Kind (ContractAdded/Removed/Changed) returns false: those never
// carry a topic/endpoint Counterpart in the sense this bullet means (their
// own Counterpart is always "" — see classify.go's doc comment).
func isTopicCounterpart(f ReviewFinding) bool {
	if len(f.Facts) == 0 || f.Facts[0].Edge == nil {
		return false
	}
	edgeType := f.Facts[0].Edge.Type
	switch f.Kind {
	case IntegrationAdded, IntegrationRemoved, ProducerChanged:
		return edgeType == "PUBLISHES_TOPIC"
	case ConsumerChanged:
		return edgeType == "CONSUMES_TOPIC"
	default:
		return false
	}
}

// contractPins collects pinnedKeys' second bullet: finding.Counterpart of
// every r.ChangedRelationships() finding whose relationship is "on a topic"
// (isTopicCounterpart) — the topic/endpoint node itself, deliberately NOT
// collapsed to the service that owns it, so mermaid draws it as its own
// intermediate node (the brief's own "battle-results" mockup example).
// These never collide with a systemPins key: a topic/endpoint node_key is
// never itself a service/domain/external_system SystemRef.Key, so
// pinnedKeys' "minus service endpoints" clause (Pin: pinnedKeys-минус-
// эндпоинты-сервисов — сервисные и доменные ключи уже входят через
// node_keys) is already satisfied by construction here, with no separate
// subtraction step needed. Deduplicated by key; order matches
// ChangedRelationships()'s own (Kind, then System.Key).
func contractPins(r *ReviewResult) []string {
	seen := make(map[string]bool)
	var out []string
	for _, f := range r.ChangedRelationships() {
		if f.Counterpart == "" || seen[f.Counterpart] || !isTopicCounterpart(f) {
			continue
		}
		seen[f.Counterpart] = true
		out = append(out, f.Counterpart)
	}
	return out
}

// pinnedKeys is task-12-brief.md's own pinnedKeys(r): every node_key
// CompactDiagram/FullDiagram ask viewmodel.BuildView to draw, systemPins'
// own keys (bullets 1+3) plus contractPins (bullet 2) — flattened into one
// set. buildDiagram itself works from the two typed halves directly
// (systemPins/contractPins), since ViewSpec draws that same distinction
// (Scope.Nodes vs Pin — see the context notes on spec.go); this flat form
// exists so a test can assert the rendered diagram never draws a node
// outside this exact set.
func pinnedKeys(r *ReviewResult) []string {
	pins := systemPins(r)
	out := make([]string, 0, len(pins)+4)
	for _, p := range pins {
		out = append(out, p.ref.Key)
	}
	return append(out, contractPins(r)...)
}

// truncateSystemPins keeps at most maxNodes entries of pins, ordered by
// severityPriority (changed > affected > informational) and, within the
// same tier, ascending Path length (systemPin.pathLen — the brief's own
// "по возрастанию длины Path", shorter paths kept first). Ties beyond that
// keep pins' own incoming order via a stable sort: systemPins' own
// construction is already deterministic (ChangedRelationships() sorted by
// Kind/System.Key, WhoMayBeAffected() sorted by System.Key). Returns the
// kept prefix (in priority order — buildDiagram sorts it again by key
// before handing it to ViewSpec, for a caller-friendly deterministic
// Scope.Nodes) and how many were cut from the tail.
func truncateSystemPins(pins []systemPin, maxNodes int) (kept []systemPin, cut int) {
	if len(pins) <= maxNodes {
		return pins, 0
	}
	ordered := make([]systemPin, len(pins))
	copy(ordered, pins)
	sort.SliceStable(ordered, func(i, j int) bool {
		if pi, pj := severityPriority(ordered[i].severity), severityPriority(ordered[j].severity); pi != pj {
			return pi < pj
		}
		return ordered[i].pathLen < ordered[j].pathLen
	})
	return ordered[:maxNodes], len(pins) - maxNodes
}

// systemMarks builds the Marks entries for pins (already deduplicated by
// key — see systemPins): one Mark per pin, kind from severityMarkKind. Nil
// when pins is empty, matching viewmodel.NormalizeMarks' own nil-friendly
// contract.
func systemMarks(pins []systemPin) map[string]viewmodel.Mark {
	if len(pins) == 0 {
		return nil
	}
	marks := make(map[string]viewmodel.Mark, len(pins))
	for _, p := range pins {
		marks[p.ref.Key] = viewmodel.Mark{Kind: severityMarkKind(p.severity)}
	}
	return marks
}

// missingPinCount counts how many of the keys buildDiagram asked
// viewmodel.BuildView to draw (nodeKeys, Scope.Nodes' own kept set; pinKeys,
// the un-truncated contract pins) came back in view.Missing instead of
// rendering — see DiagramResult.Truncated's own doc comment for why this is
// folded into CompactDiagram's Truncated rather than left silent.
func missingPinCount(v *viewmodel.View, nodeKeys, pinKeys []string) int {
	if len(v.Missing) == 0 {
		return 0
	}
	missing := make(map[string]bool, len(v.Missing))
	for _, k := range v.Missing {
		missing[k] = true
	}
	count := 0
	for _, k := range nodeKeys {
		if missing[k] {
			count++
		}
	}
	for _, k := range pinKeys {
		if missing[k] {
			count++
		}
	}
	return count
}

// buildDiagram is CompactDiagram and FullDiagram's shared builder.
// Scope.Domain comes from the first r.ChangedRelationships() finding's own
// System.Domain (that method's own sort order — "по порядку метода"); an
// empty ChangedRelationships() has no domain to pick and no changed system
// to diagram, so this returns an error rather than guessing one (a
// documented judgment call — see task-12-report.md).
//
// pinnedKeys' two typed halves land in ViewSpec's two matching fields:
// systemPins (truncated to maxNodes, priority-ordered) seed Scope.Nodes;
// contractPins — never truncated, see contractPins' own doc comment — go to
// Pin. Marks come from the KEPT systemPins only (a truncated-away pin gets
// no Mark either, consistent with it not being on the diagram at all).
//
// foldMissing controls whether missingPinCount is added into the returned
// Truncated. Both CompactDiagram and FullDiagram now pass true (fix round 1
// — see task-12-report.md's dated section): a "full" diagram that silently
// drops a cross-domain system while reporting Truncated==0 would misrepresent
// exactly the thing this field exists to be honest about. The parameter
// stays (rather than being inlined away) because the two callers still
// differ in maxNodes, and because a future caller might legitimately want
// the MaxNodes-only count back — but nothing in this package constructs one
// with foldMissing:false today.
func buildDiagram(r *ReviewResult, head *VersionGraph, maxNodes int, foldMissing, dropMarks bool) (*DiagramResult, error) {
	changed := r.ChangedRelationships()
	if len(changed) == 0 {
		return nil, fmt.Errorf("review: buildDiagram: no changed relationships to diagram")
	}
	domain := changed[0].System.Domain

	pins := systemPins(r)
	kept, cut := truncateSystemPins(pins, maxNodes)

	nodeKeys := make([]string, 0, len(kept))
	for _, p := range kept {
		nodeKeys = append(nodeKeys, p.ref.Key)
	}
	sort.Strings(nodeKeys)

	pinKeys := contractPins(r)
	sort.Strings(pinKeys)

	spec := viewmodel.ViewSpec{
		Scope:    viewmodel.ScopeSpec{Domain: domain, Nodes: nodeKeys},
		Group:    viewmodel.GroupSpec{By: "service"},
		Collapse: true,
		Layout:   viewmodel.LayoutSpec{Preset: "custom"},
		Pin:      pinKeys,
		Marks:    systemMarks(kept),
	}
	marksDropped := 0
	if dropMarks {
		marksDropped = len(spec.Marks)
		spec.Marks = nil
	}

	view, err := viewmodel.BuildView(head.Graph.Store(), spec)
	if err != nil {
		return nil, fmt.Errorf("review: buildDiagram: %w", err)
	}

	truncated := cut
	if foldMissing {
		truncated += missingPinCount(view, nodeKeys, pinKeys)
	}

	return &DiagramResult{
		Spec:      spec,
		Mermaid:   viewmodel.RenderMermaid(view),
		Truncated: truncated, MarksDropped: marksDropped,
	}, nil
}

// CompactDiagram renders r's blast radius as a compact mermaid diagram: the
// changed system(s), the intermediate contract node for a topic-shaped
// relationship, and every affected system — grouped by service, collapsed,
// bounded to o.MaxNodes distinct systems (0 -> 12). See DiagramResult's own
// doc comment for what Truncated counts.
func CompactDiagram(r *ReviewResult, head *VersionGraph, o DiagramOptions) (*DiagramResult, error) {
	maxNodes := o.MaxNodes
	if maxNodes <= 0 {
		maxNodes = defaultMaxNodes
	}
	return buildDiagram(r, head, maxNodes, true, false)
}

// FullDiagram renders the same view CompactDiagram does, with no MaxNodes
// cutoff (math.MaxInt): every system truncateSystemPins could ever cut stays
// in, so MaxNodes itself never removes anything here (truncateSystemPins(pins,
// math.MaxInt) always takes its len(pins)<=maxNodes early return, since
// len(pins) is an int and math.MaxInt is int's own maximum value — a
// tautology, not a coincidence of today's inputs; confirmed directly rather
// than assumed, see task-12-report.md's fix-round-1 section).
//
// Truncated is therefore 0 unless a pinned system falls outside this
// diagram's own chosen domain (Scope.Domain, from the first
// ChangedRelationships() finding) — the one remaining cause
// DiagramResult.Truncated documents, and the only one that can ever apply
// here now that MaxNodes is provably inert. This is FullDiagram's whole
// reason to exist: the un-abridged escape hatch a caller reaches for after
// CompactDiagram truncates, so it owes the same honesty CompactDiagram
// already gives about a blast-radius system it could not draw — a "full"
// diagram that silently under-reports would defeat the point of asking for
// it.
func FullDiagram(r *ReviewResult, head *VersionGraph) (*DiagramResult, error) {
	return buildDiagram(r, head, math.MaxInt, true, false)
}

// FullDiagramUnmarked is FullDiagram without the per-system marks, for the
// case where there are more marked systems than a view may carry
// (viewmodel.ErrTooManyMarks): the diagram still shows every system, just
// unmarked. MarksDropped reports how many marks were left out.
func FullDiagramUnmarked(r *ReviewResult, head *VersionGraph) (*DiagramResult, error) {
	return buildDiagram(r, head, math.MaxInt, true, true)
}

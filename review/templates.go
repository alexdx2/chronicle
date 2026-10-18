package review

import (
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

// This file is the one phrase table the brief asks for: every piece of
// human-facing English text RenderMarkdown builds from a ChangeKind/Reason,
// kept in one place so the wording is consistent and easy to review/change
// without hunting through render_markdown.go's own structural code.
//
// Every phrase that could carry graph-derived text (a system name, a
// counterpart, a file path) runs it through escapeText (viewmodel.TextEscape
// — see graph/viewmodel/text_export.go) before interpolating: the one
// escaping convention this whole renderer uses, per the brief.
//
// Only ONE concrete scenario is pinned byte-exact by the approved mockup
// (2026-09-25-review-report-mockup.html): producer changed on an existing
// topic (-> "the producer of `X` changed" / "`Y` publishes `X`" / "N
// consumers may be affected"), a brand-new topic with no subscribers yet
// (-> "new event, no consumers yet"), and a removed plain dependency whose
// callee loses its only caller (-> "informational `Y` loses a caller").
// Every other ChangeKind's phrase below (ConsumerChanged, ContractAdded/
// Removed/Changed, IntegrationAdded/Removed's OWN "gains a caller" mirror)
// is this task's own reasoned extrapolation, built to be the obvious
// parallel of the pinned wording — see task-13-report.md for the full list
// of these and why each reads the way it does.

// escapeText is this file's own name for viewmodel.TextEscape, used
// everywhere a display string might carry untrusted graph data.
func escapeText(s string) string { return viewmodel.TextEscape(s) }

// code wraps s in a markdown code span, after escaping it — the convention
// this renderer uses for every node/system/file name it prints (matching the
// mockup's own <code> treatment of names).
func code(s string) string {
	return "`" + escapeText(s) + "`"
}

// pluralize returns singular when n==1, plural otherwise — every count-based
// phrase in this file goes through this rather than hand-rolling an "s".
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// --- verdict headline & details (§9.1's <verdict>/<verdict details>) ------

// verdictHeadline is the normal report's own top-line sentence, templated
// purely from Verdict.SystemsAffected/Repositories (the brief's own
// instruction: "шаблон по числу SystemsAffected/Repositories"). The
// "across N repositories" clause only appears once there is more than one
// distinct repository in play — the mockup's own single example
// (SystemsAffected:3, Repositories:2) is the "across" branch; a
// same-repo review has nothing cross-repo worth flagging, so that clause
// is dropped rather than always printing "across 1 repository".
func verdictHeadline(v Verdict) string {
	if v.SystemsAffected == 0 {
		return "No systems appear to be affected."
	}
	sys := pluralize(v.SystemsAffected, "system", "systems")
	if v.Repositories <= 1 {
		return fmt.Sprintf("%d %s may be affected.", v.SystemsAffected, sys)
	}
	return fmt.Sprintf("%d %s may be affected across %d repositories.", v.SystemsAffected, sys, v.Repositories)
}

// verdictDetails is the <verdict details> line: every nonzero Verdict
// transport count, joined by " · ", exactly the brief's own instruction.
func verdictDetails(v Verdict) string {
	var parts []string
	if v.IntegrationsAdded > 0 {
		parts = append(parts, fmt.Sprintf("%d %s added", v.IntegrationsAdded, pluralize(v.IntegrationsAdded, "integration", "integrations")))
	}
	if v.DependenciesRemoved > 0 {
		parts = append(parts, fmt.Sprintf("%d %s removed", v.DependenciesRemoved, pluralize(v.DependenciesRemoved, "dependency", "dependencies")))
	}
	if v.ContractsChanged > 0 {
		parts = append(parts, fmt.Sprintf("%d %s changed", v.ContractsChanged, pluralize(v.ContractsChanged, "contract", "contracts")))
	}
	if v.UnknownFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d %s unknown impact", v.UnknownFiles, pluralize(v.UnknownFiles, "file has", "files have")))
	}
	return strings.Join(parts, " · ")
}

// --- Kind -> human label (the "Change" column) ----------------------------

// kindLabel is ChangedRelationships' "Change" column: note that
// IntegrationRemoved reads "dependency removed", not "integration removed"
// — the mockup's own word (and Verdict's own field name,
// DependenciesRemoved) for losing a dependency, kept symmetric with
// IntegrationAdded's "integration added" only because the mockup pins that
// exact asymmetry, not because the two Kinds are conceptually different.
func kindLabel(k ChangeKind) string {
	switch k {
	case IntegrationAdded:
		return "integration added"
	case IntegrationRemoved:
		return "dependency removed"
	case ProducerChanged:
		return "producer changed"
	case ConsumerChanged:
		return "consumer changed"
	case ContractAdded:
		return "contract added"
	case ContractRemoved:
		return "contract removed"
	case ContractChanged:
		return "contract changed"
	default:
		return string(k)
	}
}

// --- edge type -> verb -----------------------------------------------------

// edgeVerb turns a raw edge type into the verb the mockup's own prose uses
// ("arena-api publishes battle-results", "no longer calls jerry-api").
// Falls back to a lowercased, space-joined version of the type for any edge
// type this table doesn't name (defensive — every Kind this renderer builds
// a Relationship sentence for is seeded from one of the five named here, per
// finding.go/classify.go's own construction).
//
// The fallback branch is escaped (escapeText/viewmodel.TextEscape), unlike
// the five named branches above (static string literals, nothing to escape):
// edgeType is graph-derived text like every other display string this file
// builds, and every OTHER such string in this renderer goes through the same
// escaping — this one branch was the sole exception, caught in fix round 1
// (task-13-report.md). Not currently exploitable in practice (Task 1's
// snapshot decoder already validates edge types against the registry before
// a Fact is ever constructed, so a hostile edge-type string shouldn't reach
// here), but escaped anyway for consistency and defense in depth in case
// that upstream guarantee ever changes.
func edgeVerb(edgeType string) string {
	switch edgeType {
	case "PUBLISHES_TOPIC":
		return "publishes"
	case "CONSUMES_TOPIC":
		return "consumes"
	case "CALLS_SERVICE", "CALLS_ENDPOINT":
		return "calls"
	case "INJECTS":
		return "injects"
	case "EXPOSES_ENDPOINT":
		return "exposes"
	case "HAS_FIELD":
		return "has field"
	default:
		return escapeText(strings.ToLower(strings.ReplaceAll(edgeType, "_", " ")))
	}
}

// factEdgeType returns the edge type behind f's own originating fact — f's
// Facts[0] is always the touched/added/removed edge itself for every Kind
// that reaches this renderer's relationship/effect phrases (Integration*/
// Producer/ConsumerChanged; see finding.go's Analyze doc comment and
// diagram.go's isTopicCounterpart, which relies on the same invariant). ""
// for a Kind with no such edge (Contract*, seeded from a node or a HAS_FIELD
// edge with no verb of its own).
func factEdgeType(f ReviewFinding) string {
	if len(f.Facts) > 0 && f.Facts[0].Edge != nil {
		return f.Facts[0].Edge.Type
	}
	return ""
}

// factDisplayName is the best-effort human label for one Fact, used where a
// Kind (Contract*) has no Counterpart to hang a phrase on and must fall back
// to describing its own fact directly.
func factDisplayName(f Fact) string {
	switch {
	case f.Kind == KindNode && f.Node != nil:
		if f.Node.Name != "" {
			return f.Node.Name
		}
		return f.Node.QualifiedName
	case f.Kind == KindEdge && f.Edge != nil:
		return fmt.Sprintf("%s → %s", f.Edge.From, f.Edge.To)
	default:
		return ""
	}
}

// contractFactName picks the NODE fact out of f.Facts when there is one
// (the merged ContractAdded/Removed case, Facts=[edge,node] — see
// classify.go's merge rule) rather than the edge that merged with it, since
// the node's own Name is the contract's real display name; falls back to
// Facts[0] (a standalone contract node, or a changed contract EDGE such as
// HAS_FIELD) otherwise.
func contractFactName(f ReviewFinding) string {
	for _, fact := range f.Facts {
		if fact.Kind == KindNode {
			return factDisplayName(fact)
		}
	}
	if len(f.Facts) > 0 {
		return factDisplayName(f.Facts[0])
	}
	return f.System.Name
}

// --- "Changed system relationships" table: Relationship column -----------

// relationshipPhrase is ChangedRelationships' "Relationship" column: a
// one-line sentence built from the finding's own System (the change's
// origin), its Counterpart, and the underlying edge's verb. The two rows the
// mockup pins byte-exact are ProducerChanged ("`arena-api` publishes
// `battle-results`") and IntegrationRemoved on a plain call ("`arena-api` no
// longer calls `jerry-api`"); every other Kind's phrase is this function's
// own parallel construction — see this file's own top comment.
func relationshipPhrase(f ReviewFinding) string {
	sys := code(f.System.Name)
	edgeType := factEdgeType(f)

	switch f.Kind {
	case IntegrationAdded:
		if edgeType == "PUBLISHES_TOPIC" {
			return fmt.Sprintf("%s publishes %s", sys, code(f.Counterpart))
		}
		return fmt.Sprintf("%s now %s %s", sys, edgeVerb(edgeType), code(f.Counterpart))
	case IntegrationRemoved:
		if edgeType == "PUBLISHES_TOPIC" {
			return fmt.Sprintf("%s no longer publishes %s", sys, code(f.Counterpart))
		}
		return fmt.Sprintf("%s no longer %s %s", sys, edgeVerb(edgeType), code(f.Counterpart))
	case ProducerChanged:
		return fmt.Sprintf("%s %s %s", sys, edgeVerb(edgeType), code(f.Counterpart))
	case ConsumerChanged:
		return fmt.Sprintf("%s %s %s", sys, edgeVerb(edgeType), code(f.Counterpart))
	case ContractAdded:
		return fmt.Sprintf("%s exposes a new contract: %s", sys, code(contractFactName(f)))
	case ContractRemoved:
		return fmt.Sprintf("%s no longer exposes %s", sys, code(contractFactName(f)))
	case ContractChanged:
		return fmt.Sprintf("%s changes %s", sys, code(contractFactName(f)))
	default:
		return sys
	}
}

// --- "Changed system relationships" table: Effect column ------------------

func firstInformational(ds []ReviewFinding) *ReviewFinding {
	for i := range ds {
		if ds[i].Severity == SeverityInformational {
			return &ds[i]
		}
	}
	return nil
}

func countAffected(ds []ReviewFinding) int {
	n := 0
	for _, d := range ds {
		if d.Severity == SeverityAffected {
			n++
		}
	}
	return n
}

// informationalEffect is the "loses/gains a caller" half-sentence for an
// Informational Downstream entry — the mockup's own pinned wording
// ("jerry-api loses a caller") for ReasonProviderLostConsumer, and this
// function's own symmetric mirror for ReasonProviderGainsCaller.
func informationalEffect(info ReviewFinding) string {
	switch info.Reason {
	case ReasonProviderLostConsumer:
		return fmt.Sprintf("%s loses a caller", code(info.System.Name))
	case ReasonProviderGainsCaller:
		return fmt.Sprintf("%s gains a caller", code(info.System.Name))
	case ReasonNewSubscriptionNoPublisher:
		return "new subscription has no publisher"
	default:
		return fmt.Sprintf("%s affected", code(info.System.Name))
	}
}

// effectPhrase is ChangedRelationships' "Effect" column. Precedence:
//  1. ReasonNewTopicNoConsumers (Analyze's own degrade rule for a brand new
//     topic nobody subscribes to yet) — the mockup's own pinned "new event,
//     no consumers yet", regardless of Kind.
//  2. An Informational Downstream entry — tagged with an inline
//     `informational` marker, the brief's own "вложенный severity-тег для
//     Informational-эффектов".
//  3. One or more Affected Downstream entries — "N consumers may be
//     affected" for a topic-shaped relationship (isTopicCounterpart, the
//     SAME test diagram.go already uses to decide this), "N systems may be
//     affected" otherwise.
//  4. Nothing downstream at all (e.g. a Collapse failure zeroed both
//     halves) — a defensive fallback the mockup never exercises.
func effectPhrase(f ReviewFinding) string {
	if f.Reason == ReasonNewTopicNoConsumers {
		return "new event, no consumers yet"
	}
	if info := firstInformational(f.Downstream); info != nil {
		return "`informational` " + informationalEffect(*info)
	}
	if n := countAffected(f.Downstream); n > 0 {
		noun := pluralize(n, "system", "systems")
		if isTopicCounterpart(f) {
			noun = pluralize(n, "consumer", "consumers")
		}
		return fmt.Sprintf("%d %s may be affected", n, noun)
	}
	return "no further downstream impact"
}

// --- "Who may be affected" table: Why column ------------------------------

// whyPhrase is the "Who may be affected" table's "Why" column, keyed by the
// ORIGIN Changed finding's own Kind (copied onto every Downstream entry —
// see ReviewFinding.Kind's own doc comment) and, where available, its
// Counterpart's display name. The one mockup-pinned case is ProducerChanged
// ("the producer of `battle-results` changed"); the rest are this renderer's
// own parallel phrasing for Kinds the mockup's single example never reaches
// — see task-13-report.md.
func whyPhrase(kind ChangeKind, counterpart string) string {
	switch kind {
	case ProducerChanged:
		if counterpart != "" {
			return fmt.Sprintf("the producer of %s changed", code(counterpart))
		}
		return "the producer changed"
	case ConsumerChanged:
		return "a consumer this system depends on changed"
	case IntegrationAdded:
		if counterpart != "" {
			return fmt.Sprintf("a new integration with %s was added", code(counterpart))
		}
		return "a new integration was added"
	case IntegrationRemoved:
		if counterpart != "" {
			return fmt.Sprintf("the integration with %s was removed", code(counterpart))
		}
		return "an integration was removed"
	case ContractRemoved:
		return "a contract this system depends on was removed"
	case ContractChanged:
		return "a contract this system depends on changed"
	default:
		return "an upstream change may affect this system"
	}
}

// --- "Code changed, graph unchanged": per-fact note -----------------------

// codeChangedNote is the trailing note on a "Code changed, graph unchanged"
// line (CodeChangedGraphUnchanged only ever holds Kind ProducerChanged/
// ConsumerChanged findings with an empty Downstream — see its own doc
// comment on ReviewResult). ConsumerChanged's phrase ("caller-side change,
// nothing downstream") is the mockup's own pinned wording; ProducerChanged's
// is this function's own symmetric mirror for the one Kind the mockup never
// shows in this particular section.
func codeChangedNote(k ChangeKind) string {
	switch k {
	case ConsumerChanged:
		return "caller-side change, nothing downstream"
	case ProducerChanged:
		return "producer-side change, no subscribers"
	default:
		return "no downstream impact"
	}
}

// --- Evidence table ---------------------------------------------------------

// evidenceFactPhrase is the Evidence table's "Fact" column for one Fact
// belonging to finding f: "<from> <verb> <to>" for an edge fact, the node's
// own name for a node fact, suffixed " (removed)" when f's Kind is a
// removal — the mockup's own asymmetry (only the removed row gets a
// suffix; an added/touched fact reads as a plain present-tense sentence).
func evidenceFactPhrase(f ReviewFinding, fact Fact) string {
	suffix := ""
	if f.Kind == IntegrationRemoved || f.Kind == ContractRemoved {
		suffix = " (removed)"
	}
	switch {
	case fact.Kind == KindEdge && fact.Edge != nil:
		return fmt.Sprintf("%s %s %s%s", escapeText(fact.Edge.From), edgeVerb(fact.Edge.Type), escapeText(fact.Edge.To), suffix)
	case fact.Kind == KindNode && fact.Node != nil:
		name := fact.Node.Name
		if name == "" {
			name = fact.Node.QualifiedName
		}
		return escapeText(name) + suffix
	default:
		return escapeText(f.System.Name) + suffix
	}
}

// evidenceSourceText is the Evidence table's "Source" column: "<file>:<range>
// · blob <short-blob>", prefixed "was " for a removed fact's evidence (the
// mockup's own "was jerry.client.ts:31–36 · blob 88ab017" — the code no
// longer exists at head, so its evidence describes a past state).
func evidenceSourceText(ev EvidenceRef, removed bool) string {
	loc := escapeText(ev.File)
	if ev.Range != "" {
		loc += ":" + escapeText(ev.Range)
	}
	prefix := ""
	if removed {
		prefix = "was "
	}
	if ev.Blob != "" {
		return fmt.Sprintf("%s%s · blob %s", prefix, loc, escapeText(shortSHA(ev.Blob)))
	}
	return prefix + loc
}

// shortSHA truncates a git-style hex id to its conventional 7-character
// short form; left as-is if already shorter (defensive — every real blob/
// commit id is at least 7 hex characters, but a synthetic test id might not
// be).
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

package review

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/snapshot"
)

// MarkdownOptions configures RenderMarkdown's optional, caller-supplied
// extras: the reproduction command and two optional links for the footer,
// a table/list row cap, and (this task's own addition — see the paragraph
// below) the pre-rendered blast-radius diagrams.
//
// Compact and Full go beyond the brief's own literal 4-field struct
// (Command/JSONLink/OpenLink/MaxRows). This is a deliberate, flagged
// deviation, not an oversight — see task-13-report.md for the full
// reasoning, summarized here: Task 12's CompactDiagram/FullDiagram need a
// live *VersionGraph (specifically head.Graph.Store(), to run
// viewmodel.BuildView's queries) to produce a mermaid diagram. Neither
// ReviewResult (a plain, JSON-serializable value with no store handle) nor
// RenderMarkdown's own fixed signature (r *ReviewResult, o MarkdownOptions)
// carries one, and adding a *VersionGraph parameter to RenderMarkdown itself
// would break the "verbatim" signature the brief gives. The smallest fix
// that keeps RenderMarkdown a pure function of already-computed data (same
// contract as RenderJSON) is to let the caller — who already holds the
// VersionGraph, since it just called CompactDiagram/FullDiagram itself —
// thread the finished *DiagramResult through here. Both fields are
// nil-safe: a nil Compact/Full simply omits that diagram's section rather
// than erroring.
type MarkdownOptions struct {
	// Command is the reproduction command shown in the footer, e.g.
	// "chronicle review main...feat/critical-hits".
	Command string
	// JSONLink is an optional link to this same review's JSON artifact.
	JSONLink string
	// OpenLink is an optional "Open in Chronicle" link, also used (when set)
	// as the compact diagram's caption link.
	OpenLink string
	// MaxRows caps every table/list below that could otherwise grow
	// unboundedly; 0 means the default, 20. A cut list gets a trailing
	// "+N more" line.
	MaxRows int

	// Compact and Full are the pre-rendered blast-radius diagrams — see this
	// type's own top comment for why RenderMarkdown cannot build them
	// itself.
	Compact, Full *DiagramResult
}

// RenderMarkdown turns r into the Markdown text of a Chronicle review MR
// comment. Section order follows spec §9.1 / the approved mockup
// (2026-09-25-review-report-mockup.html) exactly.
//
// Three special-case states are decided BEFORE the normal report, each
// printing ONLY its own verdict block — no "Chronicle review" heading, no
// provenance line, no footer. This matches the mockup's own "States"
// gallery precisely: all three of its example threads (!43, !44, !45)
// render nothing but the verdict/sub-line/details shown below; see
// task-13-report.md for why that is treated as authoritative rather than
// incidental to the mockup's illustrative layout.
//
//  1. Missing-graph: r.Base.Origin or r.Head.Origin == OriginMissing. The
//     brief's own doc comment names only Head.Origin — this renderer checks
//     BOTH sides; see task-13-report.md for why (the mockup's own named
//     scenario, "!45 · add Chronicle graph", is actually a base-missing
//     case: the PR that adds the graph file necessarily has head.Origin ==
//     "snapshot", so a Head-only check would never fire for the one example
//     the brief cites).
//  2. No-system-level-change: r.Verdict.NoSystemChange.
//  3. Impact-unknown — this task's own 4th state, not in the brief's literal
//     three-way branch but named by its own commit message and pinned by a
//     fourth mockup thread ("!44 · new leaderboard filters") the brief's
//     prose never mentions. Triggered when changedFileCounts reports
//     covered == 0 (equivalently uncovered == total) AND uncovered > 0:
//     EVERY changed file is Unknown, so there is no relationship verdict to
//     compute at all.
//
//     Fix round 1 (task-13-report.md) tightened this from an earlier,
//     broader condition (len(r.ChangedRelationships())==0 &&
//     Verdict.UnknownFiles>0), which independent review caught firing on a
//     real, wrong input: a ReviewResult with real Touched findings (an inert
//     ConsumerChanged/ProducerChanged pair, empty Downstream — exactly
//     CodeChangedGraphUnchanged's own shape) PLUS one unrelated Unknown
//     file. ChangedRelationships() is empty for THAT input too (the two
//     Touched findings are excluded by its own filter, not because nothing
//     real happened), so the old condition fired and silently dropped two
//     legitimate findings with no rendering anywhere. covered==0 does not
//     have this failure mode: every ChangedRelationships() finding traces
//     back to a fact with real evidence in r.Delta, so covered==0 makes
//     ChangedRelationships() empty BY CONSTRUCTION (there is no fact left
//     for it to report), not as a coincidental side effect of its own
//     Producer/ConsumerChanged filter. See task-13-report.md's fix-round-1
//     section for the full derivation and the reviewer's repro; the same
//     mixed-shape input is now TestRenderMarkdown_MixedUnknownAndTouched,
//     pinned to fall through to the NORMAL report instead.
//
// Otherwise: the ordinary full report.
func RenderMarkdown(r *ReviewResult, o MarkdownOptions) (string, error) {
	if r.Base.Origin == OriginMissing || r.Head.Origin == OriginMissing {
		return renderMissingGraph(r, o), nil
	}
	if r.Verdict.NoSystemChange {
		return renderNoSystemChange(r, o), nil
	}
	if covered, uncovered, _ := changedFileCounts(r); covered == 0 && uncovered > 0 {
		return renderImpactUnknown(r, o), nil
	}
	return renderNormal(r, o)
}

func resolveMaxRows(n int) int {
	if n <= 0 {
		return 20
	}
	return n
}

// --- special case 1: missing graph -----------------------------------------

// renderMissingGraph is the mockup's "!45 · add Chronicle graph" state,
// verbatim. ref names whichever side actually lacks graph history: Base.Ref
// when Base.Origin is the missing one (the named scenario — a PR introducing
// the graph, base/main never had it), else Head.Ref (the rarer case: head
// alone predates the graph, e.g. reviewing an older revision against an
// already-adopted base). This interpolation is this task's own judgment call
// — the mockup's literal "main" reads as a placeholder, not a hardcoded
// constant, and the project's own emphasis on honest, specific provenance
// (elsewhere in this plan) argues for naming the real ref when one is
// available; see task-13-report.md.
func renderMissingGraph(r *ReviewResult, _ MarkdownOptions) string {
	ref := r.Base.Ref
	if r.Base.Origin != OriginMissing {
		ref = r.Head.Ref
	}
	if ref == "" {
		ref = "the working tree"
	}

	var b strings.Builder
	b.WriteString("**This change introduces the Chronicle graph.**\n\n")
	fmt.Fprintf(&b, "No graph version exists in the history of %s yet.\n\n", escapeText(ref))
	b.WriteString("Impact analysis starts with the next change. Later commits need no new snapshot: Chronicle uses the nearest graph version in history and reports what changed since.\n")
	return b.String()
}

// --- special case 2: no system-level change --------------------------------

// renderNoSystemChange is the mockup's "!43 · refactor battle guard" state:
// verdict + sub-line naming the real changed-file count (via
// changedFileCounts — uncovered is guaranteed 0 here, since NoSystemChange
// requires Verdict.UnknownFiles == 0, so total == covered), then the
// collapsed "Code changed, graph unchanged" section if CodeChangedGraphUnchanged()
// is non-empty.
//
// No footer here, despite the brief's own prose listing "подпись" (a
// signature/footer line) as part of this state's content — checked directly
// against the approved mockup's own "!43" thread (fix round 1,
// task-13-report.md) rather than trusted from the brief's paraphrase, and
// the mockup shows no footer at all for this state. Same class of
// brief-vs-mockup conflict as the missing-graph Base-or-Head check and the
// "Code changed, graph unchanged" bullet format above: the approved mockup
// is what the owner actually signed off on, so it wins over the brief's own
// secondary prose description of it.
func renderNoSystemChange(r *ReviewResult, o MarkdownOptions) string {
	_, _, total := changedFileCounts(r)

	var b strings.Builder
	b.WriteString("**No system-level change.**\n\n")
	fmt.Fprintf(&b, "All %d changed %s covered by a fresh graph. No relationships added or removed.\n",
		total, pluralize(total, "file is", "files are"))

	if sec := codeChangedGraphUnchangedSection(r, resolveMaxRows(o.MaxRows)); sec != "" {
		b.WriteString("\n")
		b.WriteString(sec)
	}
	return b.String()
}

// --- special case 3: impact unknown (this task's own 4th state) -----------

// renderImpactUnknown is the mockup's "!44 · new leaderboard filters" state.
// The verdict and sub-line reproduce the mockup's own wording exactly
// ("N of N changed files changed after their last scan"); the mockup's
// further "Relationships whose code was touched:" bulleted list is
// deliberately NOT reproduced — see this file's RenderMarkdown doc comment
// and task-13-report.md for why: given THIS branch's own (fix-round-1
// tightened) trigger condition, covered == 0, r.Delta.Touched really is
// always empty — a non-empty Touched entry always carries evidence for some
// file (coveredFiles' own construction), which would make that file
// "covered" and contradict covered == 0. (This claim was written down but
// NOT actually true under the original, broader trigger condition fix round
// 1 replaced — see task-13-report.md's fix-round-1 section; it only holds
// now that the trigger is covered == 0 itself.) The mockup's example list is
// also not reconstructible from data ReviewResult exposes anyway (it would
// need "every edge whose evidence names a now-stale file", a broader query
// than anything Diff/ClassifyDelta compute) — so even with an empty
// Delta.Touched this section stays deliberately narrower than the mockup's
// own illustration.
func renderImpactUnknown(r *ReviewResult, _ MarkdownOptions) string {
	_, uncovered, total := changedFileCounts(r)

	var b strings.Builder
	b.WriteString("**Impact unknown.**\n\n")
	fmt.Fprintf(&b, "%d of %d changed %s changed after %s last scan.\n\n",
		uncovered, total, pluralize(total, "file", "files"), pluralize(total, "its", "their"))
	b.WriteString("Scan the changed files and run `chronicle graph update`.\n")
	return b.String()
}

// --- normal report ----------------------------------------------------------

func renderNormal(r *ReviewResult, o MarkdownOptions) (string, error) {
	maxRows := resolveMaxRows(o.MaxRows)

	var b strings.Builder
	b.WriteString("### Chronicle review\n\n")

	if line := provenanceLine(r); line != "" {
		b.WriteString(line)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "**%s**\n\n", verdictHeadline(r.Verdict))
	if details := verdictDetails(r.Verdict); details != "" {
		b.WriteString(details)
		b.WriteString("\n\n")
	}

	if o.Compact != nil {
		b.WriteString("```mermaid\n")
		b.WriteString(strings.TrimRight(o.Compact.Mermaid, "\n"))
		b.WriteString("\n```\n")
		b.WriteString(diagramCaption(o))
		b.WriteString("\n\n")
	}

	if sec := whoMayBeAffectedSection(r, maxRows); sec != "" {
		b.WriteString("### Who may be affected\n\n")
		b.WriteString(sec)
		b.WriteString("\n")
	}

	if sec := changedRelationshipsSection(r, maxRows); sec != "" {
		b.WriteString("### Changed system relationships\n\n")
		b.WriteString(sec)
		b.WriteString("\n")
	}

	if sec := unknownImpactSection(r, maxRows); sec != "" {
		b.WriteString(sec)
		b.WriteString("\n")
	}

	if sec := codeChangedGraphUnchangedSection(r, maxRows); sec != "" {
		b.WriteString(sec)
		b.WriteString("\n")
	}

	if sec := evidenceSection(r, maxRows); sec != "" {
		b.WriteString(sec)
		b.WriteString("\n")
	}

	b.WriteString(fullDiagramSection(r, o))
	b.WriteString("\n")

	b.WriteString(footer(o))

	return b.String(), nil
}

// diagramCaption is the compact diagram's own caption line. The mockup's
// literal text names a level count ("Full impact graph (5 levels)") that
// ReviewResult has nowhere to source honestly: the BFS depth Analyze used is
// a caller-supplied parameter to Review, never recorded on ReviewResult
// itself (Verdict/Provenance/Delta/Findings — none of them carry it). Rather
// than hardcode a number that could silently be wrong, this omits the level
// count entirely — a documented, deliberate gap; see task-13-report.md.
func diagramCaption(o MarkdownOptions) string {
	full := "Chronicle"
	if o.OpenLink != "" {
		full = fmt.Sprintf("[Chronicle](%s)", o.OpenLink)
	}
	caption := fmt.Sprintf("Compact view: changed systems, what they touch, directly affected systems. Full impact graph in %s.", full)
	if o.Compact != nil && o.Compact.Truncated > 0 {
		caption += fmt.Sprintf(" %d more %s not shown here.", o.Compact.Truncated, pluralize(o.Compact.Truncated, "system", "systems"))
	}
	return "*" + caption + "*"
}

// --- "Who may be affected" ---------------------------------------------------

// affectedRow is one "Who may be affected" table row, already resolved to
// display strings.
type affectedRow struct {
	System SystemRef
	Why    string
	Path   []string
}

// whoMayBeAffectedRows builds the table's rows from r.WhoMayBeAffected(),
// cross-referencing each entry back to its ORIGIN top-level Changed finding
// (via findOrigin) to recover the two pieces of context a bare Downstream
// finding does not itself carry — Counterpart (for the Why phrase) and the
// origin system's own Name (to open the Path column, since SystemImpact's
// own Path never includes its own seed — see impact.go's
// pathStepsFromEntry doc comment).
func whoMayBeAffectedRows(r *ReviewResult) []affectedRow {
	entries := r.WhoMayBeAffected()
	rows := make([]affectedRow, 0, len(entries))
	for _, d := range entries {
		originName, counterpart := findOrigin(r, d)

		path := make([]string, 0, len(d.Path)+2)
		if originName != "" {
			path = append(path, originName)
		}
		for _, step := range d.Path {
			path = append(path, step.Node)
		}
		path = append(path, d.System.Name)

		rows = append(rows, affectedRow{System: d.System, Why: whyPhrase(d.Kind, counterpart), Path: path})
	}
	return rows
}

// findOrigin recovers the top-level Changed finding that produced Downstream
// entry d, by identity (System.Key + Kind + Path all equal — d is a COPY of
// exactly one such entry, so this is an exact match, not a heuristic). Two
// fields ReviewFinding's own doc comment says are "set only on the top-level
// Changed finding" — Counterpart and the origin's own System.Name — live
// only on that parent, never on d itself, which is exactly why this lookup
// exists: see task-13-report.md for the fuller explanation of this gap
// between what WhoMayBeAffected() returns and what the mockup's own "the
// producer of `battle-results` changed" wording needs.
func findOrigin(r *ReviewResult, d ReviewFinding) (originName, counterpart string) {
	for _, f := range r.Findings {
		if f.Severity != SeverityChanged {
			continue
		}
		for _, dd := range f.Downstream {
			if dd.Severity == SeverityAffected && dd.System.Key == d.System.Key && dd.Kind == d.Kind && pathEqual(dd.Path, d.Path) {
				return f.System.Name, f.Counterpart
			}
		}
	}
	return "", ""
}

func pathEqual(a, b []PathStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// homeRepo is the "local" repository a cross-repo affected system is
// compared against, for the "<repo> repository" sub-label: the first
// non-empty Repo among the review's own changed systems (ChangedRelationships'
// own sort order picks a deterministic one when several changed systems
// disagree, which real single-review data never does in practice).
func homeRepo(r *ReviewResult) string {
	for _, f := range r.ChangedRelationships() {
		if f.System.Repo != "" {
			return f.System.Repo
		}
	}
	return ""
}

func whoMayBeAffectedSection(r *ReviewResult, maxRows int) string {
	rows := whoMayBeAffectedRows(r)
	if len(rows) == 0 {
		return ""
	}
	home := homeRepo(r)

	table := make([]mdRow, 0, len(rows))
	for _, row := range rows {
		sysCell := "**" + escapeText(row.System.Name) + "**"
		if row.System.Repo != "" && row.System.Repo != home {
			sysCell += "<br>" + escapeText(row.System.Repo) + " repository"
		}
		pathCell := strings.Join(escapeAll(row.Path), " → ")
		table = append(table, mdRow{sysCell, row.Why, pathCell})
	}
	return mdTable([]string{"System", "Why", "Path"}, table, maxRows)
}

func escapeAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = escapeText(s)
	}
	return out
}

// --- "Changed system relationships" -----------------------------------------

func changedRelationshipsSection(r *ReviewResult, maxRows int) string {
	findings := r.ChangedRelationships()
	if len(findings) == 0 {
		return ""
	}
	rows := make([]mdRow, 0, len(findings))
	for _, f := range findings {
		rows = append(rows, mdRow{kindLabel(f.Kind), relationshipPhrase(f), effectPhrase(f)})
	}
	return mdTable([]string{"Change", "Relationship", "Effect"}, rows, maxRows)
}

// --- "Unknown impact" --------------------------------------------------------

// unknownFiles is the deduplicated, sorted set of file paths carrying at
// least one Severity Unknown finding. Deduplicated because a single file can
// carry TWO Unknown findings (ReasonHeadStaleFile and ReasonBaseStaleFile —
// see Review's own doc comment, "a rare case"); this renderer wants one line
// per FILE, not one per finding.
func unknownFiles(r *ReviewResult) []string {
	seen := make(map[string]bool)
	var files []string
	for _, f := range r.Findings {
		if f.Severity == SeverityUnknown && !seen[f.File] {
			seen[f.File] = true
			files = append(files, f.File)
		}
	}
	sort.Strings(files)
	return files
}

func unknownImpactSection(r *ReviewResult, maxRows int) string {
	files := unknownFiles(r)
	if len(files) == 0 {
		return ""
	}

	kept, more := files, 0
	if maxRows > 0 && len(files) > maxRows {
		kept = files[:maxRows]
		more = len(files) - maxRows
	}

	var b strings.Builder
	fmt.Fprintf(&b, "#### Unknown impact (%d %s)\n\n", len(files), pluralize(len(files), "file", "files"))
	if len(kept) == 1 {
		fmt.Fprintf(&b, "%s changed after its last scan. Chronicle does not know what the new code does. Scan it and run `chronicle graph update`.\n",
			code(kept[0]))
	} else {
		for _, f := range kept {
			fmt.Fprintf(&b, "- %s\n", code(f))
		}
		b.WriteString("\nThese changed after their last scan. Chronicle does not know what the new code does. Scan them and run `chronicle graph update`.\n")
	}
	if more > 0 {
		fmt.Fprintf(&b, "\n*+%d more*\n", more)
	}
	return b.String()
}

// --- "Code changed, graph unchanged" ----------------------------------------

func codeChangedGraphUnchangedSection(r *ReviewResult, maxRows int) string {
	findings := r.CodeChangedGraphUnchanged()
	if len(findings) == 0 {
		return ""
	}

	var lines []string
	for _, f := range findings {
		for _, fact := range f.Facts {
			lines = append(lines, codeChangedLine(f, fact))
		}
	}
	kept, more := lines, 0
	if maxRows > 0 && len(lines) > maxRows {
		kept = lines[:maxRows]
		more = len(lines) - maxRows
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<details>\n<summary>Code changed, graph unchanged (%d)</summary>\n\n", len(findings))
	for _, l := range kept {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	if more > 0 {
		fmt.Fprintf(&b, "\n*+%d more*\n", more)
	}
	b.WriteString("\n</details>\n")
	return b.String()
}

// codeChangedLine is one "Code changed, graph unchanged" line:
// "`<from>` → `<to>` (<verb>) · <file>:<range> · <note>". File/Range come
// from the fact's own first Evidence entry — already canonically sorted by
// id (Task 1's snapshot.Normalize, applied at export time), so index [0] is
// correct with no re-sorting here — per the brief.
func codeChangedLine(f ReviewFinding, fact Fact) string {
	from, to, verb := "", "", ""
	var evs []snapshot.Evidence
	switch {
	case fact.Kind == KindEdge && fact.Edge != nil:
		from, to, verb = fact.Edge.From, fact.Edge.To, edgeVerb(fact.Edge.Type)
		evs = fact.Edge.Evidence
	case fact.Kind == KindNode && fact.Node != nil:
		evs = fact.Node.Evidence
	}

	loc := ""
	if len(evs) > 0 {
		loc = escapeText(evs[0].File)
		if evs[0].Range != "" {
			loc += ":" + escapeText(evs[0].Range)
		}
	}

	return fmt.Sprintf("%s → %s (%s) · %s · %s", code(from), code(to), verb, loc, codeChangedNote(f.Kind))
}

// --- Evidence ----------------------------------------------------------------

// evidenceSection scopes its rows to r.ChangedRelationships() — NOT every
// Severity Changed finding in r.Findings. This is confirmed against the
// mockup's own concrete example rather than assumed: its "Evidence (3
// facts)" table lists exactly the 3 facts behind its "Changed system
// relationships" rows, deliberately omitting the fact behind that same
// example's "Code changed, graph unchanged (1)" entry (also Severity
// Changed) — the Evidence appendix backs the relationships table shown
// above it, not every touched-but-inert fact too. See task-13-report.md.
func evidenceSection(r *ReviewResult, maxRows int) string {
	var rows []mdRow
	for _, f := range r.ChangedRelationships() {
		removed := f.Kind == IntegrationRemoved || f.Kind == ContractRemoved
		for _, fact := range f.Facts {
			var evs []snapshot.Evidence
			switch {
			case fact.Kind == KindEdge && fact.Edge != nil:
				evs = fact.Edge.Evidence
			case fact.Kind == KindNode && fact.Node != nil:
				evs = fact.Node.Evidence
			}
			for _, ev := range evs {
				ref := EvidenceRef{File: ev.File, Blob: ev.Blob, Range: ev.Range, Extractor: ev.Extractor, Confidence: ev.Confidence}
				rows = append(rows, mdRow{
					evidenceFactPhrase(f, fact),
					evidenceSourceText(ref, removed),
					fmt.Sprintf("%.2f", ev.Confidence),
				})
			}
		}
	}
	if len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<details>\n<summary>Evidence (%d facts)</summary>\n\n", len(rows))
	b.WriteString(mdTable([]string{"Fact", "Source", "Confidence"}, rows, maxRows))
	b.WriteString("\n</details>\n")
	return b.String()
}

// --- Full diagram and details ------------------------------------------------

// fullDiagramSection is always present in the normal report (unlike every
// other section above, which is omitted when empty) — it is the one place
// the brief promises "the full list of Findings" with no "only if nonzero"
// qualifier, and o.Full's mermaid block when the caller supplied one (see
// MarkdownOptions' own doc comment for why that's optional).
func fullDiagramSection(r *ReviewResult, o MarkdownOptions) string {
	var b strings.Builder
	b.WriteString("<details>\n<summary>Full diagram and details</summary>\n\n")
	if o.Full != nil {
		b.WriteString("```mermaid\n")
		b.WriteString(strings.TrimRight(o.Full.Mermaid, "\n"))
		b.WriteString("\n```\n\n")
	}
	for _, f := range r.Findings {
		b.WriteString(findingLine(f, 0))
	}
	b.WriteString("\n</details>\n")
	return b.String()
}

// findingLine renders f (and, indented, every Downstream entry) as one
// bullet-list line — this task's own format for "every finding with its
// path" (the brief's own phrase for this section); the mockup itself never
// expands this collapsed block, so no wording here is pinned by it.
func findingLine(f ReviewFinding, depth int) string {
	indent := strings.Repeat("  ", depth)

	var line string
	if f.Severity == SeverityUnknown {
		line = fmt.Sprintf("%s- **unknown** %s (%s)\n", indent, code(f.File), escapeText(f.Reason))
	} else {
		subject := escapeText(f.System.Name)
		if f.Counterpart != "" {
			subject = fmt.Sprintf("%s → %s", escapeText(f.System.Name), escapeText(f.Counterpart))
		}
		path := ""
		if len(f.Path) > 0 {
			names := make([]string, len(f.Path))
			for i, s := range f.Path {
				names[i] = s.Node
			}
			path = " via " + strings.Join(escapeAll(names), " → ")
		}
		line = fmt.Sprintf("%s- **%s** %s `%s`%s\n", indent, f.Severity, kindLabel(f.Kind), subject, path)
	}

	var b strings.Builder
	b.WriteString(line)
	for _, d := range f.Downstream {
		b.WriteString(findingLine(d, depth+1))
	}
	return b.String()
}

// --- footer ------------------------------------------------------------------

func footer(o MarkdownOptions) string {
	parts := []string{"Deterministic review, no LLM"}
	if o.Command != "" {
		parts = append(parts, "`"+o.Command+"`")
	}
	if o.JSONLink != "" {
		parts = append(parts, fmt.Sprintf("[JSON](%s)", o.JSONLink))
	}
	if o.OpenLink != "" {
		parts = append(parts, fmt.Sprintf("[Open in Chronicle](%s)", o.OpenLink))
	}
	return strings.Join(parts, " · ") + "\n"
}

// --- shared: changed-file coverage count ------------------------------------

// changedFileCounts computes the "<covered>/<total> changed files covered"
// provenance count from r.ChangedFiles — the real `git diff --name-status`
// result between base and head (gitdiff.ChangedFiles), the same list
// Review() feeds to Diff/DiffHunks and unknownFindings — cross-referenced
// against the Severity Unknown findings those same inputs already produced.
//
// total is simply len(r.ChangedFiles): every file git reports as changed,
// full stop. A changed file counts as uncovered if EITHER its head-side path
// or (for a rename) its base-side OldPath carries a Severity Unknown finding
// — the identical dual-path check unknownFindings itself used to decide
// whether to emit one; covered is everything else.
//
// task-18-report.md's fix: the previous version of this function derived
// "covered" from an approximation instead — walking which file paths
// happened to appear in evidence anywhere in r.Delta (Added, Removed,
// Changed's Before/After, Touched, Moved's Before/After) — and total was
// covered+uncovered from two DIFFERENT, independently-computed sources
// (Delta evidence vs. Findings) that were never guaranteed to agree with, or
// even be a subset of, the real changed-file set: a comment-only edit (no
// graph-relevant fact, so absent from Delta evidence) was silently invisible
// to that count, and there was no guarantee the evidence-derived file set
// didn't OVER-count too (e.g. evidence naming a file outside the actual
// diff). Reading r.ChangedFiles directly closes both gaps: total is now the
// real number git reports changed, always.
func changedFileCounts(r *ReviewResult) (covered, uncovered, total int) {
	unknown := unknownFileSet(r)
	total = len(r.ChangedFiles)
	for _, cf := range r.ChangedFiles {
		if unknown[cf.Path] || (cf.OldPath != "" && unknown[cf.OldPath]) {
			uncovered++
			continue
		}
		covered++
	}
	return covered, uncovered, total
}

// unknownFileSet is unknownFiles's own result as a set, for changedFileCounts'
// O(1) membership checks.
func unknownFileSet(r *ReviewResult) map[string]bool {
	set := make(map[string]bool)
	for _, f := range unknownFiles(r) {
		set[f] = true
	}
	return set
}

// provenanceLine is the "Graph vN · based on commit X · c/t changed files
// covered" line, printed only when Head.Graph != nil (per the brief). If
// Base.Graph names a different graph version than Head's, a parenthetical
// names it too — the spec's own "(и базы, если она другая)" clause; the
// mockup's single example never exercises this, so its exact wording is
// this task's own extrapolation.
func provenanceLine(r *ReviewResult) string {
	if r.Head.Graph == nil {
		return ""
	}
	covered, _, total := changedFileCounts(r)
	line := fmt.Sprintf("Graph v%d · based on commit %s · %d/%d changed files covered",
		r.Head.Graph.Seq, escapeText(shortSHA(r.Head.Graph.Commit)), covered, total)

	if r.Base.Graph != nil && r.Base.Graph.Commit != r.Head.Graph.Commit {
		line += fmt.Sprintf(" (base: Graph v%d · commit %s)", r.Base.Graph.Seq, escapeText(shortSHA(r.Base.Graph.Commit)))
	}
	return line
}

// --- shared: markdown table rendering ---------------------------------------

// mdRow is one already-formatted table row (each element is one cell's
// final markdown text, escaping already applied by the caller).
type mdRow []string

// mdTable renders a GFM table, truncated to maxRows with a trailing
// "+N more" line — the brief's own "таблицы режутся с «+N more»" limit,
// applied identically everywhere this renderer builds a table.
func mdTable(headers []string, rows []mdRow, maxRows int) string {
	var b strings.Builder
	b.WriteString("| " + strings.Join(headers, " | ") + " |\n")

	seps := make([]string, len(headers))
	for i := range seps {
		seps[i] = "---"
	}
	b.WriteString("| " + strings.Join(seps, " | ") + " |\n")

	kept, more := rows, 0
	if maxRows > 0 && len(rows) > maxRows {
		kept = rows[:maxRows]
		more = len(rows) - maxRows
	}
	for _, row := range kept {
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	if more > 0 {
		fmt.Fprintf(&b, "\n*+%d more*\n", more)
	}
	return b.String()
}

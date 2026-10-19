package review

import (
	"context"
	"fmt"
	"sort"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// reviewSchemaVersion is ReviewResult's own schema identity — a report
// consumer (Task 12's renderer, chronicle-pro, a future CLI --json flag) can
// branch on it if this shape ever needs to change incompatibly. 1 because
// this is the first version of it.
const reviewSchemaVersion = 1

// missingGraphMessages are the two user-facing warnings Review's
// missing-graph branches produce. The head-side string is the brief's own
// literal text; the base-side one is this task's own wording (the brief
// names only the Reason code "base_missing_graph" for it, not an exact
// string — see task-11-report.md), deliberately parallel in shape.
const (
	headMissingGraphMessage = "This change introduces the Chronicle graph; impact analysis starts with the next change"
	baseMissingGraphMessage = "The base revision predates the Chronicle graph; impact analysis starts with the next change"
)

// Provenance identifies one side (base or head) of a Review: the revision as
// asked for, its resolved commit, how its graph was obtained (Origin, see
// graph.go's constants), the graph version behind it (nil when Origin is
// "missing" — there is no graph version to report), and which files have
// changed since that graph version was written.
type Provenance struct {
	Ref, Commit, Origin string
	Graph               *snapshot.GraphVersion
	ChangedSinceGraph   []string
}

// Verdict is the small set of numbers a report's headline is built from —
// every one of them a count over ReviewResult's own bucket methods, never a
// separate pass over Findings (see the doc comment on each field below and
// verdictFrom).
type Verdict struct {
	SystemsAffected, Repositories                                          int
	IntegrationsAdded, DependenciesRemoved, ContractsChanged, UnknownFiles int
	NoSystemChange                                                         bool
	// Learned counts facts whose evidence lies wholly outside the diff.
	Learned int
}

// ReviewResult is Review's answer: where base/head came from, the verdict
// headline, the full finding tree, the raw delta (for a renderer that wants
// more detail than the findings alone), any warnings (missing-graph being the
// only producer of these today), and the real changed-file list.
//
// ChangedFiles is the actual `git diff --name-status` result between base and
// head (gitdiff.ChangedFiles) — the same list Diff/DiffHunks and
// unknownFindings are built from. It exists on ReviewResult (task-18-report.md's
// changed-file-count fix) so a renderer can compute an honest "N/M changed
// files covered" count directly from it, instead of re-deriving an
// approximation by walking which files happen to appear in Delta's evidence
// (render_markdown.go's old coveredFiles: a real approximation, not the real
// list — a comment-only edit or a file type nothing extracts from produces no
// evidence at all, so it was invisible to that count; conversely nothing
// guaranteed the evidence-derived file set was even a SUBSET of the real
// changed files). Empty for the two missing-graph short-circuits below,
// which return before ChangedFiles is ever computed — RenderMarkdown never
// reads it on that path (it takes an entirely different, earlier branch).
type ReviewResult struct {
	SchemaVersion int
	Base, Head    Provenance
	Verdict       Verdict
	Findings      []ReviewFinding
	Delta         *GraphDelta
	ChangedFiles  []gitdiff.ChangedFile
	Warnings      []string
}

// ChangedRelationships lists every Changed finding for the report's "Changed
// system relationships" section: every Kind EXCEPT ProducerChanged/
// ConsumerChanged findings that have an EMPTY Downstream (those describe code
// touched without crossing a system boundary — CodeChangedGraphUnchanged's
// job, not this one). Sorted by Kind, then by System.Key.
func (r *ReviewResult) ChangedRelationships() []ReviewFinding {
	var out []ReviewFinding
	for _, f := range r.Findings {
		if f.Severity != SeverityChanged {
			continue
		}
		if (f.Kind == ProducerChanged || f.Kind == ConsumerChanged) && len(f.Downstream) == 0 {
			continue
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].System.Key < out[j].System.Key
	})
	return out
}

// WhoMayBeAffected flattens every Downstream Affected finding across ALL
// top-level Findings (regardless of which bucket the parent Changed finding
// itself falls into), deduplicated by System.Key — keeping the entry with
// the shortest Path — and sorted by System.Key.
func (r *ReviewResult) WhoMayBeAffected() []ReviewFinding {
	best := make(map[string]ReviewFinding)
	for _, f := range r.Findings {
		for _, d := range f.Downstream {
			if d.Severity != SeverityAffected {
				continue
			}
			prev, ok := best[d.System.Key]
			if !ok || len(d.Path) < len(prev.Path) {
				best[d.System.Key] = d
			}
		}
	}
	out := make([]ReviewFinding, 0, len(best))
	for _, f := range best {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].System.Key < out[j].System.Key })
	return out
}

// CodeChangedGraphUnchanged lists Changed findings whose Kind is
// ProducerChanged/ConsumerChanged with an empty Downstream: code was
// touched, but the change never crossed a system boundary.
func (r *ReviewResult) CodeChangedGraphUnchanged() []ReviewFinding {
	var out []ReviewFinding
	for _, f := range r.Findings {
		if f.Severity != SeverityChanged {
			continue
		}
		if (f.Kind == ProducerChanged || f.Kind == ConsumerChanged) && len(f.Downstream) == 0 {
			out = append(out, f)
		}
	}
	return out
}

// verdictFrom computes Verdict from findings alone, via the SAME bucket
// methods a report renders from — never a separate counting pass, so the
// verdict headline can never disagree with what the report body shows.
func verdictFrom(findings []ReviewFinding) Verdict {
	r := &ReviewResult{Findings: findings}
	changed := r.ChangedRelationships()
	affected := r.WhoMayBeAffected()

	v := Verdict{}
	for _, f := range findings {
		if f.Severity == SeverityUnknown {
			v.UnknownFiles++
		}
	}
	systems := make(map[string]bool, len(affected))
	repos := make(map[string]bool, len(affected))
	for _, f := range affected {
		systems[f.System.Key] = true
		repos[f.System.Repo] = true // "" counts as one value, same as any other
	}
	v.SystemsAffected = len(systems)
	v.Repositories = len(repos)

	for _, f := range changed {
		switch f.Kind {
		case IntegrationAdded:
			v.IntegrationsAdded++
		case IntegrationRemoved:
			v.DependenciesRemoved++
		case ContractChanged:
			v.ContractsChanged++
		}
	}

	v.Learned = len((&ReviewResult{Findings: findings}).LearnedFacts())
	v.NoSystemChange = v.UnknownFiles == 0 && len(changed) == 0 && len(affected) == 0
	return v
}

// ReviewInput parameterizes Review.
type ReviewInput struct {
	BaseSource, HeadSource GraphSource
	// RepoRoot is used for gitdiff.ChangedFiles/DiffHunks.
	RepoRoot string
	// Depth is the impact BFS depth; 0 means the default, 4.
	Depth int
	// MakeQuerier builds the ImpactQuerier for one loaded VersionGraph; nil
	// means the default, CoreImpactQuerier{Graph: vg.Graph}.
	MakeQuerier func(*VersionGraph) ImpactQuerier
}

const defaultReviewDepth = 4

func defaultMakeQuerier(vg *VersionGraph) ImpactQuerier {
	return CoreImpactQuerier{Graph: vg.Graph}
}

// Review compares in.BaseSource and in.HeadSource: what changed (Diff +
// ClassifyDelta), what it means and who it may affect (Analyze), and which
// changed files the graph cannot vouch for (Freshness). ctx is passed
// through to both Load calls; they run sequentially (parallelizing them is
// possible, per the brief, but not required, and sequential keeps this
// function's own error handling and tests simple).
//
// Origin=="missing" on head means head's revision predates the Chronicle
// graph entirely: Review returns immediately with a single warning
// (headMissingGraphMessage), Findings empty, and Verdict{NoSystemChange:
// false} constructed DIRECTLY (not via verdictFrom's buckets-based formula,
// which would also compute false here from zero buckets, but for the wrong
// reason — "no findings because there's no graph yet" and "no findings
// because nothing changed" are different messages a later task's report
// template must be able to tell apart). Origin=="missing" on base is the
// same shape, base-side message. head is checked first: a range whose base
// predates the graph but whose head has it is a normal, useful review (the
// PR that introduces the graph is exactly this shape), so only head's own
// absence short-circuits before base is even considered — checked second, so
// a base that predates the graph on its own (head present) still gets its
// own, different message rather than silently taking head's.
//
// Otherwise: gitdiff.ChangedFiles + DiffHunks feed Diff, Diff feeds
// ClassifyDelta, ClassifyDelta's Changes feed Analyze (querier per side from
// MakeQuerier). Every file in changedFiles whose HEAD freshness state is
// Stale/Unscanned/VersionStale (looked up by its head-side Path) becomes a
// Severity Unknown finding (ReasonHeadStaleFile); independently, the same
// file's BASE freshness state (looked up by its base-side path — OldPath for
// a rename, else Path, the same rename-aware convention Touched-detection in
// diff.go already uses) can ALSO produce a second, separate Unknown finding
// (ReasonBaseStaleFile) — a rare case (per the brief), but checked
// unconditionally rather than assumed away. Fresh and Deleted are both left
// alone: Fresh needs no finding, and Deleted is a different signal (a file
// the graph has coverage for that is now gone entirely) this task's closed
// Reason dictionary has no code for.
//
// Verdict is computed by verdictFrom from the final Findings.
func Review(ctx context.Context, in ReviewInput) (*ReviewResult, error) {
	base, err := in.BaseSource.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("review: Review: load base: %w", err)
	}
	defer base.Close()

	head, err := in.HeadSource.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("review: Review: load head: %w", err)
	}
	defer head.Close()

	if head.Origin == OriginMissing {
		return missingGraphResult(base, head, headMissingGraphMessage), nil
	}
	if base.Origin == OriginMissing {
		return missingGraphResult(base, head, baseMissingGraphMessage), nil
	}

	depth := in.Depth
	if depth <= 0 {
		depth = defaultReviewDepth
	}
	makeQuerier := in.MakeQuerier
	if makeQuerier == nil {
		makeQuerier = defaultMakeQuerier
	}

	changedFiles, err := gitdiff.ChangedFiles(in.RepoRoot, base.Commit, head.Commit)
	if err != nil {
		return nil, fmt.Errorf("review: Review: changed files: %w", err)
	}
	hunks, err := gitdiff.DiffHunks(in.RepoRoot, base.Commit, head.Commit)
	if err != nil {
		return nil, fmt.Errorf("review: Review: diff hunks: %w", err)
	}

	delta := Diff(base, head, hunks)
	codeDelta, learned := partitionLearned(delta, changedFiles)
	baseQ, headQ := makeQuerier(base), makeQuerier(head)
	codeDelta, moved := splitMovedIntegrations(codeDelta, changedFiles, baseQ, headQ)
	codeDelta, sameTopic := splitSameSystemTopic(codeDelta, base.Export, baseQ, headQ)
	changes := ClassifyDelta(codeDelta)
	findings := Analyze(changes, baseQ, headQ, depth)
	findings = append(findings, movedFindings(moved, headQ)...)
	findings = append(findings, sameSystemTopicFindings(sameTopic, headQ)...)
	findings = append(findings, learnedFindings(learned, baseQ, headQ)...)
	findings = append(findings, unknownFindings(changedFiles, base.Freshness, head.Freshness)...)

	result := &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base:          provenanceOf(base),
		Head:          provenanceOf(head),
		Findings:      findings,
		Delta:         delta,
		ChangedFiles:  changedFiles,
	}
	for _, w := range base.Warnings {
		result.Warnings = append(result.Warnings, "base graph: "+w)
	}
	for _, w := range head.Warnings {
		result.Warnings = append(result.Warnings, "head graph: "+w)
	}
	result.Verdict = verdictFrom(findings)
	return result, nil
}

func missingGraphResult(base, head *VersionGraph, message string) *ReviewResult {
	return &ReviewResult{
		SchemaVersion: reviewSchemaVersion,
		Base:          provenanceOf(base),
		Head:          provenanceOf(head),
		Verdict:       Verdict{NoSystemChange: false},
		Warnings:      []string{message},
	}
}

func provenanceOf(vg *VersionGraph) Provenance {
	p := Provenance{Ref: vg.Ref, Commit: vg.Commit, Origin: vg.Origin, ChangedSinceGraph: vg.Freshness.ChangedSince}
	if vg.Origin != OriginMissing {
		v := vg.Version
		p.Graph = &v
	}
	return p
}

// untrustedFreshness is the closed set of FileState values that make a
// changed file's facts something the graph cannot currently vouch for —
// deliberately excluding Fresh (nothing to report) and Deleted (a different
// signal, no Reason code of its own in this task).
func untrustedFreshness(st snapshot.FileState) bool {
	return st == snapshot.Stale || st == snapshot.Unscanned || st == snapshot.VersionStale
}

func freshnessIndex(fr snapshot.Freshness) map[string]snapshot.FileState {
	idx := make(map[string]snapshot.FileState, len(fr.Files))
	for _, f := range fr.Files {
		idx[f.Path] = f.State
	}
	return idx
}

// unknownFindings builds the Severity Unknown findings for changedFiles per
// Review's own doc comment: a head-side check by the file's current path,
// and an independent base-side check by its base-side path (OldPath for a
// rename).
func unknownFindings(changedFiles []gitdiff.ChangedFile, baseFreshness, headFreshness snapshot.Freshness) []ReviewFinding {
	headIdx := freshnessIndex(headFreshness)
	baseIdx := freshnessIndex(baseFreshness)

	var out []ReviewFinding
	for _, cf := range changedFiles {
		if st, ok := headIdx[cf.Path]; ok && untrustedFreshness(st) {
			out = append(out, ReviewFinding{Severity: SeverityUnknown, File: cf.Path, Reason: ReasonHeadStaleFile})
		}
		basePath := cf.Path
		if cf.OldPath != "" {
			basePath = cf.OldPath
		}
		if st, ok := baseIdx[basePath]; ok && untrustedFreshness(st) {
			out = append(out, ReviewFinding{Severity: SeverityUnknown, File: basePath, Reason: ReasonBaseStaleFile})
		}
	}
	return out
}

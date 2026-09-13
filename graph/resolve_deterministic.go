package graph

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// detDefaultExtractorID is the evidence identity a deterministic resolve
// stamps when the caller did not name one: the structural phase. The
// structural pass passes its own id explicitly (ResolveOptions.ExtractorID);
// this is only the fallback, kept as a literal because graph/ must not depend
// on the extractor package.
const detDefaultExtractorID = "chronicle-structural"

// detNameMatchConfidence is the ceiling for a link the resolver picked by
// looking a name up among candidate nodes. Even a unique match is a guess
// about identity — the source text said "PrismaService", not "this node" — so
// a deterministic resolve never claims more than this for one.
const detNameMatchConfidence = 0.7

// detResolve carries deterministic-mode state through one resolve pass. It
// lives on the Graph the same way scanFileIndex / currentFileImportMap do:
// resolveOneFact is a 1300-line switch and threading four more parameters
// through every arm would be the opposite of surgical.
type detResolve struct {
	opts ResolveOptions

	// currentFile is the file path of the extraction being resolved right
	// now. Everything recorded during that file is attributed to it.
	currentFile string

	// extractionIDs maps file path → scan_extractions.extraction_id, so the
	// unresolved list can be written back onto the row it came from.
	extractionIDs map[string]int64

	evidenceByFile   map[string][]int64
	unresolvedByFile map[string][]detUnresolved
	unresolvedCount  int

	// deferred holds the name-resolved facts held back from the first pass.
	// A name can only be looked up against a COMPLETE structural base: the
	// resolver sorts endpoint-bearing files first, so a controller's calls
	// are reached long before the provider files that define their targets
	// exist. Legacy mode papers over this by minting the target; a
	// deterministic resolve refuses to, so it waits instead.
	deferred   []detDeferredFact
	secondPass bool

	// nameIndex is flattened name → every node it could mean, built once for
	// the whole third pass. detCandidates used to run one ListNodes sweep per
	// node type per deferred fact; on a real repo that is thousands of full
	// table scans.
	nameIndex map[string][]store.NodeRow
}

// detDeferredFact is one name-resolved fact waiting for the structural base.
//
// It carries its file's import map. `injects` and `provides` resolve the
// specifier FIRST — an imported symbol's target is fixed by the source text,
// not guessed — and the per-file map is built and torn down in the first pass,
// so the fact has to take it along or the third pass would treat every
// injection as a bare name.
type detDeferredFact struct {
	filePath  string
	fact      Fact
	importMap map[string]string
}

// detIsNameResolved reports whether a fact kind's target is chosen by looking
// a NAME up among candidate nodes rather than fixed by the source text. These
// are the facts the lazy-scan spec calls "inferred": the file says
// "PrismaService", and which node that is remains a judgement.
// `injects` and `provides` are here even though they resolve the import map
// first: whether the file imported the symbol is not knowable from the kind,
// only from the fact, and the half that misses the map IS a name lookup. Both
// halves need the complete structural base, so both wait for the third pass.
func detIsNameResolved(kind string) bool {
	switch kind {
	case "call", "member_call", "calls_service", "calls_endpoint", "http_call",
		"injects", "provides":
		return true
	}
	return false
}

// detDefer parks a name-resolved fact for the second pass. Returns false when
// the fact should be resolved now (legacy mode, or already in the second pass).
func (g *Graph) detDefer(filePath string, fact Fact) bool {
	if g.det == nil || g.det.secondPass || !detIsNameResolved(fact.Kind) {
		return false
	}
	g.det.deferred = append(g.det.deferred, detDeferredFact{
		filePath: filePath, fact: fact, importMap: g.currentFileImportMap,
	})
	return true
}

// detUnresolved is one name the resolver saw and refused to link.
type detUnresolved struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Candidates []string `json:"candidates"`
}

// detOn reports whether this resolve is running in deterministic mode.
func (g *Graph) detOn() bool { return g.det != nil }

// detBegin installs deterministic state for one resolve pass (no-op when the
// caller did not ask for it). The returned func tears it down.
func (g *Graph) detBegin(opts ResolveOptions) func() {
	if !opts.Deterministic {
		return func() {}
	}
	g.det = &detResolve{
		opts:             opts,
		extractionIDs:    map[string]int64{},
		evidenceByFile:   map[string][]int64{},
		unresolvedByFile: map[string][]detUnresolved{},
	}
	return func() { g.det = nil }
}

// detExtractorVersion is the rules-pack version stamped on every ast evidence
// row. Empty options fall back to the legacy "1.0": evidence validation
// rejects an empty extractor_version, and losing the whole resolve over a
// missing version string would be a worse failure than an imprecise one.
func (d *detResolve) extractorVersion() string {
	if d.opts.ExtractorVersion == "" {
		return "1.0"
	}
	return d.opts.ExtractorVersion
}

// extractorID is who this resolve says wrote the evidence.
func (d *detResolve) extractorID() string {
	if d.opts.ExtractorID == "" {
		return detDefaultExtractorID
	}
	return d.opts.ExtractorID
}

// stamp rewrites one evidence input for deterministic mode. In legacy mode it
// is the identity function, which is what keeps the default path byte-for-byte
// unchanged.
func (g *Graph) detStampEvidence(sourceKind, extractorID, extractorVersion, metadata string) (string, string, string, string) {
	// No open extraction means this row is not an observation of a file: it
	// is a post-pass writing derived evidence (external endpoints, service
	// containment, derived flows). Those keep their own identity. Stamping
	// them would be worse than cosmetic — the refresh caller supersedes
	// "rows of this extractor id the resolve did not hand back for the
	// file", and a derived row is never handed back.
	if g.det == nil || g.det.currentFile == "" {
		return sourceKind, extractorID, extractorVersion, metadata
	}
	// A row with no file behind it (a synthetic/derived node) is not an AST
	// observation and must not claim to be one.
	if sourceKind != "synthetic" {
		sourceKind = "ast"
	}
	extractorID = g.det.extractorID()
	extractorVersion = g.det.extractorVersion()
	// No content hash here. AddEvidence's dedup path re-uses an existing row
	// without rewriting its metadata, so a hash stamped on an evidence row is
	// frozen at the moment the row was first inserted and reads as a lie about
	// every later assertion. The live answer to "what content justifies this"
	// is the phase's own per-file record (store.GetStructuralHash), which is
	// rewritten every time the file is looked at.
	return sourceKind, extractorID, extractorVersion, metadata
}

// detNoteEvidenceID records an evidence row this resolve wrote, under the file
// whose extraction was being resolved at the time.
func (g *Graph) detNoteEvidenceID(id int64) {
	if g.det == nil || id <= 0 || g.det.currentFile == "" {
		return
	}
	g.det.evidenceByFile[g.det.currentFile] = append(g.det.evidenceByFile[g.det.currentFile], id)
}

// detNoteUnresolved records a name the resolver refused to link, and how many
// candidates it saw. Called only from deterministic branches.
func (g *Graph) detNoteUnresolved(filePath, kind, name string, candidates []string) *UnresolvedRef {
	if g.det == nil {
		return nil
	}
	if candidates == nil {
		candidates = []string{}
	}
	g.det.unresolvedByFile[filePath] = append(g.det.unresolvedByFile[filePath], detUnresolved{
		Kind: kind, Name: name, Candidates: candidates,
	})
	g.det.unresolvedCount++
	reason := fmt.Sprintf("%d candidates for %q — deterministic resolve writes no edge without exactly one", len(candidates), name)
	return &UnresolvedRef{FromFile: filePath, Kind: kind, Target: name, Reason: reason}
}

// detFlush writes the deterministic outputs: the unresolved list onto each
// extraction row's metadata, and the evidence ids / counts onto the result.
func (g *Graph) detFlush(result *ResolveExtractionsResult) error {
	if g.det == nil {
		return nil
	}
	result.UnresolvedCount = g.det.unresolvedCount
	result.EvidenceIDsByFile = g.det.evidenceByFile

	files := make([]string, 0, len(g.det.unresolvedByFile))
	for f := range g.det.unresolvedByFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		id := g.det.extractionIDs[f]
		if id == 0 {
			continue
		}
		if err := g.store.UpdateExtractionMetadata(id, map[string]any{
			"unresolved": g.det.unresolvedByFile[f],
		}); err != nil {
			return fmt.Errorf("recording unresolved names for %s: %w", f, err)
		}
	}
	return nil
}

// detCandidates returns every existing node in the domain the given name could
// mean. It creates nothing: deterministic resolution links to what the graph
// already has, or records that it could not. The result is sorted by node key
// so "which one" is never decided by map order.
func (g *Graph) detCandidates(domainKey, name, layer string, nodeTypes []string) ([]store.NodeRow, error) {
	if name == "" {
		return nil, nil
	}
	flat := flattenName(name)
	if flat == "" {
		return nil, nil
	}
	seen := map[int64]bool{}
	var out []store.NodeRow
	wanted := map[string]bool{}
	for _, nt := range nodeTypes {
		wanted[nt] = true
	}
	add := func(n store.NodeRow) {
		if seen[n.NodeID] || n.Status == "deleted" || n.Layer != layer || !wanted[n.NodeType] {
			return
		}
		seen[n.NodeID] = true
		out = append(out, n)
	}

	index, err := g.detNameIndexFor(domainKey)
	if err != nil {
		// A lookup that could not read the graph is a resolver failure, not
		// an unresolvable name: recording it as "0 candidates" would tell the
		// next pass to go re-read a file over a database error.
		return nil, err
	}
	for _, n := range index[flat] {
		add(n)
	}
	// Aliases only exist for code-layer nodes (FindCodeNodesByAlias filters
	// on layer itself), so this is a no-op for data/service lookups.
	if layer == "code" {
		if aliased, err := g.store.FindCodeNodesByAlias(domainKey, name, ""); err == nil {
			for _, n := range aliased {
				add(n)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].NodeKey < out[j].NodeKey })
	return out, nil
}

// detNameIndexFor returns (building once) the flattened-name index for the
// domain: every spelling a node answers to — its name, its key tail, and the
// basename of that tail — mapped to the node. Built at the start of the third
// pass, when the structural base is complete and nothing further creates nodes
// a name lookup could mean.
func (g *Graph) detNameIndexFor(domainKey string) (map[string][]store.NodeRow, error) {
	if g.det == nil {
		return nil, nil
	}
	if g.det.nameIndex != nil {
		return g.det.nameIndex, nil
	}
	idx := map[string][]store.NodeRow{}
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	if err != nil {
		// Nothing is cached on a failed read: an empty index would answer
		// every later lookup with "no such name".
		return nil, fmt.Errorf("deterministic name index for %s: %w", domainKey, err)
	}
	for _, n := range nodes {
		spellings := map[string]bool{}
		if f := flattenName(n.Name); f != "" {
			spellings[f] = true
		}
		parts := strings.Split(n.NodeKey, ":")
		last := parts[len(parts)-1]
		if f := flattenName(last); f != "" {
			spellings[f] = true
		}
		if f := flattenName(filepath.Base(last)); f != "" {
			spellings[f] = true
		}
		for f := range spellings {
			idx[f] = append(idx[f], n)
		}
	}
	g.det.nameIndex = idx
	return idx, nil
}

// detResetNameIndex drops the cached index so the next lookup rebuilds it.
func (g *Graph) detResetNameIndex() {
	if g.det != nil {
		g.det.nameIndex = nil
	}
}

// detUpsertInferredEdge writes a name-resolved edge, but never weakens one a
// construction-fixed fact already established: a file that BOTH declares an
// injection and calls the injected object must not end up with the call's
// "inferred" overwriting the declaration's "hard". Returns whether the edge
// was written.
//
// The call's evidence is still recorded on the kept edge, so a hard edge can
// carry a 0.7 evidence row beside its own. That is deliberate: the call WAS
// observed, and trust is derived from all the evidence, not from the strongest
// row. It does mean a hard edge's computed confidence can fall when a weaker
// observation of the same relationship arrives.
func (g *Graph) detUpsertInferredEdge(row store.EdgeRow) bool {
	if existing, err := g.store.GetEdgeByKey(row.EdgeKey); err == nil && existing.DerivationKind == "hard" {
		return false
	}
	_, err := g.store.UpsertEdge(row)
	return err == nil
}

// detCandidateKeys is the shape recorded on the extraction row.
func detCandidateKeys(candidates []store.NodeRow) []string {
	keys := make([]string, 0, len(candidates))
	for _, c := range candidates {
		keys = append(keys, c.NodeKey)
	}
	return keys
}

// detUniqueTarget returns the single node a name resolves to, or nil when the
// lookup is ambiguous or empty. The caller records the refusal.
func (g *Graph) detUniqueTarget(domainKey, name, layer string, nodeTypes []string) ([]store.NodeRow, *store.NodeRow, error) {
	cands, err := g.detCandidates(domainKey, name, layer, nodeTypes)
	if err != nil {
		return nil, nil, err
	}
	if len(cands) == 1 {
		return cands, &cands[0], nil
	}
	return cands, nil, nil
}

// --- Evidence chokepoint -----------------------------------------------------
//
// Every evidence row the resolve pipeline writes goes through one of these two
// wrappers. In legacy mode they are pass-throughs to Add{Node,Edge}Evidence
// with the input the call site built — byte for byte. In deterministic mode
// they restamp the row as an AST observation and record its id under the file
// being resolved.

func (g *Graph) resolverEdgeEvidence(edgeKey string, in validate.EvidenceInput) (int64, error) {
	in.SourceKind, in.ExtractorID, in.ExtractorVersion, in.Metadata =
		g.detStampEvidence(in.SourceKind, in.ExtractorID, in.ExtractorVersion, in.Metadata)
	id, err := g.AddEdgeEvidence(edgeKey, in)
	g.detNoteEvidenceID(id)
	return id, err
}

func (g *Graph) resolverNodeEvidence(nodeKey string, in validate.EvidenceInput) (int64, error) {
	in.SourceKind, in.ExtractorID, in.ExtractorVersion, in.Metadata =
		g.detStampEvidence(in.SourceKind, in.ExtractorID, in.ExtractorVersion, in.Metadata)
	id, err := g.AddNodeEvidence(nodeKey, in)
	g.detNoteEvidenceID(id)
	return id, err
}

// detSetFile attributes everything recorded from now on to one extraction.
func (g *Graph) detSetFile(filePath string, extractionID int64) {
	if g.det == nil {
		return
	}
	g.det.currentFile = filePath
	if extractionID > 0 {
		g.det.extractionIDs[filePath] = extractionID
	}
}

// detImportMapTarget resolves a symbol through THIS file's import declarations
// to a node that already exists — construction, not a guess: the specifier
// names one file, and one file is one node.
//
// It creates nothing. A specifier pointing at a file the graph does not hold
// (an import of something outside the batch, or of a non-code asset) is not a
// target; the caller falls back to the name lookup, which refuses honestly.
// The node type is unknown here — a path-keyed node is keyed by the type the
// file turned out to be — so the three code types are tried in a fixed order.
func (g *Graph) detImportMapTarget(domainKey, symbol string, importMap map[string]string) (string, int64) {
	if importMap == nil || symbol == "" {
		return "", 0
	}
	resolvedPath, ok := importMap[symbol]
	if !ok || resolvedPath == "" {
		return "", 0
	}
	for _, nodeType := range []string{"provider", "controller", "module"} {
		key := typedNodeKeyFromFile(domainKey, resolvedPath, nodeType)
		if id, err := g.store.GetNodeIDByKey(key); err == nil && id > 0 {
			return key, id
		}
	}
	return "", 0
}

// detLinkTarget is the whole resolution rule for a symbol that a file may or
// may not have imported, shared by `injects` and `provides`:
//
//	import map hit  → that node, derivation "hard" (the specifier fixes it)
//	exactly one candidate by name → that node, "inferred" at detNameMatchConfidence
//	0 or >1         → nothing; the caller records the refusal
//
// The returned bool says whether a target was found at all.
func (g *Graph) detLinkTarget(domainKey, symbol string, importMap map[string]string) (key string, id int64, derivation string, confidence float64, cands []store.NodeRow, err error) {
	if key, id = g.detImportMapTarget(domainKey, symbol, importMap); id != 0 {
		return key, id, "hard", 0.95, nil, nil
	}
	cands, target, err := g.detUniqueTarget(domainKey, symbol, "code", []string{"provider", "controller", "module"})
	if err != nil {
		return "", 0, "", 0, nil, err
	}
	if target == nil {
		return "", 0, "", 0, cands, nil
	}
	return target.NodeKey, target.NodeID, "inferred", detNameMatchConfidence, cands, nil
}

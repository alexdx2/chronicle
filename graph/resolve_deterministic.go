package graph

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// detExtractorID is the evidence identity of the structural (AST) extractor.
// The other lane defines the same string as structural.ExtractorID; it is
// repeated here as a literal on purpose — graph/ must not depend on the
// extractor package (the extractor imports graph types, not the other way
// round). If one moves, the other moves with it.
const detExtractorID = "chronicle-ast"

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
}

// detDeferredFact is one name-resolved fact waiting for the structural base.
type detDeferredFact struct {
	filePath  string
	fact      Fact
	importMap map[string]string
}

// detIsNameResolved reports whether a fact kind's target is chosen by looking
// a NAME up among candidate nodes rather than fixed by the source text. These
// are the facts the lazy-scan spec calls "inferred": the file says
// "PrismaService", and which node that is remains a judgement.
func detIsNameResolved(kind string) bool {
	switch kind {
	case "call", "member_call", "calls_service", "calls_endpoint", "http_call":
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

// stamp rewrites one evidence input for deterministic mode. In legacy mode it
// is the identity function, which is what keeps the default path byte-for-byte
// unchanged.
func (g *Graph) detStampEvidence(sourceKind, extractorID, extractorVersion, metadata string) (string, string, string, string) {
	if g.det == nil {
		return sourceKind, extractorID, extractorVersion, metadata
	}
	// A row with no file behind it (a synthetic/derived node) is not an AST
	// observation and must not claim to be one.
	if sourceKind != "synthetic" {
		sourceKind = "ast"
	}
	extractorID = detExtractorID
	extractorVersion = g.det.extractorVersion()
	if h := g.det.opts.ContentHashes[g.det.currentFile]; h != "" {
		buf, _ := json.Marshal(map[string]string{"content_hash": h})
		metadata = string(buf)
	}
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
func (g *Graph) detCandidates(domainKey, name, layer string, nodeTypes []string) []store.NodeRow {
	if name == "" {
		return nil
	}
	flat := flattenName(name)
	if flat == "" {
		return nil
	}
	seen := map[int64]bool{}
	var out []store.NodeRow
	wanted := map[string]bool{}
	for _, nt := range nodeTypes {
		wanted[nt] = true
	}
	add := func(n store.NodeRow) {
		if seen[n.NodeID] || n.Status == "deleted" || !wanted[n.NodeType] {
			return
		}
		seen[n.NodeID] = true
		out = append(out, n)
	}

	for _, nt := range nodeTypes {
		nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, Layer: layer, NodeType: nt})
		for _, n := range nodes {
			if flattenName(n.Name) == flat {
				add(n)
				continue
			}
			// Path-keyed nodes: "code:provider:dom:src/tom-service" is the
			// node a source file called "TomService" produces.
			parts := strings.Split(n.NodeKey, ":")
			last := parts[len(parts)-1]
			if flattenName(filepath.Base(last)) == flat || flattenName(last) == flat {
				add(n)
			}
		}
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
	return out
}

// detUpsertInferredEdge writes a name-resolved edge, but never weakens one a
// construction-fixed fact already established: a file that BOTH declares an
// injection and calls the injected object must not end up with the call's
// "inferred" overwriting the declaration's "hard". Returns whether the edge
// was written.
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
func (g *Graph) detUniqueTarget(domainKey, name, layer string, nodeTypes []string) ([]store.NodeRow, *store.NodeRow) {
	cands := g.detCandidates(domainKey, name, layer, nodeTypes)
	if len(cands) == 1 {
		return cands, &cands[0]
	}
	return cands, nil
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

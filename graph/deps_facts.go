package graph

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// DepsExtractorID is the evidence identity of the deterministic dependency pass.
const DepsExtractorID = "chronicle-deps"

// depsOrigin tags the facts WriteDepsExtractions stores; the resolver gives
// them confidence 1.0 and a manifest_line assertion.
const depsOrigin = "deps"

// WriteDepsExtractions stores one extraction row per manifest file with role
// store.DepsExtractionRole and resolves them deterministically. Facts:
// "dependency" (to = normalised name, version, ecosystem, section) and
// "publishes_package". Every fact carries the manifest line and text it came
// from, so its evidence is a manifest_line assertion.
func (g *Graph) WriteDepsExtractions(domainKey string, revisionID int64, rep deps.Report) (*ResolveExtractionsResult, error) {
	byFile := map[string][]Fact{}
	for _, p := range rep.Publishes {
		byFile[p.File] = append(byFile[p.File], Fact{
			Kind: "publishes_package", To: p.Name, Ecosystem: p.Ecosystem, FromType: "file",
			Line: p.Line, Text: p.Text, Origin: depsOrigin,
		})
	}
	for _, d := range rep.Depends {
		section := d.Section
		if d.Internal {
			section = "project"
		}
		byFile[d.File] = append(byFile[d.File], Fact{
			Kind: "dependency", To: d.Name, Version: d.Version, Ecosystem: d.Ecosystem, FromType: "file",
			Section: section, Line: d.Line, Text: d.Text, Origin: depsOrigin,
		})
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := json.Marshal(byFile[f])
		if err != nil {
			return nil, err
		}
		if _, err := g.store.SaveStandingExtraction(revisionID, domainKey, f, store.DepsExtractionRole, "extracted", "file", string(b), ""); err != nil {
			return nil, fmt.Errorf("deps: save %s: %w", f, err)
		}
	}
	return g.ResolveExtractionsWithOptions(domainKey, revisionID, ResolveOptions{
		Deterministic: true, ExtractorID: DepsExtractorID, ExtractorVersion: deps.Version,
		ExtractionRole: store.DepsExtractionRole, AllowDegraded: true,
	})
}

// packageNodeKey is the identity of a package of an ecosystem in a domain.
func packageNodeKey(domainKey, ecosystem, name string) string {
	return canonicalNodeKey("code:package:" + domainKey + ":" + ecosystem + ":" + name)
}

// depsOwnLookupName is the name a deps-pass fact is looked up under among the
// domain's own packages: a project reference is known by its file stem.
func depsOwnLookupName(fact Fact) string {
	if fact.Origin == depsOrigin && fact.Section == "project" {
		return projectFileStem(fact.To)
	}
	return fact.To
}

func projectFileStem(ref string) string {
	b := path.Base(strings.ReplaceAll(ref, `\`, "/"))
	return strings.TrimSuffix(b, path.Ext(b))
}

// buildManifestLineAssertion anchors a deps-pass fact to its manifest line.
func buildManifestLineAssertion(fact Fact) (string, string) {
	text := fact.Text
	if text == "" {
		text = fact.To
	}
	a, _ := json.Marshal(map[string]any{"line": fact.Line, "text": text})
	return "manifest_line", string(a)
}

// manifestOwnerKey is the node a manifest's facts originate at: the service
// node minted from the file when there is one, else the file's module node.
func (g *Graph) manifestOwnerKey(domainKey, filePath, fromType string) string {
	if svc, err := g.store.GetServiceNodeByFilePath(domainKey, filePath); err == nil && svc != nil {
		return svc.NodeKey
	}
	return typedNodeKeyFromFile(domainKey, filePath, fromType)
}

// addPublishedPackages feeds ownPackagesForDomain: every package a manifest in
// this batch publishes is the domain's own, under its name and, for project
// files, under the file stem a ProjectReference uses.
func (g *Graph) addPublishedPackages(domainKey string, allFiles []fileFacts, add func(name, nodeKey string)) {
	for _, ff := range allFiles {
		for _, f := range ff.facts {
			if f.Kind != "publishes_package" || f.To == "" {
				continue
			}
			owner := g.manifestOwnerKey(domainKey, ff.filePath, f.FromType)
			add(f.To, owner)
			if strings.HasSuffix(strings.ToLower(path.Ext(ff.filePath)), "proj") {
				add(projectFileStem(ff.filePath), owner)
			}
		}
	}
}

// resolvePublishesPackage records that a manifest's owner publishes a package.
func (g *Graph) resolvePublishesPackage(domainKey string, revisionID int64, filePath string, fact Fact, extractorID string) createdCounts {
	var counts createdCounts
	fromNodeKey := g.manifestOwnerKey(domainKey, filePath, fact.FromType)
	fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
	toNodeKey := packageNodeKey(domainKey, fact.Ecosystem, fact.To)
	toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")
	if fromID == 0 || toID == 0 || fromID == toID {
		return counts
	}
	edgeKey := fromNodeKey + "->" + toNodeKey + ":PUBLISHES"
	if _, err := g.store.UpsertEdge(store.EdgeRow{
		EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
		FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "PUBLISHES", DerivationKind: "hard", Active: true,
		LastSeenRevisionID: revisionID, Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0,
		Metadata: "{}", DependencySource: "manifest",
	}); err != nil {
		return counts
	}
	counts.edges++
	kind, assertion := buildManifestLineAssertion(fact)
	_, _ = g.resolverEdgeEvidence(edgeKey, validate.EvidenceInput{
		TargetKind: "edge", SourceKind: "file", FilePath: filePath,
		ExtractorID: extractorID, ExtractorVersion: "1.0",
		Confidence: 1.0, RevisionID: revisionID,
		AssertionKind: kind, Assertion: assertion,
	})
	counts.evidence++
	return counts
}

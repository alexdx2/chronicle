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
	return g.WriteDepsExtractionsOpts(domainKey, revisionID, rep, DepsOptions{})
}

// DepsOptions tunes WriteDepsExtractionsOpts.
type DepsOptions struct {
	// IncludeDevDeps mirrors the domain's scan.include_dev_deps: dev and
	// transitive ("indirect") entries are resolved instead of dropped.
	IncludeDevDeps bool
}

// WriteDepsExtractionsOpts is WriteDepsExtractions with options.
func (g *Graph) WriteDepsExtractionsOpts(domainKey string, revisionID int64, rep deps.Report, opts DepsOptions) (*ResolveExtractionsResult, error) {
	byFile := map[string][]Fact{}
	for _, p := range rep.Publishes {
		// A published APPLICATION implies the service that runs it, so the
		// headless path does not depend on a model to declare it: the
		// manifest line is the evidence, and DEPENDS_ON/PUBLISHES start at
		// the service node. A library (no start script, no web framework,
		// no Web/Exe project) is a package only.
		if p.App {
			byFile[p.File] = append(byFile[p.File], Fact{
				Kind: "declares_service", To: serviceNameOfPackage(p.Ecosystem, p.Name), Ecosystem: p.Ecosystem, FromType: "file",
				Line: p.Line, Text: p.Text, Origin: depsOrigin,
			})
		}
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
	previous, err := g.store.ListStandingExtractionsByRole(domainKey, store.DepsExtractionRole)
	if err != nil {
		return nil, fmt.Errorf("deps: standing rows: %w", err)
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
	res, err := g.ResolveExtractionsWithOptions(domainKey, revisionID, ResolveOptions{
		Deterministic: true, ExtractorID: DepsExtractorID, ExtractorVersion: deps.Version,
		ExtractionRole: store.DepsExtractionRole, AllowDegraded: true, IncludeDevDeps: opts.IncludeDevDeps,
	})
	if err != nil {
		return nil, err
	}

	// The replacement contract: what a manifest asserts now is ALL it asserts.
	var superseded int64
	for _, file := range files {
		keep, err := g.depsKeep(file, revisionID, res.EvidenceIDsByFile[file])
		if err != nil {
			return nil, err
		}
		n, err := g.store.SupersedeEvidenceNotIn(file, DepsExtractorID, keep, revisionID)
		if err != nil {
			return nil, fmt.Errorf("deps: supersede %s: %w", file, err)
		}
		superseded += n
	}
	// A manifest that left the report (deleted, or no longer yielding facts)
	// asserts nothing at all.
	for _, row := range previous {
		if _, still := byFile[row.FilePath]; still {
			continue
		}
		n, err := g.store.SupersedeEvidenceNotIn(row.FilePath, DepsExtractorID, nil, revisionID)
		if err != nil {
			return nil, fmt.Errorf("deps: supersede removed %s: %w", row.FilePath, err)
		}
		superseded += n
		if err := g.store.DeleteStandingExtraction(domainKey, row.FilePath, store.DepsExtractionRole); err != nil {
			return nil, fmt.Errorf("deps: forget %s: %w", row.FilePath, err)
		}
	}
	if superseded > 0 {
		if err := g.recalculateSupersededTrust(revisionID); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// depsKeep is the evidence a manifest's resolve re-observed: the ids the
// resolve reported plus every deps row created for the file in this revision
// (a dedup hit reaches the store by a path the result map may not record).
func (g *Graph) depsKeep(filePath string, revisionID int64, fromResolve []int64) ([]int64, error) {
	created, err := g.store.EvidenceIDsCreatedIn(filePath, DepsExtractorID, revisionID)
	if err != nil {
		return nil, fmt.Errorf("deps: evidence of %s: %w", filePath, err)
	}
	seen := map[int64]bool{}
	var keep []int64
	for _, ids := range [][]int64{fromResolve, created} {
		for _, id := range ids {
			if id > 0 && !seen[id] {
				seen[id] = true
				keep = append(keep, id)
			}
		}
	}
	return keep, nil
}

// packageNodeKey is the identity of a package of an ecosystem in a domain.
func packageNodeKey(domainKey, ecosystem, name string) string {
	return canonicalNodeKey("code:package:" + domainKey + ":" + ecosystem + ":" + name)
}

// depsOwnLookupName is the name a deps-pass fact is looked up under among the
// domain's own packages: a project reference is known by its file stem.
func depsOwnLookupName(fact Fact) string {
	if fact.Origin == depsOrigin && fact.Section == "project" && fact.Ecosystem == "nuget" {
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
// this batch publishes is the domain's own, under its name. Every project file
// in the batch (published or not: IsPackable=false still has a node a
// ProjectReference must reach) is also known under the file stem.
func (g *Graph) addPublishedPackages(domainKey string, allFiles []fileFacts, add func(name, nodeKey string)) {
	for _, ff := range allFiles {
		isProject := strings.HasSuffix(strings.ToLower(path.Ext(ff.filePath)), "proj")
		stemmed := false
		for _, f := range ff.facts {
			if f.Origin != depsOrigin && f.Kind != "publishes_package" {
				continue
			}
			owner := g.manifestOwnerKey(domainKey, ff.filePath, f.FromType)
			if f.Kind == "publishes_package" && f.To != "" {
				if strings.HasPrefix(owner, "service:") {
					add(f.To, owner)
				} else {
					// No service node owns the manifest: a sibling dependency
					// lands on the package node the PUBLISHES edge targets,
					// not on the manifest's file node.
					add(f.To, packageNodeKey(domainKey, f.Ecosystem, f.To))
				}
			}
			if isProject && !stemmed {
				add(projectFileStem(ff.filePath), owner)
				stemmed = true
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
	fromDeps := fact.Origin == depsOrigin
	confidence := 0.95
	if fromDeps {
		confidence = 1.0
	}
	edgeKey := fromNodeKey + "->" + toNodeKey + ":PUBLISHES"
	if _, err := g.store.UpsertEdge(store.EdgeRow{
		EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
		FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "PUBLISHES", DerivationKind: "hard", Active: true,
		LastSeenRevisionID: revisionID, Confidence: confidence, Freshness: 1.0, TrustScore: confidence,
		Metadata: "{}", DependencySource: "manifest",
	}); err != nil {
		return counts
	}
	counts.edges++
	kind, assertion := buildManifestLineAssertion(fact)
	if !fromDeps {
		a, _ := json.Marshal(map[string]any{"substring": fact.To})
		kind, assertion = "text_contains", string(a)
	}
	_, _ = g.resolverEdgeEvidence(edgeKey, validate.EvidenceInput{
		TargetKind: "edge", SourceKind: "file", FilePath: filePath,
		ExtractorID: extractorID, ExtractorVersion: "1.0",
		Confidence: confidence, RevisionID: revisionID,
		AssertionKind: kind, Assertion: assertion,
	})
	counts.evidence++
	return counts
}

// serviceNameOfPackage is the name a deployed application goes by: the short
// name of its package, not its registry coordinates. A Maven project
// "com.corp:inventory-spring" runs as "inventory-spring", a Go module
// "github.com/corp/shipping-go/v2" as "shipping-go", a Composer package
// "corp/cms" as "cms". npm, NuGet, PyPI, Cargo and gem names are already short.
func serviceNameOfPackage(ecosystem, name string) string {
	switch ecosystem {
	case "maven", "gradle":
		if i := strings.LastIndex(name, ":"); i >= 0 {
			return name[i+1:]
		}
	case "go":
		parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
		for len(parts) > 1 && len(parts[len(parts)-1]) > 1 && parts[len(parts)-1][0] == 'v' && isAllDigits(parts[len(parts)-1][1:]) {
			parts = parts[:len(parts)-1]
		}
		return parts[len(parts)-1]
	case "composer":
		if i := strings.LastIndex(name, "/"); i >= 0 {
			return name[i+1:]
		}
	}
	return name
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

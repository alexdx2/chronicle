package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/prisma"
	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/validate"
)

// ---------------------------------------------------------------------------
// MR review report: deterministic diff → graph → blast radius. The engine is
// git-free — the caller supplies the changed-file list and any schema blobs
// (see gitdiff). Impact queries go through GraphQuerier, so a federated
// querier (chronicle-pro) makes the report cross repo boundaries unchanged.
// ---------------------------------------------------------------------------

// SchemaFieldChange is a field-level change detected in a schema file diff.
type SchemaFieldChange struct {
	Model    string `json:"model"`
	Field    string `json:"field"`
	Change   string `json:"change"` // added | removed | type_changed
	FromType string `json:"from_type,omitempty"`
	ToType   string `json:"to_type,omitempty"`
}

// ReviewEntity is one graph node touched by the diff, with its blast radius.
type ReviewEntity struct {
	NodeKey      string              `json:"node_key"`
	Name         string              `json:"name"`
	Layer        string              `json:"layer"`
	NodeType     string              `json:"node_type"`
	FilePath     string              `json:"file_path"`
	FieldChanges []SchemaFieldChange `json:"field_changes,omitempty"`
	Impact       *ImpactResult       `json:"impact,omitempty"`
}

// ReviewReportStats summarizes coverage of the report.
type ReviewReportStats struct {
	ChangedFiles  int `json:"changed_files"`
	MappedNodes   int `json:"mapped_nodes"`
	TotalImpacted int `json:"total_impacted"`
}

// ReviewReport is the full deterministic review of a diff.
type ReviewReport struct {
	Base             string            `json:"base"`
	Head             string            `json:"head"` // "" = working tree
	DomainKey        string            `json:"domain_key"`
	Entities         []ReviewEntity    `json:"entities"`
	ExternalServices []string          `json:"external_services,omitempty"`
	Unmapped         []string          `json:"unmapped,omitempty"`
	Stats            ReviewReportStats `json:"stats"`
}

// ReviewReportOptions parameterizes BuildReviewReport.
type ReviewReportOptions struct {
	Base      string
	Head      string // "" = working tree
	Depth     int    // impact depth, default 4
	Changed   []gitdiff.ChangedFile
	OldPrisma map[string][]byte // path → content at Base (only .prisma files)
	NewPrisma map[string][]byte // path → content at Head/worktree
}

// BuildReviewReport maps a diff onto the graph and computes per-entity blast
// radius. q is the impact querier — pass a federated querier for cross-repo
// radius; nil falls back to g itself. Files with no graph presence are listed
// in Unmapped, never guessed at.
func (g *Graph) BuildReviewReport(q GraphQuerier, domainKey string, opts ReviewReportOptions) (*ReviewReport, error) {
	if q == nil {
		q = g
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = 4
	}

	report := &ReviewReport{
		Base:      opts.Base,
		Head:      opts.Head,
		DomainKey: domainKey,
	}
	report.Stats.ChangedFiles = len(opts.Changed)
	if len(opts.Changed) == 0 {
		return report, nil
	}

	paths := make([]string, 0, len(opts.Changed))
	for _, cf := range opts.Changed {
		paths = append(paths, cf.Path)
	}
	nodesByPath, err := g.store.NodeKeysByFilePaths(paths)
	if err != nil {
		return nil, fmt.Errorf("BuildReviewReport: %w", err)
	}

	fieldChangesByPath := diffPrismaSchemas(opts.OldPrisma, opts.NewPrisma)

	externalSet := map[string]bool{}
	seenEntity := map[string]bool{}

	for _, cf := range opts.Changed {
		keys := nodesByPath[cf.Path]
		if len(keys) == 0 {
			report.Unmapped = append(report.Unmapped, cf.Path)
			continue
		}
		sort.Strings(keys)
		for _, key := range keys {
			if seenEntity[key] {
				continue
			}
			seenEntity[key] = true

			node, err := g.store.GetNodeByKey(key)
			if err != nil {
				continue
			}
			entity := ReviewEntity{
				NodeKey:  node.NodeKey,
				Name:     node.Name,
				Layer:    node.Layer,
				NodeType: node.NodeType,
				FilePath: cf.Path,
			}

			// Field-level rows for the model nodes of a changed .prisma file.
			if node.Layer == "data" && node.NodeType == "model" {
				for _, fc := range fieldChangesByPath[cf.Path] {
					if strings.EqualFold(fc.Model, node.Name) {
						entity.FieldChanges = append(entity.FieldChanges, fc)
					}
				}
			}

			// Blast radius: field-precision seeds when we know exactly which
			// fields changed, else the node itself.
			seeds := []string{node.NodeKey}
			if len(entity.FieldChanges) > 0 {
				seeds = nil
				for _, fc := range entity.FieldChanges {
					seeds = append(seeds, "data:field:"+domainKey+":"+
						validate.NormalizeName(fc.Model)+"/"+validate.NormalizeName(fc.Field))
				}
			}
			entity.Impact = mergeImpacts(q, seeds, node.NodeKey, depth)

			if entity.Impact != nil {
				report.Stats.TotalImpacted += entity.Impact.TotalImpacted
				for _, imp := range entity.Impact.Impacts {
					if isExternalImpact(domainKey, imp) {
						externalSet[imp.NodeKey] = true
					}
				}
			}
			report.Entities = append(report.Entities, entity)
		}
	}

	report.Stats.MappedNodes = len(report.Entities)
	for key := range externalSet {
		report.ExternalServices = append(report.ExternalServices, key)
	}
	sort.Strings(report.ExternalServices)
	sort.Strings(report.Unmapped)
	return report, nil
}

// mergeImpacts runs impact for each seed and merges results (dedup by node,
// keep highest score). Field seeds that don't resolve (field nodes not scanned
// yet) fall back to the owning node's impact.
func mergeImpacts(q GraphQuerier, seeds []string, fallbackKey string, depth int) *ImpactResult {
	var merged *ImpactResult
	seen := map[string]int{} // node_key → index in merged.Impacts
	anySeedResolved := false

	for _, seed := range seeds {
		res, err := q.QueryImpact(seed, ImpactOptions{MaxDepth: depth})
		if err != nil {
			continue
		}
		anySeedResolved = true
		if merged == nil {
			cp := *res
			merged = &cp
			for i, imp := range merged.Impacts {
				seen[imp.NodeKey] = i
			}
			continue
		}
		if res.PrecisionNote != "" {
			merged.PrecisionNote = res.PrecisionNote
		}
		for _, imp := range res.Impacts {
			if idx, ok := seen[imp.NodeKey]; ok {
				if imp.ImpactScore > merged.Impacts[idx].ImpactScore {
					merged.Impacts[idx] = imp
				}
				continue
			}
			seen[imp.NodeKey] = len(merged.Impacts)
			merged.Impacts = append(merged.Impacts, imp)
		}
		merged.AffectedSurface.Endpoints = mergeSurface(merged.AffectedSurface.Endpoints, res.AffectedSurface.Endpoints)
		merged.AffectedSurface.Topics = mergeSurface(merged.AffectedSurface.Topics, res.AffectedSurface.Topics)
	}

	if !anySeedResolved && fallbackKey != "" && len(seeds) > 0 && seeds[0] != fallbackKey {
		res, err := q.QueryImpact(fallbackKey, ImpactOptions{MaxDepth: depth})
		if err == nil {
			merged = res
		}
	}
	if merged != nil {
		merged.TotalImpacted = len(merged.Impacts)
	}
	return merged
}

func mergeSurface(a, b []SurfaceEntry) []SurfaceEntry {
	seen := map[string]bool{}
	for _, e := range a {
		seen[e.NodeKey] = true
	}
	for _, e := range b {
		if !seen[e.NodeKey] {
			seen[e.NodeKey] = true
			a = append(a, e)
		}
	}
	return a
}

// isExternalImpact reports whether an impacted node lives outside the changed
// node's domain (cross-repo / cross-domain) — the "external services" of the
// report. Federated queriers return foreign-domain keys, which this catches.
func isExternalImpact(domainKey string, imp ImpactEntry) bool {
	if imp.NodeType == "external_system" {
		return true
	}
	parts := strings.SplitN(imp.NodeKey, ":", 4)
	if len(parts) == 4 && parts[2] != "" && parts[2] != domainKey {
		return true
	}
	return false
}

// diffPrismaSchemas computes field-level changes between old and new schema
// contents, keyed by file path.
func diffPrismaSchemas(oldFiles, newFiles map[string][]byte) map[string][]SchemaFieldChange {
	result := map[string][]SchemaFieldChange{}

	allPaths := map[string]bool{}
	for p := range oldFiles {
		allPaths[p] = true
	}
	for p := range newFiles {
		allPaths[p] = true
	}

	for path := range allPaths {
		oldFields := prismaFieldTypes(oldFiles[path])
		newFields := prismaFieldTypes(newFiles[path])

		var changes []SchemaFieldChange
		for key, oldType := range oldFields {
			model, field := splitFieldKey(key)
			newType, ok := newFields[key]
			switch {
			case !ok:
				changes = append(changes, SchemaFieldChange{Model: model, Field: field, Change: "removed", FromType: oldType})
			case newType != oldType:
				changes = append(changes, SchemaFieldChange{Model: model, Field: field, Change: "type_changed", FromType: oldType, ToType: newType})
			}
		}
		for key, newType := range newFields {
			if _, ok := oldFields[key]; !ok {
				model, field := splitFieldKey(key)
				changes = append(changes, SchemaFieldChange{Model: model, Field: field, Change: "added", ToType: newType})
			}
		}
		sort.Slice(changes, func(i, j int) bool {
			if changes[i].Model != changes[j].Model {
				return changes[i].Model < changes[j].Model
			}
			return changes[i].Field < changes[j].Field
		})
		if len(changes) > 0 {
			result[path] = changes
		}
	}
	return result
}

// prismaFieldTypes flattens a schema into "Model.field" → type.
func prismaFieldTypes(content []byte) map[string]string {
	fields := map[string]string{}
	if len(content) == 0 {
		return fields
	}
	for _, m := range prisma.Extract(content).Models {
		for _, f := range m.Fields {
			fields[m.Name+"."+f.Name] = f.Type
		}
	}
	return fields
}

func splitFieldKey(key string) (model, field string) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return key, ""
}

// Markdown renders the report for direct posting on a merge request.
func (r *ReviewReport) Markdown() string {
	var b strings.Builder
	b.WriteString("# Chronicle Review Report\n\n")
	head := r.Head
	if head == "" {
		head = "working tree"
	}
	fmt.Fprintf(&b, "Base `%s` → `%s` · %d changed files · %d mapped entities · %d impacted nodes\n\n",
		r.Base, head, r.Stats.ChangedFiles, r.Stats.MappedNodes, r.Stats.TotalImpacted)

	b.WriteString("## Changed entities\n\n")
	if len(r.Entities) == 0 {
		b.WriteString("_No changed files map to graph entities._\n\n")
	}
	for _, e := range r.Entities {
		fmt.Fprintf(&b, "### %s `%s`\n\n", e.Name, e.NodeKey)
		fmt.Fprintf(&b, "%s/%s · `%s`\n\n", e.Layer, e.NodeType, e.FilePath)
		if len(e.FieldChanges) > 0 {
			b.WriteString("| Field | Change | Type |\n|---|---|---|\n")
			for _, fc := range e.FieldChanges {
				typeCol := fc.ToType
				if fc.Change == "type_changed" {
					typeCol = fc.FromType + " → " + fc.ToType
				} else if fc.Change == "removed" {
					typeCol = fc.FromType
				}
				fmt.Fprintf(&b, "| %s.%s | %s | %s |\n", fc.Model, fc.Field, fc.Change, typeCol)
			}
			b.WriteString("\n")
		}
		if e.Impact == nil || len(e.Impact.Impacts) == 0 {
			b.WriteString("No known dependents — verify the graph is current before trusting this.\n\n")
			continue
		}
		b.WriteString("| Impacted | Precision | Score | Depth |\n|---|---|---|---|\n")
		for _, imp := range e.Impact.Impacts {
			prec := imp.Precision
			if prec == "" {
				prec = "node"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %.1f | %d |\n", imp.NodeKey, prec, imp.ImpactScore, imp.Depth)
		}
		b.WriteString("\n")
		if len(e.Impact.AffectedSurface.Endpoints) > 0 || len(e.Impact.AffectedSurface.Topics) > 0 {
			b.WriteString("Affected surface: ")
			var surf []string
			for _, s := range e.Impact.AffectedSurface.Endpoints {
				surf = append(surf, "`"+s.Name+"`")
			}
			for _, s := range e.Impact.AffectedSurface.Topics {
				surf = append(surf, "topic `"+s.Name+"`")
			}
			b.WriteString(strings.Join(surf, ", ") + "\n\n")
		}
		if e.Impact.PrecisionNote != "" {
			fmt.Fprintf(&b, "_%s_\n\n", e.Impact.PrecisionNote)
		}
	}

	b.WriteString("## External services affected\n\n")
	if len(r.ExternalServices) == 0 {
		b.WriteString("_None detected._\n\n")
	} else {
		for _, s := range r.ExternalServices {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Unmapped changes\n\n")
	if len(r.Unmapped) == 0 {
		b.WriteString("_Every changed file maps to the graph._\n")
	} else {
		b.WriteString("These files have no graph coverage — impact is UNKNOWN for them, not zero:\n\n")
		for _, p := range r.Unmapped {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
	}
	return b.String()
}

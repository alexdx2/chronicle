package registry

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type EdgeTypeDef struct {
	FromLayers []string `yaml:"from_layers" json:"from_layers"`
	ToLayers   []string `yaml:"to_layers" json:"to_layers"`
}

type TraversalPolicyDef struct {
	StructuralEdgeTypes []string `yaml:"structural_edge_types" json:"structural_edge_types"`
	NoReverseImpact     []string `yaml:"no_reverse_impact" json:"no_reverse_impact"`
}

type TraversalPolicy struct {
	structural      map[string]bool
	noReverseImpact map[string]bool
}

func (p *TraversalPolicy) IsStructural(edgeType string) bool {
	return p.structural[edgeType]
}

func (p *TraversalPolicy) AllowsForwardPath(edgeType string) bool {
	return !p.structural[edgeType]
}

func (p *TraversalPolicy) AllowsReverseImpact(edgeType string) bool {
	return !p.structural[edgeType] && !p.noReverseImpact[edgeType]
}

type RegistryFile struct {
	Version            string                 `yaml:"version" json:"version"`
	Layers             []string               `yaml:"layers" json:"layers"`
	NodeTypes          map[string][]string    `yaml:"node_types" json:"node_types"`
	EdgeTypes          map[string]EdgeTypeDef `yaml:"edge_types" json:"edge_types"`
	DerivationKinds    []string               `yaml:"derivation_kinds" json:"derivation_kinds"`
	SourceKinds        []string               `yaml:"source_kinds" json:"source_kinds"`
	NodeStatuses       []string               `yaml:"node_statuses" json:"node_statuses"`
	TriggerKinds       []string               `yaml:"trigger_kinds" json:"trigger_kinds"`
	TraversalPolicyDef *TraversalPolicyDef    `yaml:"traversal_policy" json:"traversal_policy"`
	Salience           *SaliencePolicy        `yaml:"salience" json:"salience,omitempty"`
}

type Registry struct {
	layers          map[string]bool
	nodeTypes       map[string]map[string]bool
	edgeTypes       map[string]EdgeTypeDef
	derivations     map[string]bool
	sourceKinds     map[string]bool
	statuses        map[string]bool
	triggers        map[string]bool
	traversalPolicy *TraversalPolicy
	salience        *SaliencePolicy
}

func LoadFile(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading registry: %w", err)
	}
	return Load(data)
}

func Load(data []byte) (*Registry, error) {
	var f RegistryFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing registry: %w", err)
	}

	r := &Registry{
		layers:      toSet(f.Layers),
		nodeTypes:   make(map[string]map[string]bool),
		edgeTypes:   f.EdgeTypes,
		derivations: toSet(f.DerivationKinds),
		sourceKinds: toSet(f.SourceKinds),
		statuses:    toSet(f.NodeStatuses),
		triggers:    toSet(f.TriggerKinds),
	}

	for layer, types := range f.NodeTypes {
		if !r.layers[layer] {
			return nil, fmt.Errorf("registry validation: node_types references unknown layer %q", layer)
		}
		r.nodeTypes[layer] = toSet(types)
	}

	for name, et := range f.EdgeTypes {
		for _, l := range et.FromLayers {
			if !r.layers[l] {
				return nil, fmt.Errorf("registry validation: edge_type %q from_layers references unknown layer %q", name, l)
			}
		}
		for _, l := range et.ToLayers {
			if !r.layers[l] {
				return nil, fmt.Errorf("registry validation: edge_type %q to_layers references unknown layer %q", name, l)
			}
		}
	}

	policy := &TraversalPolicy{
		structural:      make(map[string]bool),
		noReverseImpact: make(map[string]bool),
	}
	if f.TraversalPolicyDef != nil {
		policy.structural = toSet(f.TraversalPolicyDef.StructuralEdgeTypes)
		policy.noReverseImpact = toSet(f.TraversalPolicyDef.NoReverseImpact)
	}
	r.traversalPolicy = policy

	if err := f.Salience.Validate(); err != nil {
		return nil, fmt.Errorf("registry validation: %w", err)
	}
	r.salience = mergeSalience(defaultSaliencePolicy(), f.Salience)

	return r, nil
}

// AsFile returns a JSON-serializable representation of the registry.
func AsFile() (*RegistryFile, error) {
	var f RegistryFile
	if err := yaml.Unmarshal(DefaultRegistryYAML, &f); err != nil {
		return nil, fmt.Errorf("parsing registry: %w", err)
	}
	return &f, nil
}

func (r *Registry) TraversalPolicy() *TraversalPolicy {
	return r.traversalPolicy
}

func (r *Registry) IsValidLayer(layer string) bool {
	return r.layers[layer]
}

func (r *Registry) IsValidNodeType(layer, nodeType string) bool {
	types, ok := r.nodeTypes[layer]
	if !ok {
		return false
	}
	return types[nodeType]
}

// ValidNodeTypes returns the valid node types for a layer.
func (r *Registry) ValidNodeTypes(layer string) []string {
	types, ok := r.nodeTypes[layer]
	if !ok {
		return nil
	}
	result := make([]string, 0, len(types))
	for t := range types {
		result = append(result, t)
	}
	return result
}

func (r *Registry) IsValidEdgeType(edgeType string) bool {
	_, ok := r.edgeTypes[edgeType]
	return ok
}

func (r *Registry) ValidateEdgeLayers(edgeType, fromLayer, toLayer string) error {
	et, ok := r.edgeTypes[edgeType]
	if !ok {
		// Unknown edge type — suggest similar ones
		suggestions := r.SuggestSimilarEdgeTypes(edgeType)
		if len(suggestions) > 0 {
			names := make([]string, 0, len(suggestions))
			for _, s := range suggestions {
				names = append(names, fmt.Sprintf("%s (%s)", s.EdgeType, s.Intent))
			}
			return fmt.Errorf("unknown edge type %q. Did you mean: %s", edgeType, strings.Join(names, ", "))
		}
		return fmt.Errorf("unknown edge type %q", edgeType)
	}
	fromOk := containsStr(et.FromLayers, fromLayer)
	if !fromOk {
		msg := fmt.Sprintf("edge type %q does not allow from_layer %q (allowed from: %v)", edgeType, fromLayer, et.FromLayers)
		suggestions := r.SuggestEdgeTypes(fromLayer, toLayer)
		if len(suggestions) > 0 {
			names := make([]string, 0, len(suggestions))
			for _, s := range suggestions {
				if len(names) >= 5 {
					break
				}
				names = append(names, fmt.Sprintf("%s (%s)", s.EdgeType, s.Intent))
			}
			msg += fmt.Sprintf(". Edges from %q to %q: %s", fromLayer, toLayer, strings.Join(names, ", "))
		}
		return fmt.Errorf("%s", msg)
	}
	toOk := containsStr(et.ToLayers, toLayer)
	if !toOk {
		msg := fmt.Sprintf("edge type %q does not allow to_layer %q (allowed to: %v)", edgeType, toLayer, et.ToLayers)
		suggestions := r.SuggestEdgeTypes(fromLayer, toLayer)
		if len(suggestions) > 0 {
			names := make([]string, 0, len(suggestions))
			for _, s := range suggestions {
				if len(names) >= 5 {
					break
				}
				names = append(names, fmt.Sprintf("%s (%s)", s.EdgeType, s.Intent))
			}
			msg += fmt.Sprintf(". Edges from %q to %q: %s", fromLayer, toLayer, strings.Join(names, ", "))
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (r *Registry) IsValidDerivation(kind string) bool {
	return r.derivations[kind]
}

func (r *Registry) IsValidSourceKind(kind string) bool {
	return r.sourceKinds[kind]
}

// ValidSourceKinds returns the list of valid source kinds.
func (r *Registry) ValidSourceKinds() []string {
	result := make([]string, 0, len(r.sourceKinds))
	for k := range r.sourceKinds {
		result = append(result, k)
	}
	return result
}

func (r *Registry) IsValidStatus(status string) bool {
	return r.statuses[status]
}

func (r *Registry) IsValidTrigger(trigger string) bool {
	return r.triggers[trigger]
}

// SchemaJSON is the serializable representation of the registry schema.
type SchemaJSON struct {
	Filter          map[string]string      `json:"filter,omitempty"`
	Layers          []string               `json:"layers,omitempty"`
	NodeTypes       map[string][]string    `json:"node_types,omitempty"`
	EdgeTypes       map[string]EdgeTypeDef `json:"edge_types,omitempty"`
	DerivationKinds []string               `json:"derivation_kinds,omitempty"`
	NodeStatuses    []string               `json:"node_statuses,omitempty"`
	// SalienceRoles is the closed vocabulary of semantic node roles the scan
	// agent may classify into (emitted in node metadata). Source of truth.
	SalienceRoles []string `json:"salience_roles,omitempty"`
}

// EdgeSuggestion is a suggested edge type with intent label.
type EdgeSuggestion struct {
	EdgeType   string `json:"edge_type"`
	Intent     string `json:"intent"`
	FromLayers []string `json:"from_layers"`
	ToLayers   []string `json:"to_layers"`
}

// edgeIntentLabels provides semantic descriptions for edge types.
var edgeIntentLabels = map[string]string{
	"INVOKES":          "runtime call",
	"REQUIRES":         "correctness dependency",
	"DEPENDS_ON":       "structural/static dependency",
	"INJECTS":          "DI constructor parameter",
	"CALLS_SYMBOL":     "direct function/method call",
	"IMPORTS":          "module import",
	"CALLS_SERVICE":    "HTTP client call to service",
	"CALLS_ENDPOINT":   "HTTP client call to endpoint",
	"REFERENCES_MODEL": "FK / @relation",
	"HAS_FIELD":        "field ownership",
	"USES_ENUM":        "enum type usage",
	"USES_MODEL":       "data model query",
	"DEFINES_MODEL":    "schema definition",
	"EXPOSES_ENDPOINT": "route handler",
	"PUBLISHES_TOPIC":  "event producer",
	"CONSUMES_TOPIC":   "event consumer",
	"TRIGGERS_FLOW":    "entry point to business process",
	"PRODUCES_OUTCOME": "flow output (event/state change)",
	"PRECEDES":         "step ordering within flow",
	"TRANSITIONS_TO":   "flow-to-flow via event",
	"PART_OF_FLOW":     "flow step membership",
	"EMITS_AFTER":      "event emission after flow step",
	"CONTAINS":         "structural containment",
	"OWNED_BY":         "ownership relation",
	"USES_QUEUE":       "message queue usage",
}

// edgeSimilarity maps edge types to semantically similar alternatives.
// Used for error suggestions when a valid edge type is used with wrong layers.
var edgeSimilarity = map[string][]string{
	"CALLS_SERVICE":  {"INVOKES", "REQUIRES", "DEPENDS_ON"},
	"CALLS_ENDPOINT": {"TRIGGERS_FLOW", "INVOKES"},
	"USES_MODEL":     {"REQUIRES", "DEPENDS_ON"},
	"CALLS_SYMBOL":   {"INVOKES", "REQUIRES"},
	"TRIGGERS_FLOW":  {"TRANSITIONS_TO", "PRECEDES", "INVOKES"},
}

// ToSchemaJSON serializes the registry into a filterable schema.
// fromLayer/toLayer filter edge types. include filters sections (edges, nodes, layers).
func (r *Registry) ToSchemaJSON(fromLayer, toLayer string, include []string) SchemaJSON {
	filtered := fromLayer != "" || toLayer != ""
	includeSet := toSet(include)
	includeAll := len(include) == 0

	schema := SchemaJSON{}

	if filtered {
		schema.Filter = make(map[string]string)
		if fromLayer != "" {
			schema.Filter["from_layer"] = fromLayer
		}
		if toLayer != "" {
			schema.Filter["to_layer"] = toLayer
		}
	}

	// Layers
	if !filtered && (includeAll || includeSet["layers"]) {
		layers := make([]string, 0, len(r.layers))
		for l := range r.layers {
			layers = append(layers, l)
		}
		schema.Layers = layers
	}

	// Salience roles (closed vocabulary for scan-time role classification).
	if includeAll || includeSet["nodes"] {
		schema.SalienceRoles = r.SaliencePolicy().KnownRoles()
	}

	// Node types
	if includeAll || includeSet["nodes"] {
		if filtered {
			// Only include node types for filtered layers
			schema.NodeTypes = make(map[string][]string)
			if fromLayer != "" {
				if types, ok := r.nodeTypes[fromLayer]; ok {
					list := make([]string, 0, len(types))
					for t := range types {
						list = append(list, t)
					}
					schema.NodeTypes[fromLayer] = list
				}
			}
			if toLayer != "" && toLayer != fromLayer {
				if types, ok := r.nodeTypes[toLayer]; ok {
					list := make([]string, 0, len(types))
					for t := range types {
						list = append(list, t)
					}
					schema.NodeTypes[toLayer] = list
				}
			}
		} else {
			schema.NodeTypes = make(map[string][]string)
			for layer, types := range r.nodeTypes {
				list := make([]string, 0, len(types))
				for t := range types {
					list = append(list, t)
				}
				schema.NodeTypes[layer] = list
			}
		}
	}

	// Edge types
	if includeAll || includeSet["edges"] {
		schema.EdgeTypes = make(map[string]EdgeTypeDef)
		for name, et := range r.edgeTypes {
			if fromLayer != "" && !containsStr(et.FromLayers, fromLayer) {
				continue
			}
			if toLayer != "" && !containsStr(et.ToLayers, toLayer) {
				continue
			}
			schema.EdgeTypes[name] = et
		}
	}

	// Derivation kinds + statuses (full schema only)
	if !filtered && (includeAll || includeSet["layers"]) {
		derivations := make([]string, 0, len(r.derivations))
		for d := range r.derivations {
			derivations = append(derivations, d)
		}
		schema.DerivationKinds = derivations

		statuses := make([]string, 0, len(r.statuses))
		for s := range r.statuses {
			statuses = append(statuses, s)
		}
		schema.NodeStatuses = statuses
	}

	return schema
}

// SuggestEdgeTypes returns edge types that allow the given from/to layer pair,
// ranked by semantic proximity if a similarity map entry exists.
func (r *Registry) SuggestEdgeTypes(fromLayer, toLayer string) []EdgeSuggestion {
	var suggestions []EdgeSuggestion
	for name, et := range r.edgeTypes {
		if fromLayer != "" && !containsStr(et.FromLayers, fromLayer) {
			continue
		}
		if toLayer != "" && !containsStr(et.ToLayers, toLayer) {
			continue
		}
		intent := edgeIntentLabels[name]
		if intent == "" {
			intent = name
		}
		suggestions = append(suggestions, EdgeSuggestion{
			EdgeType:   name,
			Intent:     intent,
			FromLayers: et.FromLayers,
			ToLayers:   et.ToLayers,
		})
	}
	return suggestions
}

// SuggestSimilarEdgeTypes returns edge types similar to the given one.
// Uses the similarity map for known types, falls back to prefix/substring matching.
func (r *Registry) SuggestSimilarEdgeTypes(edgeType string) []EdgeSuggestion {
	// Check similarity map first
	if similar, ok := edgeSimilarity[edgeType]; ok {
		var suggestions []EdgeSuggestion
		for _, name := range similar {
			if et, exists := r.edgeTypes[name]; exists {
				intent := edgeIntentLabels[name]
				if intent == "" {
					intent = name
				}
				suggestions = append(suggestions, EdgeSuggestion{
					EdgeType:   name,
					Intent:     intent,
					FromLayers: et.FromLayers,
					ToLayers:   et.ToLayers,
				})
			}
		}
		if len(suggestions) > 0 {
			return suggestions
		}
	}

	// Fallback: prefix/substring matching
	var suggestions []EdgeSuggestion
	upper := strings.ToUpper(edgeType)
	for name, et := range r.edgeTypes {
		if strings.Contains(name, upper) || strings.HasPrefix(name, upper) {
			intent := edgeIntentLabels[name]
			if intent == "" {
				intent = name
			}
			suggestions = append(suggestions, EdgeSuggestion{
				EdgeType:   name,
				Intent:     intent,
				FromLayers: et.FromLayers,
				ToLayers:   et.ToLayers,
			})
		}
	}
	return suggestions
}

func containsStr(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

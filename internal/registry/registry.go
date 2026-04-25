package registry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type EdgeTypeDef struct {
	FromLayers []string `yaml:"from_layers"`
	ToLayers   []string `yaml:"to_layers"`
}

type TraversalPolicyDef struct {
	StructuralEdgeTypes []string `yaml:"structural_edge_types"`
	NoReverseImpact     []string `yaml:"no_reverse_impact"`
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
	Version             string                 `yaml:"version"`
	Layers              []string               `yaml:"layers"`
	NodeTypes           map[string][]string    `yaml:"node_types"`
	EdgeTypes           map[string]EdgeTypeDef `yaml:"edge_types"`
	DerivationKinds     []string               `yaml:"derivation_kinds"`
	SourceKinds         []string               `yaml:"source_kinds"`
	NodeStatuses        []string               `yaml:"node_statuses"`
	TriggerKinds        []string               `yaml:"trigger_kinds"`
	TraversalPolicyDef  *TraversalPolicyDef    `yaml:"traversal_policy"`
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

	return r, nil
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

func (r *Registry) IsValidEdgeType(edgeType string) bool {
	_, ok := r.edgeTypes[edgeType]
	return ok
}

func (r *Registry) ValidateEdgeLayers(edgeType, fromLayer, toLayer string) error {
	et, ok := r.edgeTypes[edgeType]
	if !ok {
		return fmt.Errorf("unknown edge type %q", edgeType)
	}
	fromOk := false
	for _, l := range et.FromLayers {
		if l == fromLayer {
			fromOk = true
			break
		}
	}
	if !fromOk {
		return fmt.Errorf("edge type %q does not allow from_layer %q", edgeType, fromLayer)
	}
	toOk := false
	for _, l := range et.ToLayers {
		if l == toLayer {
			toOk = true
			break
		}
	}
	if !toOk {
		return fmt.Errorf("edge type %q does not allow to_layer %q", edgeType, toLayer)
	}
	return nil
}

func (r *Registry) IsValidDerivation(kind string) bool {
	return r.derivations[kind]
}

func (r *Registry) IsValidSourceKind(kind string) bool {
	return r.sourceKinds[kind]
}

func (r *Registry) IsValidStatus(status string) bool {
	return r.statuses[status]
}

func (r *Registry) IsValidTrigger(trigger string) bool {
	return r.triggers[trigger]
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

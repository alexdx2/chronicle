// Package verify provides mechanical evidence verification.
// Verifiers check whether evidence assertions still hold in source files
// without requiring LLM interpretation.
package verify

import "encoding/json"

// Result is the outcome of verifying a single evidence assertion against a file.
type Result struct {
	// Status: "valid", "missing", "ambiguous", "unsupported"
	Status string `json:"status"`

	// ChangeType: "", "moved", "value_changed", "scope_changed", "target_changed"
	ChangeType string `json:"change_type,omitempty"`

	// NewLocator is set if the assertion was found at a different location.
	NewLocator *Locator `json:"new_locator,omitempty"`

	// Reason is a human-readable explanation of the result.
	Reason string `json:"reason,omitempty"`

	// Confidence in this verification result [0,1].
	Confidence float64 `json:"confidence"`

	// SuggestedAction: "revalidate", "mark_stale", "needs_claude", "deactivate"
	SuggestedAction string `json:"suggested_action"`
}

// Locator pinpoints where in a file an assertion was found.
type Locator struct {
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	JSONPath  string `json:"json_path,omitempty"`
}

// Verifier checks a specific kind of evidence assertion against a file.
type Verifier interface {
	// Kind returns the assertion_kind this verifier handles.
	Kind() string

	// Verify checks whether the assertion still holds in the given file content.
	// oldLocator may be nil if no previous location is known.
	Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error)
}

// Registry holds all available verifiers indexed by assertion_kind.
type Registry struct {
	verifiers map[string]Verifier
}

// NewRegistry creates an empty verifier registry.
func NewRegistry() *Registry {
	return &Registry{verifiers: make(map[string]Verifier)}
}

// Register adds a verifier to the registry.
func (r *Registry) Register(v Verifier) {
	r.verifiers[v.Kind()] = v
}

// Get returns the verifier for a given assertion_kind, or nil if unsupported.
func (r *Registry) Get(kind string) Verifier {
	return r.verifiers[kind]
}

// Verify dispatches to the appropriate verifier based on assertion_kind.
func (r *Registry) Verify(assertionKind string, fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	v := r.verifiers[assertionKind]
	if v == nil {
		return &Result{
			Status:          "unsupported",
			Confidence:      0,
			SuggestedAction: "needs_claude",
			Reason:          "no verifier for assertion_kind: " + assertionKind,
		}, nil
	}
	return v.Verify(fileContent, assertion, oldLocator)
}

// DefaultRegistry creates a registry with all built-in verifiers.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(&ManifestVerifier{})
	r.Register(&GoModVerifier{})
	r.Register(&TSImportVerifier{})
	r.Register(&TSCallVerifier{})
	r.Register(&TSDecoratorVerifier{})
	r.Register(&YAMLVerifier{})
	r.Register(&PrismaSchemaVerifier{})
	r.Register(&ComplexityVerifier{})
	r.Register(&ComplexitySmellsVerifier{})
	r.Register(&TextVerifier{})
	return r
}

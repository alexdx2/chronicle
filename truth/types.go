// Package truth extracts a deterministic reference of facts from a repository's
// sources (no model involved) and compares it against a Chronicle graph snapshot.
package truth

// Class is a category of facts the reference covers.
type Class string

const (
	ClassEndpoints Class = "endpoints"
	ClassHTTP      Class = "http"
	ClassChannels  Class = "channels"
	ClassDeps      Class = "deps"
	ClassServices  Class = "services"
	ClassPackages  Class = "packages"
)

// Classes lists all classes in display order.
var Classes = []Class{ClassEndpoints, ClassHTTP, ClassChannels, ClassDeps, ClassServices, ClassPackages}

// Status is the verdict for one reference fact (or Uncovered for a graph fact).
type Status string

const (
	Confirmed      Status = "confirmed"
	ConfirmedLoose Status = "confirmed_loose"
	Missing        Status = "missing"
	OutOfScope     Status = "out_of_scope"
	Ignored        Status = "ignored"
	Uncovered      Status = "uncovered"
)

// Evidence points at a place in the sources.
type Evidence struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Fact is one reference fact extracted from sources.
type Fact struct {
	Class   Class             `json:"class"`
	Key     string            `json:"key"`
	Attrs   map[string]string `json:"attrs,omitempty"`
	Primary Evidence          `json:"primary"`
	Derived []Evidence        `json:"derived,omitempty"`
	Sources []string          `json:"sources"`
}

// RulesVersion identifies the generation of the extractor rules and the class
// list. Bump it whenever an extractor rule or the class list changes, so
// stored reports are recomputed. 2: the packages class exists. 3: appsettings URLs under any key, ts config
// URLs, example/generated code excluded.
const RulesVersion = 4

// Report is the output of an extraction run.
type Report struct {
	// RulesVersion is truth.RulesVersion at extraction time; reports stored
	// before the field existed decode with 0.
	RulesVersion int      `json:"rules_version"`
	Facts        []Fact   `json:"facts"`
	SkippedFiles []string `json:"skipped_files,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	Files        int      `json:"files"`
}

// JudgedFact is a reference fact with its comparison status.
type JudgedFact struct {
	Fact
	Status       Status `json:"status"`
	GraphNodeKey string `json:"graph_node_key,omitempty"`
}

// GraphFact is a graph-side fact the reference does not cover.
type GraphFact struct {
	Class   Class  `json:"class"`
	NodeKey string `json:"node_key"`
	Name    string `json:"name"`
}

// Counts are per-status tallies. Known is set by the compare side to
// Confirmed+Loose+Missing+OutOfScope.
type Counts struct {
	Known      int `json:"known"`
	Confirmed  int `json:"confirmed"`
	Loose      int `json:"loose"`
	Missing    int `json:"missing"`
	OutOfScope int `json:"out_of_scope"`
	Ignored    int `json:"ignored"`
	Uncovered  int `json:"uncovered"`
}

// Metrics are the ratios derived from Counts. HasData=false means "no data".
type Metrics struct {
	Coverage float64 `json:"coverage"`
	Scope    float64 `json:"scope"`
	Recall   float64 `json:"recall"`
	HasData  bool    `json:"has_data"`
}

// SourceSummary is the per-rule-source breakdown.
type SourceSummary struct {
	Source string `json:"source"`
	Counts
	Metrics
}

// ClassSummary is the per-class result with its per-source breakdown.
type ClassSummary struct {
	Class Class `json:"class"`
	Counts
	Metrics
	BySource []SourceSummary `json:"by_source"`
}

// Verdict is the full comparison result for one repo snapshot.
type Verdict struct {
	Repo         string         `json:"repo"`
	SnapshotID   string         `json:"snapshot_id"`
	CodeSha      string         `json:"code_sha"`
	Classes      []ClassSummary `json:"classes"`
	Facts        []JudgedFact   `json:"facts"`
	Uncovered    []GraphFact    `json:"uncovered"`
	SkippedFiles []string       `json:"skipped_files,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
}

// Compute derives the metrics. It is named Compute (not Metrics) because
// ClassSummary embeds both Counts and Metrics, and a method named Metrics
// would collide with the embedded field.
//
//	Coverage = (Confirmed+Loose) / Known
//	Scope    = (Confirmed+Loose+Missing) / Known
//	Recall   = (Confirmed+Loose) / (Confirmed+Loose+Missing), 0 when that is 0
func (c Counts) Compute() Metrics {
	m := Metrics{HasData: c.Known > 0}
	if c.Known > 0 {
		k := float64(c.Known)
		m.Coverage = float64(c.Confirmed+c.Loose) / k
		m.Scope = float64(c.Confirmed+c.Loose+c.Missing) / k
	}
	if d := c.Confirmed + c.Loose + c.Missing; d > 0 {
		m.Recall = float64(c.Confirmed+c.Loose) / float64(d)
	}
	return m
}

// Weakest returns the class with the lowest Coverage among those with data;
// a tie goes to the class with the larger Known. ok is false when no class has data.
func Weakest(cs []ClassSummary) (ClassSummary, bool) {
	var best ClassSummary
	found := false
	for _, c := range cs {
		if !c.HasData {
			continue
		}
		if !found || c.Coverage < best.Coverage || (c.Coverage == best.Coverage && c.Known > best.Known) {
			best, found = c, true
		}
	}
	return best, found
}

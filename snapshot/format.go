// Package snapshot defines the canonical, git-committed graph.jsonl format
// and its codec: the on-disk record types (Header, Node, Edge, Term,
// Coverage) and the deterministic Encode/Decode pair that every later graph-
// snapshot and review package builds on.
//
// A snapshot is one JSON record per line (UTF-8, newline-terminated): a
// Header first, then Node, Edge, Term and Coverage records. Encode always
// produces the same bytes for the same logical content, regardless of the
// order records were built in. Decode treats a snapshot as untrusted input:
// malformed or hostile lines are reported as Warning values and skipped
// rather than causing a panic or aborting the whole read.
package snapshot

import "encoding/json"

// Format, Version and FactSchema identify the snapshot file itself. Path is
// the file's fixed location relative to a project's .depbot directory.
const (
	Format     = "chronicle-graph"
	Version    = 1
	FactSchema = 1
	Path       = ".depbot/graph.jsonl"
)

// Header is the first line of a graph.jsonl file: format identity, the
// domains covered, and Counts, an integrity check Decode verifies against
// the records it actually reads.
type Header struct {
	Kind       string   `json:"kind"` // "header"
	Format     string   `json:"format"`
	Version    int      `json:"version"`
	Seq        int      `json:"seq"`
	Domains    []string `json:"domains"`
	FactSchema int      `json:"fact_schema"`
	Registry   string   `json:"registry,omitempty"`
	Counts     Counts   `json:"counts"`
}

// Counts records how many of each record kind a snapshot contains. Encode
// fills it in from the actual record slices; Decode compares it against what
// it actually read and warns (counts_mismatch) on a discrepancy.
type Counts struct {
	Node     int `json:"node,omitempty"`
	Edge     int `json:"edge,omitempty"`
	Term     int `json:"term,omitempty"`
	Coverage int `json:"coverage,omitempty"`
}

// Alias is one alternate name (or DNS name, or topic name) for a Node.
type Alias struct {
	Alias      string  `json:"alias,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// Evidence is the provenance of one fact attached to a Node or Edge: which
// file, blob and range it was extracted from, by which extractor, and the
// asserter's own confidence and assertion payload.
type Evidence struct {
	ID               string          `json:"id,omitempty"`
	Source           string          `json:"source,omitempty"`
	File             string          `json:"file,omitempty"`
	Blob             string          `json:"blob,omitempty"`
	Range            string          `json:"range,omitempty"`
	Locator          string          `json:"locator,omitempty"`
	Extractor        string          `json:"extractor,omitempty"`
	ExtractorVersion string          `json:"extractor_version,omitempty"`
	Rule             string          `json:"rule,omitempty"`
	Polarity         string          `json:"polarity,omitempty"`
	Status           string          `json:"status,omitempty"`
	Verification     string          `json:"verification,omitempty"`
	Confidence       float64         `json:"confidence,omitempty"`
	Assertion        json.RawMessage `json:"assertion,omitempty"`
	AssertionKind    string          `json:"assertion_kind,omitempty"`
	AssertionVersion string          `json:"assertion_version,omitempty"`
}

// Node is one graph node: a service, module, endpoint, model, or similar
// architectural fact. ID is the node_key (layer:type:domain:qualified_name);
// its format and normalization are owned by package validate, unchanged
// here. Attrs collects the small set of rarely-populated fields so a typical
// line doesn't carry empty values for them.
type Node struct {
	Kind          string            `json:"kind,omitempty"` // "node"
	ID            string            `json:"id,omitempty"`
	Layer         string            `json:"layer,omitempty"`
	Type          string            `json:"type,omitempty"`
	Domain        string            `json:"domain,omitempty"`
	Name          string            `json:"name,omitempty"`
	QualifiedName string            `json:"qualified_name,omitempty"`
	File          string            `json:"file,omitempty"`
	Lang          string            `json:"lang,omitempty"`
	Status        string            `json:"status,omitempty"`
	Attrs         map[string]string `json:"attrs,omitempty"`
	Metadata      json.RawMessage   `json:"metadata,omitempty"`
	Aliases       []Alias           `json:"aliases,omitempty"`
	Evidence      []Evidence        `json:"evidence,omitempty"`
}

// Edge is one graph edge: a dependency, call, or other directed reference
// between two nodes. ID is the edge_key (from_node_key->to_node_key:TYPE);
// From and To repeat the endpoints explicitly so a line is self-sufficient
// for diff review without parsing the key. Active has no omitempty: an
// inactive edge (active:false) is a fact in its own right and must not
// disappear from the line.
type Edge struct {
	Kind             string          `json:"kind,omitempty"` // "edge"
	ID               string          `json:"id,omitempty"`
	Type             string          `json:"type,omitempty"`
	From             string          `json:"from,omitempty"`
	To               string          `json:"to,omitempty"`
	Derivation       string          `json:"derivation,omitempty"`
	DependencySource string          `json:"dependency_source,omitempty"`
	Context          string          `json:"context,omitempty"`
	Active           bool            `json:"active"`
	Verification     string          `json:"verification,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	Evidence         []Evidence      `json:"evidence,omitempty"`
}

// Term is one domain-glossary entry. ID is "<domain>#<term>".
type Term struct {
	Kind         string   `json:"kind,omitempty"` // "term"
	ID           string   `json:"id,omitempty"`
	Domain       string   `json:"domain,omitempty"`
	Term         string   `json:"term,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	AntiPatterns []string `json:"anti_patterns,omitempty"`
	Description  string   `json:"description,omitempty"`
	Context      string   `json:"context,omitempty"`
	Examples     []string `json:"examples,omitempty"`
}

// ExtractorRun records one extractor's pass over a covered file.
type ExtractorRun struct {
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
	Status  string `json:"status,omitempty"`
}

// Coverage records that at least one extractor looked at Path, whether or
// not it found any facts there; that absence-of-facts is itself meaningful
// (a scanned-but-empty file is not the same as an unscanned one). An empty
// Blob means coverage is known but the file's current blob is not (stale).
type Coverage struct {
	Kind       string         `json:"kind,omitempty"` // "coverage"
	Path       string         `json:"path,omitempty"`
	Blob       string         `json:"blob,omitempty"`
	Extractors []ExtractorRun `json:"extractors,omitempty"`
}

// Snapshot is the in-memory form of a graph.jsonl file: a Header plus its
// four record slices. It is never marshaled as a single JSON value — Encode
// writes it as one JSON object per line.
type Snapshot struct {
	Header   Header
	Nodes    []Node
	Edges    []Edge
	Terms    []Term
	Coverage []Coverage
}

// Warning describes one tolerated defect found while decoding hostile or
// malformed input. The offending record is skipped; decoding continues.
type Warning struct {
	Line int
	Code string // "bad_json", "too_long", "unknown_kind", "bad_key", "conflict_marker", "counts_mismatch", "dangling_edge"
	Msg  string
}
